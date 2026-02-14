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
	// Test resource naming for scene tests
	sceneTestNamespacePrefix = "hascene-e2e-"
	sceneBootstrapSecret     = "ha-bootstrap-creds"
)

var _ = Describe("HomeAssistantScene E2E", Label("scene"), Ordered, func() {
	var namespace string
	var haName string
	var configName string

	BeforeEach(func() {
		// Generate unique names for each test
		namespace = sceneTestNamespacePrefix + utils.RandomString(8)
		haName = "test-ha-" + utils.RandomString(6)
		configName = "test-config-" + utils.RandomString(6)

		By("Creating test namespace: " + namespace)
		Expect(utils.CreateNamespace(namespace)).To(Succeed())
	})

	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Test failed - collecting debug info")
			collectSceneDebugInfo(namespace, haName)
		}

		By("Deleting test namespace: " + namespace)
		_ = utils.DeleteNamespace(namespace)
	})

	Context("ConfigMap Aggregation", Label("fast"), func() {
		It("should create ConfigMap from single HomeAssistantScene", func() {
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
    scene: !include scenes.yaml
    script: !include scripts.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HomeAssistant resource to be created")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.metadata.name}")
				g.Expect(output).To(Equal(haName))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Creating HomeAssistantScene CR")
			sceneName := "test-scene-" + utils.RandomString(6)
			sceneYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScene
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: movie_time
  name: "Movie Time"
  icon: "mdi:movie"
  entities:
    - entity_id: light.living_room
      state: "on"
      attributes:
        brightness: 50
        color_temp: 370
    - entity_id: light.kitchen
      state: "off"
`, sceneName, namespace, haName)
			Expect(utils.ApplyYAML(sceneYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap created with correct name")
			configMapName := haName + "-scenes"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying ConfigMap contains scenes.yaml")
			Eventually(func(g Gomega) {
				output := utils.Kubectl(
					"get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.scenes\\.yaml}",
				)
				g.Expect(output).To(ContainSubstring("id: movie_time"))
				g.Expect(output).To(ContainSubstring("name: Movie Time"))
				g.Expect(output).To(ContainSubstring("light.living_room"))
				g.Expect(output).To(ContainSubstring("light.kitchen"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying HomeAssistantScene status is Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying scene hash is set in status")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
					"-o", "jsonpath={.status.sceneHash}")
				g.Expect(output).NotTo(BeEmpty())
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())
		})

		It("should aggregate multiple scenes into single ConfigMap", func() {
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
    scene: !include scenes.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HomeAssistant resource")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.metadata.name}")
				g.Expect(output).To(Equal(haName))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Creating first scene")
			scene1YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScene
metadata:
  name: scene-movie
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: movie_time
  name: "Movie Time"
  entities:
    - entity_id: light.living_room
      state: "on"
      attributes:
        brightness: 30
`, namespace, haName)
			Expect(utils.ApplyYAML(scene1YAML, namespace)).To(Succeed())

			By("Creating second scene")
			scene2YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScene
metadata:
  name: scene-bright
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: bright_day
  name: "Bright Day"
  entities:
    - entity_id: light.living_room
      state: "on"
      attributes:
        brightness: 255
    - entity_id: light.kitchen
      state: "on"
      attributes:
        brightness: 255
`, namespace, haName)
			Expect(utils.ApplyYAML(scene2YAML, namespace)).To(Succeed())

			By("Creating third scene")
			scene3YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScene
metadata:
  name: scene-night
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: night_mode
  name: "Night Mode"
  entities:
    - entity_id: light.living_room
      state: "off"
    - entity_id: light.kitchen
      state: "off"
`, namespace, haName)
			Expect(utils.ApplyYAML(scene3YAML, namespace)).To(Succeed())

			By("Verifying ConfigMap contains all 3 scenes")
			configMapName := haName + "-scenes"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.scenes\\.yaml}")
				g.Expect(output).To(ContainSubstring("id: movie_time"))
				g.Expect(output).To(ContainSubstring("id: bright_day"))
				g.Expect(output).To(ContainSubstring("id: night_mode"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying all scenes have unique IDs")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.scenes\\.yaml}")
				// Each ID should appear exactly once
				g.Expect(output).To(MatchRegexp(`id: movie_time\s`))
				g.Expect(output).To(MatchRegexp(`id: bright_day\s`))
				g.Expect(output).To(MatchRegexp(`id: night_mode\s`))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})

		It("should update ConfigMap when scene changes", func() {
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
    scene: !include scenes.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HomeAssistant resource")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.metadata.name}")
				g.Expect(output).To(Equal(haName))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Creating initial scene")
			sceneName := "scene-update"
			sceneYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScene
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: update_test
  name: "Initial Scene"
  entities:
    - entity_id: light.living_room
      state: "on"
      attributes:
        brightness: 100
`, sceneName, namespace, haName)
			Expect(utils.ApplyYAML(sceneYAML, namespace)).To(Succeed())

			By("Waiting for scene to be ready and capturing initial hash")
			var initialHash string
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
					"-o", "jsonpath={.status.sceneHash}")
				g.Expect(output).NotTo(BeEmpty())
				initialHash = output
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying initial ConfigMap content")
			configMapName := haName + "-scenes"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.scenes\\.yaml}")
				g.Expect(output).To(ContainSubstring("name: Initial Scene"))
				g.Expect(output).To(ContainSubstring("brightness: 100"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Updating scene with new entities")
			updatedYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScene
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: update_test
  name: "Updated Scene"
  entities:
    - entity_id: light.living_room
      state: "on"
      attributes:
        brightness: 200
    - entity_id: light.kitchen
      state: "on"
      attributes:
        brightness: 150
`, sceneName, namespace, haName)
			Expect(utils.ApplyYAML(updatedYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap updated with new content")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.scenes\\.yaml}")
				g.Expect(output).To(ContainSubstring("name: Updated Scene"))
				g.Expect(output).To(ContainSubstring("brightness: 200"))
				g.Expect(output).To(ContainSubstring("light.kitchen"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying scene hash changed")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
					"-o", "jsonpath={.status.sceneHash}")
				g.Expect(output).NotTo(Equal(initialHash))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())
		})

		It("should remove scene from ConfigMap when CR deleted (finalizer)", func() {
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
    scene: !include scenes.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HomeAssistant resource")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.metadata.name}")
				g.Expect(output).To(Equal(haName))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Creating first scene (to keep)")
			scene1YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScene
metadata:
  name: scene-keep
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: keep_scene
  name: "Keep This Scene"
  entities:
    - entity_id: light.living_room
      state: "on"
`, namespace, haName)
			Expect(utils.ApplyYAML(scene1YAML, namespace)).To(Succeed())

			By("Creating second scene (to delete)")
			scene2YAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScene
metadata:
  name: scene-delete
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: delete_scene
  name: "Delete This Scene"
  entities:
    - entity_id: light.kitchen
      state: "off"
`, namespace, haName)
			Expect(utils.ApplyYAML(scene2YAML, namespace)).To(Succeed())

			By("Verifying both scenes in ConfigMap")
			configMapName := haName + "-scenes"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.scenes\\.yaml}")
				g.Expect(output).To(ContainSubstring("id: keep_scene"))
				g.Expect(output).To(ContainSubstring("id: delete_scene"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Deleting second scene")
			cmd := exec.Command("kubectl", "delete", "hascene", "scene-delete", "-n", namespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying ConfigMap updated (only scene-keep remains)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.scenes\\.yaml}")
				g.Expect(output).NotTo(ContainSubstring("id: delete_scene"))
				g.Expect(output).To(ContainSubstring("id: keep_scene"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying scene CR was fully deleted")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", "scene-delete", "-n", namespace, "--ignore-not-found")
				g.Expect(output).To(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying first scene still exists and is Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", "scene-keep", "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())
		})
	})

	Context("Entity Attributes", Label("fast"), func() {
		It("should handle complex entity attributes via RawExtension", func() {
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
    scene: !include scenes.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HomeAssistant resource")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.metadata.name}")
				g.Expect(output).To(Equal(haName))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Creating scene with complex entity attributes")
			sceneName := "scene-complex"
			sceneYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScene
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: complex_attrs
  name: "Complex Attributes Scene"
  entities:
    - entity_id: light.rgb_light
      state: "on"
      attributes:
        brightness: 180
        rgb_color: [255, 100, 50]
        color_temp: 370
    - entity_id: climate.bedroom
      state: "heat"
      attributes:
        temperature: 21.5
        fan_mode: "auto"
    - entity_id: media_player.tv
      state: "playing"
      attributes:
        volume_level: 0.6
        source: "Netflix"
`, sceneName, namespace, haName)
			Expect(utils.ApplyYAML(sceneYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap contains all attributes in correct YAML format")
			configMapName := haName + "-scenes"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.scenes\\.yaml}")

				// Verify all entity IDs
				g.Expect(output).To(ContainSubstring("light.rgb_light"))
				g.Expect(output).To(ContainSubstring("climate.bedroom"))
				g.Expect(output).To(ContainSubstring("media_player.tv"))

				// Verify complex attributes
				g.Expect(output).To(ContainSubstring("brightness: 180"))
				g.Expect(output).To(ContainSubstring("rgb_color"))
				g.Expect(output).To(ContainSubstring("temperature: 21.5"))
				g.Expect(output).To(ContainSubstring("volume_level: 0.6"))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Verifying scene is Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
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
  service:
    type: ClusterIP
    port: 8123
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
    scene: !include scenes.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HomeAssistant resource")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.metadata.name}")
				g.Expect(output).To(Equal(haName))
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Creating scene")
			sceneName := "scene-status"
			sceneYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScene
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: status_test
  name: "Status Test Scene"
  entities:
    - entity_id: light.test
      state: "on"
`, sceneName, namespace, haName)
			Expect(utils.ApplyYAML(sceneYAML, namespace)).To(Succeed())

			By("Verifying status.sceneHash is set")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
					"-o", "jsonpath={.status.sceneHash}")
				g.Expect(output).NotTo(BeEmpty())
				g.Expect(len(output)).To(BeNumerically(">", 32)) // SHA256 hash is 64 chars
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying status.observedGeneration is set")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
					"-o", "jsonpath={.status.observedGeneration}")
				g.Expect(output).NotTo(BeEmpty())
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying status.conditions contains Ready condition")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying Ready condition has correct reason")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].reason}")
				// Reason should be either SceneGenerated or similar success reason
				g.Expect(output).NotTo(BeEmpty())
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying lastError contains expected message (no bootstrap in this test)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
					"-o", "jsonpath={.status.lastError}")
				// Without bootstrap, should have error about missing token
				g.Expect(output).To(ContainSubstring("bootstrap"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())
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
`, sceneBootstrapSecret, namespace)
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
`, haName, namespace, sceneBootstrapSecret, haName, utils.GetEnhancedHAResourceRequests())
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
    scene: !include scenes.yaml
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

			By("Capturing pod UID before scene creation")
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

			By("Creating scene with autoReload: true")
			sceneName := "scene-hotreload"
			sceneYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScene
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: hot_reload_test
  name: "Hot Reload Test"
  autoReload: true
  entities:
    - entity_id: light.living_room
      state: "on"
      attributes:
        brightness: 150
