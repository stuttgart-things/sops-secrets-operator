package transform

import (
	"strings"
	"testing"

	sopsv1alpha1 "github.com/stuttgart-things/sops-secrets-operator/api/v1alpha1"
)

func TestParseFlatYAML(t *testing.T) {
	in := []byte(`
username: alice
password: s3cret
count: 42
ratio: 0.75
enabled: true
`)
	got, err := ParseFlatYAML(in)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"username": "alice",
		"password": "s3cret",
		"count":    "42",
		"ratio":    "0.75",
		"enabled":  "true",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
}

func TestParseFlatYAMLRejectsNestedMap(t *testing.T) {
	in := []byte("db:\n  user: alice\n  pass: s3cret\n")
	_, err := ParseFlatYAML(in)
	if err == nil || !strings.Contains(err.Error(), "non-scalar") {
		t.Fatalf("expected non-scalar rejection, got %v", err)
	}
}

func TestParseFlatYAMLRejectsList(t *testing.T) {
	in := []byte("entries:\n  - a\n  - b\n")
	_, err := ParseFlatYAML(in)
	if err == nil || !strings.Contains(err.Error(), "non-scalar") {
		t.Fatalf("expected non-scalar rejection, got %v", err)
	}
}

func TestParseFlatYAMLRejectsNull(t *testing.T) {
	in := []byte("foo: null\n")
	_, err := ParseFlatYAML(in)
	if err == nil || !strings.Contains(err.Error(), "null") {
		t.Fatalf("expected null rejection, got %v", err)
	}
}

func TestApplyMappingFailClosed(t *testing.T) {
	src := map[string]string{"a": "1"}
	_, err := ApplyMapping(src, []sopsv1alpha1.DataMapping{{Key: "X", From: "missing"}})
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected missing-key error, got %v", err)
	}
}

func TestApplyMappingRenames(t *testing.T) {
	src := map[string]string{"db_user": "alice", "db_pass": "s3cret"}
	out, err := ApplyMapping(src, []sopsv1alpha1.DataMapping{
		{Key: "DB_USER", From: "db_user"},
		{Key: "DB_PASSWORD", From: "db_pass"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(out["DB_USER"]) != "alice" || string(out["DB_PASSWORD"]) != "s3cret" {
		t.Fatalf("unexpected output: %+v", out)
	}
	if _, ok := out["db_user"]; ok {
		t.Fatal("source key should not appear in output")
	}
}

func TestHashSecretDataIsDeterministic(t *testing.T) {
	a := map[string][]byte{"b": []byte("2"), "a": []byte("1")}
	b := map[string][]byte{"a": []byte("1"), "b": []byte("2")}
	if HashSecretData(a) != HashSecretData(b) {
		t.Fatal("hash must be insensitive to map iteration order")
	}
	c := map[string][]byte{"a": []byte("1"), "b": []byte("3")}
	if HashSecretData(a) == HashSecretData(c) {
		t.Fatal("hash must change when values change")
	}
}
