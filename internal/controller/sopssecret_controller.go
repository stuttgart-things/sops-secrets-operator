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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	sopsv1alpha1 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha1"
	"github.com/stuttgart-things/sops-secrets-operator/internal/decrypt"
	"github.com/stuttgart-things/sops-secrets-operator/internal/keyresolve"
	"github.com/stuttgart-things/sops-secrets-operator/internal/source"
	"github.com/stuttgart-things/sops-secrets-operator/internal/transform"
)

// SopsSecretRepoRefIndex is a field index on SopsSecret.spec.source.repositoryRef.name.
const SopsSecretRepoRefIndex = ".spec.source.repositoryRef.name"

// SopsSecretReconciler reconciles SopsSecret objects (mapping mode).
type SopsSecretReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Registry *source.Registry
}

// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=sopssecrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=sopssecrets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=sopssecrets/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *SopsSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("sopssecret", req.NamespacedName)
	setStage, finish := trackReconcile("SopsSecret")
	defer finish()

	var ss sopsv1alpha1.SopsSecret
	if err := r.Get(ctx, req.NamespacedName, &ss); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Finalizer: add if missing, handle deletion if set.
	if ss.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&ss, Finalizer) {
			controllerutil.AddFinalizer(&ss, Finalizer)
			if err := r.Update(ctx, &ss); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
	} else {
		if controllerutil.ContainsFinalizer(&ss, Finalizer) {
			if err := r.deleteOwnedSecret(ctx, &ss, targetName(&ss), targetNamespace(&ss)); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&ss, Finalizer)
			if err := r.Update(ctx, &ss); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Resolve GitRepository and check it's ready.
	var repo sopsv1alpha1.GitRepository
	repoKey := client.ObjectKey{Namespace: ss.Namespace, Name: ss.Spec.Source.RepositoryRef.Name}
	if err := r.Get(ctx, repoKey, &repo); err != nil {
		msg := err.Error()
		if apierrors.IsNotFound(err) {
			msg = fmt.Sprintf("GitRepository %q not found", ss.Spec.Source.RepositoryRef.Name)
		}
		setStage(StageFetch)
		return r.failStatus(ctx, &ss, sopsv1alpha1.ConditionSourceReady, "SourceMissing", msg)
	}
	if !isSourceReady(&repo) {
		setStage(StageFetch)
		return r.failStatus(ctx, &ss, sopsv1alpha1.ConditionSourceReady, "SourceNotReady",
			fmt.Sprintf("GitRepository %q is not ready", repo.Name))
	}
	setCondition(&ss.Status.Conditions, sopsv1alpha1.ConditionSourceReady, metav1.ConditionTrue, "Ready", "source is ready")

	// Read encrypted file from the cached repo.
	content, commitSHA, err := r.Registry.Read(repoKey, ss.Spec.Source.Path)
	if err != nil {
		setStage(StageFetch)
		return r.failStatus(ctx, &ss, sopsv1alpha1.ConditionSourceReady, "ReadFailed", err.Error())
	}

	// Resolve age key and decrypt.
	ageKey, err := keyresolve.Age(ctx, r.Client, ss.Namespace, ss.Spec.Decryption.KeyRef)
	if err != nil {
		setStage(StageDecrypt)
		return r.failStatus(ctx, &ss, sopsv1alpha1.ConditionDecrypted, "KeyResolveFailed", err.Error())
	}
	plaintext, err := decrypt.DecryptAge(content, ss.Spec.Source.Path, ageKey)
	if err != nil {
		setStage(StageDecrypt)
		return r.failStatus(ctx, &ss, sopsv1alpha1.ConditionDecrypted, "DecryptFailed", err.Error())
	}

	// Parse flat YAML, enforce flat-only guard, apply mapping.
	flat, err := transform.ParseFlatYAML(plaintext)
	if err != nil {
		setStage(StageDecrypt)
		return r.failStatus(ctx, &ss, sopsv1alpha1.ConditionDecrypted, "ParseFailed", err.Error())
	}
	data, err := transform.ApplyMapping(flat, ss.Spec.Data)
	if err != nil {
		setStage(StageDecrypt)
		return r.failStatus(ctx, &ss, sopsv1alpha1.ConditionDecrypted, "MappingFailed", err.Error())
	}
	setCondition(&ss.Status.Conditions, sopsv1alpha1.ConditionDecrypted, metav1.ConditionTrue, "Decrypted", "decryption + mapping ok")

	// Apply target Secret.
	hash := transform.HashSecretData(data)
	if err := r.applyTargetSecret(ctx, &ss, data, hash, commitSHA); err != nil {
		log.Error(err, "apply target secret failed")
		setStage(StageApply)
		return r.failStatus(ctx, &ss, sopsv1alpha1.ConditionApplied, "ApplyFailed", err.Error())
	}
	setCondition(&ss.Status.Conditions, sopsv1alpha1.ConditionApplied, metav1.ConditionTrue, "Applied",
		fmt.Sprintf("applied %d keys", len(data)))

	ss.Status.LastAppliedHash = hash
	ss.Status.LastSyncedCommit = commitSHA
	ss.Status.ObservedGeneration = ss.Generation
	if err := r.Status().Update(ctx, &ss); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *SopsSecretReconciler) applyTargetSecret(ctx context.Context, ss *sopsv1alpha1.SopsSecret, data map[string][]byte, hash, commitSHA string) error {
	secret := &corev1.Secret{}
	secret.Name = targetName(ss)
	secret.Namespace = targetNamespace(ss)

	ownerKey := fmt.Sprintf("SopsSecret/%s/%s", ss.Namespace, ss.Name)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		// Adoption check on pre-existing Secret.
		if secret.ResourceVersion != "" {
			managedBy := secret.Labels[ManagedByLabel]
			existingOwner := secret.Annotations[OwnerAnnotation]
			switch {
			case managedBy == "" && !ss.Spec.Target.Adopt:
				return fmt.Errorf("target Secret %s/%s exists but is not managed by this operator; set target.adopt=true to take over",
					secret.Namespace, secret.Name)
			case managedBy == ManagedByValue && existingOwner != "" && existingOwner != ownerKey:
				return fmt.Errorf("target Secret %s/%s is already owned by %q", secret.Namespace, secret.Name, existingOwner)
			}
		}

		// Labels.
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels[ManagedByLabel] = ManagedByValue

		// Annotations.
		if secret.Annotations == nil {
			secret.Annotations = map[string]string{}
		}
		secret.Annotations[OwnerAnnotation] = ownerKey
		secret.Annotations[OwnerUIDAnnotation] = string(ss.UID)
		secret.Annotations[ContentHashAnnotation] = hash
		secret.Annotations[SourceCommitAnnotation] = commitSHA

		// Type.
		if ss.Spec.Target.Type != "" {
			secret.Type = ss.Spec.Target.Type
		} else if secret.Type == "" {
			secret.Type = corev1.SecretTypeOpaque
		}

		// Data is authoritative: drop any key not declared in spec.data.
		secret.Data = data
		return nil
	})
	return err
}

