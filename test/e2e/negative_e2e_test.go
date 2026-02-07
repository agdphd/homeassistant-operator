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
	"os/exec"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/przemekhys/homeassistant-operator/test/utils"
)

const (
	negativeNamespacePrefix = "neg-e2e-"
)

var _ = Describe("Negative & Error Handling E2E", Label("negative"), Ordered, func() {
	var (
		namespace      string
		haName         string
		configName     string
		secretsName    string
		automationName string
	)

	BeforeEach(func() {
		namespace = negativeNamespacePrefix + utils.RandomString(8)
		haName = "test-ha-" + utils.RandomString(6)
		configName = "test-config-" + utils.RandomString(6)
		secretsName = "test-hasecrets-" + utils.RandomString(6)
		automationName = "test-auto-" + utils.RandomString(6)

		By("Creating test namespace: " + namespace)
		Expect(utils.CreateNamespace(namespace)).To(Succeed())
	})

	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Test failed - collecting debug info")
			collectNegativeDebugInfo(namespace, haName, configName, secretsName, automationName)
		}

		By("Deleting test namespace: " + namespace)
		if err := utils.DeleteNamespace(namespace); err != nil {
			fmt.Printf("Warning: failed to delete namespace %s: %v\n", namespace, err)
		}
	})

	// =========================================================================
	// GROUP: HomeAssistant Controller - negative
	// =========================================================================

	Context("HomeAssistant Controller - negative", func() {
		It("N1: Missing HomeAssistantConfiguration CR - HA waits", Label("fast", "infra-only"), func() {
			By("Creating HomeAssistant CR without HomeAssistantConfiguration")
			haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "1Gi"
  %s
`, haName, namespace, utils.GetDefaultHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Verifying HA status Ready=False (waiting for configuration)")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("False"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying StatefulSet NOT created")
			output := utils.Kubectl("get", "statefulset", haName, "-n", namespace, "--ignore-not-found")
			Expect(output).To(BeEmpty())

			By("Creating the missing HomeAssistantConfiguration CR")
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
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Verifying StatefulSet is now created")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "statefulset", haName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying recovery - StatefulSet exists (fast test, don't wait for pod)")
			// For fast test, don't wait for pod Ready=True (requires 12+ min pod startup)
			// Just verify StatefulSet exists and is stable
			Consistently(func(g Gomega) {
				output := utils.Kubectl("get", "statefulset", haName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, 5*time.Second, 1*time.Second).Should(Succeed())
		})

		It("N2: Deleting HomeAssistantConfiguration while running", Label("slow", "pod-required"), func() {
			By("Creating HomeAssistant CR")
			haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "1Gi"
  %s
`, haName, namespace, utils.GetEnhancedHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantConfiguration CR")
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
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for pod to be ready")
			podName := haName + "-0"
			Eventually(func(g Gomega) {
				phase := utils.Kubectl("get", "pod", podName, "-n", namespace,
					"-o", "jsonpath={.status.phase}")
				g.Expect(phase).To(Equal("Running"))

				readyOutput := utils.Kubectl("get", "pod", podName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(readyOutput).To(Equal("True"))
			}, utils.HAPodReadyTimeout, 10*time.Second).Should(Succeed())

			By("Deleting HomeAssistantConfiguration CR")
			deleteCmd := exec.Command("kubectl", "delete", "haconfig", configName, "-n", namespace)
			_, err := utils.Run(deleteCmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying ConfigMap deleted (cascade via owner ref)")
			configMapName := haName + "-configuration"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace, "--ignore-not-found")
				g.Expect(output).To(BeEmpty())
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying HA status goes NotReady")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("False"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Recreating HomeAssistantConfiguration CR for recovery")
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Verifying HA recovers to Ready=True")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.HAPodReadyTimeout, 5*time.Second).Should(Succeed())
		})
	})

	// =========================================================================
	// GROUP: HomeAssistantSecrets Controller - negative
	// =========================================================================

	Context("HomeAssistantSecrets Controller - negative", func() {
		It("N3: Reference to nonexistent Secret", Label("fast", "infra-only"), func() {
			By("Creating HomeAssistant CR")
			haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "1Gi"
  %s
`, haName, namespace, utils.GetDefaultHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantConfiguration CR")
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
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantSecrets referencing nonexistent Secret")
			haSecretsYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantSecrets
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  autoRestart: false
  secretRefs:
    - name: nonexistent-secret
`, secretsName, namespace, haName)
			Expect(utils.ApplyYAML(haSecretsYAML, namespace)).To(Succeed())

			By("Verifying status Ready=False with reason about missing Secret")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "hasecrets", secretsName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("False"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying generated Secret NOT created")
			generatedSecretName := haName + "-generated-secrets"
			output := utils.Kubectl("get", "secret", generatedSecretName, "-n", namespace, "--ignore-not-found")
			Expect(output).To(BeEmpty())

			By("Creating the missing source Secret")
			secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: nonexistent-secret
  namespace: %s
type: Opaque
stringData:
  existing_key: "value1"
`, namespace)
			Expect(utils.ApplyYAML(secretYAML, namespace)).To(Succeed())

			By("Verifying reconciliation succeeds - Ready=True")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "hasecrets", secretsName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying generated Secret now exists")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "secret", generatedSecretName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())
		})

		It("N4: Secret with missing keys - partial extraction", Label("fast", "infra-only"), func() {
			By("Creating HomeAssistant CR")
			haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "1Gi"
  %s
`, haName, namespace, utils.GetDefaultHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantConfiguration CR")
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
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating K8s Secret with only 'existing_key'")
			sourceName := "partial-secret-" + utils.RandomString(6)
			secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  existing_key: "value1"
`, sourceName, namespace)
			Expect(utils.ApplyYAML(secretYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantSecrets requesting existing_key and missing_key")
			haSecretsYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantSecrets
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  autoRestart: false
  secretRefs:
    - name: %s
      keys:
        - existing_key
        - missing_key
`, secretsName, namespace, haName, sourceName)
			Expect(utils.ApplyYAML(haSecretsYAML, namespace)).To(Succeed())

			By("Verifying status Ready=True (graceful handling)")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "hasecrets", secretsName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying generated Secret contains existing_key")
			generatedSecretName := haName + "-generated-secrets"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "secret", generatedSecretName, "-n", namespace,
					"-o", "jsonpath={.data.secrets\\.yaml}")
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())
		})

		It("N5: HomeAssistantSecrets with nonexistent homeAssistantRef", Label("fast", "infra-only"), func() {
			By("Creating HomeAssistantSecrets CR with nonexistent homeAssistantRef")
			haSecretsYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantSecrets
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: nonexistent
  autoRestart: false
  secretRefs:
    - name: some-secret
`, secretsName, namespace)
			Expect(utils.ApplyYAML(haSecretsYAML, namespace)).To(Succeed())

			By("Verifying status Ready=False")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "hasecrets", secretsName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("False"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying generated Secret NOT created")
			generatedSecretName := "nonexistent-generated-secrets"
			output := utils.Kubectl("get", "secret", generatedSecretName, "-n", namespace, "--ignore-not-found")
			Expect(output).To(BeEmpty())

			By("Verifying operator pod is still Running")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pods", "-n", "homeassistant-operator-system",
					"-l", "control-plane=controller-manager",
					"-o", "jsonpath={.items[0].status.phase}")
				g.Expect(output).To(Equal("Running"))
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())
		})

		It("N6: Deleting source Secret triggers error state", Label("fast", "infra-only"), func() {
			sourceName := "delete-me-secret-" + utils.RandomString(6)

			By("Creating HomeAssistant CR")
			haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "1Gi"
  %s
`, haName, namespace, utils.GetDefaultHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantConfiguration CR")
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
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating source K8s Secret")
			secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  existing_key: "value1"
`, sourceName, namespace)
			Expect(utils.ApplyYAML(secretYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantSecrets CR")
			haSecretsYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantSecrets
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  autoRestart: false
  secretRefs:
    - name: %s
`, secretsName, namespace, haName, sourceName)
			Expect(utils.ApplyYAML(haSecretsYAML, namespace)).To(Succeed())

			By("Waiting for Ready=True")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "hasecrets", secretsName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Deleting the source K8s Secret")
			deleteCmd := exec.Command("kubectl", "delete", "secret", sourceName, "-n", namespace)
			_, err := utils.Run(deleteCmd)
			Expect(err).NotTo(HaveOccurred())

			By("Triggering reconciliation by annotating HomeAssistantSecrets")
			Expect(utils.PatchResource("hasecrets", secretsName, namespace, "merge",
				`{"metadata":{"annotations":{"trigger-reconcile":"now"}}}`)).To(Succeed())

			By("Verifying status Ready=False after reconciliation")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "hasecrets", secretsName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("False"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Recreating source Secret")
			Expect(utils.ApplyYAML(secretYAML, namespace)).To(Succeed())

			By("Triggering reconciliation again")
			Expect(utils.PatchResource("hasecrets", secretsName, namespace, "merge",
				`{"metadata":{"annotations":{"trigger-reconcile":"again"}}}`)).To(Succeed())

			By("Verifying recovery - Ready=True")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "hasecrets", secretsName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())
		})
	})

	// =========================================================================
	// GROUP: HomeAssistantConfiguration Controller - negative
	// =========================================================================

	Context("HomeAssistantConfiguration Controller - negative", func() {
		It("N7: HAConfig with nonexistent homeAssistantRef", Label("fast", "infra-only"), func() {
			By("Creating HAConfig CR with nonexistent homeAssistantRef")
			configYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: nonexistent
  configuration: |
    homeassistant:
      name: Home
    default_config:
`, configName, namespace)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Verifying status Ready=False")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "haconfig", configName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("False"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying ConfigMap NOT created")
			configMapName := "nonexistent-configuration"
			output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace, "--ignore-not-found")
			Expect(output).To(BeEmpty())

			By("Verifying operator pod is still Running (controller doesn't crash)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pods", "-n", "homeassistant-operator-system",
					"-l", "control-plane=controller-manager",
					"-o", "jsonpath={.items[0].status.phase}")
				g.Expect(output).To(Equal("Running"))
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())
		})

		It("N8: Empty configuration", Label("fast", "infra-only"), func() {
			By("Creating HomeAssistant CR")
			haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "1Gi"
  %s
`, haName, namespace, utils.GetDefaultHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HAConfig with empty configuration")
			configYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  configuration: ""
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap created with empty content")
			configMapName := haName + "-configuration"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying status Ready=True")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "haconfig", configName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying status.configHash is set")
			Eventually(func(g Gomega) {
				hash := utils.Kubectl("get", "haconfig", configName, "-n", namespace,
					"-o", "jsonpath={.status.configHash}")
				g.Expect(hash).NotTo(BeEmpty())
			}, utils.StatusUpdateTimeout, 2*time.Second).Should(Succeed())
		})
	})

	// =========================================================================
	// GROUP: HomeAssistantAutomation Controller - negative
	// =========================================================================

	Context("HomeAssistantAutomation Controller - negative", func() {
		It("N9: Automation with nonexistent homeAssistantRef", Label("fast", "infra-only"), func() {
			By("Creating HomeAssistantAutomation with nonexistent homeAssistantRef")
			automationYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: nonexistent
  enabled: true
  alias: "Test Automation"
  triggers:
    - platform: time
      at: "12:00:00"
  actions:
    - service: light.turn_on
      target:
        entity_id: light.test
`, automationName, namespace)
			Expect(utils.ApplyYAML(automationYAML, namespace)).To(Succeed())

			By("Verifying status Ready=False")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("False"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying ConfigMap NOT created")
			configMapName := "nonexistent-automations"
			output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace, "--ignore-not-found")
			Expect(output).To(BeEmpty())
		})

		It("N10: Automation with empty triggers (CRD validation)", Label("skip"), func() {
			By("Attempting to apply automation YAML with empty triggers")
			automationYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  enabled: true
  alias: "Empty Triggers"
  triggers: []
  actions:
    - service: light.turn_on
      target:
        entity_id: light.test
`, automationName, namespace, haName)

			err := utils.ApplyYAML(automationYAML, namespace)

			By("Verifying kubectl apply returns validation error")
			Expect(err).To(HaveOccurred(), "Expected CRD validation to reject empty triggers")

			By("Verifying CR was not created in cluster")
			output := utils.Kubectl("get", "haauto", automationName, "-n", namespace, "--ignore-not-found")
			Expect(output).To(BeEmpty())
		})

		It("N11: Automation hot-reload without API token - graceful skip", Label("fast", "infra-only"), func() {
			By("Creating HomeAssistant CR WITHOUT bootstrap")
			haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "1Gi"
  %s
`, haName, namespace, utils.GetDefaultHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantConfiguration CR")
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
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating automation (autoReload defaults to true)")
			automationYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  enabled: true
  alias: "No Token Automation"
  triggers:
    - platform: time
      at: "12:00:00"
  actions:
    - service: light.turn_on
      target:
        entity_id: light.test
`, automationName, namespace, haName)
			Expect(utils.ApplyYAML(automationYAML, namespace)).To(Succeed())

			By("Waiting for ConfigMap to be created")
			configMapName := haName + "-automations"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("No Token Automation"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Updating automation to trigger reload attempt")
			updatedYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  enabled: true
  alias: "Updated No Token"
  triggers:
    - platform: time
      at: "13:00:00"
  actions:
    - service: light.turn_on
      target:
        entity_id: light.test
`, automationName, namespace, haName)
			Expect(utils.ApplyYAML(updatedYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap updated")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("Updated No Token"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying status Ready=True (graceful skip of reload)")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying no StatefulSet annotation change (no pod restart triggered)")
			// For fast test, verify StatefulSet annotation didn't change (which would trigger restart)
			// Don't wait for pod as that requires 12+ min startup
			Consistently(func(g Gomega) {
				// Check that automations-hash annotation is not set (would only be set if restart was triggered)
				hash := utils.Kubectl("get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.spec.template.metadata.annotations.ha\\.homeassistant\\.io/automations-hash}")
				// Should be empty since no bootstrap/API token means no StatefulSet annotation update
				g.Expect(hash).To(BeEmpty())
			}, 10*time.Second, 2*time.Second).Should(Succeed())
		})
	})

	// =========================================================================
	// GROUP: Cascade Delete & Cleanup
	// =========================================================================

	Context("Cascade Delete & Cleanup", func() {
		It("N12: Deleting HA CR cleans up child resources", Label("fast", "infra-only"), func() {
			auto1Name := "cleanup-auto1-" + utils.RandomString(6)
			auto2Name := "cleanup-auto2-" + utils.RandomString(6)
			sourceName := "cleanup-secret-" + utils.RandomString(6)

			By("Creating source K8s Secret")
			secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  existing_key: "value1"
`, sourceName, namespace)
			Expect(utils.ApplyYAML(secretYAML, namespace)).To(Succeed())

			By("Creating HomeAssistant CR")
			haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "1Gi"
  %s
`, haName, namespace, utils.GetDefaultHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantConfiguration CR")
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
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantSecrets CR")
			haSecretsYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantSecrets
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  autoRestart: false
  secretRefs:
    - name: %s
`, secretsName, namespace, haName, sourceName)
			Expect(utils.ApplyYAML(haSecretsYAML, namespace)).To(Succeed())

			By("Creating first automation")
			auto1YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  enabled: true
  alias: "Cleanup Auto 1"
  triggers:
    - platform: time
      at: "12:00:00"
  actions:
    - service: light.turn_on
      target:
        entity_id: light.test
`, auto1Name, namespace, haName)
			Expect(utils.ApplyYAML(auto1YAML, namespace)).To(Succeed())

			By("Creating second automation")
			auto2YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  enabled: true
  alias: "Cleanup Auto 2"
  triggers:
    - platform: time
      at: "13:00:00"
  actions:
    - service: light.turn_off
      target:
        entity_id: light.test
`, auto2Name, namespace, haName)
			Expect(utils.ApplyYAML(auto2YAML, namespace)).To(Succeed())

			By("Waiting for StatefulSet to exist")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "statefulset", haName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Waiting for Service to exist")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "service", haName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Waiting for ConfigMaps to exist")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", haName+"-configuration", "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", haName+"-automations", "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Waiting for generated Secret to exist")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "secret", haName+"-generated-secrets", "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			By("Deleting HA CR")
			deleteCmd := exec.Command("kubectl", "delete", "ha", haName, "-n", namespace)
			_, err := utils.Run(deleteCmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying StatefulSet deleted")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "statefulset", haName, "-n", namespace, "--ignore-not-found")
				g.Expect(output).To(BeEmpty())
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying Service deleted")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "service", haName, "-n", namespace, "--ignore-not-found")
				g.Expect(output).To(BeEmpty())
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying HAConfig CR still exists")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haconfig", configName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			By("Verifying HomeAssistantSecrets CR still exists")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hasecrets", secretsName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			By("Verifying Automation CRs still exist")
			for _, name := range []string{auto1Name, auto2Name} {
				output := utils.Kubectl("get", "haauto", name, "-n", namespace)
				Expect(output).NotTo(BeEmpty())
			}
		})

		It("N13: Deleting namespace cleans everything", Label("fast", "infra-only"), func() {
			By("Creating HomeAssistant CR")
			haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "1Gi"
  %s
`, haName, namespace, utils.GetDefaultHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantConfiguration CR")
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
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			sourceName := "ns-cleanup-secret-" + utils.RandomString(6)
			By("Creating source Secret")
			secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  existing_key: "value1"
`, sourceName, namespace)
			Expect(utils.ApplyYAML(secretYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantSecrets CR")
			haSecretsYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantSecrets
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  autoRestart: false
  secretRefs:
    - name: %s
`, secretsName, namespace, haName, sourceName)
			Expect(utils.ApplyYAML(haSecretsYAML, namespace)).To(Succeed())

			By("Creating automation")
			automationYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  enabled: true
  alias: "NS Cleanup Test"
  triggers:
    - platform: time
      at: "12:00:00"
  actions:
    - service: light.turn_on
      target:
        entity_id: light.test
`, automationName, namespace, haName)
			Expect(utils.ApplyYAML(automationYAML, namespace)).To(Succeed())

			By("Waiting for resources to be created")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "statefulset", haName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Deleting the namespace")
			deleteCmd := exec.Command("kubectl", "delete", "namespace", namespace, "--timeout=2m")
			_, err := utils.Run(deleteCmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying namespace is deleted (not stuck in Terminating)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "namespace", namespace, "--ignore-not-found")
				g.Expect(output).To(BeEmpty())
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("Verifying all CRD resources deleted")
			output := utils.Kubectl("get", "ha", haName, "-n", namespace, "--ignore-not-found")
			Expect(output).To(BeEmpty())

			output = utils.Kubectl("get", "haconfig", configName, "-n", namespace, "--ignore-not-found")
			Expect(output).To(BeEmpty())

			output = utils.Kubectl("get", "hasecrets", secretsName, "-n", namespace, "--ignore-not-found")
			Expect(output).To(BeEmpty())

			output = utils.Kubectl("get", "haauto", automationName, "-n", namespace, "--ignore-not-found")
			Expect(output).To(BeEmpty())

			// Mark namespace as already deleted so AfterEach won't try again
			namespace = "already-deleted-" + utils.RandomString(8)
		})
	})
})

