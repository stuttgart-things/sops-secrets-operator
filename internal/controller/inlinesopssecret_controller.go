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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	sopsv1alpha1 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha1"
	"github.com/stuttgart-things/sops-secrets-operator/internal/decrypt"
	"github.com/stuttgart-things/sops-secrets-operator/internal/keyresolve"
	"github.com/stuttgart-things/sops-secrets-operator/internal/transform"
)

// inlineSopsDecryptPath is fed to the SOPS format detector. The content is
// always treated as YAML; the literal name does not reach disk.
const inlineSopsDecryptPath = "inline.yaml"

// InlineSopsSecretReconciler reconciles InlineSopsSecret objects.
type InlineSopsSecretReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=inlinesopssecrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=inlinesopssecrets/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=sops.stuttgart-things.com,resources=inlinesopssecrets/finalizers,verbs=update

func (r *InlineSopsSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx).WithValues("inlinesopssecret", req.NamespacedName)
	setStage, finish := trackReconcile("InlineSopsSecret")
	defer finish()

	var is sopsv1alpha1.InlineSopsSecret
	if err := r.Get(ctx, req.NamespacedName, &is); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Finalizer handling.
	if is.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(&is, Finalizer) {
			controllerutil.AddFinalizer(&is, Finalizer)
			if err := r.Update(ctx, &is); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{Requeue: true}, nil
		}
	} else {
		if controllerutil.ContainsFinalizer(&is, Finalizer) {
			if err := r.deleteOwnedSecretInline(ctx, &is); err != nil {
				return ctrl.Result{}, err
			}
			controllerutil.RemoveFinalizer(&is, Finalizer)
			if err := r.Update(ctx, &is); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Resolve age key and decrypt the inline payload.
	ageKey, err := keyresolve.Age(ctx, r.Client, is.Namespace, is.Spec.Decryption.KeyRef)
	if err != nil {
		setStage(StageDecrypt)
		return r.failInlineStatus(ctx, &is, sopsv1alpha1.ConditionDecrypted, "KeyResolveFailed", err.Error())
	}
	plaintext, err := decrypt.DecryptAge([]byte(is.Spec.EncryptedYAML), inlineSopsDecryptPath, ageKey)
	if err != nil {
		setStage(StageDecrypt)
		return r.failInlineStatus(ctx, &is, sopsv1alpha1.ConditionDecrypted, "DecryptFailed", err.Error())
	}

	// Branch on mode: Mapping vs Manifest.
	switch is.Spec.Mode {
	case sopsv1alpha1.InlineModeMapping:
		flat, err := transform.ParseFlatYAML(plaintext)
		if err != nil {
			setStage(StageDecrypt)
			return r.failInlineStatus(ctx, &is, sopsv1alpha1.ConditionDecrypted, "ParseFailed", err.Error())
		}
		data, err := transform.ApplyMapping(flat, is.Spec.Data)
		if err != nil {
			setStage(StageDecrypt)
			return r.failInlineStatus(ctx, &is, sopsv1alpha1.ConditionDecrypted, "MappingFailed", err.Error())
		}
		setCondition(&is.Status.Conditions, sopsv1alpha1.ConditionDecrypted, metav1.ConditionTrue, "Decrypted", "decryption + mapping ok")

		hash := transform.HashSecretData(data)
		if err := r.applyInlineMappingSecret(ctx, &is, data, hash); err != nil {
			log.Error(err, "apply inline mapping secret failed")
			setStage(StageApply)
			return r.failInlineStatus(ctx, &is, sopsv1alpha1.ConditionApplied, "ApplyFailed", err.Error())
		}
		setCondition(&is.Status.Conditions, sopsv1alpha1.ConditionApplied, metav1.ConditionTrue, "Applied",
			fmt.Sprintf("applied %d keys", len(data)))
		is.Status.LastAppliedHash = hash

	case sopsv1alpha1.InlineModeManifest:
		parsed, err := transform.ParseManifest(plaintext)
		if err != nil {
			setStage(StageDecrypt)
			return r.failInlineStatus(ctx, &is, sopsv1alpha1.ConditionDecrypted, "ParseFailed", err.Error())
		}
		transform.NormalizeSecretData(parsed)

		name := is.Spec.Target.Name
		if name == "" {
			name = parsed.Name
		}
		if name == "" {
			setStage(StageDecrypt)
			return r.failInlineStatus(ctx, &is, sopsv1alpha1.ConditionDecrypted, "NameMissing",
				"manifest has no metadata.name and spec.target.name is not set")
		}
		ns := is.Spec.Target.Namespace
		if ns == "" {
			ns = is.Namespace
		}
		setCondition(&is.Status.Conditions, sopsv1alpha1.ConditionDecrypted, metav1.ConditionTrue, "Decrypted", "decryption + validation ok")

		hash := transform.HashManifestSecret(parsed)
		if err := r.applyInlineManifestSecret(ctx, &is, parsed, name, ns, hash); err != nil {
			log.Error(err, "apply inline manifest secret failed")
			setStage(StageApply)
			return r.failInlineStatus(ctx, &is, sopsv1alpha1.ConditionApplied, "ApplyFailed", err.Error())
		}
		setCondition(&is.Status.Conditions, sopsv1alpha1.ConditionApplied, metav1.ConditionTrue, "Applied",
			fmt.Sprintf("applied Secret %s/%s", ns, name))
		is.Status.LastAppliedHash = hash

	default:
		setStage(StageDecrypt)
		return r.failInlineStatus(ctx, &is, sopsv1alpha1.ConditionDecrypted, "InvalidMode",
			fmt.Sprintf("unknown mode %q", is.Spec.Mode))
	}

	is.Status.ObservedGeneration = is.Generation
	if err := r.Status().Update(ctx, &is); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *InlineSopsSecretReconciler) applyInlineMappingSecret(ctx context.Context, is *sopsv1alpha1.InlineSopsSecret, data map[string][]byte, hash string) error {
	name := is.Spec.Target.Name
	if name == "" {
		name = is.Name
	}
	ns := is.Spec.Target.Namespace
	if ns == "" {
		ns = is.Namespace
	}

	secret := &corev1.Secret{}
	secret.Name = name
	secret.Namespace = ns

	ownerKey := fmt.Sprintf("InlineSopsSecret/%s/%s", is.Namespace, is.Name)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if secret.ResourceVersion != "" {
			managedBy := secret.Labels[ManagedByLabel]
			existingOwner := secret.Annotations[OwnerAnnotation]
			switch {
			case managedBy == "" && !is.Spec.Target.Adopt:
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
		secret.Annotations[OwnerUIDAnnotation] = string(is.UID)
		secret.Annotations[ContentHashAnnotation] = hash

		if is.Spec.Target.Type != "" {
			secret.Type = is.Spec.Target.Type
		} else if secret.Type == "" {
			secret.Type = corev1.SecretTypeOpaque
		}
		secret.Data = data
		return nil
	})
	return err
}

func (r *InlineSopsSecretReconciler) applyInlineManifestSecret(
	ctx context.Context,
	is *sopsv1alpha1.InlineSopsSecret,
	parsed *corev1.Secret,
	name, namespace, hash string,
) error {
	out := &corev1.Secret{}
	out.Name = name
	out.Namespace = namespace

	ownerKey := fmt.Sprintf("InlineSopsSecret/%s/%s", is.Namespace, is.Name)

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, out, func() error {
		if out.ResourceVersion != "" {
			managedBy := out.Labels[ManagedByLabel]
			existingOwner := out.Annotations[OwnerAnnotation]
			switch {
			case managedBy == "" && !is.Spec.Target.Adopt:
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
		out.Annotations[OwnerUIDAnnotation] = string(is.UID)
		out.Annotations[ContentHashAnnotation] = hash

		switch {
		case is.Spec.Target.Type != "":
			out.Type = is.Spec.Target.Type
		case parsed.Type != "":
			out.Type = parsed.Type
		default:
			out.Type = corev1.SecretTypeOpaque
		}
		out.Data = parsed.Data
		out.StringData = nil
		return nil
	})
	return err
}

// deleteOwnedSecretInline deletes the target Secret on CR finalization.
// For Mapping mode or when target.name is set we know the exact name; for
// Manifest mode without an explicit name we scan managed Secrets by
// owner annotation.
func (r *InlineSopsSecretReconciler) deleteOwnedSecretInline(ctx context.Context, is *sopsv1alpha1.InlineSopsSecret) error {
	ns := is.Spec.Target.Namespace
	if ns == "" {
		ns = is.Namespace
	}
	ownerKey := fmt.Sprintf("InlineSopsSecret/%s/%s", is.Namespace, is.Name)

	name := is.Spec.Target.Name
	if name == "" && is.Spec.Mode == sopsv1alpha1.InlineModeMapping {
		name = is.Name
	}
	if name != "" {
		return r.deleteIfOwnedInline(ctx, name, ns, ownerKey)
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

func (r *InlineSopsSecretReconciler) deleteIfOwnedInline(ctx context.Context, name, namespace, ownerKey string) error {
	var sec corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &sec); err != nil {
		return client.IgnoreNotFound(err)
	}
	if sec.Labels[ManagedByLabel] != ManagedByValue || sec.Annotations[OwnerAnnotation] != ownerKey {
		return nil
	}
	return client.IgnoreNotFound(r.Delete(ctx, &sec))
}

func (r *InlineSopsSecretReconciler) failInlineStatus(
	ctx context.Context, is *sopsv1alpha1.InlineSopsSecret,
	condType, reason, msg string,
) (ctrl.Result, error) {
	setCondition(&is.Status.Conditions, condType, metav1.ConditionFalse, reason, msg)
	if err := r.Status().Update(ctx, is); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: retryAfter}, nil
}

func (r *InlineSopsSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&sopsv1alpha1.InlineSopsSecret{}).
		Named("inlinesopssecret").
		Complete(r)
}
