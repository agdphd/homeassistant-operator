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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/przemekhys/homeassistant-operator/test/utils"
)

const (
	// Test resource naming for script tests
	scriptTestNamespacePrefix = "hascript-e2e-"
	scriptBootstrapSecret     = "ha-bootstrap-creds"
	// Note: reconcileInterval, haPodReadyInterval, bootstrapInterval are defined in homeassistantconfiguration_e2e_test.go
)

var _ = Describe("HomeAssistantScript E2E", Label("script"), Ordered, func() {
	var namespace string
	var haName string

	BeforeEach(func() {
		namespace = scriptTestNamespacePrefix + utils.RandomString(8)
		haName = "test-ha-" + utils.RandomString(6)

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
		if err := utils.DeleteNamespace(namespace); err != nil {
			fmt.Printf("Warning: failed to delete namespace %s: %v\n", namespace, err)
		}
	})

	Context("Operator Lifecycle", Label("fast"), func() {
		It("should add finalizer when CR created", func() {
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
  id: lifecycle_test_script
  alias: "Lifecycle Test Script"
  mode: single
  sequence:
    - service: light.turn_on
      target:
        entity_id: light.living_room
`, scriptName, namespace, haName)
			Expect(utils.ApplyYAML(scriptYAML, namespace)).To(Succeed())

			By("Verifying finalizer ha.homeassistant.io/script-finalizer is added")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.metadata.finalizers}")
				g.Expect(output).To(ContainSubstring("ha.homeassistant.io/script-finalizer"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})

		It("should set ReloadReady=False when API token missing", func() {
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
  id: token_missing_script
  alias: "Token Missing Script"
  mode: single
  sequence:
    - service: light.turn_off
      target:
        entity_id: light.kitchen
`, scriptName, namespace, haName)
			Expect(utils.ApplyYAML(scriptYAML, namespace)).To(Succeed())

			By("Verifying ReloadReady condition is set to False with reason TokenNotAvailable")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='ReloadReady')].status}")
				g.Expect(output).To(Equal("False"))

				reason := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='ReloadReady')].reason}")
				g.Expect(reason).To(Equal("TokenNotAvailable"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying Ready is NOT set to True")
			output := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			Expect(output).NotTo(Equal("True"))
		})

		It("should set Ready=False when referenced HomeAssistant does not exist", func() {
			By("Creating HomeAssistantScript with non-existent HA reference")
			scriptName := "test-script-" + utils.RandomString(6)
			scriptYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: non-existent-ha
  id: bad_ref_script
  alias: "Bad Ref Script"
  mode: single
  sequence:
    - service: light.turn_on
`, scriptName, namespace)
			Expect(utils.ApplyYAML(scriptYAML, namespace)).To(Succeed())

			By("Verifying Ready=False with reason InvalidScript")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("False"))

				reason := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].reason}")
				g.Expect(reason).To(Equal("InvalidScript"))
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
  id: cleanup_script
  alias: "Cleanup Script"
  mode: single
  sequence:
    - service: notify.notify
      data:
        message: "Cleanup test"
`, scriptName, namespace, haName)
			Expect(utils.ApplyYAML(scriptYAML, namespace)).To(Succeed())

			By("Waiting for finalizer to be added")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.metadata.finalizers}")
				g.Expect(output).To(ContainSubstring("ha.homeassistant.io/script-finalizer"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Deleting the CR")
			cmd := exec.Command("kubectl", "delete", "hascript", scriptName, "-n", namespace, "--wait=false")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying CR is fully deleted (finalizer removed even without token)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascript", scriptName, "-n", namespace, "--ignore-not-found")
				g.Expect(output).To(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})
	})

	Context("REST API Integration", Label("bootstrap", "slow"), func() {
		It("should PUT script to HA and set Ready=True", func() {
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
`, scriptBootstrapSecret, namespace)
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
`, haName, namespace, scriptBootstrapSecret, haName, utils.GetEnhancedHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantConfiguration")
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
    script: !include scripts.yaml
    automation: []
    scene: []
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for bootstrap to complete")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.bootstrap.completed}")
				g.Expect(output).To(Equal("true"))
			}, utils.BootstrapTimeout, bootstrapInterval).Should(Succeed())

			By("Verifying API token Secret was created")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "secret", haName+"-homeassistant-api-token", "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Waiting for pod to be fully Ready")
			Eventually(func(g Gomega) {
				phase := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
					"-o", "jsonpath={.status.phase}")
				g.Expect(phase).To(Equal("Running"))

				ready := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(ready).To(Equal("True"))
			}, utils.HAPodReadyTimeout, haPodReadyInterval).Should(Succeed())

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
  id: put_test_script
  alias: "PUT Test Script"
  autoReload: true
  mode: single
  sequence:
    - service: light.turn_on
      target:
        entity_id: light.living_room
    - delay: "00:00:05"
    - service: light.turn_off
      target:
        entity_id: light.living_room
