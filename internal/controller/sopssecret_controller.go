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
	sopsv1alpha2 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha2"
	"github.com/stuttgart-things/sops-secrets-operator/internal/decrypt"
	"github.com/stuttgart-things/sops-secrets-operator/internal/keyresolve"
	"github.com/stuttgart-things/sops-secrets-operator/internal/source"
	"github.com/stuttgart-things/sops-secrets-operator/internal/transform"
)

// Field indexers on SopsSecret. They split GitRepository-backed and
// ObjectSource-backed CRs so each Watches() can map only the dependents
// that reference the changed source.
const (
	SopsSecretGitRefIndex    = ".spec.source.sourceRef.git.name"
	SopsSecretObjectRefIndex = ".spec.source.sourceRef.object.name"
)

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

	var ss sopsv1alpha2.SopsSecret
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

	// Resolve the source CR by sourceRef.Kind, fetch the encrypted bytes
	// plus a "revision" string (commit SHA for git, ETag for object).
	content, revision, srcErr := r.fetchSource(ctx, &ss)
	if srcErr != nil {
		setStage(StageFetch)
		return r.failStatus(ctx, &ss, sopsv1alpha2.ConditionSourceReady, srcErr.reason, srcErr.msg)
	}
	setCondition(&ss.Status.Conditions, sopsv1alpha2.ConditionSourceReady, metav1.ConditionTrue, "Ready", "source is ready")

	ageKey, err := keyresolve.Age(ctx, r.Client, ss.Namespace, keyresolve.SecretKeyRef{
		Name: ss.Spec.Decryption.KeyRef.Name,
		Key:  ss.Spec.Decryption.KeyRef.Key,
	})
	if err != nil {
		setStage(StageDecrypt)
		return r.failStatus(ctx, &ss, sopsv1alpha2.ConditionDecrypted, "KeyResolveFailed", err.Error())
	}
	plaintext, err := decrypt.DecryptAge(content, ss.Spec.Source.Path, ageKey)
	if err != nil {
		setStage(StageDecrypt)
		return r.failStatus(ctx, &ss, sopsv1alpha2.ConditionDecrypted, "DecryptFailed", err.Error())
	}

	flat, err := transform.ParseFlatYAML(plaintext)
	if err != nil {
		setStage(StageDecrypt)
		return r.failStatus(ctx, &ss, sopsv1alpha2.ConditionDecrypted, "ParseFailed", err.Error())
	}
	data, err := transform.ApplyMapping(flat, convertSpecDataMappings(ss.Spec.Data))
	if err != nil {
		setStage(StageDecrypt)
		return r.failStatus(ctx, &ss, sopsv1alpha2.ConditionDecrypted, "MappingFailed", err.Error())
	}
	setCondition(&ss.Status.Conditions, sopsv1alpha2.ConditionDecrypted, metav1.ConditionTrue, "Decrypted", "decryption + mapping ok")

	hash := transform.HashSecretData(data)
	if err := r.applyTargetSecret(ctx, &ss, data, hash, revision); err != nil {
		log.Error(err, "apply target secret failed")
		setStage(StageApply)
		return r.failStatus(ctx, &ss, sopsv1alpha2.ConditionApplied, "ApplyFailed", err.Error())
	}
	setCondition(&ss.Status.Conditions, sopsv1alpha2.ConditionApplied, metav1.ConditionTrue, "Applied",
		fmt.Sprintf("applied %d keys", len(data)))

	ss.Status.LastAppliedHash = hash
	ss.Status.LastSyncedCommit = revision
	ss.Status.LastProcessedReconcileToken = ss.Annotations[ReconcileRequestAnnotation]
	ss.Status.ObservedGeneration = ss.Generation
	if err := r.Status().Update(ctx, &ss); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// sourceFetchError carries a stable reason + message for failStatus.
type sourceFetchError struct {
	reason string
	msg    string
}

func (e *sourceFetchError) Error() string { return e.msg }

