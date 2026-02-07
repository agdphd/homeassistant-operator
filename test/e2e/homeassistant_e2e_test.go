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
	haNamespacePrefix = "ha-ctrl-e2e-"
)

var _ = Describe("HomeAssistant E2E", Label("homeassistant"), Ordered, func() {
	var (
		namespace  string
		haName     string
		configName string
	)

	BeforeEach(func() {
		namespace = haNamespacePrefix + utils.RandomString(8)
		haName = "test-ha-" + utils.RandomString(6)
		configName = "test-config-" + utils.RandomString(6)

		By("Creating test namespace: " + namespace)
		Expect(utils.CreateNamespace(namespace)).To(Succeed())
	})

	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Test failed - collecting debug info")
			collectHADebugInfo(namespace, haName, configName)
		}

		By("Deleting test namespace: " + namespace)
		if err := utils.DeleteNamespace(namespace); err != nil {
			fmt.Printf("Warning: failed to delete namespace %s: %v\n", namespace, err)
		}
	})

	// HA1: Version/image update triggers pod restart
	Context("Version Update", Label("slow", "pod-required"), func() {
		It("should restart pod when version is updated", func() {
			By("Creating HomeAssistant CR with version 2024.1")
			haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "2024.1"
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

			By("Capturing initial pod UID")
			initialPodUID := utils.Kubectl("get", "pod", podName, "-n", namespace,
				"-o", "jsonpath={.metadata.uid}")
			Expect(initialPodUID).NotTo(BeEmpty())

			By("Updating HA CR version to stable")
			updatedHAYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
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
			Expect(utils.ApplyYAML(updatedHAYAML, namespace)).To(Succeed())

			By("Verifying StatefulSet image changed")
			Eventually(func(g Gomega) {
				image := utils.Kubectl("get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.containers[0].image}")
				g.Expect(image).To(ContainSubstring(":stable"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying pod UID changed (pod restarted)")
			Eventually(func(g Gomega) {
				currentPodUID := utils.Kubectl("get", "pod", podName, "-n", namespace,
					"-o", "jsonpath={.metadata.uid}")
				g.Expect(currentPodUID).NotTo(BeEmpty())
				g.Expect(currentPodUID).NotTo(Equal(initialPodUID))
			}, utils.RestartTimeout, 5*time.Second).Should(Succeed())

			By("Verifying status version updated")
			Eventually(func(g Gomega) {
				version := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.version}")
				g.Expect(version).To(Equal("stable"))
			}, utils.StatusUpdateTimeout, 2*time.Second).Should(Succeed())
		})
	})

	// HA2: Timezone change updates pod env
	Context("Timezone Update", Label("slow", "pod-required"), func() {
		It("should restart pod when timezone is changed", func() {
			By("Creating HomeAssistant CR with timezone UTC")
			haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  timezone: "UTC"
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

			By("Capturing initial pod UID")
			initialPodUID := utils.Kubectl("get", "pod", podName, "-n", namespace,
				"-o", "jsonpath={.metadata.uid}")
			Expect(initialPodUID).NotTo(BeEmpty())

			By("Updating HA CR timezone to Europe/Warsaw")
			updatedHAYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  timezone: "Europe/Warsaw"
  storage:
    size: "1Gi"
  %s
`, haName, namespace, utils.GetEnhancedHAResourceRequests())
			Expect(utils.ApplyYAML(updatedHAYAML, namespace)).To(Succeed())

			By("Verifying StatefulSet container env TZ=Europe/Warsaw")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.containers[0].env[?(@.name=='TZ')].value}")
				g.Expect(output).To(Equal("Europe/Warsaw"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying pod restarted (UID changed)")
			Eventually(func(g Gomega) {
				currentPodUID := utils.Kubectl("get", "pod", podName, "-n", namespace,
					"-o", "jsonpath={.metadata.uid}")
				g.Expect(currentPodUID).NotTo(BeEmpty())
				g.Expect(currentPodUID).NotTo(Equal(initialPodUID))
			}, utils.RestartTimeout, 5*time.Second).Should(Succeed())
		})
	})

	// HA3: Service type ClusterIP -> LoadBalancer
	Context("Service Type Update", Label("fast", "infra-only"), func() {
		It("should update Service type from ClusterIP to LoadBalancer", func() {
			By("Creating HomeAssistant CR with ClusterIP service")
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

			By("Waiting for Service with type ClusterIP")
			serviceName := haName
			Eventually(func(g Gomega) {
				svcType := utils.Kubectl("get", "service", serviceName, "-n", namespace,
					"-o", "jsonpath={.spec.type}")
				g.Expect(svcType).To(Equal("ClusterIP"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Updating HA CR service type to LoadBalancer")
			updatedHAYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "1Gi"
  service:
    type: LoadBalancer
  %s
`, haName, namespace, utils.GetEnhancedHAResourceRequests())
			Expect(utils.ApplyYAML(updatedHAYAML, namespace)).To(Succeed())

			By("Verifying Service type changed to LoadBalancer")
			Eventually(func(g Gomega) {
				svcType := utils.Kubectl("get", "service", serviceName, "-n", namespace,
					"-o", "jsonpath={.spec.type}")
				g.Expect(svcType).To(Equal("LoadBalancer"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())
		})
	})

	// HA4: Resource limits update
	Context("Resource Limits Update", Label("slow", "pod-required"), func() {
		It("should restart pod when resource limits are updated", func() {
			By("Creating HomeAssistant CR with initial resources (Enhanced for faster startup)")
			haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "1Gi"
  resources:
    requests:
      cpu: "2000m"
      memory: "1Gi"
    limits:
      memory: "2Gi"
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

			By("Capturing initial pod UID")
			initialPodUID := utils.Kubectl("get", "pod", podName, "-n", namespace,
				"-o", "jsonpath={.metadata.uid}")
			Expect(initialPodUID).NotTo(BeEmpty())

			By("Updating resources: cpu=1500m, mem=768Mi, limit=1536Mi")
			updatedHAYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "1Gi"
  resources:
    requests:
      cpu: "1500m"
      memory: "768Mi"
    limits:
      memory: "1536Mi"
`, haName, namespace)
			Expect(utils.ApplyYAML(updatedHAYAML, namespace)).To(Succeed())

			By("Verifying StatefulSet container resources updated")
			Eventually(func(g Gomega) {
				cpuReq := utils.Kubectl("get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.containers[0].resources.requests.cpu}")
				g.Expect(cpuReq).To(Equal("1500m"))

				memReq := utils.Kubectl("get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.containers[0].resources.requests.memory}")
				g.Expect(memReq).To(Equal("768Mi"))

				memLimit := utils.Kubectl("get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.containers[0].resources.limits.memory}")
				g.Expect(memLimit).To(Equal("1536Mi"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying pod restarted (UID changed)")
			Eventually(func(g Gomega) {
				currentPodUID := utils.Kubectl("get", "pod", podName, "-n", namespace,
					"-o", "jsonpath={.metadata.uid}")
				g.Expect(currentPodUID).NotTo(BeEmpty())
				g.Expect(currentPodUID).NotTo(Equal(initialPodUID))
			}, utils.RestartTimeout, 5*time.Second).Should(Succeed())
		})
	})

	// HA5: Full bootstrap flow
	Context("Full Bootstrap Flow", Label("slow", "bootstrap", "pod-required"), func() {
		It("should complete bootstrap and create API token", func() {
			credentialsSecretName := "bootstrap-creds-" + utils.RandomString(6)

			By("Creating credentials Secret")
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
  bootstrap:
    enabled: true
    createApiToken: true
    credentials:
      secretRef:
        name: %s
  %s
`, haName, namespace, credentialsSecretName, utils.GetEnhancedHAResourceRequests())
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

			By("Waiting for bootstrap to complete")
			Eventually(func(g Gomega) {
				bootstrapCompleted := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.bootstrap.completed}")
				g.Expect(bootstrapCompleted).To(Equal("true"))
			}, utils.BootstrapTimeout, 10*time.Second).Should(Succeed())

			By("Verifying API token Secret created")
			tokenSecretName := haName + "-homeassistant-api-token"
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "secret", tokenSecretName, "-n", namespace,
					"-o", "jsonpath={.data.token}")
				g.Expect(output).NotTo(BeEmpty())
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying status bootstrap.apiTokenReady: true")
			apiTokenReady := utils.Kubectl("get", "ha", haName, "-n", namespace,
				"-o", "jsonpath={.status.bootstrap.apiTokenReady}")
			Expect(apiTokenReady).To(Equal("true"))

			By("Verifying condition BootstrapReady=True")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='BootstrapReady')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.StatusUpdateTimeout, 2*time.Second).Should(Succeed())
		})
	})

	// HA6: Bootstrap with missing credentials Secret
	Context("Bootstrap Missing Credentials", Label("slow", "bootstrap", "pod-required"), func() {
		It("should handle missing credentials Secret gracefully", func() {
			credentialsSecretName := "missing-creds-" + utils.RandomString(6)

			By("Creating HomeAssistant CR with bootstrap referencing non-existent credentials")
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
    createApiToken: true
    credentials:
      secretRef:
        name: %s
  %s
`, haName, namespace, credentialsSecretName, utils.GetEnhancedHAResourceRequests())
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

			By("Verifying pod starts normally despite missing credentials")
			podName := haName + "-0"
			Eventually(func(g Gomega) {
				phase := utils.Kubectl("get", "pod", podName, "-n", namespace,
					"-o", "jsonpath={.status.phase}")
				g.Expect(phase).To(Equal("Running"))

				readyOutput := utils.Kubectl("get", "pod", podName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(readyOutput).To(Equal("True"))
			}, utils.HAPodReadyTimeout, 10*time.Second).Should(Succeed())

			By("Verifying bootstrap not completed")
			Consistently(func(g Gomega) {
				bootstrapCompleted := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.bootstrap.completed}")
				g.Expect(bootstrapCompleted).NotTo(Equal("true"))
			}, 30*time.Second, 5*time.Second).Should(Succeed())

			By("Creating the missing credentials Secret")
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

			By("Verifying bootstrap proceeds after credentials are created")
			Eventually(func(g Gomega) {
				bootstrapCompleted := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.bootstrap.completed}")
				g.Expect(bootstrapCompleted).To(Equal("true"))
			}, utils.BootstrapTimeout, 10*time.Second).Should(Succeed())
		})
	})

	// HA7: Bootstrap onboarding already completed / idempotency
	Context("Bootstrap Idempotency", Label("slow", "bootstrap", "pod-required"), func() {
		It("should handle onboarding already completed gracefully", func() {
			credentialsSecretName := "bootstrap-idemp-creds-" + utils.RandomString(6)

			By("Creating credentials Secret")
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
  bootstrap:
    enabled: true
    createApiToken: true
    credentials:
      secretRef:
        name: %s
  %s
`, haName, namespace, credentialsSecretName, utils.GetEnhancedHAResourceRequests())
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

			By("Waiting for bootstrap to complete")
			Eventually(func(g Gomega) {
				bootstrapCompleted := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.bootstrap.completed}")
				g.Expect(bootstrapCompleted).To(Equal("true"))
			}, utils.BootstrapTimeout, 10*time.Second).Should(Succeed())

			By("Deleting HA CR (PVC preserves data)")
			deleteCmd := exec.Command("kubectl", "delete", "ha", haName, "-n", namespace)
			_, err := utils.Run(deleteCmd)
			Expect(err).NotTo(HaveOccurred())

			By("Waiting for HA CR to be deleted")
			Eventually(func(g Gomega) {
				checkCmd := exec.Command("kubectl", "get", "ha", haName, "-n", namespace)
				_, err := utils.Run(checkCmd)
				g.Expect(err).To(HaveOccurred())
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Recreating HA CR with same parameters")
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Recreating HomeAssistantConfiguration CR")
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Verifying bootstrap handles already-completed onboarding gracefully")
			Eventually(func(g Gomega) {
				bootstrapCompleted := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.bootstrap.completed}")
				g.Expect(bootstrapCompleted).To(Equal("true"))
			}, utils.BootstrapTimeout, 10*time.Second).Should(Succeed())

			By("Verifying condition BootstrapReady=True after recreation")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='BootstrapReady')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.StatusUpdateTimeout, 2*time.Second).Should(Succeed())
		})
	})

	// HA8: Status fields correctly reflect state
	Context("Status Fields", Label("fast", "infra-only"), func() {
		It("should correctly reflect status fields", func() {
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

			By("Verifying status.phase transitions to Running")
			Eventually(func(g Gomega) {
				phase := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.phase}")
				g.Expect(phase).To(Equal("Running"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying status.ready is true")
			Eventually(func(g Gomega) {
				ready := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.ready}")
				g.Expect(ready).To(Equal("true"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying status.version matches spec")
			Eventually(func(g Gomega) {
				version := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.version}")
				g.Expect(version).To(Equal("stable"))
			}, utils.StatusUpdateTimeout, 2*time.Second).Should(Succeed())

			By("Verifying condition Ready=True")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(status).To(Equal("True"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())
		})
	})

	// HA9: PVC with custom storageClassName and accessMode
	Context("PVC Custom Storage", Label("fast", "infra-only"), func() {
		It("should create PVC with correct size and accessMode", func() {
			By("Creating HomeAssistant CR with custom storage settings")
			haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "stable"
  storage:
    size: "2Gi"
    accessMode: ReadWriteOnce
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

			By("Verifying PVC created with correct settings")
			pvcName := haName + "-data-" + haName + "-0"
			Eventually(func(g Gomega) {
				// Check PVC exists - try StatefulSet PVC naming first
				output := utils.Kubectl("get", "pvc", pvcName, "-n", namespace,
					"-o", "jsonpath={.spec.resources.requests.storage}")
				if output == "" {
					// Fallback: try alternative PVC naming
					altPvcName := haName + "-data"
					output = utils.Kubectl("get", "pvc", altPvcName, "-n", namespace,
						"-o", "jsonpath={.spec.resources.requests.storage}")
				}
				g.Expect(output).To(Equal("2Gi"))
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			By("Verifying PVC accessMode is ReadWriteOnce")
			Eventually(func(g Gomega) {
				accessMode := utils.Kubectl("get", "pvc", pvcName, "-n", namespace,
					"-o", "jsonpath={.spec.accessModes[0]}")
				if accessMode == "" {
					altPvcName := haName + "-data"
					accessMode = utils.Kubectl("get", "pvc", altPvcName, "-n", namespace,
						"-o", "jsonpath={.spec.accessModes[0]}")
				}
				g.Expect(accessMode).To(Equal("ReadWriteOnce"))
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())
		})
	})
})

// collectHADebugInfo collects debug information when a HomeAssistant test fails
func collectHADebugInfo(namespace, haName, configName string) {
	fmt.Println("\n=== DEBUG INFO: HomeAssistant Test Failed ===")

	// HomeAssistant CR describe
	fmt.Println("\n--- HomeAssistant describe ---")
	descCmd := exec.Command("kubectl", "describe", "ha", haName, "-n", namespace)
	output, _ := descCmd.CombinedOutput()
	fmt.Println(string(output))

	// HomeAssistant status
	fmt.Println("\n--- HomeAssistant status ---")
	statusCmd := exec.Command("kubectl", "get", "ha", haName, "-n", namespace, "-o", "yaml")
	output, _ = statusCmd.CombinedOutput()
	fmt.Println(string(output))

	// HomeAssistantConfiguration describe
	fmt.Println("\n--- HomeAssistantConfiguration describe ---")
	configDescCmd := exec.Command("kubectl", "describe", "haconfig", configName, "-n", namespace)
	output, _ = configDescCmd.CombinedOutput()
	fmt.Println(string(output))

	// StatefulSet
	fmt.Println("\n--- StatefulSet ---")
	stsCmd := exec.Command("kubectl", "get", "statefulset", haName, "-n", namespace, "-o", "yaml")
	output, _ = stsCmd.CombinedOutput()
	fmt.Println(string(output))

	// Pods
	fmt.Println("\n--- Pods ---")
	podsCmd := exec.Command("kubectl", "get", "pods", "-n", namespace, "-o", "wide")
	output, _ = podsCmd.CombinedOutput()
	fmt.Println(string(output))

	// Pod logs
	fmt.Println("\n--- HA Pod logs (last 50 lines) ---")
	podName := haName + "-0"
	podLogsCmd := exec.Command("kubectl", "logs", podName, "-n", namespace, "--tail=50")
	output, _ = podLogsCmd.CombinedOutput()
	fmt.Println(string(output))

	// Services
	fmt.Println("\n--- Services ---")
	svcCmd := exec.Command("kubectl", "get", "services", "-n", namespace, "-o", "wide")
	output, _ = svcCmd.CombinedOutput()
	fmt.Println(string(output))

	// PVCs
	fmt.Println("\n--- PVCs ---")
	pvcCmd := exec.Command("kubectl", "get", "pvc", "-n", namespace, "-o", "wide")
	output, _ = pvcCmd.CombinedOutput()
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
