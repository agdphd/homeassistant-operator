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

var _ = Describe("HomeAssistantAutomation E2E", Ordered, func() {
	var (
		namespace      string
		haName         string
		configName     string
		automationName string
	)

	BeforeEach(func() {
		namespace = "haauto-e2e-" + utils.RandomString(8)
		haName = "test-ha-" + utils.RandomString(6)
		configName = "test-config-" + utils.RandomString(6)
		automationName = "test-automation-" + utils.RandomString(6)

		By("Creating test namespace: " + namespace)
		Expect(utils.CreateNamespace(namespace)).To(Succeed())
	})

	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Test failed - collecting debug info")
			collectAutomationDebugInfo(namespace, haName, automationName)
		}

		By("Deleting test namespace: " + namespace)
		if err := utils.DeleteNamespace(namespace); err != nil {
			// Log error but don't fail the test - cleanup is best effort
			fmt.Printf("Warning: failed to delete namespace %s: %v\n", namespace, err)
		}
	})

	Context("Basic Automation Lifecycle", Label("automation", "fast", "infra-only"), func() {
		It("should create automation and generate ConfigMap", func() {
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
`, haName, namespace)
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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantAutomation CR")
			automationYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "Sunset Lights"
  triggers:
    - platform: sun
      event: sunset
  actions:
    - service: light.turn_on
      target:
        entity_id: light.living_room
`, automationName, namespace, haName)
			Expect(utils.ApplyYAML(automationYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap created with automation")
			configMapName := haName + "-automations"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).NotTo(BeEmpty())
				g.Expect(output).To(ContainSubstring("Sunset Lights"))
				g.Expect(output).To(ContainSubstring("id: " + automationName))
				g.Expect(output).To(ContainSubstring("platform: sun"))
				g.Expect(output).To(ContainSubstring("light.turn_on"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying automation status Ready=True")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying automation hash populated")
			hash := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
				"-o", "jsonpath={.status.automationHash}")
			Expect(hash).NotTo(BeEmpty())
		})

		It("should aggregate multiple automations into one ConfigMap", func() {
			auto1Name := "sunset-lights-" + utils.RandomString(6)
			auto2Name := "morning-alarm-" + utils.RandomString(6)
			auto3Name := "door-notify-" + utils.RandomString(6)

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
`, haName, namespace)
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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating first automation: sunset-lights")
			auto1YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "Sunset Lights"
  triggers:
    - platform: sun
      event: sunset
  actions:
    - service: light.turn_on
      target:
        entity_id: light.living_room
`, auto1Name, namespace, haName)
			Expect(utils.ApplyYAML(auto1YAML, namespace)).To(Succeed())

			By("Creating second automation: morning-alarm")
			auto2YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "Morning Alarm"
  triggers:
    - platform: time
      at: "07:00:00"
  actions:
    - service: media_player.play_media
      target:
        entity_id: media_player.bedroom
`, auto2Name, namespace, haName)
			Expect(utils.ApplyYAML(auto2YAML, namespace)).To(Succeed())

			By("Creating third automation: door-notify")
			auto3YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "Door Notification"
  triggers:
    - platform: state
      entity_id: binary_sensor.front_door
      to: "on"
  actions:
    - service: notify.mobile_app
      data:
        message: "Front door opened"
`, auto3Name, namespace, haName)
			Expect(utils.ApplyYAML(auto3YAML, namespace)).To(Succeed())

			By("Verifying ConfigMap contains all 3 automations")
			configMapName := haName + "-automations"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).NotTo(BeEmpty())
				g.Expect(output).To(ContainSubstring("Sunset Lights"))
				g.Expect(output).To(ContainSubstring("Morning Alarm"))
				g.Expect(output).To(ContainSubstring("Door Notification"))
				g.Expect(output).To(ContainSubstring("id: " + auto1Name))
				g.Expect(output).To(ContainSubstring("id: " + auto2Name))
				g.Expect(output).To(ContainSubstring("id: " + auto3Name))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying all 3 automations have Ready=True")
			for _, name := range []string{auto1Name, auto2Name, auto3Name} {
				Eventually(func(g Gomega) {
					status := utils.Kubectl("get", "haauto", name, "-n", namespace,
						"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
					g.Expect(status).To(Equal("True"))
				}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())
			}

			By("Verifying ConfigMap is owned by HomeAssistant (not individual automations)")
			ownerKind := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
				"-o", "jsonpath={.metadata.ownerReferences[0].kind}")
			Expect(ownerKind).To(Equal("HomeAssistant"))
		})

		It("should skip disabled automation in ConfigMap", func() {
			enabledAutoName := "enabled-auto-" + utils.RandomString(6)
			disabledAutoName := "disabled-auto-" + utils.RandomString(6)

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
`, haName, namespace)
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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating enabled automation")
			enabledYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "Enabled Automation"
  enabled: true
  triggers:
    - platform: time
      at: "12:00:00"
  actions:
    - service: light.turn_on
`, enabledAutoName, namespace, haName)
			Expect(utils.ApplyYAML(enabledYAML, namespace)).To(Succeed())

			By("Creating disabled automation")
			disabledYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "Disabled Automation"
  enabled: false
  triggers:
    - platform: time
      at: "13:00:00"
  actions:
    - service: light.turn_off
`, disabledAutoName, namespace, haName)
			Expect(utils.ApplyYAML(disabledYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap contains ONLY enabled automation")
			configMapName := haName + "-automations"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).NotTo(BeEmpty())
				g.Expect(output).To(ContainSubstring("Enabled Automation"))
				g.Expect(output).NotTo(ContainSubstring("Disabled Automation"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying disabled automation has Ready=True but not in ConfigMap")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "haauto", disabledAutoName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Enabling previously disabled automation")
			patchCmd := exec.Command("kubectl", "patch", "haauto", disabledAutoName, "-n", namespace,
				"--type", "json", "-p", `[{"op":"replace","path":"/spec/enabled","value":true}]`)
			_, err := utils.Run(patchCmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying automation now appears in ConfigMap")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("Disabled Automation"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())
		})

		It("should update ConfigMap when automation spec changes", func() {
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
`, haName, namespace)
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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating automation with brightness: 100")
			automationYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "Test Light Control"
  triggers:
    - platform: time
      at: "18:00:00"
  actions:
    - service: light.turn_on
      target:
        entity_id: light.bedroom
      data:
        brightness: 100