// fetchSource resolves spec.source.sourceRef.kind to either a GitRepository
// or ObjectSource, verifies its readiness, and reads the encrypted file
// from the Registry. The "revision" is the git commit SHA or object ETag
// observed at read time.
//
// Force-sync at the consumer level (an annotation on the SopsSecret itself)
// only marks the next reconcile as "honored": every reconcile already runs
// the full read/decrypt/apply pipeline, so there is nothing to skip. To
// force a fresh upstream fetch, annotate the source CR — those reconcilers
// invalidate the cache before EnsureCached / EnsureObjectCached runs again.
func (r *SopsSecretReconciler) fetchSource(ctx context.Context, ss *sopsv1alpha2.SopsSecret) ([]byte, string, *sourceFetchError) {
	kind := ss.Spec.Source.SourceRef.Kind
	name := ss.Spec.Source.SourceRef.Name
	path := ss.Spec.Source.Path
	srcKey := client.ObjectKey{Namespace: ss.Namespace, Name: name}

	switch kind {
	case sopsv1alpha2.SourceKindGitRepository:
		// Fetch the storage version (v1alpha1) so envtest can run without
		// a conversion webhook server. v1alpha1 and v1alpha2 GitRepository
		// schemas are isomorphic, so this is purely a wire-format choice.
		var repo sopsv1alpha1.GitRepository
		if err := r.Get(ctx, srcKey, &repo); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, "", &sourceFetchError{"SourceMissing", fmt.Sprintf("GitRepository %q not found", name)}
			}
			return nil, "", &sourceFetchError{"SourceMissing", err.Error()}
		}
		if !isGitSourceReady(&repo) {
			return nil, "", &sourceFetchError{"SourceNotReady", fmt.Sprintf("GitRepository %q is not ready", name)}
		}
		content, sha, err := r.Registry.Read(srcKey, path)
		if err != nil {
			return nil, "", &sourceFetchError{"ReadFailed", err.Error()}
		}
		return content, sha, nil

	case sopsv1alpha2.SourceKindObjectSource:
		var os sopsv1alpha2.ObjectSource
		if err := r.Get(ctx, srcKey, &os); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, "", &sourceFetchError{"SourceMissing", fmt.Sprintf("ObjectSource %q not found", name)}
			}
			return nil, "", &sourceFetchError{"SourceMissing", err.Error()}
		}
		if !isObjectSourceReady(&os) {
			return nil, "", &sourceFetchError{"SourceNotReady", fmt.Sprintf("ObjectSource %q is not ready", name)}
		}
		content, etag, err := r.Registry.ReadObject(ctx, srcKey, path)
		if err != nil {
			return nil, "", &sourceFetchError{"ReadFailed", err.Error()}
		}
		return content, etag, nil

	default:
		return nil, "", &sourceFetchError{"UnknownSourceKind", fmt.Sprintf("unsupported sourceRef.kind %q", kind)}
	}
}

func (r *SopsSecretReconciler) applyTargetSecret(ctx context.Context, ss *sopsv1alpha2.SopsSecret, data map[string][]byte, hash, revision string) error {
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

		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels[ManagedByLabel] = ManagedByValue

		if secret.Annotations == nil {
			secret.Annotations = map[string]string{}
		}
		secret.Annotations[OwnerAnnotation] = ownerKey
		secret.Annotations[OwnerUIDAnnotation] = string(ss.UID)
		secret.Annotations[ContentHashAnnotation] = hash
		secret.Annotations[SourceCommitAnnotation] = revision

		if ss.Spec.Target.Type != "" {
			secret.Type = ss.Spec.Target.Type
		} else if secret.Type == "" {
			secret.Type = corev1.SecretTypeOpaque
		}

		secret.Data = data
		return nil
	})
	return err
}