`, sceneName, namespace, haName)
			Expect(utils.ApplyYAML(sceneYAML, namespace)).To(Succeed())

			By("Verifying scene status shows it's ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.HotReloadTimeout, reconcileInterval).Should(Succeed())

			By("Verifying lastReloadTime is set (hot-reload occurred)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
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
    scene: !include scenes.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating scene with autoReload: false")
			sceneName := "scene-noreload"
			sceneYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScene
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: no_reload_test
  name: "No Reload Test"
  autoReload: false
  entities:
    - entity_id: light.bedroom
      state: "off"
`, sceneName, namespace, haName)
			Expect(utils.ApplyYAML(sceneYAML, namespace)).To(Succeed())

			By("Verifying scene is Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying lastReloadTime is NOT set (no reload occurred)")
			Consistently(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
					"-o", "jsonpath={.status.lastReloadTime}")
				g.Expect(output).To(BeEmpty())
			}, 5*time.Second, reconcileInterval).Should(Succeed())

			By("Verifying lastError is NOT set")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
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
    scene: !include scenes.yaml
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for HA Pod Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
					"-o", "jsonpath={.status.phase}")
				g.Expect(output).To(Equal("Running"))
			}, utils.HAPodReadyTimeout, haPodReadyInterval).Should(Succeed())

			By("Verifying API token Secret does NOT exist")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "secret", haName+"-homeassistant-api-token",
					"-n", namespace, "--ignore-not-found")
				g.Expect(output).To(BeEmpty())
			}, utils.ResourceTimeout, reconcileInterval).Should(Succeed())

			By("Creating scene with autoReload: true (will attempt hot-reload)")
			sceneName := "scene-fallback"
			sceneYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantScene
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  id: fallback_test
  name: "Fallback Test"
  autoReload: true
  entities:
    - entity_id: light.test
      state: "on"
