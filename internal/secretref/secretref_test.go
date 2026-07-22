package secretref

import (
	"errors"
	"strings"
	"testing"
)

func TestParseGlobal(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		wantNil bool
		wantErr bool
		wantNS  string
		wantN   string
	}{
		{name: "empty means not configured", value: "", wantNil: true},
		{name: "whitespace means not configured", value: "   ", wantNil: true},
		{name: "namespace and name", value: "sops-system/age-key", wantNS: "sops-system", wantN: "age-key"},
		{name: "trims surrounding space", value: " sops-system / age-key ", wantNS: "sops-system", wantN: "age-key"},
		{name: "bare name is rejected", value: "age-key", wantErr: true},
		{name: "empty namespace is rejected", value: "/age-key", wantErr: true},
		{name: "empty name is rejected", value: "sops-system/", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseGlobal(tt.value, "age.agekey")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("want error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.wantNil {
				if got != nil {
					t.Fatalf("want nil, got %+v", got)
				}
				return
			}
			if got.Namespace != tt.wantNS || got.Name != tt.wantN {
				t.Errorf("got %q/%q, want %q/%q", got.Namespace, got.Name, tt.wantNS, tt.wantN)
			}
			if got.Key != "age.agekey" {
				t.Errorf("data key: got %q, want %q", got.Key, "age.agekey")
			}
		})
	}
}

func TestResolve(t *testing.T) {
	global := &Global{Namespace: "sops-system", Name: "shared-age", Key: "age.agekey"}

	tests := []struct {
		name       string
		resolver   Resolver
		crNS       string
		ref        Ref
		global     *Global
		wantNS     string
		wantName   string
		wantKey    string
		wantOrigin Origin
		wantErr    string // substring; "" = no error
	}{
		{
			name:       "local ref wins over the global default",
			resolver:   Resolver{},
			crNS:       "team-a",
			ref:        Ref{Name: "own-age", Key: "age.agekey"},
			global:     global,
			wantNS:     "team-a",
			wantName:   "own-age",
			wantKey:    "age.agekey",
			wantOrigin: OriginLocal,
		},
		{
			name:       "no ref falls back to the global default",
			resolver:   Resolver{},
			crNS:       "team-a",
			ref:        Ref{},
			global:     global,
			wantNS:     "sops-system",
			wantName:   "shared-age",
			wantKey:    "age.agekey",
			wantOrigin: OriginGlobal,
		},
		{
			name:     "no ref and no global is an error",
			resolver: Resolver{},
			crNS:     "team-a",
			ref:      Ref{},
			global:   nil,
			wantErr:  "no operator-level default",
		},
		{
			name:       "ref naming the CR's own namespace stays local",
			resolver:   Resolver{},
			crNS:       "team-a",
			ref:        Ref{Namespace: "team-a", Name: "own-age", Key: "age.agekey"},
			wantNS:     "team-a",
			wantName:   "own-age",
			wantOrigin: OriginLocal,
			wantKey:    "age.agekey",
		},
		{
			// The security default: an unconfigured Resolver must behave
			// exactly like the operator did before #47/#48.
			name:     "cross-namespace ref is rejected by default",
			resolver: Resolver{},
			crNS:     "team-a",
			ref:      Ref{Namespace: "platform-creds", Name: "shared", Key: "age.agekey"},
			wantErr:  "does not permit",
		},
		{
			name:     "cross-namespace ref to a non-allowed namespace is rejected",
			resolver: NewResolver([]string{"platform-creds"}),
			crNS:     "team-a",
			ref:      Ref{Namespace: "kube-system", Name: "shared", Key: "age.agekey"},
			wantErr:  `"kube-system"/"shared"`,
		},
		{
			name:       "cross-namespace ref to an allowed namespace resolves",
			resolver:   NewResolver([]string{"platform-creds", "shared-creds"}),
			crNS:       "team-a",
			ref:        Ref{Namespace: "platform-creds", Name: "shared", Key: "age.agekey"},
			wantNS:     "platform-creds",
			wantName:   "shared",
			wantKey:    "age.agekey",
			wantOrigin: OriginCrossNamespace,
		},
		{
			// The admin set the flag, so the global is not subject to the
			// allowlist that exists to constrain tenants.
			name:       "global default is exempt from the allowlist",
			resolver:   NewResolver([]string{"platform-creds"}),
			crNS:       "team-a",
			ref:        Ref{},
			global:     global,
			wantNS:     "sops-system",
			wantName:   "shared-age",
			wantOrigin: OriginGlobal,
			wantKey:    "age.agekey",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.resolver.Resolve(tt.crNS, tt.ref, tt.global)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("want error containing %q, got %+v", tt.wantErr, got)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %q does not contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Namespace != tt.wantNS || got.Name != tt.wantName {
				t.Errorf("got %q/%q, want %q/%q", got.Namespace, got.Name, tt.wantNS, tt.wantName)
			}
			if got.DataKey != tt.wantKey {
				t.Errorf("data key: got %q, want %q", got.DataKey, tt.wantKey)
			}
			if got.Origin != tt.wantOrigin {
				t.Errorf("origin: got %q, want %q", got.Origin, tt.wantOrigin)
			}
		})
	}
}

func TestResolveNoReferenceIsSentinel(t *testing.T) {
	_, err := Resolver{}.Resolve("team-a", Ref{}, nil)
	if !errors.Is(err, ErrNoReference) {
		t.Fatalf("want ErrNoReference, got %v", err)
	}
}

func TestNewResolverIgnoresEmptyEntries(t *testing.T) {
	r := NewResolver([]string{"", "  ", "platform-creds", ""})
	if got := r.AllowedNamespaces(); len(got) != 1 || got[0] != "platform-creds" {
		t.Fatalf("got %v, want [platform-creds]", got)
	}
}

func TestAllowedNamespacesIsSorted(t *testing.T) {
	r := NewResolver([]string{"zulu", "alpha", "mike"})
	got := r.AllowedNamespaces()
	want := []string{"alpha", "mike", "zulu"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestIndexValues(t *testing.T) {
	global := &Global{Namespace: "sops-system", Name: "shared-age", Key: "age.agekey"}

	tests := []struct {
		name     string
		resolver Resolver
		crNS     string
		ref      Ref
		global   *Global
		want     []string
	}{
		{
			name: "local ref indexes the CR's namespace",
			crNS: "team-a",
			ref:  Ref{Name: "own-age"},
			want: []string{"team-a/own-age"},
		},
		{
			name:   "no ref with a global indexes the global",
			crNS:   "team-a",
			global: global,
			want:   []string{"sops-system/shared-age"},
		},
		{
			name: "no ref and no global indexes nothing",
			crNS: "team-a",
			want: nil,
		},
		{
			// A CR the policy currently rejects still gets indexed, so
			// that permitting the namespace later wakes it up.
			name: "rejected cross-namespace ref is still indexed",
			crNS: "team-a",
			ref:  Ref{Namespace: "platform-creds", Name: "shared"},
			want: []string{"platform-creds/shared"},
		},
		{
			name:     "permitted cross-namespace ref indexes the target",
			resolver: NewResolver([]string{"platform-creds"}),
			crNS:     "team-a",
			ref:      Ref{Namespace: "platform-creds", Name: "shared"},
			want:     []string{"platform-creds/shared"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.resolver.IndexValues(tt.crNS, tt.ref, tt.global)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("got %v, want %v", got, tt.want)
				}
			}
		})
	}
}
