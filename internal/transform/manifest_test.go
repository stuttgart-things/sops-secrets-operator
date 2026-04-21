package transform

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestParseManifestValid(t *testing.T) {
	in := []byte(`
apiVersion: v1
kind: Secret
metadata:
  name: app-creds
  namespace: apps
  labels:
    app: web
  annotations:
    team: platform
type: Opaque
data:
  password: czNjcmV0
stringData:
  username: alice
`)
	sec, err := ParseManifest(in)
	if err != nil {
		t.Fatal(err)
	}
	if sec.Name != "app-creds" {
		t.Errorf("name = %q", sec.Name)
	}
	if sec.Type != corev1.SecretTypeOpaque {
		t.Errorf("type = %q", sec.Type)
	}
	if string(sec.Data["password"]) != "s3cret" {
		t.Errorf("password base64-decode: %q", sec.Data["password"])
	}
	if sec.StringData["username"] != "alice" {
		t.Errorf("stringData username = %q", sec.StringData["username"])
	}
	if sec.Labels["app"] != "web" {
		t.Errorf("labels: %+v", sec.Labels)
	}
}

func TestParseManifestRejectsNonSecret(t *testing.T) {
	in := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n")
	_, err := ParseManifest(in)
	if err == nil || !strings.Contains(err.Error(), "ConfigMap") {
		t.Fatalf("expected ConfigMap rejection, got %v", err)
	}
}

func TestParseManifestRejectsWrongAPIVersion(t *testing.T) {
	in := []byte("apiVersion: apps/v1\nkind: Secret\nmetadata:\n  name: x\n")
	_, err := ParseManifest(in)
	if err == nil || !strings.Contains(err.Error(), "v1") {
		t.Fatalf("expected apiVersion rejection, got %v", err)
	}
}

func TestParseManifestRejectsUnknownTopLevelField(t *testing.T) {
	in := []byte("apiVersion: v1\nkind: Secret\nmetadata:\n  name: x\nspec:\n  foo: bar\n")
	_, err := ParseManifest(in)
	if err == nil || !strings.Contains(err.Error(), "spec") {
		t.Fatalf("expected spec rejection, got %v", err)
	}
}

func TestParseManifestRejectsUnknownMetadataField(t *testing.T) {
	in := []byte(`
apiVersion: v1
kind: Secret
metadata:
  name: x
  ownerReferences:
    - apiVersion: v1
      kind: ConfigMap
      name: evil
      uid: abc
`)
	_, err := ParseManifest(in)
	if err == nil || !strings.Contains(err.Error(), "ownerReferences") {
		t.Fatalf("expected ownerReferences rejection, got %v", err)
	}
}

func TestNormalizeSecretData(t *testing.T) {
	sec := &corev1.Secret{
		Data:       map[string][]byte{"a": []byte("1")},
		StringData: map[string]string{"b": "2", "a": "override"},
	}
	NormalizeSecretData(sec)
	if sec.StringData != nil {
		t.Error("stringData should be cleared")
	}
	if string(sec.Data["a"]) != "override" {
		t.Errorf("stringData should win on key collision, got %q", sec.Data["a"])
	}
	if string(sec.Data["b"]) != "2" {
		t.Errorf("missing merged key: %+v", sec.Data)
	}
}

func TestHashManifestSecretDeterministic(t *testing.T) {
	a := &corev1.Secret{
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"y": []byte("2"), "x": []byte("1")},
	}
	b := &corev1.Secret{
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"x": []byte("1"), "y": []byte("2")},
	}
	if HashManifestSecret(a) != HashManifestSecret(b) {
		t.Fatal("hash must be stable across map iteration order")
	}

	c := &corev1.Secret{
		Type: corev1.SecretTypeDockerConfigJson,
		Data: map[string][]byte{"x": []byte("1"), "y": []byte("2")},
	}
	if HashManifestSecret(a) == HashManifestSecret(c) {
		t.Fatal("hash must differ when type differs")
	}
}
