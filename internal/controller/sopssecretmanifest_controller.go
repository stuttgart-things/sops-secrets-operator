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
	"maps"

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

const (
	SopsSecretManifestGitRefIndex    = ".spec.source.sourceRef.git.name.manifest"
	SopsSecretManifestObjectRefIndex = ".spec.source.sourceRef.object.name.manifest"
)

// SopsSecretManifestReconciler reconciles SopsSecretManifest objects (pass-through mode).
type SopsSecretManifestReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Registry *source.Registry
}

// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=sopssecretmanifests,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=sopssecretmanifests/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=sopssecretmanifests/finalizers,verbs=update

func (r *SopsSecretManifestReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("sopssecretmanifest", req.NamespacedName)
	setStage, finish := trackReconcile("SopsSecretManifest")
	defer finish()

	var sm sopsv1alpha2.SopsSecretManifest
	if err := r.Get(ctx, req.NamespacedName, &sm); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Finalizer handling.
	if sm.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&sm, Finalizer) {
			controllerutil.AddFinalizer(&sm, Finalizer)
			if err := r.Update(ctx, &sm); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
	} else {
		if controllerutil.ContainsFinalizer(&sm, Finalizer) {
			if err := r.deleteOwnedSecretIfKnown(ctx, &sm); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&sm, Finalizer)
			if err := r.Update(ctx, &sm); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	content, revision, srcErr := r.fetchManifestSource(ctx, &sm)
	if srcErr != nil {
		setStage(StageFetch)
		return r.failManifestStatus(ctx, &sm, sopsv1alpha2.ConditionSourceReady, srcErr.reason, srcErr.msg)
	}
	setCondition(&sm.Status.Conditions, sopsv1alpha2.ConditionSourceReady, metav1.ConditionTrue, "Ready", "source is ready")

	ageKey, err := keyresolve.Age(ctx, r.Client, sm.Namespace, keyresolve.SecretKeyRef{
		Name: sm.Spec.Decryption.KeyRef.Name,
		Key:  sm.Spec.Decryption.KeyRef.Key,
	})
	if err != nil {
		setStage(StageDecrypt)
		return r.failManifestStatus(ctx, &sm, sopsv1alpha2.ConditionDecrypted, "KeyResolveFailed", err.Error())
	}
	plaintext, err := decrypt.DecryptAge(content, sm.Spec.Source.Path, ageKey)
	if err != nil {
		setStage(StageDecrypt)
		return r.failManifestStatus(ctx, &sm, sopsv1alpha2.ConditionDecrypted, "DecryptFailed", err.Error())
	}

	parsed, err := transform.ParseManifest(plaintext)
	if err != nil {
		setStage(StageDecrypt)
		return r.failManifestStatus(ctx, &sm, sopsv1alpha2.ConditionDecrypted, "ParseFailed", err.Error())
	}
	transform.NormalizeSecretData(parsed)

	targetNS := sm.Spec.Target.Namespace
	if targetNS == "" {
		targetNS = sm.Namespace
	}
	targetName := sm.Spec.Target.NameOverride
	if targetName == "" {
		targetName = parsed.Name
	}
	if targetName == "" {
		setStage(StageDecrypt)
		return r.failManifestStatus(ctx, &sm, sopsv1alpha2.ConditionDecrypted, "NameMissing",
			"manifest has no metadata.name and target.nameOverride is not set")
	}
	setCondition(&sm.Status.Conditions, sopsv1alpha2.ConditionDecrypted, metav1.ConditionTrue, "Decrypted", "decryption + validation ok")

	hash := transform.HashManifestSecret(parsed)
	if err := r.applyManifestSecret(ctx, &sm, parsed, targetName, targetNS, hash, revision); err != nil {
		log.Error(err, "apply manifest secret failed")
		setStage(StageApply)
		return r.failManifestStatus(ctx, &sm, sopsv1alpha2.ConditionApplied, "ApplyFailed", err.Error())
	}
	setCondition(&sm.Status.Conditions, sopsv1alpha2.ConditionApplied, metav1.ConditionTrue, "Applied",
		fmt.Sprintf("applied Secret %s/%s", targetNS, targetName))

	sm.Status.LastAppliedHash = hash
	sm.Status.LastSyncedCommit = revision
	sm.Status.LastProcessedReconcileToken = sm.Annotations[ReconcileRequestAnnotation]
	sm.Status.ObservedGeneration = sm.Generation
	if err := r.Status().Update(ctx, &sm); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *SopsSecretManifestReconciler) fetchManifestSource(ctx context.Context, sm *sopsv1alpha2.SopsSecretManifest) ([]byte, string, *sourceFetchError) {
	kind := sm.Spec.Source.SourceRef.Kind
	name := sm.Spec.Source.SourceRef.Name
	path := sm.Spec.Source.Path
	srcKey := client.ObjectKey{Namespace: sm.Namespace, Name: name}

	switch kind {
	case sopsv1alpha2.SourceKindGitRepository:
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

func (r *SopsSecretManifestReconciler) applyManifestSecret(
	ctx context.Context,
	sm *sopsv1alpha2.SopsSecretManifest,
	parsed *corev1.Secret,
	name, namespace, hash, revision string,
) error {
	out := &corev1.Secret{}
	out.Name = name
	out.Namespace = namespace

	ownerKey := fmt.Sprintf("SopsSecretManifest/%s/%s", sm.Namespace, sm.Name)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, out, func() error {
		if out.ResourceVersion != "" {
			managedBy := out.Labels[ManagedByLabel]
			existingOwner := out.Annotations[OwnerAnnotation]
			switch {
			case managedBy == "" && !sm.Spec.Target.Adopt:
				return fmt.Errorf("target Secret %s/%s exists but is not managed by this operator; set target.adopt=true to take over",
					out.Namespace, out.Name)
			case managedBy == ManagedByValue && existingOwner != "" && existingOwner != ownerKey:
				return fmt.Errorf("target Secret %s/%s is already owned by %q", out.Namespace, out.Name, existingOwner)
			}
		}

		if out.Labels == nil {
			out.Labels = map[string]string{}
		}
		maps.Copy(out.Labels, parsed.Labels)
		out.Labels[ManagedByLabel] = ManagedByValue

		if out.Annotations == nil {
			out.Annotations = map[string]string{}
		}
		maps.Copy(out.Annotations, parsed.Annotations)
		out.Annotations[OwnerAnnotation] = ownerKey
		out.Annotations[OwnerUIDAnnotation] = string(sm.UID)
		out.Annotations[ContentHashAnnotation] = hash
		out.Annotations[SourceCommitAnnotation] = revision

		out.Type = parsed.Type
		if out.Type == "" {
			out.Type = corev1.SecretTypeOpaque
		}
		out.Data = parsed.Data
		out.StringData = nil
		return nil
	})
	return err
}

// deleteOwnedSecretIfKnown deletes the target Secret on CR finalization.
// The decrypted manifest's name is unavailable here, so we either use
// target.nameOverride if set, or scan managed Secrets in the namespace
// for the one whose owner annotation matches this CR.
func (r *SopsSecretManifestReconciler) deleteOwnedSecretIfKnown(ctx context.Context, sm *sopsv1alpha2.SopsSecretManifest) error {
	ns := sm.Spec.Target.Namespace
	if ns == "" {
		ns = sm.Namespace
	}
	ownerKey := fmt.Sprintf("SopsSecretManifest/%s/%s", sm.Namespace, sm.Name)

	if sm.Spec.Target.NameOverride != "" {
		return r.deleteIfOwned(ctx, sm.Spec.Target.NameOverride, ns, ownerKey)
	}

	var list corev1.SecretList
	if err := r.List(ctx, &list,
		client.InNamespace(ns),
		client.MatchingLabels{ManagedByLabel: ManagedByValue},
	); err != nil {
		return err
	}
	for i := range list.Items {
		sec := &list.Items[i]
		if sec.Annotations[OwnerAnnotation] != ownerKey {
			continue
		}
		if err := client.IgnoreNotFound(r.Delete(ctx, sec)); err != nil {
			return err
		}
	}
	return nil
}

func (r *SopsSecretManifestReconciler) deleteIfOwned(ctx context.Context, name, namespace, ownerKey string) error {
	var sec corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &sec); err != nil {
		return client.IgnoreNotFound(err)
	}
	if sec.Labels[ManagedByLabel] != ManagedByValue || sec.Annotations[OwnerAnnotation] != ownerKey {
		return nil
	}
	return client.IgnoreNotFound(r.Delete(ctx, &sec))
}

