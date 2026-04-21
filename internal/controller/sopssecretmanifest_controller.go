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

// SopsSecretManifestRepoRefIndex is a field index on
// SopsSecretManifest.spec.source.repositoryRef.name.
const SopsSecretManifestRepoRefIndex = ".spec.source.repositoryRef.name.manifest"

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

	var sm sopsv1alpha1.SopsSecretManifest
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

	// Source GitRepository must be ready.
	var repo sopsv1alpha1.GitRepository
	repoKey := client.ObjectKey{Namespace: sm.Namespace, Name: sm.Spec.Source.RepositoryRef.Name}
	if err := r.Get(ctx, repoKey, &repo); err != nil {
		msg := err.Error()
		if apierrors.IsNotFound(err) {
			msg = fmt.Sprintf("GitRepository %q not found", sm.Spec.Source.RepositoryRef.Name)
		}
		return r.failManifestStatus(ctx, &sm, sopsv1alpha1.ConditionSourceReady, "SourceMissing", msg)
	}
	if !isSourceReady(&repo) {
		return r.failManifestStatus(ctx, &sm, sopsv1alpha1.ConditionSourceReady, "SourceNotReady",
			fmt.Sprintf("GitRepository %q is not ready", repo.Name))
	}
	setCondition(&sm.Status.Conditions, sopsv1alpha1.ConditionSourceReady, metav1.ConditionTrue, "Ready", "source is ready")

	// Read + decrypt.
	content, commitSHA, err := r.Registry.Read(repoKey, sm.Spec.Source.Path)
	if err != nil {
		return r.failManifestStatus(ctx, &sm, sopsv1alpha1.ConditionSourceReady, "ReadFailed", err.Error())
	}
	ageKey, err := keyresolve.Age(ctx, r.Client, sm.Namespace, sm.Spec.Decryption.KeyRef)
	if err != nil {
		return r.failManifestStatus(ctx, &sm, sopsv1alpha1.ConditionDecrypted, "KeyResolveFailed", err.Error())
	}
	plaintext, err := decrypt.DecryptAge(content, sm.Spec.Source.Path, ageKey)
	if err != nil {
		return r.failManifestStatus(ctx, &sm, sopsv1alpha1.ConditionDecrypted, "DecryptFailed", err.Error())
	}

	// Parse decrypted manifest into a Secret, enforcing the whitelist.
	parsed, err := transform.ParseManifest(plaintext)
	if err != nil {
		return r.failManifestStatus(ctx, &sm, sopsv1alpha1.ConditionDecrypted, "ParseFailed", err.Error())
	}
	transform.NormalizeSecretData(parsed)

	// Resolve target identity. Namespace is authoritative from the CR;
	// whatever the manifest claimed is ignored.
	targetNS := sm.Spec.Target.Namespace
	if targetNS == "" {
		targetNS = sm.Namespace
	}
	targetName := sm.Spec.Target.NameOverride
	if targetName == "" {
		targetName = parsed.Name
	}
	if targetName == "" {
		return r.failManifestStatus(ctx, &sm, sopsv1alpha1.ConditionDecrypted, "NameMissing",
			"manifest has no metadata.name and target.nameOverride is not set")
	}
	setCondition(&sm.Status.Conditions, sopsv1alpha1.ConditionDecrypted, metav1.ConditionTrue, "Decrypted", "decryption + validation ok")

	hash := transform.HashManifestSecret(parsed)
	if err := r.applyManifestSecret(ctx, &sm, parsed, targetName, targetNS, hash, commitSHA); err != nil {
		log.Error(err, "apply manifest secret failed")
		return r.failManifestStatus(ctx, &sm, sopsv1alpha1.ConditionApplied, "ApplyFailed", err.Error())
	}
	setCondition(&sm.Status.Conditions, sopsv1alpha1.ConditionApplied, metav1.ConditionTrue, "Applied",
		fmt.Sprintf("applied Secret %s/%s", targetNS, targetName))

	sm.Status.LastAppliedHash = hash
	sm.Status.LastSyncedCommit = commitSHA
	sm.Status.ObservedGeneration = sm.Generation
	if err := r.Status().Update(ctx, &sm); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *SopsSecretManifestReconciler) applyManifestSecret(
	ctx context.Context,
	sm *sopsv1alpha1.SopsSecretManifest,
	parsed *corev1.Secret,
	name, namespace, hash, commitSHA string,
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

		// Preserve user labels/annotations from the decrypted manifest,
		// then overlay our ownership markers (ours win on conflict).
		if out.Labels == nil {
			out.Labels = map[string]string{}
		}
		for k, v := range parsed.Labels {
			out.Labels[k] = v
		}
		out.Labels[ManagedByLabel] = ManagedByValue

		if out.Annotations == nil {
			out.Annotations = map[string]string{}
		}
		for k, v := range parsed.Annotations {
			out.Annotations[k] = v
		}
		out.Annotations[OwnerAnnotation] = ownerKey
		out.Annotations[OwnerUIDAnnotation] = string(sm.UID)
		out.Annotations[ContentHashAnnotation] = hash
		out.Annotations[SourceCommitAnnotation] = commitSHA

		out.Type = parsed.Type
		if out.Type == "" {
			out.Type = corev1.SecretTypeOpaque
		}
		// Data is authoritative — replace wholesale.
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
func (r *SopsSecretManifestReconciler) deleteOwnedSecretIfKnown(ctx context.Context, sm *sopsv1alpha1.SopsSecretManifest) error {
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
	ctx context.Context, sm *sopsv1alpha1.SopsSecretManifest,
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
		&sopsv1alpha1.SopsSecretManifest{},
		SopsSecretManifestRepoRefIndex,
		func(obj client.Object) []string {
			s := obj.(*sopsv1alpha1.SopsSecretManifest)
			return []string{s.Spec.Source.RepositoryRef.Name}
		},
	); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&sopsv1alpha1.SopsSecretManifest{}).
		Watches(&sopsv1alpha1.GitRepository{}, handler.EnqueueRequestsFromMapFunc(r.mapRepoToSopsSecretManifests)).
		Named("sopssecretmanifest").
		Complete(r)
}

func (r *SopsSecretManifestReconciler) mapRepoToSopsSecretManifests(ctx context.Context, obj client.Object) []reconcile.Request {
	var list sopsv1alpha1.SopsSecretManifestList
	if err := r.List(ctx, &list,
		client.InNamespace(obj.GetNamespace()),
		client.MatchingFields{SopsSecretManifestRepoRefIndex: obj.GetName()},
	); err != nil {
		return nil
	}
	out := make([]reconcile.Request, 0, len(list.Items))
	for _, s := range list.Items {
		out = append(out, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&s)})
	}
	return out
}
