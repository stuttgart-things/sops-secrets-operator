//go:build e2e
// +build e2e

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

package e2e

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stuttgart-things/sops-secrets-operator/test/utils"
)

// namespace where the project is deployed in
const namespace = "sops-secrets-operator-system"

// serviceAccountName created for the project
const serviceAccountName = "sops-secrets-operator-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "sops-secrets-operator-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "sops-secrets-operator-metrics-binding"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("creating manager namespace")
		cmd := exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics",
			"-n", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		// Drain every operator-managed CR across all namespaces *before*
		// the controller goes away. If `make uninstall` deletes the CRDs
		// first, the apiserver garbage-collects every CR — but the
		// controller is already gone, no one strips the finalizer, and
		// the CRDs stay Terminating waiting for it. With the controller
		// still alive here, the finalizers run cleanly.
		By("draining operator-managed CRs while the controller is still alive")
		for _, kind := range []string{
			"sopssecrets.sops.stuttgart-things.com",
			"sopssecretmanifests.sops.stuttgart-things.com",
			"inlinesopssecrets.sops.stuttgart-things.com",
			"gitrepositories.sops.stuttgart-things.com",
			"objectsources.sops.stuttgart-things.com",
		} {
			cmd = exec.Command("kubectl", "delete", kind, "--all",
				"--all-namespaces", "--ignore-not-found", "--timeout=30s")
			_, _ = utils.Run(cmd)
		}

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy", "ignore-not-found=true")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall", "ignore-not-found=true")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				// Get the name of the controller-manager pod
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				// Validate the pod's status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating a ClusterRoleBinding for the service account to allow access to metrics")
			cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
				"--clusterrole=sops-secrets-operator-metrics-reader",
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
			)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("ensuring the controller pod is ready")
			verifyControllerPodReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", controllerPodName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Controller pod not ready")
			}
			Eventually(verifyControllerPodReady, 3*time.Minute, time.Second).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("Serving metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted, 3*time.Minute, time.Second).Should(Succeed())

			// +kubebuilder:scaffold:e2e-metrics-webhooks-readiness

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": [
								"for i in $(seq 1 30); do curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics && exit 0 || sleep 2; done; exit 1"
							],
							"securityContext": {
								"readOnlyRootFilesystem": true,
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccountName": "%s"
					}
				}`, token, metricsServiceName, namespace, serviceAccountName))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			verifyMetricsAvailable := func(g Gomega) {
				metricsOutput, err := getMetricsOutput()
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
				g.Expect(metricsOutput).NotTo(BeEmpty())
				g.Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
			}
			Eventually(verifyMetricsAvailable, 2*time.Minute).Should(Succeed())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks

		It("should round-trip a v1alpha1 SopsSecret through the conversion webhook to v1alpha2 storage", func() {
			const conversionNs = "e2e-conversion"
			By("creating a namespace for the conversion test")
			cmd := exec.Command("kubectl", "create", "ns", conversionNs)
			_, _ = utils.Run(cmd)
			DeferCleanup(func() {
				cmd := exec.Command("kubectl", "delete", "ns", conversionNs, "--ignore-not-found")
				_, _ = utils.Run(cmd)
			})

			// The CR references a GitRepository that does not exist; the
			// reconciler will surface a not-ready condition, but conversion
			// happens at admission time and is what we are asserting here.
			manifest := `---
apiVersion: sops.stuttgart-things.com/v1alpha1
kind: SopsSecret
metadata:
  name: conversion-probe
  namespace: e2e-conversion
spec:
  source:
    repositoryRef:
      name: nonexistent-repo
    path: prod/app/creds.enc.yaml
  decryption:
    keyRef:
      name: dummy-age-key
      key: age.agekey
  data:
    - key: DATABASE_URL
      from: database_url
