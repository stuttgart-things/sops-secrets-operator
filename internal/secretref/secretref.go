// Package secretref resolves the Secret a CR points at into a concrete
// namespace/name, applying the operator's cross-namespace policy and any
// operator-level default.
//
// Three credential kinds share this logic — the age decryption key (#47),
// GitRepository auth and ObjectSource auth (#48) — so the precedence rule
// and the security gate are written once and behave identically for all of
// them.
package secretref

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Origin records where a resolved reference came from. It is surfaced on
// status conditions so a cluster admin can tell a tenant that silently
// fell back to the shared credential from one that named its own.
type Origin string

const (
	// OriginLocal means the CR named a Secret in its own namespace.
	OriginLocal Origin = "local"
	// OriginCrossNamespace means the CR named a Secret in another
	// namespace and the operator permits that namespace.
	OriginCrossNamespace Origin = "cross-namespace"
	// OriginGlobal means the CR named nothing and the operator-level
	// default was used.
	OriginGlobal Origin = "global"
)

// ErrNoReference is returned when a CR omits its reference and no
// operator-level default is configured.
var ErrNoReference = errors.New("no secret reference on the resource and no operator-level default configured")

// Global is an operator-level default Secret, configured by flag. Because
// only the cluster admin can set it, it is exempt from the cross-namespace
// allowlist — the allowlist exists to stop *tenants* from reaching into
// namespaces, not to constrain the admin's own configuration.
type Global struct {
	Namespace string
	Name      string
	// Key is the entry within the Secret. Only meaningful for the age
	// key; the auth resolvers read a fixed set of keys and ignore it.
	Key string
}

// ParseGlobal parses a "<namespace>/<name>" flag value. An empty value
// means "not configured" and yields (nil, nil) — the caller keeps today's
// behaviour of requiring a reference on the CR.
func ParseGlobal(value, dataKey string) (*Global, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	ns, name, ok := strings.Cut(value, "/")
	if !ok {
		return nil, fmt.Errorf("expected <namespace>/<name>, got %q", value)
	}
	ns, name = strings.TrimSpace(ns), strings.TrimSpace(name)
	if ns == "" || name == "" {
		return nil, fmt.Errorf("expected <namespace>/<name>, got %q", value)
	}
	return &Global{Namespace: ns, Name: name, Key: dataKey}, nil
}

// Ref is a CR-supplied reference. Namespace is empty unless the CR
// explicitly set one; Name is empty when the CR omitted the reference
// entirely.
type Ref struct {
	Namespace string
	Name      string
	Key       string
}

// Resolved is the outcome of resolution: the Secret to read, the entry
// within it, and where the reference came from.
type Resolved struct {
	client.ObjectKey
	// DataKey is the entry within the Secret, for callers that need one.
	DataKey string
	Origin  Origin
}

// Resolver applies the operator's policy to CR-supplied references.
//
// The zero value permits same-namespace references only, which is exactly
// the behaviour that shipped before #47/#48 — so a Resolver that is never
// configured cannot change how an existing cluster resolves anything.
type Resolver struct {
	allowed map[string]struct{}
}

// NewResolver builds a Resolver permitting cross-namespace references into
// the given namespaces. Empty entries are ignored; an empty list disables
// cross-namespace references entirely.
func NewResolver(namespaces []string) Resolver {
	r := Resolver{}
	for _, ns := range namespaces {
		ns = strings.TrimSpace(ns)
		if ns == "" {
			continue
		}
		if r.allowed == nil {
			r.allowed = make(map[string]struct{}, len(namespaces))
		}
		r.allowed[ns] = struct{}{}
	}
	return r
}

// AllowedNamespaces returns the permitted namespaces, sorted. For logging
// at startup and for error messages.
func (r Resolver) AllowedNamespaces() []string {
	out := make([]string, 0, len(r.allowed))
	for ns := range r.allowed {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

// Resolve picks the Secret to read.
//
// Precedence, matching #47 and #48:
//
//  1. the CR's own reference — in its namespace, or in another namespace
//     if the CR says so and the operator permits it;
//  2. the operator-level default, when the CR names nothing;
//  3. otherwise ErrNoReference.
func (r Resolver) Resolve(crNamespace string, ref Ref, global *Global) (Resolved, error) {
	if ref.Name == "" {
		if global == nil {
			return Resolved{}, ErrNoReference
		}
		return Resolved{
			ObjectKey: client.ObjectKey{Namespace: global.Namespace, Name: global.Name},
			DataKey:   global.Key,
			Origin:    OriginGlobal,
		}, nil
	}

	ns := ref.Namespace
	if ns == "" || ns == crNamespace {
		return Resolved{
			ObjectKey: client.ObjectKey{Namespace: crNamespace, Name: ref.Name},
			DataKey:   ref.Key,
			Origin:    OriginLocal,
		}, nil
	}

	if _, ok := r.allowed[ns]; !ok {
		return Resolved{}, fmt.Errorf(
			"secret reference to %q/%q crosses namespaces, which this operator does not permit (allowed: %s); "+
				"set --secret-ref-namespaces to permit it",
			ns, ref.Name, describeAllowed(r.AllowedNamespaces()))
	}

	return Resolved{
		ObjectKey: client.ObjectKey{Namespace: ns, Name: ref.Name},
		DataKey:   ref.Key,
		Origin:    OriginCrossNamespace,
	}, nil
}

func describeAllowed(nss []string) string {
	if len(nss) == 0 {
		return "none"
	}
	return strings.Join(nss, ",")
}

// IndexKey is the value a field index stores for a resolved reference, and
// the value a Secret event is looked up by. Namespace-qualified so a watch
// can match a Secret in any namespace.
func IndexKey(namespace, name string) string {
	return namespace + "/" + name
}

// IndexValues returns the index entries for a CR holding the given
// reference. Resolution errors are deliberately swallowed: a reference the
// policy rejects still gets indexed, so that granting the namespace later
// (or fixing the CR) wakes the CR up instead of leaving it stuck until its
// resync interval. A spurious wakeup is cheap; a missed one is not.
func (r Resolver) IndexValues(crNamespace string, ref Ref, global *Global) []string {
	res, err := r.Resolve(crNamespace, ref, global)
	if err == nil {
		return []string{IndexKey(res.Namespace, res.Name)}
	}
	if errors.Is(err, ErrNoReference) {
		return nil
	}
	// Policy rejection: index what the CR asked for.
	return []string{IndexKey(ref.Namespace, ref.Name)}
}
