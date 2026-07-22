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
	"errors"
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
	"github.com/stuttgart-things/sops-secrets-operator/internal/secretref"
	"github.com/stuttgart-things/sops-secrets-operator/internal/source"
)

const (
	defaultSyncInterval = 5 * time.Minute
	// retryAfter is the backoff between retries when a reconcile fails.
	retryAfter = 30 * time.Second

	// resyncAfter is how long a secret-producing reconciler waits before
	// re-applying an object that already succeeded.
	//
	// Without it a successful reconcile is terminal: nothing watches the
	// Secret we write (it may live in another namespace via target.namespace,
	// so ownerReferences — and therefore Owns() — do not apply), so a Secret
	// deleted out of band never comes back while the CR still reports
	// Applied=True. See #83.
	resyncAfter = 5 * time.Minute

	// GitRepoAuthSecretIndex is a field index on GitRepository pointing at
	// the auth Secret it resolves to, as "<namespace>/<name>". Used to
	// enqueue GitRepositories when that Secret changes.
	//
	// Namespace-qualified since #48: the Secret may live outside the CR's
	// namespace, so a bare name would collide across namespaces.
	GitRepoAuthSecretIndex = ".spec.auth.secretRef"
)

// GitRepositoryReconciler reconciles GitRepository objects.
type GitRepositoryReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Registry *source.Registry
	CredentialPolicy
}

// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=gitrepositories,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=gitrepositories/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=gitrepositories/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *GitRepositoryReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("gitrepository", req.NamespacedName)
	ctx, t := trackReconcile(ctx, "GitRepository", req.Namespace, req.Name)
	defer t.Finish()

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

	authCtx := t.Stage(ctx, StageAuth)
	auth, authOrigin, err := r.resolveAuth(authCtx, &gr)
	if err != nil {
		log.Error(err, "auth resolution failed")
		t.Fail(StageAuth, err)
		setCondition(&gr.Status.Conditions, sopsv1alpha1.ConditionAuthResolved, metav1.ConditionFalse, "AuthFailed", err.Error())
		setCondition(&gr.Status.Conditions, sopsv1alpha1.ConditionSourceReady, metav1.ConditionFalse, "AuthFailed", "waiting for auth")
		gr.Status.CacheReady = false
		if uerr := r.Status().Update(ctx, &gr); uerr != nil {
			return ctrl.Result{}, uerr
		}
		return ctrl.Result{RequeueAfter: retryAfter}, nil
	}
	setCondition(&gr.Status.Conditions, sopsv1alpha1.ConditionAuthResolved, metav1.ConditionTrue, "AuthOK", describeAuthOrigin(authOrigin))

	cfg := git.Config{
		URL:      gr.Spec.URL,
		Branch:   gr.Spec.Branch,
		Revision: gr.Spec.Revision,
		Auth:     auth,
	}
	fetchCtx := t.Stage(ctx, StageFetch)
	sha, err := r.Registry.EnsureCached(fetchCtx, req.NamespacedName, cfg)
	if err != nil {
		log.Error(err, "fetch failed")
		t.Fail(StageFetch, err)
		setCondition(&gr.Status.Conditions, sopsv1alpha1.ConditionSourceReady, metav1.ConditionFalse, "FetchFailed", err.Error())
		gr.Status.CacheReady = false
		if uerr := r.Status().Update(ctx, &gr); uerr != nil {
			return ctrl.Result{}, uerr
		}
		return ctrl.Result{RequeueAfter: retryAfter}, nil
	}
	t.SetCommit(sha)

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

// gitAuthRef reads the CR's auth reference into the neutral form the
// resolver takes. A nil secretRef yields the zero Ref, which the resolver
// reads as "fall back to the operator's default, if any".
func gitAuthRef(gr *sopsv1alpha1.GitRepository) secretref.Ref {
	if gr.Spec.Auth == nil || gr.Spec.Auth.SecretRef == nil {
		return secretref.Ref{}
	}
	return secretref.Ref{
		Namespace: gr.Spec.Auth.SecretRef.Namespace,
		Name:      gr.Spec.Auth.SecretRef.Name,
	}
}

// resolveAuth resolves the credential for a GitRepository. The returned
// Origin says where it came from, for the AuthResolved condition.
func (r *GitRepositoryReconciler) resolveAuth(ctx context.Context, gr *sopsv1alpha1.GitRepository) (git.Auth, secretref.Origin, error) {
	// No auth block at all still means "clone unauthenticated". Only a CR
	// that asked for auth and named nothing reaches the resolver.
	if gr.Spec.Auth == nil {
		return git.Auth{}, "", nil
	}

	res, err := r.SecretRefs.Resolve(gr.Namespace, gitAuthRef(gr), r.GlobalGitAuth)
	if err != nil {
		if errors.Is(err, secretref.ErrNoReference) {
			return git.Auth{}, "", fmt.Errorf(
				"spec.auth is set but names no secretRef, and the operator has no --global-git-auth-secret configured")
		}
		return git.Auth{}, "", err
	}

	var sec corev1.Secret
	if err := r.Get(ctx, res.ObjectKey, &sec); err != nil {
		return git.Auth{}, "", fmt.Errorf("get auth secret %s/%s: %w", res.Namespace, res.Name, err)
	}

	switch gr.Spec.Auth.Type {
	case sopsv1alpha1.GitAuthBasic:
		if len(sec.Data["password"]) == 0 {
			return git.Auth{}, "", fmt.Errorf("auth secret %q: missing 'password' key", sec.Name)
		}
		return git.Auth{Basic: &git.BasicAuth{
			Username: string(sec.Data["username"]),
			Password: string(sec.Data["password"]),
		}}, res.Origin, nil

	case sopsv1alpha1.GitAuthSSH:
		if len(sec.Data["privateKey"]) == 0 {
			return git.Auth{}, "", fmt.Errorf("auth secret %q: missing 'privateKey' key", sec.Name)
		}
		if len(sec.Data["knownHosts"]) == 0 {
			return git.Auth{}, "", fmt.Errorf("auth secret %q: missing 'knownHosts' key (strict host-key checking is required)", sec.Name)
		}
		return git.Auth{SSH: &git.SSHAuth{
			User:       string(sec.Data["user"]),
			PrivateKey: sec.Data["privateKey"],
			Passphrase: sec.Data["passphrase"],
			KnownHosts: sec.Data["knownHosts"],
		}}, res.Origin, nil

	default:
		return git.Auth{}, "", fmt.Errorf("unknown auth type %q", gr.Spec.Auth.Type)
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
			return r.SecretRefs.IndexValues(g.Namespace, gitAuthRef(g), r.GlobalGitAuth)
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
	// Listed across all namespaces on purpose: the index is
	// namespace-qualified, so a Secret only matches GitRepositories that
	// actually resolve to it — including ones in other namespaces that
	// reference it, and ones that fall back to the operator's default.
	var list sopsv1alpha1.GitRepositoryList
	if err := r.List(ctx, &list,
		client.MatchingFields{GitRepoAuthSecretIndex: secretref.IndexKey(obj.GetNamespace(), obj.GetName())},
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