func (r *SopsSecretReconciler) deleteOwnedSecret(ctx context.Context, ss *sopsv1alpha1.SopsSecret, name, namespace string) error {
	var sec corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &sec); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if sec.Labels[ManagedByLabel] != ManagedByValue {
		return nil // not ours; leave it
	}
	ownerKey := fmt.Sprintf("SopsSecret/%s/%s", ss.Namespace, ss.Name)
	if sec.Annotations[OwnerAnnotation] != ownerKey {
		return nil // managed by this operator, but a different CR owns it
	}
	return client.IgnoreNotFound(r.Delete(ctx, &sec))
}

func (r *SopsSecretReconciler) failStatus(ctx context.Context, ss *sopsv1alpha1.SopsSecret, condType, reason, msg string) (ctrl.Result, error) {
	setCondition(&ss.Status.Conditions, condType, metav1.ConditionFalse, reason, msg)
	if err := r.Status().Update(ctx, ss); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: retryAfter}, nil
}

func targetName(ss *sopsv1alpha1.SopsSecret) string {
	if ss.Spec.Target.Name != "" {
		return ss.Spec.Target.Name
	}
	return ss.Name
}

func targetNamespace(ss *sopsv1alpha1.SopsSecret) string {
	if ss.Spec.Target.Namespace != "" {
		return ss.Spec.Target.Namespace
	}
	return ss.Namespace
}

func isSourceReady(repo *sopsv1alpha1.GitRepository) bool {
	for _, c := range repo.Status.Conditions {
		if c.Type == sopsv1alpha1.ConditionSourceReady {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

func (r *SopsSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&sopsv1alpha1.SopsSecret{},
		SopsSecretRepoRefIndex,
		func(obj client.Object) []string {
			s := obj.(*sopsv1alpha1.SopsSecret)
			return []string{s.Spec.Source.RepositoryRef.Name}
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&sopsv1alpha1.SopsSecret{}).
		Watches(&sopsv1alpha1.GitRepository{}, handler.EnqueueRequestsFromMapFunc(r.mapRepoToSopsSecrets)).
		Named("sopssecret").
		Complete(r)
}

func (r *SopsSecretReconciler) mapRepoToSopsSecrets(ctx context.Context, obj client.Object) []reconcile.Request {
	var list sopsv1alpha1.SopsSecretList
	if err := r.List(ctx, &list,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{SopsSecretRepoRefIndex: obj.GetName()},
	); err != nil {
		return nil
	}
	out := make([]reconcile.Request, 0, len(list.Items))
	for _, s := range list.Items {
		out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&s)})
	}
	return out
}
