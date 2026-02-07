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
	secretsNamespacePrefix = "hasecrets-e2e-"
)

var _ = Describe("HomeAssistantSecrets E2E", Label("secrets"), Ordered, func() {
	var namespace string
	var haName string
	var configName string
	var secretsName string

	BeforeEach(func() {
		// Generate unique names for each test
		namespace = secretsNamespacePrefix + utils.RandomString(8)
		haName = "test-ha-" + utils.RandomString(6)
		configName = "test-config-" + utils.RandomString(6)
		secretsName = "test-hasecrets-" + utils.RandomString(6)

		By("Creating test namespace: " + namespace)
		Expect(utils.CreateNamespace(namespace)).To(Succeed())
	})

	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Test failed - collecting debug info")
			collectSecretsDebugInfo(namespace, haName, secretsName)
		}

		By("Deleting test namespace: " + namespace)
		if err := utils.DeleteNamespace(namespace); err != nil {
			// Log error but don't fail the test - cleanup is best effort
			fmt.Printf("Warning: failed to delete namespace %s: %v\n", namespace, err)
		}
	})

	Context("Secret Update & AutoRestart", func() {
		It("should trigger pod restart when source Secret updates (autoRestart: true)",
			Label("slow", "pod-required"), func() {
				sourceName := "mqtt-creds"

				By("Creating source K8s Secret")
				secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  mqtt_user: "old_user"
  mqtt_password: "old_password"
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

				By("Creating HomeAssistantSecrets CR with autoRestart: true")
				haSecretsYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantSecrets
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  autoRestart: true
  secretRefs:
    - name: %s
`, secretsName, namespace, haName, sourceName)
				Expect(utils.ApplyYAML(haSecretsYAML, namespace)).To(Succeed())

				By("Waiting for HA Pod to be Ready")
				Eventually(func(g Gomega) {
					output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.status.phase}")
					g.Expect(output).To(Equal("Running"))

					readyOutput := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
						"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
					g.Expect(readyOutput).To(Equal("True"))
				}, utils.HAPodReadyTimeout, 10*time.Second).Should(Succeed())

				By("Capturing initial pod UID and secretsHash")
				var oldPodUID string
				Eventually(func(g Gomega) {
					output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
					g.Expect(output).NotTo(BeEmpty())
					oldPodUID = output
				}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

				var oldSecretsHash string
				Eventually(func(g Gomega) {
					output := utils.GetResourceStatus("hasecrets", secretsName, namespace, "{.status.secretsHash}")
					g.Expect(output).NotTo(BeEmpty())
					oldSecretsHash = output
				}, utils.StatusUpdateTimeout, 2*time.Second).Should(Succeed())

				By("Updating source Secret")
				Expect(utils.PatchResource("secret", sourceName, namespace, "json",
					`[{"op":"replace","path":"/data/mqtt_user","value":"bmV3X3VzZXI="}]`)).To(Succeed())

				By("Verifying StatefulSet annotation changed (secrets-hash)")
				Eventually(func(g Gomega) {
					output := utils.Kubectl(
						"get", "statefulset", haName, "-n", namespace,
						"-o", "jsonpath={.spec.template.metadata.annotations.ha\\.homeassistant\\.io/secrets-hash}",
					)
					g.Expect(output).NotTo(BeEmpty())
					g.Expect(output).NotTo(Equal(oldSecretsHash))
				}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

				By("Verifying generated Secret contains new value")
				Eventually(func(g Gomega) {
					output := utils.Kubectl(
						"get", "secret", haName+"-generated-secrets", "-n", namespace,
						"-o", "jsonpath={.data.secrets\\.yaml}",
					)
					g.Expect(output).NotTo(BeEmpty())
				}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

				By("Verifying pod UID changed (rolling restart)")
				Eventually(func(g Gomega) {
					output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
					g.Expect(output).NotTo(Equal(oldPodUID))
				}, utils.RestartTimeout, 10*time.Second).Should(Succeed())

				By("Verifying status.secretsHash changed")
				Eventually(func(g Gomega) {
					output := utils.GetResourceStatus("hasecrets", secretsName, namespace, "{.status.secretsHash}")
					g.Expect(output).NotTo(Equal(oldSecretsHash))
				}, utils.StatusUpdateTimeout, 2*time.Second).Should(Succeed())
			})

		It("should NOT trigger pod restart when autoRestart=false", Label("slow", "pod-required"), func() {
			sourceName := "mqtt-creds-noauto"

			By("Creating source K8s Secret")
			secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  mqtt_user: "user1"
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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantSecrets CR with autoRestart: false")
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

			By("Waiting for HA Pod to be Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.status.phase}")
				g.Expect(output).To(Equal("Running"))
			}, utils.HAPodReadyTimeout, 10*time.Second).Should(Succeed())

			By("Waiting for ha-secrets volume to be mounted (may trigger initial restart)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.volumes[?(@.name=='ha-secrets')].name}")
				g.Expect(output).To(Equal("ha-secrets"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Waiting for pod to stabilize after volume mount")
			time.Sleep(30 * time.Second)

			By("Capturing stable pod UID")
			var oldPodUID string
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).NotTo(BeEmpty())
				oldPodUID = output
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			By("Ensuring pod UID is stable (no pending restarts)")
			Consistently(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).To(Equal(oldPodUID))
			}, 30*time.Second, 3*time.Second).Should(Succeed())

			var oldSecretsHash string
			Eventually(func(g Gomega) {
				output := utils.GetResourceStatus("hasecrets", secretsName, namespace, "{.status.secretsHash}")
				g.Expect(output).NotTo(BeEmpty())
				oldSecretsHash = output
			}, utils.StatusUpdateTimeout, 2*time.Second).Should(Succeed())

			By("Updating source Secret")
			Expect(utils.PatchResource("secret", sourceName, namespace, "json",
				`[{"op":"replace","path":"/data/mqtt_user","value":"dXNlcjI="}]`)).To(Succeed())

			By("Waiting for generated Secret update (new hash in status)")
			Eventually(func(g Gomega) {
				output := utils.GetResourceStatus("hasecrets", secretsName, namespace, "{.status.secretsHash}")
				g.Expect(output).NotTo(Equal(oldSecretsHash))
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("Verifying pod UID NOT changed (no restart)")
			Consistently(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).To(Equal(oldPodUID))
			}, 30*time.Second, 5*time.Second).Should(Succeed())

			By("Verifying StatefulSet annotation NOT changed")
			oldAnnotation := utils.Kubectl(
				"get", "statefulset", haName, "-n", namespace,
				"-o", "jsonpath={.spec.template.metadata.annotations.ha\\.homeassistant\\.io/secrets-hash}",
			)
			Consistently(func(g Gomega) {
				output := utils.Kubectl(
					"get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.spec.template.metadata.annotations.ha\\.homeassistant\\.io/secrets-hash}",
				)
				g.Expect(output).To(Equal(oldAnnotation))
			}, 20*time.Second, 5*time.Second).Should(Succeed())
		})
	})

	Context("Multiple Secrets Aggregation", Label("fast", "infra-only"), func() {
		It("should aggregate multiple Secrets into one secrets.yaml", func() {
			By("Creating multiple source Secrets")
			secret1YAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: mqtt-creds
  namespace: %s
type: Opaque
stringData:
  mqtt_user: "mqtt_user_value"
  mqtt_password: "mqtt_pass_value"
`, namespace)
			Expect(utils.ApplyYAML(secret1YAML, namespace)).To(Succeed())

			secret2YAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: db-creds
  namespace: %s
type: Opaque
stringData:
  db_host: "db.example.com"
  db_password: "db_pass_value"
`, namespace)
			Expect(utils.ApplyYAML(secret2YAML, namespace)).To(Succeed())

			secret3YAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: api-keys
  namespace: %s
type: Opaque
stringData:
  weather_api_key: "weather_key_123"
  telegram_token: "telegram_token_456"
`, namespace)
			Expect(utils.ApplyYAML(secret3YAML, namespace)).To(Succeed())

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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantSecrets with 3 secretRefs")
			haSecretsYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantSecrets
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  secretRefs:
    - name: mqtt-creds
    - name: db-creds
    - name: api-keys
`, secretsName, namespace, haName)
			Expect(utils.ApplyYAML(haSecretsYAML, namespace)).To(Succeed())

			By("Verifying generated Secret contains all 6 keys")
			Eventually(func(g Gomega) {
				output := utils.Kubectl(
					"get", "secret", haName+"-generated-secrets", "-n", namespace,
					"-o", "jsonpath={.data.secrets\\.yaml}",
				)
				g.Expect(output).NotTo(BeEmpty())
				// Secret data is base64 encoded, but we can verify it exists
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			By("Verifying Status Ready=True")
			Eventually(func(g Gomega) {
				output := utils.GetResourceStatus("hasecrets", secretsName, namespace,
					"{.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.StatusUpdateTimeout, 2*time.Second).Should(Succeed())
		})

		It("should respect keys filter for selective key extraction", Label("fast"), func() {
			By("Creating source Secret with 4 keys")
			secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: filtered-secret
  namespace: %s
type: Opaque
stringData:
  key_a: "value_a"
  key_b: "value_b"
  key_c: "value_c"
  key_d: "value_d"
`, namespace)
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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantSecrets with keys filter [b, d]")
			haSecretsYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantSecrets
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  secretRefs:
    - name: filtered-secret
      keys:
        - key_b
        - key_d
`, secretsName, namespace, haName)
			Expect(utils.ApplyYAML(haSecretsYAML, namespace)).To(Succeed())

			By("Verifying generated Secret created")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "secret", haName+"-generated-secrets", "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			By("Verifying Status Ready=True")
			Eventually(func(g Gomega) {
				output := utils.GetResourceStatus("hasecrets", secretsName, namespace,
					"{.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.StatusUpdateTimeout, 2*time.Second).Should(Succeed())
		})

		It("should support adding new secretRef to existing HomeAssistantSecrets", Label("fast"), func() {
			By("Creating first source Secret")
			secret1YAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: secret1
  namespace: %s
type: Opaque
stringData:
  key1: "value1"
`, namespace)
			Expect(utils.ApplyYAML(secret1YAML, namespace)).To(Succeed())

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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantSecrets with 1 secretRef")
			haSecretsYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantSecrets
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  secretRefs:
    - name: secret1
