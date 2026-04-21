// Package keyresolve resolves age private keys from Kubernetes Secret
// references. Kept separate from the controller so it is testable without
// an envtest harness.
package keyresolve

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sopsv1alpha1 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha1"
)

// Age reads the age private key bytes from the named Secret+key in the
// given namespace. Returns an error if the Secret or key is missing, or
// if the key value is empty.
func Age(ctx context.Context, c client.Client, namespace string, ref sopsv1alpha1.SecretKeyRef) ([]byte, error) {
	var sec corev1.Secret
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: ref.Name}, &sec); err != nil {
		return nil, fmt.Errorf("get age-key secret %q: %w", ref.Name, err)
	}
	val, ok := sec.Data[ref.Key]
	if !ok {
		return nil, fmt.Errorf("secret %q has no key %q", ref.Name, ref.Key)
	}
	if len(val) == 0 {
		return nil, fmt.Errorf("age key at %q/%q is empty", ref.Name, ref.Key)
	}
	return val, nil
}