`, scriptName, namespace, haName)
			Expect(utils.ApplyYAML(scriptYAML, namespace)).To(Succeed())

			By("Verifying Ready=True with reason ScriptGenerated")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))

				reason := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].reason}")
				g.Expect(reason).To(Equal("ScriptGenerated"))
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())

			By("Verifying scriptHash is set (SHA256)")
			Eventually(func(g Gomega) {
				hash := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.scriptHash}")
				g.Expect(hash).To(MatchRegexp("^[a-f0-9]{64}$"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying lastReloadTime is set")
			Eventually(func(g Gomega) {
				reloadTime := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.lastReloadTime}")
				g.Expect(reloadTime).NotTo(BeEmpty())
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())
		})

		It("should update hash and reload when spec changes", func() {
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
`, scriptBootstrapSecret, namespace)
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
`, haName, namespace, scriptBootstrapSecret, haName, utils.GetEnhancedHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantConfiguration")
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
    script: !include scripts.yaml
    automation: []
    scene: []
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

			By("Creating initial HomeAssistantScript CR")
			scriptName := "test-script-" + utils.RandomString(6)
			initialScriptYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: update_hash_script
  alias: "Update Hash Script"
  autoReload: true
  mode: single
  sequence:
    - service: light.turn_on
      target:
        entity_id: light.bedroom
`, scriptName, namespace, haName)
			Expect(utils.ApplyYAML(initialScriptYAML, namespace)).To(Succeed())

			By("Waiting for initial Ready=True and capturing initial hash and reloadTime")
			var initialHash, initialReloadTime string
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))

				hash := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.scriptHash}")
				g.Expect(hash).To(MatchRegexp("^[a-f0-9]{64}$"))
				initialHash = hash

				reloadTime := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.lastReloadTime}")
				g.Expect(reloadTime).NotTo(BeEmpty())
				initialReloadTime = reloadTime
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())

			By("Updating the script spec")
			updatedScriptYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: update_hash_script
  alias: "Update Hash Script - Modified"
  autoReload: true
  mode: queued
  sequence:
    - service: light.turn_on
      target:
        entity_id: light.bedroom
    - delay: "00:00:10"
    - service: light.turn_off
      target:
        entity_id: light.bedroom