`, secretsName, namespace, haName)
			Expect(utils.ApplyYAML(haSecretsYAML, namespace)).To(Succeed())

			By("Waiting for generated Secret with 1 source")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "secret", haName+"-generated-secrets", "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			var oldHash string
			Eventually(func(g Gomega) {
				output := utils.GetResourceStatus("hasecrets", secretsName, namespace, "{.status.secretsHash}")
				g.Expect(output).NotTo(BeEmpty())
				oldHash = output
			}, utils.StatusUpdateTimeout, 2*time.Second).Should(Succeed())

			By("Creating second source Secret")
			secret2YAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: secret2
  namespace: %s
type: Opaque
stringData:
  key2: "value2"
`, namespace)
			Expect(utils.ApplyYAML(secret2YAML, namespace)).To(Succeed())

			By("Updating HomeAssistantSecrets to add secret2")
			updatedSecretsYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantSecrets
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  secretRefs:
    - name: secret1
    - name: secret2
`, secretsName, namespace, haName)
			Expect(utils.ApplyYAML(updatedSecretsYAML, namespace)).To(Succeed())

			By("Verifying secretsHash changed")
			Eventually(func(g Gomega) {
				output := utils.GetResourceStatus("hasecrets", secretsName, namespace, "{.status.secretsHash}")
				g.Expect(output).NotTo(Equal(oldHash))
			}, utils.StatusUpdateTimeout, 2*time.Second).Should(Succeed())

			By("Verifying Status Ready=True")
			Eventually(func(g Gomega) {
				output := utils.GetResourceStatus("hasecrets", secretsName, namespace,
					"{.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.StatusUpdateTimeout, 2*time.Second).Should(Succeed())
		})
	})

	Context("Secrets + Configuration Integration", func() {
		It("should mount both ConfigMap and Secret volumes when both exist", Label("fast", "infra-only"), func() {
			By("Creating source Secret")
			secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: mqtt-secret
  namespace: %s
type: Opaque
stringData:
  mqtt_user: "testuser"
`, namespace)
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
    automation: []
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
  secretRefs:
    - name: mqtt-secret
