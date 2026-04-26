//go:build e2e
// +build e2e

/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package e2e

import (
	"encoding/base64"
	"fmt"
	"os/exec"
	"strings"

	"github.com/stuttgart-things/sops-secrets-operator/internal/testutil"
	"github.com/stuttgart-things/sops-secrets-operator/test/utils"
)

// kubectlApply pipes a YAML document to `kubectl apply -f -`.
func kubectlApply(manifest string) (string, error) {
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	return utils.Run(cmd)
}

// kubectlDelete is the cleanup counterpart for kubectlApply. Errors are
// swallowed because deletes during teardown are best-effort.
func kubectlDelete(manifest string) {
	cmd := exec.Command("kubectl", "delete", "--ignore-not-found", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	_, _ = utils.Run(cmd)
}

// e2eAge wraps a generated age identity together with an encrypted YAML
// payload encoded against that identity's recipient.
type e2eAge struct {
	Key        testutil.AgeKey
	Plaintext  []byte
	Ciphertext []byte
}

// newE2EAge returns a fresh age identity and a SOPS-encrypted YAML doc
// keyed to it. The plaintext carries two well-known fields so reconcile
// assertions can pin exact values.
func newE2EAge(tb testutil.TestingT) e2eAge {
	tb.Helper()
	key := testutil.GenerateAge(tb)
	plaintext := []byte("database_url: postgres://app@db:5432/app\napi_token: e2e-token-xyz\n")
	ct := testutil.EncryptYAML(tb, key.PublicKey, plaintext)
	return e2eAge{Key: key, Plaintext: plaintext, Ciphertext: ct}
}

// ageKeySecretManifest returns the YAML for a k8s Secret carrying the
// age private key under the `age.agekey` field. The private key is
// indented under a YAML block scalar so embedded newlines (none today,
// but possible in multi-line keys) survive.
func ageKeySecretManifest(namespace, name, privateKey string) string {
	return fmt.Sprintf(`---
apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  age.agekey: %q
`, name, namespace, privateKey)
}

// nginxFixtureManifest returns a Pod + Service that serves `content` at
// http://<name>.<namespace>.svc:80/<filename>. The content is delivered
// via a ConfigMap.binaryData (base64) so SOPS YAML — which contains
// literal newlines and quote chars that complicate YAML string escaping —
// round-trips byte-for-byte to the file.
func nginxFixtureManifest(namespace, name, filename string, content []byte) string {
	encoded := base64.StdEncoding.EncodeToString(content)
	return fmt.Sprintf(`---
apiVersion: v1
kind: ConfigMap
metadata:
  name: %s-content
  namespace: %s
binaryData:
  %s: %s
---
apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
    app: %s
spec:
  containers:
  - name: nginx
    image: nginx:1.27-alpine
    ports:
    - containerPort: 80
    volumeMounts:
    - name: content
      mountPath: /usr/share/nginx/html
      readOnly: true
  volumes:
  - name: content
    configMap:
      name: %s-content
---
apiVersion: v1
kind: Service
metadata:
  name: %s
  namespace: %s
spec:
  selector:
    app: %s
  ports:
  - port: 80
    targetPort: 80
`, name, namespace, filename, encoded,
		name, namespace, name,
		name,
		name, namespace, name)
}