`, automationName, namespace, haName)
			Expect(utils.ApplyYAML(automationYAML, namespace)).To(Succeed())

			By("Waiting for initial ConfigMap")
			configMapName := haName + "-automations"
			var initialHash string
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("brightness: 100"))

				hash := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
					"-o", "jsonpath={.status.automationHash}")
				g.Expect(hash).NotTo(BeEmpty())
				initialHash = hash
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Updating automation - changing brightness to 50")
			updatedYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "Test Light Control"
  triggers:
    - platform: time
      at: "18:00:00"
  actions:
    - service: light.turn_on
      target:
        entity_id: light.bedroom
      data:
        brightness: 50
`, automationName, namespace, haName)
			Expect(utils.ApplyYAML(updatedYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap updated with new brightness")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("brightness: 50"))
				g.Expect(output).NotTo(ContainSubstring("brightness: 100"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying automation hash changed")
			Eventually(func(g Gomega) {
				newHash := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
					"-o", "jsonpath={.status.automationHash}")
				g.Expect(newHash).NotTo(Equal(initialHash))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying status Ready=True")
			status := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			Expect(status).To(Equal("True"))
		})
	})

	Context("Finalizer & Deletion", Label("automation", "fast", "infra-only"), func() {
		It("should update ConfigMap when deleting one of multiple automations", func() {
			auto1Name := "keep-auto1-" + utils.RandomString(6)
			auto2Name := "delete-auto-" + utils.RandomString(6)
			auto3Name := "keep-auto2-" + utils.RandomString(6)

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
`, haName, namespace)
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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating 3 automations")
			for i, name := range []string{auto1Name, auto2Name, auto3Name} {
				autoYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "Automation %d"
  triggers:
    - platform: time
      at: "12:00:00"
  actions:
    - service: light.turn_on
`, name, namespace, haName, i+1)
				Expect(utils.ApplyYAML(autoYAML, namespace)).To(Succeed())
			}

			By("Waiting for ConfigMap with 3 automations")
			configMapName := haName + "-automations"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("Automation 1"))
				g.Expect(output).To(ContainSubstring("Automation 2"))
				g.Expect(output).To(ContainSubstring("Automation 3"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Deleting automation 2")
			deleteCmd := exec.Command("kubectl", "delete", "haauto", auto2Name, "-n", namespace)
			_, err := utils.Run(deleteCmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying ConfigMap now contains only 2 automations")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("Automation 1"))
				g.Expect(output).NotTo(ContainSubstring("Automation 2"))
				g.Expect(output).To(ContainSubstring("Automation 3"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying remaining automations still Ready=True")
			for _, name := range []string{auto1Name, auto3Name} {
				status := utils.Kubectl("get", "haauto", name, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				Expect(status).To(Equal("True"))
			}

			By("Verifying deleted automation CR no longer exists")
			checkCmd := exec.Command("kubectl", "get", "haauto", auto2Name, "-n", namespace)
			_, err = utils.Run(checkCmd)
			Expect(err).To(HaveOccurred()) // Should fail - resource deleted
		})

		It("should leave ConfigMap with empty list when deleting last automation", func() {
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
`, haName, namespace)
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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating single automation")
			automationYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "Last Automation"
  triggers:
    - platform: time
      at: "12:00:00"
  actions:
    - service: light.turn_on
`, automationName, namespace, haName)
			Expect(utils.ApplyYAML(automationYAML, namespace)).To(Succeed())

			By("Waiting for ConfigMap with 1 automation")
			configMapName := haName + "-automations"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("Last Automation"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Deleting the automation")
			deleteCmd := exec.Command("kubectl", "delete", "haauto", automationName, "-n", namespace)
			_, err := utils.Run(deleteCmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying ConfigMap exists with empty array")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				// ConfigMap should exist but with empty array "[]" or equivalent
				g.Expect(output).To(Or(Equal("[]"), Equal(""), ContainSubstring("[]")))
				g.Expect(output).NotTo(ContainSubstring("Last Automation"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying automation CR deleted")
			checkCmd := exec.Command("kubectl", "get", "haauto", automationName, "-n", namespace)
			_, err = utils.Run(checkCmd)
			Expect(err).To(HaveOccurred())
		})

		It("should use finalizer to update ConfigMap before deletion", func() {
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
`, haName, namespace)
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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating automation")
			automationYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "Test Finalizer"
  triggers:
    - platform: time
      at: "12:00:00"
  actions:
    - service: light.turn_on
`, automationName, namespace, haName)
			Expect(utils.ApplyYAML(automationYAML, namespace)).To(Succeed())

			By("Waiting for automation to be ready")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying finalizer is present")
			finalizers := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
				"-o", "jsonpath={.metadata.finalizers}")
			Expect(finalizers).To(ContainSubstring("ha.homeassistant.io/automation-finalizer"))

			By("Deleting automation")
			deleteCmd := exec.Command("kubectl", "delete", "haauto", automationName, "-n", namespace, "--wait=false")
			_, err := utils.Run(deleteCmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying ConfigMap is updated (automation removed)")
			configMapName := haName + "-automations"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).NotTo(ContainSubstring("Test Finalizer"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying CR is finally deleted")
			Eventually(func(g Gomega) {
				checkCmd := exec.Command("kubectl", "get", "haauto", automationName, "-n", namespace)
				_, err := utils.Run(checkCmd)
				g.Expect(err).To(HaveOccurred()) // Should not exist
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())
		})
	})

	Context("Hot-Reload via REST API", Label("automation", "bootstrap", "slow", "pod-required"), func() {
		It("should hot-reload automation without pod restart when API token available", func() {
			By("Creating credentials Secret for bootstrap")
			credentialsSecretName := "bootstrap-creds-" + utils.RandomString(6)
			credentialsYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  username: "admin"
  password: "testpassword123"
`, credentialsSecretName, namespace)
			Expect(utils.ApplyYAML(credentialsYAML, namespace)).To(Succeed())

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
  %s
  bootstrap:
    enabled: true
    credentials:
      secretRef:
        name: %s
    createApiToken: true
    location:
      name: "Test Location"
      latitude: "52.2297"
      longitude: "21.0122"
      elevation: 100
      timeZone: "Europe/Warsaw"
      currency: "PLN"
      unitSystem: "metric"
`, haName, namespace, utils.GetEnhancedHAResourceRequests(), credentialsSecretName)
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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for bootstrap to complete and API token to be created")
			tokenSecretName := haName + "-homeassistant-api-token"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "secret", tokenSecretName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())

				bootstrapStatus := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.bootstrap.completed}")
				g.Expect(bootstrapStatus).To(Equal("true"))
			}, utils.BootstrapTimeout, 5*time.Second).Should(Succeed())

			By("Waiting for HA pod to be ready")
			podName := haName + "-0"
			Eventually(func(g Gomega) {
				phase := utils.Kubectl("get", "pod", podName, "-n", namespace,
					"-o", "jsonpath={.status.phase}")
				g.Expect(phase).To(Equal("Running"))
			}, utils.HAPodReadyTimeout, 5*time.Second).Should(Succeed())

			By("Capturing pod UID before automation update")
			initialPodUID := utils.Kubectl("get", "pod", podName, "-n", namespace,
				"-o", "jsonpath={.metadata.uid}")
			Expect(initialPodUID).NotTo(BeEmpty())

			By("Creating automation with autoReload enabled (default)")
			automationYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "Hot Reload Test"
  triggers:
    - platform: time
      at: "18:00:00"
  actions:
    - service: light.turn_on
      target:
        entity_id: light.bedroom
      data:
        brightness: 100
`, automationName, namespace, haName)
			Expect(utils.ApplyYAML(automationYAML, namespace)).To(Succeed())

			By("Waiting for automation to be ready")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Updating automation - changing brightness to trigger reload")
			updatedYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "Hot Reload Test"
  triggers:
    - platform: time
      at: "18:00:00"
  actions:
    - service: light.turn_on
      target:
        entity_id: light.bedroom
      data:
        brightness: 50
`, automationName, namespace, haName)
			Expect(utils.ApplyYAML(updatedYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap updated")
			configMapName := haName + "-automations"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("brightness: 50"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying lastReloadTime was updated")
			Eventually(func(g Gomega) {
				reloadTime := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
					"-o", "jsonpath={.status.lastReloadTime}")
				g.Expect(reloadTime).NotTo(BeEmpty())
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying pod UID NOT changed (hot-reload, not restart)")
			Consistently(func(g Gomega) {
				currentPodUID := utils.Kubectl("get", "pod", podName, "-n", namespace,
					"-o", "jsonpath={.metadata.uid}")
				g.Expect(currentPodUID).To(Equal(initialPodUID))
			}, 30*time.Second, 5*time.Second).Should(Succeed())

			By("Verifying status Ready=True")
			status := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			Expect(status).To(Equal("True"))
		})

		It("should skip reload when autoReload is false", func() {
			By("Creating credentials Secret for bootstrap")
			credentialsSecretName := "bootstrap-creds2-" + utils.RandomString(6)
			credentialsYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  username: "admin"
  password: "testpassword456"
`, credentialsSecretName, namespace)
			Expect(utils.ApplyYAML(credentialsYAML, namespace)).To(Succeed())

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
  %s
  bootstrap:
    enabled: true
    credentials:
      secretRef:
        name: %s
    createApiToken: true
`, haName, namespace, utils.GetEnhancedHAResourceRequests(), credentialsSecretName)
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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for bootstrap and pod ready")
			Eventually(func(g Gomega) {
				bootstrapStatus := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.bootstrap.completed}")
				g.Expect(bootstrapStatus).To(Equal("true"))
			}, utils.BootstrapTimeout, 5*time.Second).Should(Succeed())

			podName := haName + "-0"
			Eventually(func(g Gomega) {
				phase := utils.Kubectl("get", "pod", podName, "-n", namespace,
					"-o", "jsonpath={.status.phase}")
				g.Expect(phase).To(Equal("Running"))
			}, utils.HAPodReadyTimeout, 5*time.Second).Should(Succeed())

			By("Creating automation with autoReload: false")
			automationYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "No Auto Reload"
  autoReload: false
  triggers:
    - platform: time
      at: "19:00:00"
  actions:
    - service: light.turn_off
`, automationName, namespace, haName)
			Expect(utils.ApplyYAML(automationYAML, namespace)).To(Succeed())

			By("Waiting for automation to be ready")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Capturing initial lastReloadTime before update")
			initialReloadTime := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
				"-o", "jsonpath={.status.lastReloadTime}")

			By("Updating automation")
			patchCmd := exec.Command("kubectl", "patch", "haauto", automationName, "-n", namespace,
				"--type", "json", "-p", `[{"op":"replace","path":"/spec/alias","value":"Updated No Reload"}]`)
			_, err := utils.Run(patchCmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying ConfigMap updated")
			configMapName := haName + "-automations"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("Updated No Reload"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying lastReloadTime remains unchanged (autoReload: false)")
			Consistently(func(g Gomega) {
				reloadTime := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
					"-o", "jsonpath={.status.lastReloadTime}")
				g.Expect(reloadTime).To(Equal(initialReloadTime))
			}, 15*time.Second, 3*time.Second).Should(Succeed())

			By("Verifying pod UID not changed")
			initialPodUID := utils.Kubectl("get", "pod", podName, "-n", namespace,
				"-o", "jsonpath={.metadata.uid}")
			Consistently(func(g Gomega) {
				currentPodUID := utils.Kubectl("get", "pod", podName, "-n", namespace,
					"-o", "jsonpath={.metadata.uid}")
				g.Expect(currentPodUID).To(Equal(initialPodUID))
			}, 15*time.Second, 3*time.Second).Should(Succeed())
		})
	})

	Context("Enable/Disable Toggle", Label("automation", "fast", "infra-only"), func() {
		It("should toggle automation between enabled and disabled states", func() {
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
`, haName, namespace)
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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating automation with enabled: true")
			automationYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "Toggle Test"
  enabled: true
  triggers:
    - platform: time
      at: "12:00:00"
  actions:
    - service: light.turn_on
`, automationName, namespace, haName)
			Expect(utils.ApplyYAML(automationYAML, namespace)).To(Succeed())

			By("Verifying automation appears in ConfigMap")
			configMapName := haName + "-automations"
			var initialHash string
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("Toggle Test"))

				hash := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
					"-o", "jsonpath={.status.automationHash}")
				initialHash = hash
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Disabling automation (enabled: false)")
			patchCmd := exec.Command("kubectl", "patch", "haauto", automationName, "-n", namespace,
				"--type", "json", "-p", `[{"op":"replace","path":"/spec/enabled","value":false}]`)
			_, err := utils.Run(patchCmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying automation disappears from ConfigMap")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).NotTo(ContainSubstring("Toggle Test"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying CR still exists with Ready=True")
			status := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			Expect(status).To(Equal("True"))

			By("Verifying hash changed after disable")
			disabledHash := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
				"-o", "jsonpath={.status.automationHash}")
			Expect(disabledHash).NotTo(Equal(initialHash))

			By("Re-enabling automation (enabled: true)")
			patchCmd = exec.Command("kubectl", "patch", "haauto", automationName, "-n", namespace,
				"--type", "json", "-p", `[{"op":"replace","path":"/spec/enabled","value":true}]`)
			_, err = utils.Run(patchCmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying automation reappears in ConfigMap")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("Toggle Test"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying hash changed after re-enable")
			finalHash := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
				"-o", "jsonpath={.status.automationHash}")
			Expect(finalHash).NotTo(Equal(disabledHash))
		})
	})

	Context("Automation Spec Fields", Label("automation", "fast", "infra-only"), func() {
		It("should correctly serialize all spec fields to ConfigMap YAML", func() {
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
`, haName, namespace)
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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating automation with all optional fields")
			automationYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "Full Spec Test"
  description: "Test automation with all fields"
  mode: restart
  max: 5
  triggers:
    - platform: time
      at: "12:00:00"
    - platform: state
      entity_id: binary_sensor.motion
      to: "on"
  conditions:
    - condition: state
      entity_id: sun.sun
      state: "above_horizon"
  actions:
    - service: light.turn_on
      target:
        entity_id: light.living_room
      data:
        brightness: 255
    - service: notify.mobile_app
      data:
        message: "Lights turned on"
`, automationName, namespace, haName)
			Expect(utils.ApplyYAML(automationYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap YAML contains all fields with correct snake_case")
			configMapName := haName + "-automations"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")

				// Required fields
				g.Expect(output).To(ContainSubstring("id: " + automationName))
				g.Expect(output).To(ContainSubstring("alias: Full Spec Test"))

				// Optional fields
				g.Expect(output).To(ContainSubstring("description: Test automation with all fields"))
				g.Expect(output).To(ContainSubstring("mode: restart"))
				g.Expect(output).To(ContainSubstring("max: 5"))

				// Triggers (multiple)
				g.Expect(output).To(ContainSubstring("platform: time"))
				g.Expect(output).To(ContainSubstring("at: \"12:00:00\""))
				g.Expect(output).To(ContainSubstring("platform: state"))
				g.Expect(output).To(ContainSubstring("entity_id: binary_sensor.motion"))

				// Conditions
				g.Expect(output).To(ContainSubstring("condition: state"))
				g.Expect(output).To(ContainSubstring("entity_id: sun.sun"))

				// Actions (multiple)
				g.Expect(output).To(ContainSubstring("service: light.turn_on"))
				g.Expect(output).To(ContainSubstring("brightness: 255"))
				g.Expect(output).To(ContainSubstring("service: notify.mobile_app"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying status Ready=True")
			status := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			Expect(status).To(Equal("True"))
		})

		It("should handle minimal automation spec (only required fields)", func() {
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
`, haName, namespace)
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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating minimal automation (only required fields)")
			automationYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  alias: "Minimal Automation"
  triggers:
    - platform: time
      at: "06:00:00"
  actions:
    - service: light.turn_on
`, automationName, namespace, haName)
			Expect(utils.ApplyYAML(automationYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap generated correctly")
			configMapName := haName + "-automations"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")

				// Required fields present
				g.Expect(output).To(ContainSubstring("id: " + automationName))
				g.Expect(output).To(ContainSubstring("alias: Minimal Automation"))
				g.Expect(output).To(ContainSubstring("platform: time"))
				g.Expect(output).To(ContainSubstring("service: light.turn_on"))

				// Optional fields not present (or have defaults)
				// Note: Some fields might have default values set by controller
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying auto-generated ID matches CR name")
			output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
				"-o", "jsonpath={.data.automations\\.yaml}")
			Expect(output).To(ContainSubstring("id: " + automationName))

			By("Verifying status Ready=True")
			status := utils.Kubectl("get", "haauto", automationName, "-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			Expect(status).To(Equal("True"))
		})
	})
})

// collectAutomationDebugInfo collects debug information when automation test fails
func collectAutomationDebugInfo(namespace, haName, automationName string) {
	fmt.Println("\n=== DEBUG INFO: HomeAssistantAutomation Test Failed ===")

	// Automation CR describe
	fmt.Println("\n--- HomeAssistantAutomation describe ---")
	descCmd := exec.Command("kubectl", "describe", "haauto", automationName, "-n", namespace)
	output, _ := descCmd.CombinedOutput()
	fmt.Println(string(output))

	// Automation status
	fmt.Println("\n--- HomeAssistantAutomation status ---")
	statusCmd := exec.Command("kubectl", "get", "haauto", automationName, "-n", namespace, "-o", "yaml")
	output, _ = statusCmd.CombinedOutput()
	fmt.Println(string(output))

	// ConfigMap content
	fmt.Println("\n--- Automations ConfigMap ---")
	configMapName := haName + "-automations"
	cmCmd := exec.Command("kubectl", "get", "configmap", configMapName, "-n", namespace, "-o", "yaml")
	output, _ = cmCmd.CombinedOutput()
	fmt.Println(string(output))

	// HomeAssistant CR
	fmt.Println("\n--- HomeAssistant CR ---")
	haCmd := exec.Command("kubectl", "get", "ha", haName, "-n", namespace, "-o", "yaml")
	output, _ = haCmd.CombinedOutput()
	fmt.Println(string(output))

	// Events
	fmt.Println("\n--- Events ---")
	eventsCmd := exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
	output, _ = eventsCmd.CombinedOutput()
	fmt.Println(string(output))

	// Operator logs (last 50 lines)
	fmt.Println("\n--- Operator logs (last 50 lines) ---")
	logsCmd := exec.Command("kubectl", "logs", "-n", "homeassistant-operator-system",
		"-l", "control-plane=controller-manager", "--tail=50")
	output, _ = logsCmd.CombinedOutput()
	fmt.Println(string(output))

	fmt.Println("\n=== END DEBUG INFO ===")
}