func (r *SopsSecretManifestReconciler) failManifestStatus(
	ctx context.Context, sm *sopsv1alpha2.SopsSecretManifest,
	condType, reason, msg string,
) (ctrl.Result, error) {
	setCondition(&sm.Status.Conditions, condType, metav1.ConditionFalse, reason, msg)
	if err := r.Status().Update(ctx, sm); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: retryAfter}, nil
}

func (r *SopsSecretManifestReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if err := mgr.GetFieldIndexer().IndexField(
		context.Background(),
		&sopsv1alpha2.SopsSecretManifest{},
		SopsSecretManifestGitRefIndex,
		func(obj client.Object) []string {
			s := obj.(*sopsv1alpha2.SopsSecretManifest)
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
		&sopsv1alpha2.SopsSecretManifest{},
		SopsSecretManifestObjectRefIndex,
		func(obj client.Object) []string {
			s := obj.(*sopsv1alpha2.SopsSecretManifest)
			if s.Spec.Source.SourceRef.Kind != sopsv1alpha2.SourceKindObjectSource {
				return nil
			}
			return []string{s.Spec.Source.SourceRef.Name}
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&sopsv1alpha2.SopsSecretManifest{}).
		Watches(&sopsv1alpha1.GitRepository{}, handler.EnqueueRequestsFromMapFunc(r.mapGitRepoToManifests)).
		Watches(&sopsv1alpha2.ObjectSource{}, handler.EnqueueRequestsFromMapFunc(r.mapObjectSourceToManifests)).
		Named("sopssecretmanifest").
		Complete(r)
}

func (r *SopsSecretManifestReconciler) mapGitRepoToManifests(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.mapSourceToManifests(ctx, obj, SopsSecretManifestGitRefIndex)
}

func (r *SopsSecretManifestReconciler) mapObjectSourceToManifests(ctx context.Context, obj client.Object) []reconcile.Request {
	return r.mapSourceToManifests(ctx, obj, SopsSecretManifestObjectRefIndex)
}

func (r *SopsSecretManifestReconciler) mapSourceToManifests(ctx context.Context, obj client.Object, index string) []reconcile.Request {
	var list sopsv1alpha2.SopsSecretManifestList
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
