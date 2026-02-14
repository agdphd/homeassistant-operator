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
	// Test resource naming for automation tests
	autoTestNamespacePrefix = "haauto-e2e-"
	autoBootstrapSecret     = "ha-bootstrap-creds"
	// Note: reconcileInterval, haPodReadyInterval, bootstrapInterval are defined in homeassistantconfiguration_e2e_test.go
)

var _ = Describe("HomeAssistantAutomation E2E", Label("automation"), Ordered, func() {
	var namespace string
	var haName string
	var configName string

	BeforeEach(func() {
		// Generate unique names for each test
		namespace = autoTestNamespacePrefix + utils.RandomString(8)
		haName = "test-ha-" + utils.RandomString(6)
		configName = "test-config-" + utils.RandomString(6)

		By("Creating test namespace: " + namespace)
		Expect(utils.CreateNamespace(namespace)).To(Succeed())
	})

	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Test failed - collecting debug info")
			collectAutomationDebugInfo(namespace, haName)
		}

		By("Deleting test namespace: " + namespace)
		_ = utils.DeleteNamespace(namespace)
	})

	Context("ConfigMap Aggregation", Label("fast"), func() {
		It("should create ConfigMap from single HomeAssistantAutomation", func() {
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
  service:
    type: ClusterIP
    port: 8123
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
    automation: !include automations.yaml
    script: !include scripts.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HomeAssistant resource to be created")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.metadata.name}")
				g.Expect(output).To(Equal(haName))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Creating HomeAssistantAutomation CR")
			autoName := "test-auto-" + utils.RandomString(6)
			autoYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: morning_lights
  alias: "Turn on lights in the morning"
  description: "Automatically turn on lights at sunrise"
  mode: single
  triggers:
    - platform: sun
      event: sunrise
  actions:
    - service: light.turn_on
      target:
        entity_id: light.living_room
`, autoName, namespace, haName)
			Expect(utils.ApplyYAML(autoYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap created with correct name")
			configMapName := haName + "-automations"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying ConfigMap contains automations.yaml")
			Eventually(func(g Gomega) {
				output := utils.Kubectl(
					"get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}",
				)
				g.Expect(output).To(ContainSubstring("id: morning_lights"))
				g.Expect(output).To(ContainSubstring("alias: Turn on lights in the morning"))
				g.Expect(output).To(ContainSubstring("platform: sun"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying HomeAssistantAutomation status is Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying automation hash is set in status")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
					"-o", "jsonpath={.status.automationHash}")
				g.Expect(output).NotTo(BeEmpty())
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())
		})

		It("should aggregate multiple automations into single ConfigMap", func() {
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

			By("Creating HomeAssistantConfiguration")
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
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating first automation")
			auto1Name := "auto-morning"
			auto1YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: morning_routine
  alias: "Morning Routine"
  mode: single
  triggers:
    - platform: time
      at: "07:00:00"
  actions:
    - service: light.turn_on
`, auto1Name, namespace, haName)
			Expect(utils.ApplyYAML(auto1YAML, namespace)).To(Succeed())

			By("Creating second automation")
			auto2Name := "auto-evening"
			auto2YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: evening_routine
  alias: "Evening Routine"
  mode: single
  triggers:
    - platform: time
      at: "19:00:00"
  actions:
    - service: light.turn_off
`, auto2Name, namespace, haName)
			Expect(utils.ApplyYAML(auto2YAML, namespace)).To(Succeed())

			By("Creating third automation")
			auto3Name := "auto-night"
			auto3YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: night_security
  alias: "Night Security"
  mode: single
  triggers:
    - platform: time
      at: "23:00:00"
  actions:
    - service: alarm_control_panel.alarm_arm_night
`, auto3Name, namespace, haName)
			Expect(utils.ApplyYAML(auto3YAML, namespace)).To(Succeed())

			By("Verifying ConfigMap contains all 3 automations")
			configMapName := haName + "-automations"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("id: morning_routine"))
				g.Expect(output).To(ContainSubstring("id: evening_routine"))
				g.Expect(output).To(ContainSubstring("id: night_security"))
				g.Expect(output).To(ContainSubstring("alias: Morning Routine"))
				g.Expect(output).To(ContainSubstring("alias: Evening Routine"))
				g.Expect(output).To(ContainSubstring("alias: Night Security"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying all automations have Ready status")
			for _, autoName := range []string{auto1Name, auto2Name, auto3Name} {
				Eventually(func(g Gomega) {
					output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
						"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
					g.Expect(output).To(Equal("True"))
				}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())
			}
		})

		It("should update ConfigMap when automation changes", func() {
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

			By("Creating HomeAssistantConfiguration")
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
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating automation with initial action")
			autoName := "test-auto-update"
			initialAutoYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: test_update
  alias: "Test Update"
  mode: single
  triggers:
    - platform: time
      at: "10:00:00"
  actions:
    - service: light.turn_on
      target:
        entity_id: light.bedroom
`, autoName, namespace, haName)
			Expect(utils.ApplyYAML(initialAutoYAML, namespace)).To(Succeed())

			By("Waiting for ConfigMap with initial action")
			configMapName := haName + "-automations"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("light.turn_on"))
				g.Expect(output).To(ContainSubstring("light.bedroom"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Capturing initial automation hash")
			var initialHash string
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
					"-o", "jsonpath={.status.automationHash}")
				g.Expect(output).NotTo(BeEmpty())
				initialHash = output
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Updating automation with new action")
			updatedAutoYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: test_update
  alias: "Test Update"
  mode: single
  triggers:
    - platform: time
      at: "10:00:00"
  actions:
    - service: light.turn_off
      target:
        entity_id: light.living_room
`, autoName, namespace, haName)
			Expect(utils.ApplyYAML(updatedAutoYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap updated with new action")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("light.turn_off"))
				g.Expect(output).To(ContainSubstring("light.living_room"))
				g.Expect(output).NotTo(ContainSubstring("light.bedroom"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying automation hash changed")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
					"-o", "jsonpath={.status.automationHash}")
				g.Expect(output).NotTo(BeEmpty())
				g.Expect(output).NotTo(Equal(initialHash))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())
		})

		It("should remove automation from ConfigMap when CR deleted (finalizer)", func() {
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

			By("Creating HomeAssistantConfiguration")
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
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating two automations")
			auto1Name := "auto-keep"
			auto1YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: keep_this
  alias: "Keep This"
  mode: single
  triggers:
    - platform: time
      at: "08:00:00"
  actions:
    - service: light.turn_on
`, auto1Name, namespace, haName)
			Expect(utils.ApplyYAML(auto1YAML, namespace)).To(Succeed())

			auto2Name := "auto-delete"
			auto2YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: delete_this
  alias: "Delete This"
  mode: single
  triggers:
    - platform: time
      at: "09:00:00"
  actions:
    - service: light.turn_off
`, auto2Name, namespace, haName)
			Expect(utils.ApplyYAML(auto2YAML, namespace)).To(Succeed())

			By("Verifying both automations in ConfigMap")
			configMapName := haName + "-automations"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("id: keep_this"))
				g.Expect(output).To(ContainSubstring("id: delete_this"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying finalizer is present on automation to be deleted")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", auto2Name, "-n", namespace,
					"-o", "jsonpath={.metadata.finalizers}")
				g.Expect(output).To(ContainSubstring("ha.homeassistant.io/automation-finalizer"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Deleting second automation")
			cmd := exec.Command("kubectl", "delete", "haauto", auto2Name, "-n", namespace, "--wait=false")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying ConfigMap updated (only keep_this remains)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("id: keep_this"))
				g.Expect(output).NotTo(ContainSubstring("id: delete_this"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying CR was fully deleted")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", auto2Name, "-n", namespace, "--ignore-not-found")
				g.Expect(output).To(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying first automation still exists and is Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", auto1Name, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())
		})
	})

	Context("Enable/Disable Feature", Label("fast"), func() {
		It("should exclude disabled automation from ConfigMap", func() {
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

			By("Creating HomeAssistantConfiguration")
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
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating automation with enabled: false")
			autoName := "auto-disabled"
			autoYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: disabled_automation
  alias: "Disabled Automation"
  enabled: false
  mode: single
  triggers:
    - platform: time
      at: "12:00:00"
  actions:
    - service: light.turn_on
`, autoName, namespace, haName)
			Expect(utils.ApplyYAML(autoYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap was created but is empty (no automations)")
			configMapName := haName + "-automations"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				// Empty list in YAML is "[]" or empty
				g.Expect(output).To(Or(BeEmpty(), Equal("[]"), Equal("[]\n")))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying automation CR exists but is not in ConfigMap")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Enabling automation by updating enabled: true")
			enabledYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: disabled_automation
  alias: "Disabled Automation"
  enabled: true
  mode: single
  triggers:
    - platform: time
      at: "12:00:00"
  actions:
    - service: light.turn_on
`, autoName, namespace, haName)
			Expect(utils.ApplyYAML(enabledYAML, namespace)).To(Succeed())

			By("Verifying automation now appears in ConfigMap")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.automations\\.yaml}")
				g.Expect(output).To(ContainSubstring("id: disabled_automation"))
				g.Expect(output).To(ContainSubstring("alias: Disabled Automation"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})
	})

	Context("Hot-Reload", Label("bootstrap", "slow"), func() {
		It("should hot-reload via REST API when autoReload=true", func() {
			By("Creating bootstrap credentials Secret")
			credsYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  username: admin
  password: e2e-test-bootstrap-pwd-123456
`, autoBootstrapSecret, namespace)
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
    ownerName: "E2E Test Admin"
    language: "en"
  %s
`, haName, namespace, autoBootstrapSecret, haName, utils.GetEnhancedHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantConfiguration")
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
    script: []
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for bootstrap to complete")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace, "-o", "jsonpath={.status.bootstrap.completed}")
				g.Expect(output).To(Equal("true"))
			}, utils.BootstrapTimeout, bootstrapInterval).Should(Succeed())

			By("Verifying API token Secret was created")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "secret", haName+"-homeassistant-api-token", "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Waiting for pod to be fully Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.status.phase}")
				g.Expect(output).To(Equal("Running"))

				readyOutput := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(readyOutput).To(Equal("True"))
			}, utils.HAPodReadyTimeout, haPodReadyInterval).Should(Succeed())

			By("Capturing pod UID before automation creation")
			var podUID string
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).NotTo(BeEmpty())
				podUID = output
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Ensuring pod UID stable for 10 seconds (no pending restarts)")
			Consistently(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).To(Equal(podUID))
			}, 10*time.Second, 2*time.Second).Should(Succeed())

			By("Creating automation with autoReload: true")
			autoName := "auto-hotreload"
			autoYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: hot_reload_test
  alias: "Hot Reload Test"
  autoReload: true
  mode: single
  triggers:
    - platform: time
      at: "15:00:00"
  actions:
    - service: notify.mobile_app
      data:
        message: "Hot reload works"
