/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sopsv1alpha1 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha1"
	"github.com/stuttgart-things/sops-secrets-operator/internal/git"
	"github.com/stuttgart-things/sops-secrets-operator/internal/source"
)

const (
	defaultSyncInterval = 5 * time.Minute
	// retryAfter is the backoff between retries when a reconcile fails.
	retryAfter = 30 * time.Second

	// GitRepoAuthSecretIndex is a field index on GitRepository pointing at
	// the name of its auth secret reference. Used to enqueue GitRepositories
	// when a referenced Secret changes.
	GitRepoAuthSecretIndex = ".spec.auth.secretRef.name"
)

// GitRepositoryReconciler reconciles GitRepository objects.
type GitRepositoryReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Registry *source.Registry
}

// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=gitrepositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=gitrepositories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=gitrepositories/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *GitRepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("gitrepository", req.NamespacedName)
	setStage, finish := trackReconcile("GitRepository")
	defer finish()

	var gr sopsv1alpha1.GitRepository
	if err := r.Get(ctx, req.NamespacedName, &gr); err != nil {
		if apierrors.IsNotFound(err) {
			r.Registry.Forget(req.NamespacedName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !gr.DeletionTimestamp.IsZero() {
		r.Registry.Forget(req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Force-sync: a changed reconcile-requested annotation drops the cache
	// before the EnsureCached call, so the next fetch re-pulls from origin
	// regardless of the configured commit/branch.
	reqToken := gr.Annotations[ReconcileRequestAnnotation]
	if reqToken != "" && reqToken != gr.Status.LastProcessedReconcileToken {
		r.Registry.Forget(req.NamespacedName)
	}

	auth, err := r.resolveAuth(ctx, &gr)
	if err != nil {
		log.Error(err, "auth resolution failed")
		setStage(StageAuth)
		setCondition(&gr.Status.Conditions, sopsv1alpha1.ConditionAuthResolved, metav1.ConditionFalse, "AuthFailed", err.Error())
		setCondition(&gr.Status.Conditions, sopsv1alpha1.ConditionSourceReady, metav1.ConditionFalse, "AuthFailed", "waiting for auth")
		gr.Status.CacheReady = false
		if uerr := r.Status().Update(ctx, &gr); uerr != nil {
			return ctrl.Result{}, uerr
		}
		return ctrl.Result{RequeueAfter: retryAfter}, nil
	}
	setCondition(&gr.Status.Conditions, sopsv1alpha1.ConditionAuthResolved, metav1.ConditionTrue, "AuthOK", "auth resolved")

	cfg := git.Config{
		URL:      gr.Spec.URL,
		Branch:   gr.Spec.Branch,
		Revision: gr.Spec.Revision,
		Auth:     auth,
	}
	sha, err := r.Registry.EnsureCached(ctx, req.NamespacedName, cfg)
	if err != nil {
		log.Error(err, "fetch failed")
		setStage(StageFetch)
		setCondition(&gr.Status.Conditions, sopsv1alpha1.ConditionSourceReady, metav1.ConditionFalse, "FetchFailed", err.Error())
		gr.Status.CacheReady = false
		if uerr := r.Status().Update(ctx, &gr); uerr != nil {
			return ctrl.Result{}, uerr
		}
		return ctrl.Result{RequeueAfter: retryAfter}, nil
	}

	gr.Status.LastSyncedCommit = sha
	gr.Status.CacheReady = true
	gr.Status.LastProcessedReconcileToken = reqToken
	gr.Status.ObservedGeneration = gr.Generation
	short := sha
	if len(short) > 7 {
		short = short[:7]
	}
	setCondition(&gr.Status.Conditions, sopsv1alpha1.ConditionSourceReady, metav1.ConditionTrue, "Ready", "cache at "+short)
	if err := r.Status().Update(ctx, &gr); err != nil {
		return ctrl.Result{}, err
	}

	interval := gr.Spec.Interval.Duration
	if interval == 0 {
		interval = defaultSyncInterval
	}
	return ctrl.Result{RequeueAfter: interval}, nil
}

func (r *GitRepositoryReconciler) resolveAuth(ctx context.Context, gr *sopsv1alpha1.GitRepository) (git.Auth, error) {
	if gr.Spec.Auth == nil {
		return git.Auth{}, nil
	}
	var sec corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: gr.Namespace, Name: gr.Spec.Auth.SecretRef.Name}, &sec); err != nil {
		return git.Auth{}, fmt.Errorf("get auth secret: %w", err)
	}

	switch gr.Spec.Auth.Type {
	case sopsv1alpha1.GitAuthBasic:
		if len(sec.Data["password"]) == 0 {
			return git.Auth{}, fmt.Errorf("auth secret %q: missing 'password' key", sec.Name)
		}
		return git.Auth{Basic: &git.BasicAuth{
			Username: string(sec.Data["username"]),
			Password: string(sec.Data["password"]),
		}}, nil

	case sopsv1alpha1.GitAuthSSH:
		if len(sec.Data["privateKey"]) == 0 {
			return git.Auth{}, fmt.Errorf("auth secret %q: missing 'privateKey' key", sec.Name)
		}
		if len(sec.Data["knownHosts"]) == 0 {
			return git.Auth{}, fmt.Errorf("auth secret %q: missing 'knownHosts' key (strict host-key checking is required)", sec.Name)
		}
		return git.Auth{SSH: &git.SSHAuth{
			User:       string(sec.Data["user"]),
			PrivateKey: sec.Data["privateKey"],
			Passphrase: sec.Data["passphrase"],
			KnownHosts: sec.Data["knownHosts"],
		}}, nil

	default:
		return git.Auth{}, fmt.Errorf("unknown auth type %q", gr.Spec.Auth.Type)
	}
}

func (r *GitRepositoryReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&sopsv1alpha1.GitRepository{},
		GitRepoAuthSecretIndex,
		func(obj client.Object) []string {
			g := obj.(*sopsv1alpha1.GitRepository)
			if g.Spec.Auth == nil {
				return nil
			}
			return []string{g.Spec.Auth.SecretRef.Name}
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&sopsv1alpha1.GitRepository{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(r.mapSecretToGitRepos)).
		Named("gitrepository").
		Complete(r)
}

func (r *GitRepositoryReconciler) mapSecretToGitRepos(ctx context.Context, obj client.Object) []reconcile.Request {
	var list sopsv1alpha1.GitRepositoryList
	if err := r.List(ctx, &list,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{GitRepoAuthSecretIndex: obj.GetName()},
	); err != nil {
		return nil
	}
	out := make([]reconcile.Request, 0, len(list.Items))
	for _, g := range list.Items {
		out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&g)})
	}
	return out
}

// setCondition upserts a condition by type in the given slice.
func setCondition(conds *[]metav1.Condition, condType string, status metav1.ConditionStatus, reason, msg string) {
	meta.SetStatusCondition(conds, metav1.Condition{
		Type:    condType,
		Status:  status,
		Reason:  reason,
		Message: msg,
	})
}