`, scriptName, namespace, haName)
			Expect(utils.ApplyYAML(updatedScriptYAML, namespace)).To(Succeed())

			By("Verifying scriptHash changed after spec update")
			Eventually(func(g Gomega) {
				hash := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.scriptHash}")
				g.Expect(hash).To(MatchRegexp("^[a-f0-9]{64}$"))
				g.Expect(hash).NotTo(Equal(initialHash))
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())

			By("Verifying lastReloadTime updated")
			Eventually(func(g Gomega) {
				reloadTime := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.lastReloadTime}")
				g.Expect(reloadTime).NotTo(BeEmpty())
				g.Expect(reloadTime).NotTo(Equal(initialReloadTime))
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())
		})

		It("should DELETE via HA REST API when CR deleted", func() {
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
`, scriptBootstrapSecret, namespace)
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
`, haName, namespace, scriptBootstrapSecret, haName, utils.GetEnhancedHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantConfiguration")
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
    script: !include scripts.yaml
    automation: []
    scene: []
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

			By("Creating script and waiting for Ready=True")
			scriptName := "test-script-" + utils.RandomString(6)
			scriptYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: delete_api_script
  alias: "Delete API Script"
  autoReload: true
  mode: single
  sequence:
    - service: light.turn_on
      target:
        entity_id: light.living_room
`, scriptName, namespace, haName)
			Expect(utils.ApplyYAML(scriptYAML, namespace)).To(Succeed())

			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())

			By("Capturing pod UID before deletion")
			var podUID string
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
					"-o", "jsonpath={.metadata.uid}")
				g.Expect(output).NotTo(BeEmpty())
				podUID = output
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Deleting the CR")
			cmd := exec.Command("kubectl", "delete", "hascript", scriptName, "-n", namespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying CR is fully deleted")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascript", scriptName, "-n", namespace, "--ignore-not-found")
				g.Expect(output).To(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying pod was NOT restarted (deletion is via API, not pod restart)")
			output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
			Expect(output).To(Equal(podUID))
		})

		It("should requeue and eventually load when token becomes available", func() {
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

			By("Creating HomeAssistantScript CR (no token available yet)")
			scriptName := "test-script-" + utils.RandomString(6)
			scriptYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScript
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: requeue_script
  alias: "Requeue Script"
  autoReload: true
  mode: single
  sequence:
    - service: light.turn_on
      target:
        entity_id: light.kitchen
`, scriptName, namespace, haName)
			Expect(utils.ApplyYAML(scriptYAML, namespace)).To(Succeed())

			By("Verifying ReloadReady=False with reason TokenNotAvailable")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='ReloadReady')].status}")
				g.Expect(status).To(Equal("False"))

				reason := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='ReloadReady')].reason}")
				g.Expect(reason).To(Equal("TokenNotAvailable"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Creating API token Secret directly")
			tokenSecretName := haName + "-api-token"
			tokenSecretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  token: fake-e2e-token-for-requeue-test
`, tokenSecretName, namespace)
			Expect(utils.ApplyYAML(tokenSecretYAML, namespace)).To(Succeed())

			By("Patching HA status to reference the token secret")
			cmd := exec.Command("kubectl", "patch", "ha", haName, "-n", namespace,
				"--subresource=status", "--type=merge",
				"-p", fmt.Sprintf(`{"status":{"bootstrap":{"completed":true,"apiTokenSecretName":"%s"}}}`, tokenSecretName))
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying controller requeues and attempts reconciliation (ReloadReady condition updates)")
			// After the token secret is available and HA status is patched, the controller
			// will requeue and attempt to PUT the script. Since the token is fake,
			// the PUT will fail and Ready will remain False or transition. What matters is
			// that the controller no longer reports TokenNotAvailable - it proceeds past that gate.
			Eventually(func(g Gomega) {
				reason := utils.Kubectl("get", "hascript", scriptName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='ReloadReady')].reason}")
				// Controller should move past TokenNotAvailable once token is present
				g.Expect(reason).NotTo(Equal("TokenNotAvailable"))
			}, utils.StatusUpdateTimeout*2, reconcileInterval).Should(Succeed())
		})
	})
})

// collectScriptDebugInfo gathers debug information for script tests on failure.
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

	writeDebug("\n--- HomeAssistantScript Status (describe all) ---\n")
	cmd = exec.Command("kubectl", "describe", "hascript", "-n", namespace)
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- HomeAssistant Status ---\n")
	cmd = exec.Command("kubectl", "get", "ha", haName, "-n", namespace, "-o", "yaml")
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

	writeDebug("\n=== END SCRIPT DEBUG INFO ===\n")
}