// collectNegativeDebugInfo collects debug information when a negative test fails.
func collectNegativeDebugInfo(namespace string, names ...string) {
	writeDebug := func(format string, args ...any) {
		_, _ = fmt.Fprintf(GinkgoWriter, format, args...)
	}

	writeDebug("\n=== NEGATIVE TEST DEBUG INFO ===\n")

	writeDebug("\n--- All resources in namespace ---\n")
	cmd := exec.Command("kubectl", "get", "ha,haconfig,hasecrets,haauto", "-n", namespace, "-o", "wide")
	output, err := utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- All pods in namespace ---\n")
	cmd = exec.Command("kubectl", "get", "pods", "-n", namespace, "-o", "wide")
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- ConfigMaps in namespace ---\n")
	cmd = exec.Command("kubectl", "get", "configmaps", "-n", namespace, "-o", "wide")
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- Secrets in namespace ---\n")
	cmd = exec.Command("kubectl", "get", "secrets", "-n", namespace, "-o", "wide")
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- StatefulSets in namespace ---\n")
	cmd = exec.Command("kubectl", "get", "statefulsets", "-n", namespace, "-o", "wide")
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	// Describe each named resource
	resourceTypes := []string{"ha", "haconfig", "hasecrets", "haauto"}
	for i, name := range names {
		if name == "" || i >= len(resourceTypes) {
			continue
		}
		writeDebug("\n--- Describe %s/%s ---\n", resourceTypes[i], name)
		cmd = exec.Command("kubectl", "describe", resourceTypes[i], name, "-n", namespace)
		output, err = utils.Run(cmd)
		if err == nil {
			writeDebug("%s\n", output)
		}
	}

	writeDebug("\n--- Events ---\n")
	cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- Operator Logs (last 100 lines) ---\n")
	cmd = exec.Command("kubectl", "logs", "-n", "homeassistant-operator-system",
		"-l", "control-plane=controller-manager", "--tail=100")
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n=== END NEGATIVE TEST DEBUG INFO ===\n")
}