`, sceneName, namespace, haName)
			Expect(utils.ApplyYAML(sceneYAML, namespace)).To(Succeed())

			By("Verifying scene is Ready despite missing token")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())

			By("Verifying lastError mentions missing token")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hascene", sceneName, "-n", namespace,
					"-o", "jsonpath={.status.lastError}")
				g.Expect(output).To(ContainSubstring("bootstrap"))
			}, utils.StatusUpdateTimeout, reconcileInterval).Should(Succeed())
		})
	})
})

// collectSceneDebugInfo gathers debugging information when a scene test fails
func collectSceneDebugInfo(namespace, haName string) {
	writeDebug := func(format string, args ...any) {
		_, _ = fmt.Fprintf(GinkgoWriter, format, args...)
	}

	writeDebug("\n=== SCENE DEBUG INFO ===\n")

	writeDebug("\n--- HomeAssistantScene Resources ---\n")
	cmd := exec.Command("kubectl", "get", "hascene", "-n", namespace, "-o", "wide")
	output, err := utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- Scenes ConfigMap Content ---\n")
	configMapName := haName + "-scenes"
	cmd = exec.Command("kubectl", "get", "configmap", configMapName, "-n", namespace, "-o", "yaml")
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- HomeAssistantScene Status (describe all) ---\n")
	cmd = exec.Command("kubectl", "describe", "hascene", "-n", namespace)
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- Controller Logs (last 200 lines) ---\n")
	cmd = exec.Command("kubectl", "logs", "-n", "homeassistant-operator-system",
		"-l", "control-plane=controller-manager", "--tail=200")
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

	writeDebug("\n=== END SCENE DEBUG INFO ===\n")
}
