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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/przemekhys/homeassistant-operator/test/utils"
)

const (
	// Test resource naming for integration tests
	integrationTestNamespacePrefix = "haint-e2e-"
)

var _ = Describe("HomeAssistantIntegration E2E", Label("integration"), Ordered, func() {
	var namespace string
	var haName string

	BeforeEach(func() {
		namespace = integrationTestNamespacePrefix + utils.RandomString(8)
		haName = "test-ha-" + utils.RandomString(6)

		By("Creating test namespace: " + namespace)
		Expect(utils.CreateNamespace(namespace)).To(Succeed())
	})

	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Test failed - collecting debug info")
			collectIntegrationDebugInfo(namespace, haName)
		}

		By("Deleting test namespace: " + namespace)
		if err := utils.DeleteNamespace(namespace); err != nil {
			fmt.Printf("Warning: failed to delete namespace %s: %v\n", namespace, err)
		}
	})

	Context("Operator Lifecycle", Label("fast"), func() {
		It("should create HomeAssistantIntegration CR", func() {
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
  service:
    type: ClusterIP
    port: 8123
  %s
`, haName, namespace, utils.GetDefaultHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantIntegration CR")
			intName := "test-int-" + utils.RandomString(6)
			intYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantIntegration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  domain: recorder
`, intName, namespace, haName)
			Expect(utils.ApplyYAML(intYAML, namespace)).To(Succeed())

			By("Verifying finalizer ha.homeassistant.io/integration-finalizer is added")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haint", intName, "-n", namespace,
					"-o", "jsonpath={.metadata.finalizers}")
				g.Expect(output).To(ContainSubstring("ha.homeassistant.io/integration-finalizer"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})

		It("should set IntegrationReady=False when HA not bootstrapped (no API token)", func() {
			By("Creating HomeAssistant CR without bootstrap")
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

			By("Creating HomeAssistantIntegration CR")
			intName := "test-int-" + utils.RandomString(6)
			intYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantIntegration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  domain: recorder
`, intName, namespace, haName)
			Expect(utils.ApplyYAML(intYAML, namespace)).To(Succeed())

			By("Verifying IntegrationReady condition is set to False with reason TokenNotAvailable")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "haint", intName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='IntegrationReady')].status}")
				g.Expect(status).To(Equal("False"))

				reason := utils.Kubectl("get", "haint", intName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='IntegrationReady')].reason}")
				g.Expect(reason).To(Equal("TokenNotAvailable"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying IntegrationReady is NOT set to True")
			output := utils.Kubectl("get", "haint", intName, "-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='IntegrationReady')].status}")
			Expect(output).NotTo(Equal("True"))
		})

		It("should set IntegrationReady=False when referenced HomeAssistant does not exist", func() {
			By("Creating HomeAssistantIntegration with non-existent HA reference")
			intName := "test-int-" + utils.RandomString(6)
			intYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantIntegration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: non-existent-ha
  domain: recorder
`, intName, namespace)
			Expect(utils.ApplyYAML(intYAML, namespace)).To(Succeed())

			By("Verifying IntegrationReady=False with reason HANotReady")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "haint", intName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='IntegrationReady')].status}")
				g.Expect(status).To(Equal("False"))

				reason := utils.Kubectl("get", "haint", intName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='IntegrationReady')].reason}")
				g.Expect(reason).To(Equal("HANotReady"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())
		})

		It("should delete CR cleanly when no bootstrap token", func() {
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

			By("Creating HomeAssistantIntegration CR")
			intName := "test-int-" + utils.RandomString(6)
			intYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantIntegration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  domain: recorder
`, intName, namespace, haName)
			Expect(utils.ApplyYAML(intYAML, namespace)).To(Succeed())

			By("Waiting for finalizer to be added")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haint", intName, "-n", namespace,
					"-o", "jsonpath={.metadata.finalizers}")
				g.Expect(output).To(ContainSubstring("ha.homeassistant.io/integration-finalizer"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Deleting the HomeAssistantIntegration CR")
			output := utils.Kubectl("delete", "haint", intName, "-n", namespace)
			Expect(output).NotTo(ContainSubstring("Error"))

			By("Verifying the CR is fully deleted")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haint", intName, "-n", namespace, "--ignore-not-found")
				g.Expect(output).To(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})
	})

	Context("Full lifecycle with bootstrap", Label("bootstrap", "slow", "pod-required"), func() {
		const intBootstrapSecret = "ha-int-bootstrap-creds"

		// applyBootstrappedHA creates bootstrap credentials, HA CR and HAC, then waits
		// for the bootstrap process to complete and the pod to be Ready.
		applyBootstrappedHA := func(namespace, haName string) {
			By("Creating bootstrap credentials Secret")
			credsYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  username: admin
  password: e2e-int-bootstrap-pwd-123456
`, intBootstrapSecret, namespace)
			Expect(utils.ApplyYAML(credsYAML, namespace)).To(Succeed())

			By("Creating HomeAssistant CR with bootstrap enabled")
			haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "1Gi"
  bootstrap:
    enabled: true
    credentials:
      secretRef:
        name: %s
    createApiToken: true
    apiTokenSecretName: %s-homeassistant-api-token
    ownerName: "E2E Int Admin"
    language: "en"
  %s
`, haName, namespace, intBootstrapSecret, haName, utils.GetEnhancedHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating minimal HomeAssistantConfiguration")
			configName := "test-config-" + utils.RandomString(6)
			configYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  configuration: |
    automation: !include automations.yaml
    scene: []
    script: []
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for bootstrap to complete")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.bootstrap.completed}")
				g.Expect(output).To(Equal("true"))
			}, utils.BootstrapTimeout, bootstrapInterval).Should(Succeed())

			By("Waiting for pod to be fully Ready")
			Eventually(func(g Gomega) {
				phase := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
					"-o", "jsonpath={.status.phase}")
				g.Expect(phase).To(Equal("Running"))
				ready := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(ready).To(Equal("True"))
			}, utils.HAPodReadyTimeout, haPodReadyInterval).Should(Succeed())
		}

		It("should set IntegrationReady=True and store entryID after config flow", func() {
			applyBootstrappedHA(namespace, haName)

			By("Creating HomeAssistantIntegration for moon (name field is optional, form returns create_entry)")
			intName := "test-int-" + utils.RandomString(6)
			intYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantIntegration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  domain: moon
  configuration:
    name:
      value: "Moon"
`, intName, namespace, haName)
			Expect(utils.ApplyYAML(intYAML, namespace)).To(Succeed())

			By("Verifying IntegrationReady=True")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "haint", intName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='IntegrationReady')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())

			By("Verifying entryID is set in status")
			Eventually(func(g Gomega) {
				entryID := utils.Kubectl("get", "haint", intName, "-n", namespace,
					"-o", "jsonpath={.status.entryID}")
				g.Expect(entryID).NotTo(BeEmpty())
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying configHash is set (SHA256)")
			Eventually(func(g Gomega) {
				hash := utils.Kubectl("get", "haint", intName, "-n", namespace,
					"-o", "jsonpath={.status.configHash}")
				g.Expect(hash).To(MatchRegexp("^[a-f0-9]{64}$"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())
		})

		It("should requeue and eventually become Ready when bootstrap token becomes available", func() {
			By("Creating HomeAssistant CR without bootstrap")
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

			By("Creating HomeAssistantIntegration before bootstrap")
			intName := "test-int-" + utils.RandomString(6)
			intYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantIntegration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  domain: moon
  configuration:
    name:
      value: "Moon"
`, intName, namespace, haName)
			Expect(utils.ApplyYAML(intYAML, namespace)).To(Succeed())

			By("Verifying IntegrationReady=False with TokenNotAvailable")
			Eventually(func(g Gomega) {
				reason := utils.Kubectl("get", "haint", intName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='IntegrationReady')].reason}")
				g.Expect(reason).To(Equal("TokenNotAvailable"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Enabling bootstrap on the HomeAssistant CR")
			credsYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  username: admin
  password: e2e-int-bootstrap-pwd-123456
`, intBootstrapSecret, namespace)
			Expect(utils.ApplyYAML(credsYAML, namespace)).To(Succeed())

			configName := "test-config-" + utils.RandomString(6)
			configYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  configuration: |
    automation: !include automations.yaml
    scene: []
    script: []
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			patchYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "1Gi"
  bootstrap:
    enabled: true
    credentials:
      secretRef:
        name: %s
    createApiToken: true
    apiTokenSecretName: %s-homeassistant-api-token
    ownerName: "E2E Int Admin"
    language: "en"
  %s
`, haName, namespace, intBootstrapSecret, haName, utils.GetEnhancedHAResourceRequests())
			Expect(utils.ApplyYAML(patchYAML, namespace)).To(Succeed())

			By("Waiting for bootstrap to complete")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.bootstrap.completed}")
				g.Expect(output).To(Equal("true"))
			}, utils.BootstrapTimeout, bootstrapInterval).Should(Succeed())

			By("Waiting for pod to be Ready")
			Eventually(func(g Gomega) {
				ready := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(ready).To(Equal("True"))
			}, utils.HAPodReadyTimeout, haPodReadyInterval).Should(Succeed())

			By("Verifying integration eventually becomes Ready=True after token is available")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "haint", intName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='IntegrationReady')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())
		})

		It("should remove finalizer and allow CR deletion after cleanup", func() {
			applyBootstrappedHA(namespace, haName)

			By("Creating HomeAssistantIntegration")
			intName := "test-int-" + utils.RandomString(6)
			intYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantIntegration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  domain: moon
  configuration:
    name:
      value: "Moon"
`, intName, namespace, haName)
			Expect(utils.ApplyYAML(intYAML, namespace)).To(Succeed())

			By("Waiting for IntegrationReady=True")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "haint", intName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='IntegrationReady')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())

			By("Deleting the HomeAssistantIntegration CR")
			output := utils.Kubectl("delete", "haint", intName, "-n", namespace)
			Expect(output).NotTo(ContainSubstring("Error"))

			By("Verifying the CR is fully removed (finalizer cleared)")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "haint", intName, "-n", namespace, "--ignore-not-found")
				g.Expect(out).To(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})

		It("should reconfigure (new configHash) when spec.configuration changes", func() {
			applyBootstrappedHA(namespace, haName)

			By("Creating HomeAssistantIntegration with initial configuration")
			intName := "test-int-" + utils.RandomString(6)
			intYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantIntegration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  domain: moon
  configuration:
    name:
      value: "Moon"
`, intName, namespace, haName)
			Expect(utils.ApplyYAML(intYAML, namespace)).To(Succeed())

			By("Waiting for IntegrationReady=True and recording initial configHash")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "haint", intName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='IntegrationReady')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())

			initialHash := utils.Kubectl("get", "haint", intName, "-n", namespace,
				"-o", "jsonpath={.status.configHash}")
			Expect(initialHash).NotTo(BeEmpty())

			By("Updating spec.configuration to trigger reconfiguration")
			// Changing the name value changes the configHash → triggers delete+recreate
			patchedYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantIntegration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  domain: moon
  configuration:
    name:
      value: "MoonRenamed"
`, intName, namespace, haName)
			Expect(utils.ApplyYAML(patchedYAML, namespace)).To(Succeed())

			By("Verifying configHash changes after spec update")
			Eventually(func(g Gomega) {
				newHash := utils.Kubectl("get", "haint", intName, "-n", namespace,
					"-o", "jsonpath={.status.configHash}")
				g.Expect(newHash).NotTo(BeEmpty())
				g.Expect(newHash).NotTo(Equal(initialHash))
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())

			By("Verifying IntegrationReady=True after reconfiguration")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "haint", intName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='IntegrationReady')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())
		})
	})
})

// collectIntegrationDebugInfo gathers debug information for failed integration tests
func collectIntegrationDebugInfo(namespace, haName string) {
	fmt.Printf("\n=== Integration Debug Info for namespace %s ===\n", namespace)

	fmt.Println("\n--- HomeAssistantIntegration resources ---")
	fmt.Println(utils.Kubectl("get", "haint", "-n", namespace, "-o", "wide"))

	fmt.Println("\n--- HomeAssistantIntegration status ---")
	jsonpath := `jsonpath={range .items[*]}{.metadata.name}: {.status}{"\n"}{end}`
	fmt.Println(utils.Kubectl("get", "haint", "-n", namespace, "-o", jsonpath))

	fmt.Println("\n--- HomeAssistant status ---")
	fmt.Println(utils.Kubectl("get", "ha", haName, "-n", namespace, "-o", "jsonpath={.status}"))

	fmt.Println("\n--- Recent events ---")
	fmt.Println(utils.Kubectl("get", "events", "-n", namespace, "--sort-by=.lastTimestamp"))
}