`
			By("applying the v1alpha1 SopsSecret (admission goes through the conversion webhook)")
			out, err := kubectlApply(manifest)
			Expect(err).NotTo(HaveOccurred(), "kubectl apply output: %s", out)

			By("reading the same CR via the v1alpha2 endpoint to confirm sourceRef was populated by ConvertTo")
			verifyConverted := func(g Gomega) {
				cmd := exec.Command("kubectl", "get",
					"sopssecrets.v1alpha2.sops.stuttgart-things.com", "conversion-probe",
					"-n", conversionNs,
					"-o", "jsonpath={.spec.source.sourceRef.kind}/{.spec.source.sourceRef.name}",
				)
				kind, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(kind).To(Equal("GitRepository/nonexistent-repo"))
			}
			Eventually(verifyConverted, 30*time.Second).Should(Succeed())
		})

		It("should reconcile a v1alpha2 SopsSecret backed by an ObjectSource", func() {
			const objNs = "e2e-objectsource"

			By("creating the test namespace")
			cmd := exec.Command("kubectl", "create", "ns", objNs)
			_, _ = utils.Run(cmd)
			DeferCleanup(func() {
				cmd := exec.Command("kubectl", "delete", "ns", objNs, "--ignore-not-found")
				_, _ = utils.Run(cmd)
			})

			By("generating a fresh age identity and a SOPS-encrypted YAML payload")
			fixture := newE2EAge(GinkgoT())

			By("deploying an in-cluster nginx Pod + Service that serves the encrypted YAML")
			nginxYAML := nginxFixtureManifest(objNs, "encrypted-source", "shared.enc.yaml", fixture.Ciphertext)
			out, err := kubectlApply(nginxYAML)
			Expect(err).NotTo(HaveOccurred(), "kubectl apply nginx fixture: %s", out)

			By("waiting for the nginx Pod to be Ready")
			cmd = exec.Command("kubectl", "wait", "--for=condition=Ready",
				"pod/encrypted-source", "-n", objNs, "--timeout=2m")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "nginx Pod did not become Ready")

			By("creating the age private-key Secret")
			out, err = kubectlApply(ageKeySecretManifest(objNs, "sops-age-key", fixture.Key.PrivateKey))
			Expect(err).NotTo(HaveOccurred(), "kubectl apply age key: %s", out)

			By("creating the ObjectSource")
			objectSrcURL := fmt.Sprintf("http://encrypted-source.%s.svc:80/shared.enc.yaml", objNs)
			objectSrc := fmt.Sprintf(`---
apiVersion: sops.stuttgart-things.com/v1alpha2
kind: ObjectSource
metadata:
  name: shared-secrets-https
  namespace: %s
spec:
  url: %s
  auth:
    type: none
`, objNs, objectSrcURL)
			out, err = kubectlApply(objectSrc)
			Expect(err).NotTo(HaveOccurred(), "kubectl apply ObjectSource: %s", out)

			By("creating the v1alpha2 SopsSecret that consumes the ObjectSource")
			sopsSecret := fmt.Sprintf(`---
apiVersion: sops.stuttgart-things.com/v1alpha2
kind: SopsSecret
metadata:
  name: app-creds-https
  namespace: %s
spec:
  source:
    sourceRef:
      kind: ObjectSource
      name: shared-secrets-https
    path: shared.enc.yaml
  decryption:
    keyRef:
      name: sops-age-key
      key: age.agekey
  data:
    - key: DATABASE_URL
      from: database_url
    - key: API_TOKEN
      from: api_token
`, objNs)
			out, err = kubectlApply(sopsSecret)
			Expect(err).NotTo(HaveOccurred(), "kubectl apply SopsSecret: %s", out)

			By("waiting for the target Secret to be reconciled with the decrypted values")
			verifyTargetSecret := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "secret", "app-creds-https",
					"-n", objNs,
					"-o", "jsonpath={.data.DATABASE_URL}/{.data.API_TOKEN}",
				)
				out, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(out).NotTo(BeEmpty())

				expectedURL := base64.StdEncoding.EncodeToString([]byte("postgres://app@db:5432/app"))
				expectedTok := base64.StdEncoding.EncodeToString([]byte("e2e-token-xyz"))
				g.Expect(out).To(Equal(expectedURL + "/" + expectedTok))
			}
			Eventually(verifyTargetSecret, 3*time.Minute, 2*time.Second).Should(Succeed())
		})

		// TODO: Customize the e2e test suite with scenarios specific to your project.
		// Consider applying sample/CR(s) and check their status and/or verifying
		// the reconciliation by using the metrics, i.e.:
		// metricsOutput, err := getMetricsOutput()
		// Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
		// Expect(metricsOutput).To(ContainSubstring(
		//    fmt.Sprintf(`controller_runtime_reconcile_total{controller="%s",result="success"} 1`,
		//    strings.ToLower(<Kind>),
		// ))
	})
})

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	// Temporary file to store the token request
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		// Execute kubectl command to create the token
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		// Parse the JSON output to extract the token
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() (string, error) {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	return utils.Run(cmd)
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
