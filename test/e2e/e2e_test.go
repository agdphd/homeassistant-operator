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
	"fmt"
	"os"
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/przemekhys/homeassistant-operator/test/utils"
)

// namespace where the project is deployed in
const namespace = "homeassistant-operator-system"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "homeassistant-operator-controller-manager-metrics-service"

var _ = Describe("Manager", Label("core", "fast"), Ordered, func() {
	// Manager namespace setup is now handled in BeforeSuite (e2e_suite_test.go)
	// This ensures all tests share the same controller deployment
	BeforeAll(func() {
		// Ensure the namespace has the required security policy label
		By("labeling the namespace to enforce the restricted security policy")
		cmd := exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")
	})

	SetDefaultEventuallyTimeout(utils.DefaultEventuallyTimeout)
	SetDefaultEventuallyPollingInterval(utils.DefaultEventuallyPollingInterval)

	Context("Manager", func() {
		var controllerPodName string

		BeforeEach(func() {
			// Discover controller pod name at the start of each test to avoid race conditions
			cmd := exec.Command("kubectl", "get",
				"pods", "-l", "control-plane=controller-manager",
				"-o", "go-template={{ range .items }}"+
					"{{ if not .metadata.deletionTimestamp }}"+
					"{{ .metadata.name }}"+
					"{{ \"\\n\" }}{{ end }}{{ end }}",
				"-n", namespace,
			)

			podOutput, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
			podNames := utils.GetNonEmptyLines(podOutput)
			Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
			controllerPodName = podNames[0]
			Expect(controllerPodName).To(ContainSubstring("controller-manager"))
		})

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

		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				// Validate the pod's status
				cmd := exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the metrics service is available", func() {
			By("validating that the metrics service is available")
			verifyMetricsService := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")
			}
			Eventually(verifyMetricsService, 30*time.Second).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("controller-runtime.metrics\tServing metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted, 30*time.Second).Should(Succeed())
		})

		It("should create HomeAssistant resource successfully", Label("homeassistant"), func() {
			const (
				testNamespace = "ha-e2e-test"
				haName        = "test-ha-e2e"
				configName    = "test-ha-e2e-config"
			)

			By("creating test namespace")
			cmd := exec.Command("kubectl", "create", "ns", testNamespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			defer func() {
				cmd := exec.Command("kubectl", "delete", "ns", testNamespace, "--ignore-not-found=true")
				_, _ = utils.Run(cmd)
			}()

			By("creating HomeAssistant instance")
			haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "1Gi"
`, haName, testNamespace)

			tmpFile, err := os.CreateTemp("", "ha-e2e-*.yaml")
			Expect(err).NotTo(HaveOccurred())
			haFile := tmpFile.Name()
			_ = tmpFile.Close()
			defer func() { _ = os.Remove(haFile) }()

			err = os.WriteFile(haFile, []byte(haYAML), 0644)
			Expect(err).NotTo(HaveOccurred())

			cmd = exec.Command("kubectl", "apply", "-f", haFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("creating HomeAssistantConfiguration instance")
			configYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  configuration: |
    homeassistant:
      name: Home
    default_config:
`, configName, testNamespace, haName)

			tmpFile2, err := os.CreateTemp("", "ha-config-e2e-*.yaml")
			Expect(err).NotTo(HaveOccurred())
			configFile := tmpFile2.Name()
			_ = tmpFile2.Close()
			defer func() { _ = os.Remove(configFile) }()

			err = os.WriteFile(configFile, []byte(configYAML), 0644)
			Expect(err).NotTo(HaveOccurred())

			cmd = exec.Command("kubectl", "apply", "-f", configFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying HomeAssistant resource was created")
			verifyHACreated := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "ha", haName, "-n", testNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}
			Eventually(verifyHACreated, 30*time.Second).Should(Succeed())

			By("verifying StatefulSet was created")
			verifyStatefulSet := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "statefulset", haName, "-n", testNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}
			Eventually(verifyStatefulSet, 30*time.Second).Should(Succeed())

			By("verifying Service was created")
			verifyService := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "service", haName, "-n", testNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}
			Eventually(verifyService, 30*time.Second).Should(Succeed())

			By("verifying PVC was created")
			verifyPVC := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pvc", haName+"-data", "-n", testNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}
			Eventually(verifyPVC, 30*time.Second).Should(Succeed())
		})

		It("should create HomeAssistantSecrets resource successfully", Label("secrets"), func() {
			const (
				testNamespace = "hasec-e2e-test"
				haName        = "test-ha-sec-e2e"
				configName    = "test-ha-sec-e2e-config"
				secretName    = "test-secret-e2e"
				haSecretsName = "test-hasecrets-e2e"
			)

			By("creating test namespace")
			cmd := exec.Command("kubectl", "create", "ns", testNamespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			defer func() {
				cmd := exec.Command("kubectl", "delete", "ns", testNamespace, "--ignore-not-found=true")
				_, _ = utils.Run(cmd)
			}()

			By("creating source Secret")
			secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  mqtt_user: "testuser"
  mqtt_password: "testpass"
`, secretName, testNamespace)

			tmpFile3, err := os.CreateTemp("", "secret-e2e-*.yaml")
			Expect(err).NotTo(HaveOccurred())
			secretFile := tmpFile3.Name()
			_ = tmpFile3.Close()
			defer func() { _ = os.Remove(secretFile) }()

			err = os.WriteFile(secretFile, []byte(secretYAML), 0644)
			Expect(err).NotTo(HaveOccurred())

			cmd = exec.Command("kubectl", "apply", "-f", secretFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("creating HomeAssistant instance")
			haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "1Gi"
`, haName, testNamespace)

			tmpFile4, err := os.CreateTemp("", "ha-hasec-e2e-*.yaml")
			Expect(err).NotTo(HaveOccurred())
			haFile := tmpFile4.Name()
			_ = tmpFile4.Close()
			defer func() { _ = os.Remove(haFile) }()

			err = os.WriteFile(haFile, []byte(haYAML), 0644)
			Expect(err).NotTo(HaveOccurred())

			cmd = exec.Command("kubectl", "apply", "-f", haFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("creating HomeAssistantConfiguration instance")
			configYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  configuration: |
    homeassistant:
      name: Home
    default_config:
`, configName, testNamespace, haName)

			tmpFile5, err := os.CreateTemp("", "ha-sec-config-e2e-*.yaml")
			Expect(err).NotTo(HaveOccurred())
			configFile := tmpFile5.Name()
			_ = tmpFile5.Close()
			defer func() { _ = os.Remove(configFile) }()

			err = os.WriteFile(configFile, []byte(configYAML), 0644)
			Expect(err).NotTo(HaveOccurred())

			cmd = exec.Command("kubectl", "apply", "-f", configFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("creating HomeAssistantSecrets instance")
			haSecretsYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantSecrets
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  secretRefs:
    - name: %s
      keys:
        - mqtt_user
        - mqtt_password
`, haSecretsName, testNamespace, haName, secretName)

			tmpFile6, err := os.CreateTemp("", "hasecrets-e2e-*.yaml")
			Expect(err).NotTo(HaveOccurred())
			haSecretsFile := tmpFile6.Name()
			_ = tmpFile6.Close()
			defer func() { _ = os.Remove(haSecretsFile) }()

			err = os.WriteFile(haSecretsFile, []byte(haSecretsYAML), 0644)
			Expect(err).NotTo(HaveOccurred())

			cmd = exec.Command("kubectl", "apply", "-f", haSecretsFile)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying generated Secret was created")
			verifyGeneratedSecret := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "secret", haName+"-generated-secrets", "-n", testNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}
			Eventually(verifyGeneratedSecret, 30*time.Second).Should(Succeed())
		})
	})
})
