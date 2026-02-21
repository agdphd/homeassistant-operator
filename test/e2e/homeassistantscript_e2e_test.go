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
	// Test resource naming for script tests
	scriptTestNamespacePrefix = "hascript-e2e-"
	scriptBootstrapSecret     = "ha-bootstrap-creds"
)

var _ = Describe("HomeAssistantScript E2E", Label("script"), Ordered, func() {
	var namespace string
	var haName string
	var configName string

	BeforeEach(func() {
		// Generate unique names for each test
		namespace = scriptTestNamespacePrefix + utils.RandomString(8)
		haName = "test-ha-" + utils.RandomString(6)
		configName = "test-config-" + utils.RandomString(6)

		By("Creating test namespace: " + namespace)
		Expect(utils.CreateNamespace(namespace)).To(Succeed())
	})

	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Test failed - collecting debug info")
			collectScriptDebugInfo(namespace, haName)
		}

		By("Deleting test namespace: " + namespace)
		_ = utils.DeleteNamespace(namespace)
	})

	Context("ConfigMap Aggregation", Label("fast"), func() {
		It("should create ConfigMap from single HomeAssistantScript", func() {
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
    script: !include scripts.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HomeAssistant resource to be created")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.metadata.name}")
				g.Expect(output).To(Equal(haName))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Creating HomeAssistantScript CR")
			scriptName := "test-script-" + utils.RandomString(6)
			scriptYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: notify_mobile
  alias: "Notify Mobile Devices"
  description: "Send notification to all mobile devices"
  icon: "mdi:cellphone-message"
  mode: single
  sequence:
    - service: notify.mobile_app_phone
      data:
        title: "Alert"
        message: "Test notification"
    - delay:
        seconds: 1
    - service: notify.mobile_app_tablet
      data:
        title: "Alert"
        message: "Test notification"
`, scriptName, namespace, haName)
			Expect(utils.ApplyYAML(scriptYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap created with correct name")
			configMapName := haName + "-scripts"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying ConfigMap contains scripts.yaml")
			Eventually(func(g Gomega) {
				output := utils.Kubectl(
					"get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.scripts\\.yaml}",
				)
				g.Expect(output).To(ContainSubstring("notify_mobile:"))
				g.Expect(output).To(ContainSubstring("alias: Notify Mobile Devices"))
				g.Expect(output).To(ContainSubstring("mode: single"))
				g.Expect(output).To(ContainSubstring("notify.mobile_app_phone"))
				g.Expect(output).To(ContainSubstring("notify.mobile_app_tablet"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying HomeAssistantScript status is Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})

		It("should aggregate multiple scripts into single ConfigMap", func() {
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
    script: !include scripts.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HomeAssistant resource")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Creating first HomeAssistantScript")
			script1Name := "script-1-" + utils.RandomString(6)
			script1YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: script_one
  alias: "Script One"
  sequence:
    - service: light.turn_on
      target:
        entity_id: light.living_room
`, script1Name, namespace, haName)
			Expect(utils.ApplyYAML(script1YAML, namespace)).To(Succeed())

			By("Creating second HomeAssistantScript")
			script2Name := "script-2-" + utils.RandomString(6)
			script2YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: script_two
  alias: "Script Two"
  sequence:
    - service: light.turn_off
      target:
        entity_id: light.bedroom
`, script2Name, namespace, haName)
			Expect(utils.ApplyYAML(script2YAML, namespace)).To(Succeed())

			By("Creating third HomeAssistantScript")
			script3Name := "script-3-" + utils.RandomString(6)
			script3YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: script_three
  alias: "Script Three"
  sequence:
    - service: climate.set_temperature
      data:
        temperature: 20
`, script3Name, namespace, haName)
			Expect(utils.ApplyYAML(script3YAML, namespace)).To(Succeed())

			By("Verifying ConfigMap contains all 3 scripts")
			configMapName := haName + "-scripts"
			Eventually(func(g Gomega) {
				output := utils.Kubectl(
					"get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.scripts\\.yaml}",
				)
				g.Expect(output).To(ContainSubstring("script_one:"))
				g.Expect(output).To(ContainSubstring("script_two:"))
				g.Expect(output).To(ContainSubstring("script_three:"))
				g.Expect(output).To(ContainSubstring("Script One"))
				g.Expect(output).To(ContainSubstring("Script Two"))
				g.Expect(output).To(ContainSubstring("Script Three"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})

		It("should update ConfigMap when script changes", func() {
			By("Creating HomeAssistant and Configuration")
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

			configYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  configuration: |
    script: !include scripts.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HomeAssistant resource")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Creating initial HomeAssistantScript")
			scriptName := "update-test-" + utils.RandomString(6)
			initialYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: update_test
  alias: "Initial Alias"
  sequence:
    - service: light.turn_on
`, scriptName, namespace, haName)
			Expect(utils.ApplyYAML(initialYAML, namespace)).To(Succeed())

			By("Verifying initial ConfigMap content")
			configMapName := haName + "-scripts"
			Eventually(func(g Gomega) {
				output := utils.Kubectl(
					"get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.scripts\\.yaml}",
				)
				g.Expect(output).To(ContainSubstring("alias: Initial Alias"))
				g.Expect(output).To(ContainSubstring("light.turn_on"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Getting initial script hash")
			var initialHash string
			Eventually(func(g Gomega) {
				hash := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.scriptHash}")
				g.Expect(hash).NotTo(BeEmpty())
				initialHash = hash
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Updating HomeAssistantScript with new content")
			updatedYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: update_test
  alias: "Updated Alias"
  description: "This script was updated"
  sequence:
    - service: light.turn_off
    - delay:
        seconds: 5
`, scriptName, namespace, haName)
			Expect(utils.ApplyYAML(updatedYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap updated with new content")
			Eventually(func(g Gomega) {
				output := utils.Kubectl(
					"get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.scripts\\.yaml}",
				)
				g.Expect(output).To(ContainSubstring("alias: Updated Alias"))
				g.Expect(output).To(ContainSubstring("description: This script was updated"))
				g.Expect(output).To(ContainSubstring("light.turn_off"))
				g.Expect(output).NotTo(ContainSubstring("light.turn_on"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying script hash changed")
			Eventually(func(g Gomega) {
				newHash := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.scriptHash}")
				g.Expect(newHash).NotTo(Equal(initialHash))
				g.Expect(newHash).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})

		It("should remove script from ConfigMap when CR deleted (finalizer)", func() {
			By("Creating HomeAssistant and Configuration")
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

			configYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  configuration: |
    script: !include scripts.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HomeAssistant resource")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Creating two HomeAssistantScript CRs")
			script1Name := "finalizer-1-" + utils.RandomString(6)
			script1YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: finalizer_script_1
  alias: "Finalizer Script 1"
  sequence:
    - service: test.service_1
`, script1Name, namespace, haName)
			Expect(utils.ApplyYAML(script1YAML, namespace)).To(Succeed())

			script2Name := "finalizer-2-" + utils.RandomString(6)
			script2YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: finalizer_script_2
  alias: "Finalizer Script 2"
  sequence:
    - service: test.service_2
`, script2Name, namespace, haName)
			Expect(utils.ApplyYAML(script2YAML, namespace)).To(Succeed())

			By("Verifying both scripts in ConfigMap")
			configMapName := haName + "-scripts"
			Eventually(func(g Gomega) {
				output := utils.Kubectl(
					"get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.scripts\\.yaml}",
				)
				g.Expect(output).To(ContainSubstring("finalizer_script_1:"))
				g.Expect(output).To(ContainSubstring("finalizer_script_2:"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Deleting first HomeAssistantScript")
			cmd := exec.Command("kubectl", "delete", "hascript", script1Name, "-n", namespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying ConfigMap updated (only script 2 remains)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl(
					"get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.scripts\\.yaml}",
				)
				g.Expect(output).NotTo(ContainSubstring("finalizer_script_1:"))
				g.Expect(output).To(ContainSubstring("finalizer_script_2:"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying first script CR was fully deleted")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascript", script1Name, "-n", namespace,
					"--ignore-not-found")
				g.Expect(output).To(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})
	})

	Context("Script Fields and Parameters", Label("fast"), func() {
		It("should handle script with input fields correctly", func() {
			By("Creating HomeAssistant and Configuration")
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

			configYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  configuration: |
    script: !include scripts.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HomeAssistant resource")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Creating HomeAssistantScript with fields")
			scriptName := "fields-test-" + utils.RandomString(6)
			scriptYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: notify_with_params
  alias: "Notify with Parameters"
  fields:
    message:
      description: "Message to send"
      example: "Hello World"
      selector:
        text:
          multiline: true
    title:
      description: "Notification title"
      example: "Alert"
      selector:
        text:
          multiline: false
  sequence:
    - service: notify.mobile_app
      data:
        title: "{{ title }}"
        message: "{{ message }}"
`, scriptName, namespace, haName)
			Expect(utils.ApplyYAML(scriptYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap contains fields definition")
			configMapName := haName + "-scripts"
			Eventually(func(g Gomega) {
				output := utils.Kubectl(
					"get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.scripts\\.yaml}",
				)
				g.Expect(output).To(ContainSubstring("fields:"))
				g.Expect(output).To(ContainSubstring("message:"))
				g.Expect(output).To(ContainSubstring("title:"))
				g.Expect(output).To(ContainSubstring("description: Message to send"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})
	})

	Context("Script Modes", Label("fast"), func() {
		It("should handle different script modes correctly", func() {
			By("Creating HomeAssistant and Configuration")
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

			configYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  configuration: |
    script: !include scripts.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HomeAssistant resource")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			modes := []struct {
				mode string
				max  int
			}{
				{"single", 1},
				{"restart", 1},
				{"queued", 5},
				{"parallel", 10},
			}

			for _, m := range modes {
				By(fmt.Sprintf("Creating script with mode: %s", m.mode))
				scriptName := fmt.Sprintf("mode-%s-%s", m.mode, utils.RandomString(6))
				scriptYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: %s_script
  alias: "%s Script"
  mode: %s
  max: %d
  maxExceeded: warning
  sequence:
    - delay:
        seconds: 1
`, scriptName, namespace, haName, m.mode, m.mode, m.mode, m.max)
				Expect(utils.ApplyYAML(scriptYAML, namespace)).To(Succeed())

				By(fmt.Sprintf("Verifying ConfigMap contains mode: %s", m.mode))
				configMapName := haName + "-scripts"
				Eventually(func(g Gomega) {
					output := utils.Kubectl(
						"get", "configmap", configMapName, "-n", namespace,
						"-o", "jsonpath={.data.scripts\\.yaml}",
					)
					g.Expect(output).To(ContainSubstring(fmt.Sprintf("%s_script:", m.mode)))
					g.Expect(output).To(ContainSubstring(fmt.Sprintf("mode: %s", m.mode)))
					g.Expect(output).To(ContainSubstring(fmt.Sprintf("max: %d", m.max)))
				}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
			}
		})
	})

	Context("Status Fields", Label("fast"), func() {
		It("should populate all status fields correctly", func() {
			By("Creating HomeAssistant and Configuration without bootstrap")
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

			configYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  configuration: |
    script: !include scripts.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HomeAssistant resource")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Creating HomeAssistantScript")
			scriptName := "status-test-" + utils.RandomString(6)
			scriptYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: status_test
  alias: "Status Test Script"
  sequence:
    - service: test.service
`, scriptName, namespace, haName)
			Expect(utils.ApplyYAML(scriptYAML, namespace)).To(Succeed())

			By("Verifying status.scriptHash is populated")
			Eventually(func(g Gomega) {
				hash := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.scriptHash}")
				g.Expect(hash).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying status.observedGeneration matches metadata.generation")
			Eventually(func(g Gomega) {
				generation := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.metadata.generation}")
				observedGen := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.observedGeneration}")
				g.Expect(observedGen).To(Equal(generation))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying status.conditions contains Ready=True")
			Eventually(func(g Gomega) {
				readyStatus := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(readyStatus).To(Equal("True"))

				readyReason := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].reason}")
				g.Expect(readyReason).To(Equal("ScriptGenerated"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying status.lastError is set (no bootstrap token)")
			Eventually(func(g Gomega) {
				lastError := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.lastError}")
				g.Expect(lastError).To(ContainSubstring("token"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})
	})

	Context("Hot-Reload", Label("slow", "bootstrap"), func() {
		It("should hot-reload via REST API when autoReload=true", func() {
			By("Creating bootstrap Secret")
			secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  username: "admin"
  password: "test-password-12345"
`, scriptBootstrapSecret, namespace)
			Expect(utils.ApplyYAML(secretYAML, namespace)).To(Succeed())

			By("Creating HomeAssistant with bootstrap")
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
  bootstrap:
    enabled: true
    credentials:
      secretRef:
        name: %s
    location:
      name: "Test Home"
      latitude: "52.52"
      longitude: "13.405"
      elevation: 34
      unitSystem: "metric"
      timeZone: "Europe/Berlin"
      currency: "EUR"
    analytics: false
  %s
`, haName, namespace, scriptBootstrapSecret, utils.GetDefaultHAResourceRequests())
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
    script: !include scripts.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for bootstrap to complete")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.bootstrap.completed}")
				g.Expect(output).To(Equal("true"))
			}, utils.BootstrapTimeout, reconcileInterval).Should(Succeed())

			By("Getting pod UID before creating script")
			podName := haName + "-0"
			var initialPodUID string
			Eventually(func(g Gomega) {
				uid := utils.Kubectl("get", "pod", podName, "-n", namespace,
					"-o", "jsonpath={.metadata.uid}")
				g.Expect(uid).NotTo(BeEmpty())
				initialPodUID = uid
			}, utils.HAPodReadyTimeout, reconcileInterval).Should(Succeed())

			By("Creating HomeAssistantScript with autoReload=true")
			scriptName := "hotreload-test-" + utils.RandomString(6)
			scriptYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: hotreload_test
  alias: "Hot Reload Test"
  autoReload: true
  sequence:
    - service: notify.persistent_notification
      data:
        message: "Hot reload test"
`, scriptName, namespace, haName)
			Expect(utils.ApplyYAML(scriptYAML, namespace)).To(Succeed())

			By("Verifying status.lastReloadTime is set")
			Eventually(func(g Gomega) {
				reloadTime := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.lastReloadTime}")
				g.Expect(reloadTime).NotTo(BeEmpty())
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())

			By("Verifying status.lastReloadMethod is hot-reload")
			Eventually(func(g Gomega) {
				method := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.lastReloadMethod}")
				g.Expect(method).To(Equal("hot-reload"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying pod was NOT restarted (UID unchanged)")
			Consistently(func(g Gomega) {
				currentUID := utils.Kubectl("get", "pod", podName, "-n", namespace,
					"-o", "jsonpath={.metadata.uid}")
				g.Expect(currentUID).To(Equal(initialPodUID))
			}, 10*time.Second, reconcileInterval).Should(Succeed())
		})

		It("should skip reload when autoReload=false", func() {
			By("Creating HomeAssistant and Configuration (no bootstrap needed)")
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

			configYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  configuration: |
    script: !include scripts.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HomeAssistant resource")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Creating HomeAssistantScript with autoReload=false")
			scriptName := "no-reload-" + utils.RandomString(6)
			scriptYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: no_reload_test
  alias: "No Reload Test"
  autoReload: false
  sequence:
    - service: test.service
`, scriptName, namespace, haName)
			Expect(utils.ApplyYAML(scriptYAML, namespace)).To(Succeed())

			By("Verifying script is Ready")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying status.lastReloadTime is NOT set")
			Consistently(func(g Gomega) {
				reloadTime := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.lastReloadTime}")
				g.Expect(reloadTime).To(BeEmpty())
			}, 10*time.Second, reconcileInterval).Should(Succeed())

			By("Verifying status.lastError is empty (autoReload disabled)")
			Eventually(func(g Gomega) {
				lastError := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.lastError}")
				g.Expect(lastError).To(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})
	})

	Context("Fallback Mechanisms", Label("slow"), func() {
		It("should skip reload and set LastError when API token missing", func() {
			By("Creating HomeAssistant WITHOUT bootstrap")
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
    script: !include scripts.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HomeAssistant resource")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Creating HomeAssistantScript with autoReload=true (will fail)")
			scriptName := "fallback-test-" + utils.RandomString(6)
			scriptYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: fallback_test
  alias: "Fallback Test"
  autoReload: true
  sequence:
    - service: test.service
`, scriptName, namespace, haName)
			Expect(utils.ApplyYAML(scriptYAML, namespace)).To(Succeed())

			By("Verifying status.lastError contains token message")
			Eventually(func(g Gomega) {
				lastError := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.lastError}")
				g.Expect(lastError).To(ContainSubstring("token"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying script is still Ready despite reload failure")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})
	})
})

// collectScriptDebugInfo collects debugging information when script test fails
func collectScriptDebugInfo(namespace, haName string) {
	writeDebug := func(format string, args ...any) {
		_, _ = fmt.Fprintf(GinkgoWriter, format, args...)
	}

	writeDebug("\n=== SCRIPT DEBUG INFO ===\n")

	writeDebug("\n--- HomeAssistantScript Resources ---\n")
	cmd := exec.Command("kubectl", "get", "hascript", "-n", namespace, "-o", "wide")
	output, err := utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- HomeAssistantScript YAML ---\n")
	cmd = exec.Command("kubectl", "get", "hascript", "-n", namespace, "-o", "yaml")
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- Scripts ConfigMap Content ---\n")
	configMapName := haName + "-scripts"
	cmd = exec.Command("kubectl", "get", "configmap", configMapName, "-n", namespace, "-o", "yaml")
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- HomeAssistant Resource ---\n")
	cmd = exec.Command("kubectl", "get", "ha", haName, "-n", namespace, "-o", "yaml")
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
}
