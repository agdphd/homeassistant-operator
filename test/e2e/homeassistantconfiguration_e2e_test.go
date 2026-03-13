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
	// Polling intervals (kept local as they're test-specific preferences)
	reconcileInterval  = 2 * time.Second
	haPodReadyInterval = 10 * time.Second
	bootstrapInterval  = 10 * time.Second

	// Test resource naming
	testNamespacePrefix  = "haconfig-e2e-"
	bootstrapCertsSecret = "ha-bootstrap-creds"
)

var _ = Describe("HomeAssistantConfiguration E2E", Label("configuration"), Ordered, func() {
	var namespace string
	var haName string
	var configName string

	BeforeEach(func() {
		// Generate unique names for each test
		namespace = testNamespacePrefix + utils.RandomString(8)
		haName = "test-ha-" + utils.RandomString(6)
		configName = "test-config-" + utils.RandomString(6)

		By("Creating test namespace: " + namespace)
		Expect(utils.CreateNamespace(namespace)).To(Succeed())
	})

	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Test failed - collecting debug info")
			collectDebugInfo(namespace, haName, configName)
		}

		By("Deleting test namespace: " + namespace)
		if err := utils.DeleteNamespace(namespace); err != nil {
			// Log error but don't fail the test - cleanup is best effort
			fmt.Printf("Warning: failed to delete namespace %s: %v\n", namespace, err)
		}
	})

	Context("ConfigMap Operations", Label("fast", "infra-only"), func() {
		It("should generate ConfigMap from HomeAssistantConfiguration", func() {
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

			By("Verifying ConfigMap created with correct name")
			configMapName := haName + "-configuration"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying ConfigMap contains configuration.yaml")
			Eventually(func(g Gomega) {
				output := utils.Kubectl(
					"get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.configuration\\.yaml}",
				)
				g.Expect(output).To(ContainSubstring("automation:"))
				g.Expect(output).To(ContainSubstring("script:"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying ConfigMap was created (hash annotation NOT expected on initial creation)")
			// NOTE: With new architecture, hash annotation is only added when restart strategy is used
			// For initial creation, ConfigMap has no hash annotation
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.configuration\\.yaml}")
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying HomeAssistantConfiguration status has ConfigHash")
			// The status.configHash is always set (computed from spec.configuration)
			// But ConfigMap annotation is only set when restart is triggered
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haconfig", configName, "-n", namespace, "-o", "jsonpath={.status.configHash}")
				g.Expect(output).NotTo(BeEmpty())
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())
		})

		It("should auto-inject !include directives when not present in spec", func() {
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

			By("Creating HomeAssistantConfiguration CR without !include directives")
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
      name: My Home
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			configMapName := haName + "-configuration"
			By("Waiting for ConfigMap to be created")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying ConfigMap contains auto-injected !include directives")
			Eventually(func(g Gomega) {
				output := utils.Kubectl(
					"get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.configuration\\.yaml}",
				)
				g.Expect(output).To(ContainSubstring("automation: !include automations.yaml"))
				g.Expect(output).To(ContainSubstring("scene: !include scenes.yaml"))
				g.Expect(output).To(ContainSubstring("script: !include scripts.yaml"))
				g.Expect(output).To(ContainSubstring("homeassistant:"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})

		It("should not duplicate !include directives already present in spec", func() {
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

			By("Creating HomeAssistantConfiguration CR with !include already present")
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
    scene: !include scenes.yaml
    script: !include scripts.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			configMapName := haName + "-configuration"
			By("Waiting for ConfigMap to be created")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying ConfigMap does not contain duplicate !include entries")
			Eventually(func(g Gomega) {
				output := utils.Kubectl(
					"get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.configuration\\.yaml}",
				)
				g.Expect(output).To(ContainSubstring("automation: !include automations.yaml"))
				// Count occurrences - should appear exactly once
				count := 0
				for i := 0; i < len(output)-len("automation: !include"); i++ {
					if output[i:i+len("automation: !include")] == "automation: !include" {
						count++
					}
				}
				g.Expect(count).To(Equal(1), "automation: !include should appear exactly once")
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})

		It("should prevent external ConfigMap modifications", func() {
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
    automation: original_config
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			configMapName := haName + "-configuration"
			By("Waiting for ConfigMap to be created")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Externally modifying ConfigMap")
			cmd := exec.Command("kubectl", "patch", "configmap", configMapName, "-n", namespace,
				"-p", `{"data":{"configuration.yaml":"automation: external_edit"}}`)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying ConfigMap is restored to CRD spec within 30 seconds")
			Eventually(func(g Gomega) {
				output := utils.Kubectl(
					"get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.configuration\\.yaml}",
				)
				g.Expect(output).To(ContainSubstring("original_config"))
				g.Expect(output).NotTo(ContainSubstring("external_edit"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})
	})

	Context("Configuration Reload - Restart Path", Label("slow", "pod-required"), func() {
		It("should trigger pod restart on critical section change", func() {
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
`, haName, namespace, utils.GetEnhancedHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantConfiguration with initial timezone")
			configYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  reloadStrategy: auto
  configuration: |
    homeassistant:
      timezone: UTC
    automation: []
    scene: []
    script: []
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HA Pod to be Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.status.phase}")
				g.Expect(output).To(Equal("Running"))

				readyOutput := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(readyOutput).To(Equal("True"))
			}, utils.HAPodReadyTimeout, haPodReadyInterval).Should(Succeed())

			By("Capturing initial pod UID")
			var oldPodUID string
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).NotTo(BeEmpty())
				oldPodUID = output
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Capturing initial StatefulSet annotation hash")
			var oldHash string
			Eventually(func(g Gomega) {
				output := utils.Kubectl(
					"get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.spec.template.metadata.annotations.ha\\.homeassistant\\.io/config-hash}",
				)
				// May be empty initially, that's OK
				oldHash = output
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Updating configuration with timezone change (critical section)")
			updateConfigYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  reloadStrategy: auto
  configuration: |
    homeassistant:
      timezone: Europe/Warsaw
    automation: []
    scene: []
    script: []
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(updateConfigYAML, namespace)).To(Succeed())

			By("Verifying StatefulSet annotation changed (indicating restart triggered)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl(
					"get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.spec.template.metadata.annotations.ha\\.homeassistant\\.io/config-hash}",
				)
				g.Expect(output).NotTo(BeEmpty())
				g.Expect(output).NotTo(Equal(oldHash))
			}, utils.ReconciliationTimeout, reconcileInterval).Should(Succeed())

			By("Verifying pod restarted (new UID after rolling restart)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).NotTo(Equal(oldPodUID))
			}, utils.RestartTimeout, haPodReadyInterval).Should(Succeed())

			By("Verifying HAConfig status shows restart method")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haconfig", configName, "-n", namespace, "-o", "jsonpath={.status.lastReloadMethod}")
				g.Expect(output).To(Equal("restart"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())
		})
	})

	Context("Configuration Reload - Hot-Reload Path", Label("bootstrap", "slow", "pod-required"), func() {
		It("should perform hot-reload without pod restart when possible", func() {
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
`, bootstrapCertsSecret, namespace)
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
`, haName, namespace, bootstrapCertsSecret, haName, utils.GetEnhancedHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantConfiguration with reloadable config")
			configYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  reloadStrategy: auto
  configuration: |
    automation: []
    scene: []
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

			By("Waiting for ConfigMap to be generated")
			configMapName := haName + "-configuration"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Waiting for pod to have ConfigMap volume mounted (may trigger restart)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
					"-o", "jsonpath={.spec.volumes[?(@.name=='ha-configuration')].name}")
				g.Expect(output).To(Equal("ha-configuration"))
			}, utils.RestartTimeout, haPodReadyInterval).Should(Succeed())

			By("Waiting for pod to be fully Ready after volume mount stabilization")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.status.phase}")
				g.Expect(output).To(Equal("Running"))

				readyOutput := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(readyOutput).To(Equal("True"))
			}, utils.HAPodReadyTimeout, haPodReadyInterval).Should(Succeed())

			By("Capturing initial pod UID AFTER ConfigMap mount stabilization")
			var oldPodUID, oldCreationTime string
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).NotTo(BeEmpty())
				oldPodUID = output
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.creationTimestamp}")
				g.Expect(output).NotTo(BeEmpty())
				oldCreationTime = output
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Ensuring no pending restarts (pod UID stable for 10 seconds)")
			Consistently(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).To(Equal(oldPodUID))
			}, 10*time.Second, 2*time.Second).Should(Succeed())

			By("Updating configuration with reloadable change (automation)")
			updateConfigYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  reloadStrategy: auto
  configuration: |
    automation:
      - alias: "Test Automation"
        trigger:
          platform: time
          at: "10:00:00"
        action:
          service: light.turn_on
          entity_id: light.living_room
    scene: []
    script: []
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(updateConfigYAML, namespace)).To(Succeed())

			By("Verifying HAConfig status shows hot-reload method")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haconfig", configName, "-n", namespace, "-o", "jsonpath={.status.lastReloadMethod}")
				g.Expect(output).To(Equal("hot-reload"))
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())

			By("Verifying pod was NOT restarted (UID unchanged)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).To(Equal(oldPodUID))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying pod creation timestamp unchanged (no restart)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.creationTimestamp}")
				g.Expect(output).To(Equal(oldCreationTime))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})
	})

	Context("Fallback Mechanisms", func() {
		It("should fallback to restart when API token is missing", Label("slow", "pod-required"), func() {
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

			By("Creating HomeAssistantConfiguration instance")
			initialConfigYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
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
    automation: []
    scene: []
    script: []
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(initialConfigYAML, namespace)).To(Succeed())

			By("Waiting for HA Pod Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.status.phase}")
				g.Expect(output).To(Equal("Running"))

				readyOutput := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(readyOutput).To(Equal("True"))
			}, utils.HAPodReadyTimeout, haPodReadyInterval).Should(Succeed())

			By("Creating initial HomeAssistantConfiguration with auto strategy (default)")
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
    automation: []
    scene: []
    script: []
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Verifying API token Secret does NOT exist")
			output := utils.Kubectl("get", "secret", haName+"-homeassistant-api-token", "-n", namespace, "--ignore-not-found")
			Expect(output).To(BeEmpty())

			By("Capturing initial pod UID")
			var oldPodUID string
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).NotTo(BeEmpty())
				oldPodUID = output
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Updating configuration")
			updateConfigYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  configuration: |
    automation:
      - alias: "Test"
        trigger:
          platform: time
          at: "10:00:00"
    scene: []
    script: []
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(updateConfigYAML, namespace)).To(Succeed())

			By("Verifying fallback occurred (reload method is restart)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haconfig", configName, "-n", namespace, "-o", "jsonpath={.status.lastReloadMethod}")
				g.Expect(output).To(Equal("restart"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying pod restarted (new UID due to fallback)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).NotTo(Equal(oldPodUID))
			}, utils.RestartTimeout, haPodReadyInterval).Should(Succeed())
		})

	})

	Context("Status Fields", Label("slow", "pod-required"), func() {
		It("should populate all status fields correctly on restart", func() {
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

			By("Creating HomeAssistantConfiguration with initial timezone")
			configYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  reloadStrategy: restart
  configuration: |
    homeassistant:
      name: Home
      timezone: UTC
    default_config:
    automation: []
    scene: []
    script: []
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HA Pod to be Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.status.phase}")
				g.Expect(output).To(Equal("Running"))

				readyOutput := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(readyOutput).To(Equal("True"))
			}, utils.HAPodReadyTimeout, haPodReadyInterval).Should(Succeed())

			By("Verifying configHash is set")
			var initialHash string
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haconfig", configName, "-n", namespace, "-o", "jsonpath={.status.configHash}")
				g.Expect(output).NotTo(BeEmpty())
				initialHash = output
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Capturing initial pod UID")
			var oldPodUID string
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).NotTo(BeEmpty())
				oldPodUID = output
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Updating configuration with critical section change (timezone)")
			updateConfigYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  reloadStrategy: restart
  configuration: |
    homeassistant:
      name: Home
      timezone: Europe/Warsaw
    default_config:
    automation: []
    scene: []
    script: []
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(updateConfigYAML, namespace)).To(Succeed())

			By("Verifying configHash changed")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haconfig", configName, "-n", namespace, "-o", "jsonpath={.status.configHash}")
				g.Expect(output).NotTo(Equal(initialHash))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying lastReloadTime is set")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haconfig", configName, "-n", namespace, "-o", "jsonpath={.status.lastReloadTime}")
				g.Expect(output).NotTo(BeEmpty())
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying lastReloadMethod is restart (not hot-reload)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haconfig", configName, "-n", namespace, "-o", "jsonpath={.status.lastReloadMethod}")
				g.Expect(output).To(Equal("restart"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying pod was restarted (new UID)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).NotTo(Equal(oldPodUID))
			}, utils.RestartTimeout, haPodReadyInterval).Should(Succeed())
		})
	})
})

// collectDebugInfo gathers debug information from the test environment on test failure.
func collectDebugInfo(namespace, haName, configName string) {
	// Helper function for best-effort logging to GinkgoWriter
	// Ignores write errors since debug output is non-critical
	writeDebug := func(format string, args ...any) {
		_, _ = fmt.Fprintf(GinkgoWriter, format, args...) // best-effort logging; ignore write errors
	}

	writeDebug("\n=== DEBUG INFO ===\n")

	writeDebug("\n--- HAConfig Status ---\n")
	cmd := exec.Command("kubectl", "describe", "haconfig", configName, "-n", namespace)
	output, err := utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- ConfigMap Content ---\n")
	configMapName := haName + "-configuration"
	cmd = exec.Command("kubectl", "get", "configmap", configMapName, "-n", namespace, "-o", "yaml")
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- StatefulSet Annotations ---\n")
	cmd = exec.Command(
		"kubectl", "get", "statefulset", haName, "-n", namespace,
		"-o", "jsonpath={.spec.template.metadata.annotations}",
	)
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- Pod Logs (last 100 lines) ---\n")
	cmd = exec.Command("kubectl", "logs", haName+"-0", "-n", namespace, "--tail=100")
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

	writeDebug("\n--- Controller Logs ---\n")
	cmd = exec.Command(
		"kubectl", "logs", "-n", "homeassistant-operator-system",
		"-l", "control-plane=controller-manager", "--tail=200",
	)
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n=== END DEBUG INFO ===\n")
}