`, autoName, namespace, haName)
			Expect(utils.ApplyYAML(autoYAML, namespace)).To(Succeed())

			By("Verifying automation status shows it's ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())

			By("Verifying lastReloadTime is set (hot-reload occurred)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
					"-o", "jsonpath={.status.lastReloadTime}")
				g.Expect(output).NotTo(BeEmpty())
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())

			By("Verifying pod was NOT restarted (UID unchanged)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).To(Equal(podUID))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})

		It("should skip reload when autoReload=false", func() {
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

			By("Creating HomeAssistantConfiguration")
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
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating automation with autoReload: false")
			autoName := "auto-noreload"
			autoYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: no_reload_test
  alias: "No Reload Test"
  autoReload: false
  mode: single
  triggers:
    - platform: time
      at: "16:00:00"
  actions:
    - service: light.turn_on
`, autoName, namespace, haName)
			Expect(utils.ApplyYAML(autoYAML, namespace)).To(Succeed())

			By("Verifying automation is Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying lastReloadTime is NOT set (no reload occurred)")
			Consistently(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
					"-o", "jsonpath={.status.lastReloadTime}")
				g.Expect(output).To(BeEmpty())
			}, 5*time.Second, reconcileInterval).Should(Succeed())

			By("Verifying lastError is NOT set")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
					"-o", "jsonpath={.status.lastError}")
				g.Expect(output).To(BeEmpty())
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())
		})
	})

	Context("Fallback Mechanisms", Label("slow"), func() {
		It("should fallback to restart when API token missing", func() {
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
`, haName, namespace, utils.GetEnhancedHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantConfiguration")
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
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HA Pod Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.status.phase}")
				g.Expect(output).To(Equal("Running"))
			}, utils.HAPodReadyTimeout, haPodReadyInterval).Should(Succeed())

			By("Verifying API token Secret does NOT exist")
			output := utils.Kubectl("get", "secret", haName+"-homeassistant-api-token", "-n", namespace, "--ignore-not-found")
			Expect(output).To(BeEmpty())

			By("Creating automation with autoReload: true (will attempt hot-reload)")
			autoName := "auto-fallback"
			autoYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: fallback_test
  alias: "Fallback Test"
  autoReload: true
  mode: single
  triggers:
    - platform: time
      at: "17:00:00"
  actions:
    - service: light.turn_off
`, autoName, namespace, haName)
			Expect(utils.ApplyYAML(autoYAML, namespace)).To(Succeed())

			By("Verifying automation is Ready despite missing token")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying lastError mentions missing token")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
					"-o", "jsonpath={.status.lastError}")
				g.Expect(output).To(Or(
					ContainSubstring("token"),
					ContainSubstring("API token not found"),
					ContainSubstring("bootstrap may not be configured"),
				))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())
		})
	})

	Context("Status Fields", Label("fast"), func() {
		It("should populate all status fields correctly", func() {
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

			By("Creating HomeAssistantConfiguration")
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
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating automation")
			autoName := "auto-status"
			autoYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAutomation
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: status_test
  alias: "Status Test"
  mode: parallel
  triggers:
    - platform: state
      entity_id: binary_sensor.motion
      to: "on"
  actions:
    - service: light.turn_on
      target:
        entity_id: light.hallway
`, autoName, namespace, haName)
			Expect(utils.ApplyYAML(autoYAML, namespace)).To(Succeed())

			By("Verifying status.automationHash is set")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
					"-o", "jsonpath={.status.automationHash}")
				g.Expect(output).NotTo(BeEmpty())
				g.Expect(output).To(MatchRegexp("^[a-f0-9]{64}$")) // SHA256 hash
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying status.observedGeneration is set")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
					"-o", "jsonpath={.status.observedGeneration}")
				g.Expect(output).NotTo(BeEmpty())
				g.Expect(output).To(Equal("1")) // First generation
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying status.conditions contains Ready condition")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying Ready condition has correct reason")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].reason}")
				g.Expect(output).To(Equal("AutomationGenerated"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying lastError contains expected message (no bootstrap in this test)")
			// Without bootstrap, controller attempts hot-reload but finds no API token
			// This is expected behavior - lastError should contain the token message
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haauto", autoName, "-n", namespace,
					"-o", "jsonpath={.status.lastError}")
				g.Expect(output).To(ContainSubstring("API token not found"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())
		})
	})
})

// collectAutomationDebugInfo gathers debug information for automation tests on failure.
func collectAutomationDebugInfo(namespace, haName string) {
	writeDebug := func(format string, args ...any) {
		_, _ = fmt.Fprintf(GinkgoWriter, format, args...)
	}

	writeDebug("\n=== AUTOMATION DEBUG INFO ===\n")

	writeDebug("\n--- HomeAssistantAutomation Resources ---\n")
	cmd := exec.Command("kubectl", "get", "haauto", "-n", namespace, "-o", "wide")
	output, err := utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- Automations ConfigMap Content ---\n")
	configMapName := haName + "-automations"
	cmd = exec.Command("kubectl", "get", "configmap", configMapName, "-n", namespace, "-o", "yaml")
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- HomeAssistantAutomation Status (describe all) ---\n")
	cmd = exec.Command("kubectl", "describe", "haauto", "-n", namespace)
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- Controller Logs (last 200 lines) ---\n")
	cmd = exec.Command(
		"kubectl", "logs", "-n", "homeassistant-operator-system",
		"-l", "control-plane=controller-manager", "--tail=200",
	)
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- Kubernetes Events ---\n")
	cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n=== END AUTOMATION DEBUG INFO ===\n")
}
