package controller

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/stuttgart-things/sops-secrets-operator/internal/keyresolve"
	"github.com/stuttgart-things/sops-secrets-operator/internal/secretref"
)

// CredentialPolicy is the operator-level credential configuration shared by
// every reconciler: which namespaces a CR may reach into, and which Secret
// to fall back on when a CR names none.
//
// Embedding it keeps the wiring in cmd/main.go to one literal per
// reconciler, and means the zero value — no cross-namespace refs, no
// global defaults — is the pre-#47/#48 behaviour.
type CredentialPolicy struct {
	// SecretRefs gates cross-namespace Secret references.
	SecretRefs secretref.Resolver

	// GlobalAgeKey is the age key used by CRs that omit
	// spec.decryption.keyRef. Nil means such CRs fail, as before.
	GlobalAgeKey *secretref.Global

	// GlobalGitAuth is the credential used by GitRepositories whose
	// spec.auth omits secretRef. Nil means such CRs fail.
	GlobalGitAuth *secretref.Global

	// GlobalObjectAuth is the same for ObjectSource.
	GlobalObjectAuth *secretref.Global
}

// ageKeyRef reads a CR's decryption block into the neutral form the
// resolver takes. A nil block yields the zero Ref, which the resolver reads
// as "fall back to the operator's global key, if any".
//
// Takes the two fields rather than the API struct so it works for both
// api versions without either of them importing the other.
func ageKeyRef(name, key, namespace string) secretref.Ref {
	return secretref.Ref{Namespace: namespace, Name: name, Key: key}
}

// resolveAgeKey resolves a CR's decryption key reference against the
// operator's policy and reads the key material.
//
// Returns the origin alongside the key so the caller can record on the CR's
// status which key was actually used — without that, a tenant silently
// falling back to the shared cluster key looks identical to one using its
// own (#47).
func (p CredentialPolicy) resolveAgeKey(
	ctx context.Context,
	c client.Client,
	crNamespace string,
	ref secretref.Ref,
) ([]byte, secretref.Origin, error) {
	res, err := p.SecretRefs.Resolve(crNamespace, ref, p.GlobalAgeKey)
	if err != nil {
		return nil, "", fmt.Errorf("resolve age key: %w", err)
	}
	key, err := keyresolve.Age(ctx, c, res.Namespace, keyresolve.SecretKeyRef{
		Name: res.Name,
		Key:  res.DataKey,
	})
	if err != nil {
		return nil, "", err
	}
	return key, res.Origin, nil
}

// describeAuthOrigin renders the auth-credential origin for a status
// condition message. The empty Origin means the resource asked for no
// authentication at all.
func describeAuthOrigin(o secretref.Origin) string {
	switch o {
	case secretref.OriginGlobal:
		return "auth resolved from the operator's global credential"
	case secretref.OriginCrossNamespace:
		return "auth resolved from a credential in another namespace"
	case secretref.OriginLocal:
		return "auth resolved"
	default:
		return "no authentication configured"
	}
}

// describeKeyOrigin renders the key origin for a status condition message.
func describeKeyOrigin(o secretref.Origin) string {
	switch o {
	case secretref.OriginGlobal:
		return "using the operator's global age key"
	case secretref.OriginCrossNamespace:
		return "using an age key from another namespace"
	default:
		return "using the resource's own age key"
	}
}