func (r *SopsSecretReconciler) deleteOwnedSecret(ctx context.Context, ss *sopsv1alpha2.SopsSecret, name, namespace string) error {
	var sec corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &sec); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if sec.Labels[ManagedByLabel] != ManagedByValue {
		return nil
	}
	ownerKey := fmt.Sprintf("SopsSecret/%s/%s", ss.Namespace, ss.Name)
	if sec.Annotations[OwnerAnnotation] != ownerKey {
		return nil
	}
	return client.IgnoreNotFound(r.Delete(ctx, &sec))
}

func (r *SopsSecretReconciler) failStatus(ctx context.Context, ss *sopsv1alpha2.SopsSecret, condType, reason, msg string) (ctrl.Result, error) {
	setCondition(&ss.Status.Conditions, condType, metav1.ConditionFalse, reason, msg)
	if err := r.Status().Update(ctx, ss); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: retryAfter}, nil
}

func targetName(ss *sopsv1alpha2.SopsSecret) string {
	if ss.Spec.Target.Name != "" {
		return ss.Spec.Target.Name
	}
	return ss.Name
}

func targetNamespace(ss *sopsv1alpha2.SopsSecret) string {
	if ss.Spec.Target.Namespace != "" {
		return ss.Spec.Target.Namespace
	}
	return ss.Namespace
}

func isGitSourceReady(repo *sopsv1alpha1.GitRepository) bool {
	for _, c := range repo.Status.Conditions {
		if c.Type == sopsv1alpha1.ConditionSourceReady {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

func isObjectSourceReady(os *sopsv1alpha2.ObjectSource) bool {
	for _, c := range os.Status.Conditions {
		if c.Type == ObjectConditionSourceReady {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

func convertSpecDataMappings(in []sopsv1alpha2.DataMapping) []transform.DataMapping {
	if len(in) == 0 {
		return nil
	}
	out := make([]transform.DataMapping, len(in))
	for i, m := range in {
		out[i] = transform.DataMapping{Key: m.Key, From: m.From}
	}
	return out
}

func (r *SopsSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&sopsv1alpha2.SopsSecret{},
		SopsSecretGitRefIndex,
		func(obj client.Object) []string {
			s := obj.(*sopsv1alpha2.SopsSecret)
			if s.Spec.Source.SourceRef.Kind != sopsv1alpha2.SourceKindGitRepository {
				return nil
			}
			return []string{s.Spec.Source.SourceRef.Name}
		},
	); err != nil {
		return err
	}
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&sopsv1alpha2.SopsSecret{},
		SopsSecretObjectRefIndex,
		func(obj client.Object) []string {
			s := obj.(*sopsv1alpha2.SopsSecret)
			if s.Spec.Source.SourceRef.Kind != sopsv1alpha2.SourceKindObjectSource {
				return nil
			}
			return []string{s.Spec.Source.SourceRef.Name}
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&sopsv1alpha2.SopsSecret{}).
		Watches(&sopsv1alpha1.GitRepository{}, handler.EnqueueRequestsFromMapFunc(r.mapGitRepoToSopsSecrets)).
		Watches(&sopsv1alpha2.ObjectSource{}, handler.EnqueueRequestsFromMapFunc(r.mapObjectSourceToSopsSecrets)).
		Named("sopssecret").
		Complete(r)
}

func (r *SopsSecretReconciler) mapGitRepoToSopsSecrets(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.mapSourceToSopsSecrets(ctx, obj, SopsSecretGitRefIndex)
}

func (r *SopsSecretReconciler) mapObjectSourceToSopsSecrets(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.mapSourceToSopsSecrets(ctx, obj, SopsSecretObjectRefIndex)
}

func (r *SopsSecretReconciler) mapSourceToSopsSecrets(ctx context.Context, obj client.Object, index string) []reconcile.Request {
	var list sopsv1alpha2.SopsSecretList
	if err := r.List(ctx, &list,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{index: obj.GetName()},
	); err != nil {
		return nil
	}
	out := make([]reconcile.Request, 0, len(list.Items))
	for _, s := range list.Items {
		out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&s)})
	}
	return out
}