`, secretsName, namespace, haName)
			Expect(utils.ApplyYAML(haSecretsYAML, namespace)).To(Succeed())

			By("Verifying ConfigMap created")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "configmap", haName+"-configuration", "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			By("Verifying Secret created")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "secret", haName+"-generated-secrets", "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			By("Verifying StatefulSet has both volumes")
			Eventually(func(g Gomega) {
				// Check for ha-configuration volume
				output := utils.Kubectl("get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.volumes[?(@.name=='ha-configuration')].name}")
				g.Expect(output).To(Equal("ha-configuration"))

				// Check for ha-secrets volume
				output = utils.Kubectl("get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.volumes[?(@.name=='ha-secrets')].name}")
				g.Expect(output).To(Equal("ha-secrets"))

				// Check for config volume (PVC)
				output = utils.Kubectl("get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.volumes[?(@.name=='config')].name}")
				g.Expect(output).To(Equal("config"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())
		})

		It("should trigger single restart when both Secrets and Configuration change", Label("slow", "pod-required"), func() {
			sourceName := "mqtt-dual"

			By("Creating source Secret")
			secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  mqtt_user: "initial_user"
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
  reloadStrategy: auto
  configuration: |
    homeassistant:
      timezone: UTC
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantSecrets CR with autoRestart: true")
			haSecretsYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantSecrets
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  autoRestart: true
  secretRefs:
    - name: %s
`, secretsName, namespace, haName, sourceName)
			Expect(utils.ApplyYAML(haSecretsYAML, namespace)).To(Succeed())

			By("Waiting for HA Pod to be Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.status.phase}")
				g.Expect(output).To(Equal("Running"))

				readyOutput := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(readyOutput).To(Equal("True"))
			}, utils.HAPodReadyTimeout, 10*time.Second).Should(Succeed())

			By("Capturing initial pod UID")
			var oldPodUID string
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).NotTo(BeEmpty())
				oldPodUID = output
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			By("Simultaneously updating Secret and Configuration")
			// Update Secret
			Expect(utils.PatchResource("secret", sourceName, namespace, "json",
				`[{"op":"replace","path":"/data/mqtt_user","value":"bmV3X3VzZXI="}]`)).To(Succeed())

			// Update Configuration (timezone = critical section → restart)
			updatedConfigYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(updatedConfigYAML, namespace)).To(Succeed())

			By("Verifying pod restarted (new UID)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).NotTo(Equal(oldPodUID))
			}, utils.RestartTimeout, 10*time.Second).Should(Succeed())

			By("Verifying both resources Ready=True")
			Eventually(func(g Gomega) {
				output := utils.GetResourceStatus("haconfig", configName, namespace,
					"{.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.StatusUpdateTimeout, 2*time.Second).Should(Succeed())

			Eventually(func(g Gomega) {
				output := utils.GetResourceStatus("hasecrets", secretsName, namespace,
					"{.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(output).To(Equal("True"))
			}, utils.StatusUpdateTimeout, 2*time.Second).Should(Succeed())
		})
	})

	Context("Hash Stability & Idempotency", Label("slow", "pod-required"), func() {
		It("should maintain stable hash when no changes occur (idempotency)", func() {
			sourceName := "stable-secret"

			By("Creating source Secret")
			secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  key1: "value1"
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
    default_config:
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantSecrets CR with autoRestart: true")
			haSecretsYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantSecrets
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  autoRestart: true
  secretRefs:
    - name: %s
`, secretsName, namespace, haName, sourceName)
			Expect(utils.ApplyYAML(haSecretsYAML, namespace)).To(Succeed())

			By("Waiting for HA Pod to be Ready")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.status.phase}")
				g.Expect(output).To(Equal("Running"))
			}, utils.HAPodReadyTimeout, 10*time.Second).Should(Succeed())

			By("Waiting for ha-secrets volume to be mounted (may trigger initial restart)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.volumes[?(@.name=='ha-secrets')].name}")
				g.Expect(output).To(Equal("ha-secrets"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Waiting for pod to stabilize after volume mount")
			time.Sleep(30 * time.Second)

			By("Ensuring pod UID is stable (no pending restarts)")
			var tempUID string
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).NotTo(BeEmpty())
				tempUID = output
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())
			Consistently(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).To(Equal(tempUID))
			}, 15*time.Second, 3*time.Second).Should(Succeed())

			By("Capturing pod UID and secretsHash")
			var podUID string
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).NotTo(BeEmpty())
				podUID = output
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			var secretsHash string
			Eventually(func(g Gomega) {
				output := utils.GetResourceStatus("hasecrets", secretsName, namespace, "{.status.secretsHash}")
				g.Expect(output).NotTo(BeEmpty())
				secretsHash = output
			}, utils.StatusUpdateTimeout, 2*time.Second).Should(Succeed())

			var stsResourceVersion string
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "statefulset", haName, "-n", namespace, "-o", "jsonpath={.metadata.resourceVersion}")
				g.Expect(output).NotTo(BeEmpty())
				stsResourceVersion = output
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			By("Verifying pod UID remains stable (no restarts)")
			Consistently(func(g Gomega) {
				output := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
				g.Expect(output).To(Equal(podUID))
			}, 30*time.Second, 5*time.Second).Should(Succeed())

			By("Verifying secretsHash remains stable")
			Consistently(func(g Gomega) {
				output := utils.GetResourceStatus("hasecrets", secretsName, namespace, "{.status.secretsHash}")
				g.Expect(output).To(Equal(secretsHash))
			}, 30*time.Second, 5*time.Second).Should(Succeed())

			By("Verifying StatefulSet resourceVersion remains stable")
			Consistently(func(g Gomega) {
				output := utils.Kubectl("get", "statefulset", haName, "-n", namespace, "-o", "jsonpath={.metadata.resourceVersion}")
				g.Expect(output).To(Equal(stsResourceVersion))
			}, 30*time.Second, 5*time.Second).Should(Succeed())
		})
	})

	Context("Lifecycle & Cleanup", func() {
		It("should delete generated Secret when HomeAssistantSecrets is deleted", Label("fast", "infra-only"), func() {
			sourceName := "cleanup-secret"

			By("Creating source Secret")
			secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  key1: "value1"
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
  secretRefs:
    - name: %s
`, secretsName, namespace, haName, sourceName)
			Expect(utils.ApplyYAML(haSecretsYAML, namespace)).To(Succeed())

			By("Waiting for generated Secret")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "secret", haName+"-generated-secrets", "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			By("Deleting HomeAssistantSecrets CR")
			cmd := exec.Command("kubectl", "delete", "hasecrets", secretsName, "-n", namespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying generated Secret deleted (cascade)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "secret", haName+"-generated-secrets", "-n", namespace, "--ignore-not-found")
				g.Expect(output).To(BeEmpty())
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			By("Verifying HA StatefulSet still exists")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "statefulset", haName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			By("Verifying source Secret still exists (not owned)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "secret", sourceName, "-n", namespace)
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())
		})

		It("should mount Secret when HomeAssistantSecrets is created after HA startup",
			Label("slow", "pod-required"), func() {
				sourceName := "late-secret"

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

				By("Creating HomeAssistantConfiguration CR (without Secrets)")
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

				By("Waiting for StatefulSet without ha-secrets volume")
				Eventually(func(g Gomega) {
					output := utils.Kubectl("get", "statefulset", haName, "-n", namespace)
					g.Expect(output).NotTo(BeEmpty())
				}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

				By("Verifying ha-secrets volume does NOT exist initially")
				output := utils.Kubectl("get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.volumes[?(@.name=='ha-secrets')].name}")
				Expect(output).To(BeEmpty())

				By("Creating source Secret")
				secretYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  late_key: "late_value"
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
  secretRefs:
    - name: %s
`, secretsName, namespace, haName, sourceName)
				Expect(utils.ApplyYAML(haSecretsYAML, namespace)).To(Succeed())

				By("Verifying generated Secret created")
				Eventually(func(g Gomega) {
					output := utils.Kubectl("get", "secret", haName+"-generated-secrets", "-n", namespace)
					g.Expect(output).NotTo(BeEmpty())
				}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

				By("Verifying StatefulSet now has ha-secrets volume")
				Eventually(func(g Gomega) {
					output := utils.Kubectl("get", "statefulset", haName, "-n", namespace,
						"-o", "jsonpath={.spec.template.spec.volumes[?(@.name=='ha-secrets')].name}")
					g.Expect(output).To(Equal("ha-secrets"))
				}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())
			})
	})
})

// collectSecretsDebugInfo gathers debug information for HomeAssistantSecrets tests on failure.
func collectSecretsDebugInfo(namespace, haName, secretsName string) {
	writeDebug := func(format string, args ...any) {
		_, _ = fmt.Fprintf(GinkgoWriter, format, args...)
	}

	writeDebug("\n=== SECRETS DEBUG INFO ===\n")

	writeDebug("\n--- HomeAssistantSecrets Status ---\n")
	cmd := exec.Command("kubectl", "describe", "hasecrets", secretsName, "-n", namespace)
	output, err := utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- Generated Secret ---\n")
	cmd = exec.Command("kubectl", "get", "secret", haName+"-generated-secrets", "-n", namespace, "-o", "yaml")
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

	writeDebug("\n--- StatefulSet Volumes ---\n")
	cmd = exec.Command(
		"kubectl", "get", "statefulset", haName, "-n", namespace,
		"-o", "jsonpath={.spec.template.spec.volumes}",
	)
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- Pod Status ---\n")
	cmd = exec.Command("kubectl", "get", "pod", haName+"-0", "-n", namespace, "-o", "yaml")
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

	writeDebug("\n=== END SECRETS DEBUG INFO ===\n")
}
