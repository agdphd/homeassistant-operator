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
	addonTestNamespacePrefix = "haaddon-e2e-"
	addonBootstrapSecret     = "ha-addon-bootstrap-creds"
	addonReconcileInterval   = 2 * time.Second
	addonPodReadyInterval    = 10 * time.Second
)

var _ = Describe("HomeAssistantAddon E2E", Label("addon"), Ordered, func() {
	var (
		namespace string
		haName    string
	)

	BeforeEach(func() {
		namespace = addonTestNamespacePrefix + utils.RandomString(8)
		haName = "test-ha-" + utils.RandomString(6)

		By("Creating test namespace: " + namespace)
		Expect(utils.CreateNamespace(namespace)).To(Succeed())
	})

	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Test failed - collecting debug info")
			collectAddonDebugInfo(namespace, haName)
		}

		By("Deleting test namespace: " + namespace)
		_ = utils.DeleteNamespace(namespace)
	})

	// ---- Fast Tests: verify K8s resource creation, no pod startup required ----

	Context("Mosquito Profile", Label("fast"), func() {
		It("should create StatefulSet, Service, ConfigMap and set status fields", func() {
			addonName := "test-mosquito"
			resourceName := haName + "-" + addonName

			By("Creating HomeAssistant CR")
			Expect(utils.ApplyYAML(addonMinimalHAYAML(haName, namespace), namespace)).To(Succeed())
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "ha", haName, "-n", namespace, "-o", "jsonpath={.metadata.name}")
				g.Expect(out).To(Equal(haName))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Creating HomeAssistantAddon with mosquito profile")
			addonYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAddon
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  profile: mosquito
`, addonName, namespace, haName)
			Expect(utils.ApplyYAML(addonYAML, namespace)).To(Succeed())

			By("Verifying StatefulSet was created with eclipse-mosquitto:2 image")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "statefulset", resourceName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.containers[0].image}")
				g.Expect(out).To(Equal("eclipse-mosquitto:2"))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying Service with mqtt port 1883")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "service", resourceName, "-n", namespace,
					"-o", "jsonpath={.spec.ports[?(@.name=='mqtt')].port}")
				g.Expect(out).To(Equal("1883"))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying ConfigMap with mosquitto.conf")
			configMapName := resourceName + "-config"
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.mosquitto\\.conf}")
				g.Expect(out).To(ContainSubstring("listener 1883"))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying status.resolvedImage")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "haaddon", addonName, "-n", namespace,
					"-o", "jsonpath={.status.resolvedImage}")
				g.Expect(out).To(Equal("eclipse-mosquitto:2"))
			}, utils.StatusUpdateTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying status.workloadType is StatefulSet")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "haaddon", addonName, "-n", namespace,
					"-o", "jsonpath={.status.workloadType}")
				g.Expect(out).To(Equal("StatefulSet"))
			}, utils.StatusUpdateTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying status.serviceName")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "haaddon", addonName, "-n", namespace,
					"-o", "jsonpath={.status.serviceName}")
				g.Expect(out).To(Equal(resourceName))
			}, utils.StatusUpdateTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying status.phase is set")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "haaddon", addonName, "-n", namespace,
					"-o", "jsonpath={.status.phase}")
				g.Expect(out).NotTo(BeEmpty())
			}, utils.StatusUpdateTimeout, addonReconcileInterval).Should(Succeed())
		})
	})

	Context("MariaDB Profile", Label("fast"), func() {
		It("should create StatefulSet with MySQL port 3306", func() {
			addonName := "test-mariadb"
			resourceName := haName + "-" + addonName

			By("Creating HomeAssistant CR")
			Expect(utils.ApplyYAML(addonMinimalHAYAML(haName, namespace), namespace)).To(Succeed())
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "ha", haName, "-n", namespace, "-o", "jsonpath={.metadata.name}")
				g.Expect(out).To(Equal(haName))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Creating HomeAssistantAddon with mariadb profile")
			addonYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAddon
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  profile: mariadb
`, addonName, namespace, haName)
			Expect(utils.ApplyYAML(addonYAML, namespace)).To(Succeed())

			By("Verifying StatefulSet with mariadb:11 image")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "statefulset", resourceName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.containers[0].image}")
				g.Expect(out).To(Equal("mariadb:11"))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying Service with MySQL port 3306")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "service", resourceName, "-n", namespace,
					"-o", "jsonpath={.spec.ports[?(@.name=='mysql')].port}")
				g.Expect(out).To(Equal("3306"))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying status.workloadType is StatefulSet")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "haaddon", addonName, "-n", namespace,
					"-o", "jsonpath={.status.workloadType}")
				g.Expect(out).To(Equal("StatefulSet"))
			}, utils.StatusUpdateTimeout, addonReconcileInterval).Should(Succeed())
		})
	})

	Context("Node-RED Profile", Label("fast"), func() {
		It("should create StatefulSet with HTTP port 1880", func() {
			addonName := "test-nodered"
			resourceName := haName + "-" + addonName

			By("Creating HomeAssistant CR")
			Expect(utils.ApplyYAML(addonMinimalHAYAML(haName, namespace), namespace)).To(Succeed())
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "ha", haName, "-n", namespace, "-o", "jsonpath={.metadata.name}")
				g.Expect(out).To(Equal(haName))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Creating HomeAssistantAddon with node-red profile")
			addonYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAddon
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  profile: node-red
`, addonName, namespace, haName)
			Expect(utils.ApplyYAML(addonYAML, namespace)).To(Succeed())

			By("Verifying StatefulSet with nodered/node-red:latest image")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "statefulset", resourceName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.containers[0].image}")
				g.Expect(out).To(Equal("nodered/node-red:latest"))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying Service with HTTP port 1880")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "service", resourceName, "-n", namespace,
					"-o", "jsonpath={.spec.ports[?(@.name=='http')].port}")
				g.Expect(out).To(Equal("1880"))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())
		})
	})

	Context("Custom Image (no profile)", Label("fast"), func() {
		It("should create Deployment (no storage) with custom image", func() {
			addonName := "test-custom"
			resourceName := haName + "-" + addonName

			By("Creating HomeAssistant CR")
			Expect(utils.ApplyYAML(addonMinimalHAYAML(haName, namespace), namespace)).To(Succeed())
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "ha", haName, "-n", namespace, "-o", "jsonpath={.metadata.name}")
				g.Expect(out).To(Equal(haName))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Creating HomeAssistantAddon with custom image and no storage")
			addonYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAddon
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  image: nginx:alpine
  ports:
    - name: http
      port: 80
      protocol: TCP
`, addonName, namespace, haName)
			Expect(utils.ApplyYAML(addonYAML, namespace)).To(Succeed())

			By("Verifying Deployment (not StatefulSet) was created")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "deployment", resourceName, "-n", namespace,
					"-o", "jsonpath={.metadata.name}")
				g.Expect(out).To(Equal(resourceName))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying StatefulSet was NOT created")
			Consistently(func(g Gomega) {
				out := utils.Kubectl("get", "statefulset", resourceName, "-n", namespace, "--ignore-not-found")
				g.Expect(out).To(BeEmpty())
			}, 5*time.Second, addonReconcileInterval).Should(Succeed())

			By("Verifying image is nginx:alpine")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "deployment", resourceName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.containers[0].image}")
				g.Expect(out).To(Equal("nginx:alpine"))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying status.workloadType is Deployment")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "haaddon", addonName, "-n", namespace,
					"-o", "jsonpath={.status.workloadType}")
				g.Expect(out).To(Equal("Deployment"))
			}, utils.StatusUpdateTimeout, addonReconcileInterval).Should(Succeed())
		})
	})

	Context("Profile + Resource Override", Label("fast"), func() {
		It("should apply user resource limits on top of profile defaults", func() {
			addonName := "test-override"
			resourceName := haName + "-" + addonName

			By("Creating HomeAssistant CR")
			Expect(utils.ApplyYAML(addonMinimalHAYAML(haName, namespace), namespace)).To(Succeed())
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "ha", haName, "-n", namespace, "-o", "jsonpath={.metadata.name}")
				g.Expect(out).To(Equal(haName))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Creating HomeAssistantAddon with mariadb profile + custom resources")
			addonYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAddon
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  profile: mariadb
  resources:
    requests:
      memory: "256Mi"
      cpu: "250m"
    limits:
      memory: "1Gi"
`, addonName, namespace, haName)
			Expect(utils.ApplyYAML(addonYAML, namespace)).To(Succeed())

			By("Verifying StatefulSet uses overridden memory request 256Mi")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "statefulset", resourceName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.containers[0].resources.requests.memory}")
				g.Expect(out).To(Equal("256Mi"))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying profile image (mariadb:11) is still used")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "statefulset", resourceName, "-n", namespace,
					"-o", "jsonpath={.spec.template.spec.containers[0].image}")
				g.Expect(out).To(Equal("mariadb:11"))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())
		})
	})

	Context("Garbage Collection", Label("fast"), func() {
		It("should delete child resources when addon CR is deleted", func() {
			addonName := "test-gc"
			resourceName := haName + "-" + addonName

			By("Creating HomeAssistant CR")
			Expect(utils.ApplyYAML(addonMinimalHAYAML(haName, namespace), namespace)).To(Succeed())
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "ha", haName, "-n", namespace, "-o", "jsonpath={.metadata.name}")
				g.Expect(out).To(Equal(haName))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Creating HomeAssistantAddon with mariadb profile (StatefulSet + Service)")
			addonYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAddon
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  profile: mariadb
`, addonName, namespace, haName)
			Expect(utils.ApplyYAML(addonYAML, namespace)).To(Succeed())

			By("Waiting for StatefulSet to be created")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "statefulset", resourceName, "-n", namespace,
					"-o", "jsonpath={.metadata.name}")
				g.Expect(out).To(Equal(resourceName))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Waiting for Service to be created")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "service", resourceName, "-n", namespace,
					"-o", "jsonpath={.metadata.name}")
				g.Expect(out).To(Equal(resourceName))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Deleting the addon CR")
			cmd := exec.Command("kubectl", "delete", "haaddon", addonName, "-n", namespace)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("Verifying addon CR is fully deleted")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "haaddon", addonName, "-n", namespace, "--ignore-not-found")
				g.Expect(out).To(BeEmpty())
			}, utils.ReconciliationTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying StatefulSet is garbage collected (owner reference)")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "statefulset", resourceName, "-n", namespace, "--ignore-not-found")
				g.Expect(out).To(BeEmpty())
			}, utils.ReconciliationTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying Service is garbage collected (owner reference)")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "service", resourceName, "-n", namespace, "--ignore-not-found")
				g.Expect(out).To(BeEmpty())
			}, utils.ReconciliationTimeout, addonReconcileInterval).Should(Succeed())
		})
	})

	Context("HAIntegration", Label("fast"), func() {
		It("should inject mqtt broker into HomeAssistantConfiguration", func() {
			addonName := "test-mqtt"
			// HAIntegration looks for HAConfig named "<ha-name>-config" by convention
			configName := haName + "-config"

			By("Creating HomeAssistant CR")
			Expect(utils.ApplyYAML(addonMinimalHAYAML(haName, namespace), namespace)).To(Succeed())
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "ha", haName, "-n", namespace, "-o", "jsonpath={.metadata.name}")
				g.Expect(out).To(Equal(haName))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Creating HomeAssistantConfiguration with name convention <ha-name>-config")
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
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantAddon with mosquito profile (includes haIntegration.mqtt)")
			addonYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAddon
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  profile: mosquito
`, addonName, namespace, haName)
			Expect(utils.ApplyYAML(addonYAML, namespace)).To(Succeed())

			By("Verifying HomeAssistantConfiguration has mqtt section with correct broker DNS")
			expectedBroker := fmt.Sprintf("%s-%s.%s.svc.cluster.local", haName, addonName, namespace)
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "haconfig", configName, "-n", namespace,
					"-o", "jsonpath={.spec.configuration}")
				g.Expect(out).To(ContainSubstring("mqtt:"))
				g.Expect(out).To(ContainSubstring(expectedBroker))
			}, utils.ReconciliationTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying HAIntegrationReady condition is set")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "haaddon", addonName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='HAIntegrationReady')].status}")
				g.Expect(out).To(Equal("True"))
			}, utils.StatusUpdateTimeout, addonReconcileInterval).Should(Succeed())
		})
	})

	// ---- Slow Tests: require actual pod startup ----

	Context("Mosquito + HA Bootstrap", Label("bootstrap", "slow"), func() {
		It("should verify mqtt section appears in HA configuration.yaml", func() {
			addonName := "mosquito"
			configName := haName + "-config"

			By("Creating bootstrap credentials Secret")
			credsYAML := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
type: Opaque
stringData:
  username: admin
  password: e2e-addon-test-pwd-789012
`, addonBootstrapSecret, namespace)
			Expect(utils.ApplyYAML(credsYAML, namespace)).To(Succeed())

			By("Creating HomeAssistant with bootstrap enabled")
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
    ownerName: "E2E Addon Test"
    language: "en"
  %s
`, haName, namespace, addonBootstrapSecret, haName, utils.GetEnhancedHAResourceRequests())
			Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

			By("Creating HomeAssistantConfiguration (name convention: <ha-name>-config)")
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
      unit_system: metric
`, configName, namespace, haName)
			Expect(utils.ApplyYAML(configYAML, namespace)).To(Succeed())

			By("Waiting for bootstrap to complete")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.bootstrap.completed}")
				g.Expect(out).To(Equal("true"))
			}, utils.BootstrapTimeout, bootstrapInterval).Should(Succeed())

			By("Waiting for HA pod to be Ready")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				g.Expect(out).To(Equal("True"))
			}, utils.HAPodReadyTimeout, addonPodReadyInterval).Should(Succeed())

			By("Creating mosquito addon with haIntegration")
			addonYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAddon
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  profile: mosquito
`, addonName, namespace, haName)
			Expect(utils.ApplyYAML(addonYAML, namespace)).To(Succeed())

			By("Verifying mqtt broker is injected into HomeAssistantConfiguration")
			expectedBroker := fmt.Sprintf("%s-%s.%s.svc.cluster.local", haName, addonName, namespace)
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "haconfig", configName, "-n", namespace,
					"-o", "jsonpath={.spec.configuration}")
				g.Expect(out).To(ContainSubstring("mqtt:"))
				g.Expect(out).To(ContainSubstring(expectedBroker))
			}, utils.ReconciliationTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying generated ConfigMap contains mqtt section")
			// configuration.yaml is mounted with subPath, so kubelet does NOT propagate
			// ConfigMap updates to the pod's mounted file. Instead, verify the ConfigMap
			// that the controller generated — this is the source of truth for HA config.
			configMapName := haName + "-configuration"
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "configmap", configMapName, "-n", namespace,
					"-o", "jsonpath={.data.configuration\\.yaml}")
				g.Expect(out).To(ContainSubstring("mqtt:"))
				g.Expect(out).To(ContainSubstring(expectedBroker))
			}, utils.HotReloadTimeout, addonReconcileInterval).Should(Succeed())
		})
	})

	Context("Node-RED + Ingress", Label("slow"), func() {
		It("should create Ingress resource and serve Node-RED UI when pod is ready", func() {
			addonName := "test-nodered-ingress"
			resourceName := haName + "-" + addonName
			ingressHost := "nodered.e2e.test"

			By("Creating HomeAssistant CR")
			Expect(utils.ApplyYAML(addonMinimalHAYAML(haName, namespace), namespace)).To(Succeed())
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "ha", haName, "-n", namespace, "-o", "jsonpath={.metadata.name}")
				g.Expect(out).To(Equal(haName))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Creating Node-RED addon with Ingress enabled")
			addonYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantAddon
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  profile: node-red
  ingress:
    enabled: true
    host: %s
    targetPort: 1880
`, addonName, namespace, haName, ingressHost)
			Expect(utils.ApplyYAML(addonYAML, namespace)).To(Succeed())

			By("Verifying Ingress resource was created")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "ingress", resourceName, "-n", namespace,
					"-o", "jsonpath={.metadata.name}")
				g.Expect(out).To(Equal(resourceName))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying Ingress has correct host")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "ingress", resourceName, "-n", namespace,
					"-o", "jsonpath={.spec.rules[0].host}")
				g.Expect(out).To(Equal(ingressHost))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Verifying Ingress backend points to the addon Service")
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "ingress", resourceName, "-n", namespace,
					"-o", "jsonpath={.spec.rules[0].http.paths[0].backend.service.name}")
				g.Expect(out).To(Equal(resourceName))
			}, utils.ResourceTimeout, addonReconcileInterval).Should(Succeed())

			By("Waiting for Node-RED pod to be Ready (readiness probe: HTTP GET / port 1880)")
			podName := resourceName + "-0"
			Eventually(func(g Gomega) {
				out := utils.Kubectl("get", "pod", podName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type==\"Ready\")].status}")
				g.Expect(out).To(Equal("True"))
			}, utils.HAPodReadyTimeout, addonPodReadyInterval).Should(Succeed())

			By("Verifying Node-RED HTTP server started (via pod logs)")
			// kubectl exec with wget is unreliable in k3d — Kubectl() returns "" on any non-zero
			// exit code, masking the actual error. Instead, check pod logs for the startup message
			// that node-red emits when the HTTP server is ready. The ReadinessProbe (HTTP GET /
			// port 1880) already ensures the server is up before the pod is marked Ready, so by
			// the time we reach this step the log line should already be present.
			Eventually(func(g Gomega) {
				out := utils.Kubectl("logs", podName, "-n", namespace)
				g.Expect(out).To(ContainSubstring("Server now running"))
			}, utils.RestartTimeout, addonReconcileInterval).Should(Succeed())
		})
	})
})

// addonMinimalHAYAML returns a minimal HomeAssistant YAML without bootstrap.
// Used in fast tests that only need the HA CR to exist for addon controller validation.
func addonMinimalHAYAML(haName, namespace string) string {
	return fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
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
}

// collectAddonDebugInfo gathers debug information for addon tests on failure.
func collectAddonDebugInfo(namespace, haName string) {
	writeDebug := func(format string, args ...any) {
		_, _ = fmt.Fprintf(GinkgoWriter, format, args...)
	}

	writeDebug("\n=== ADDON DEBUG INFO ===\n")

	writeDebug("\n--- HomeAssistantAddon Resources ---\n")
	cmd := exec.Command("kubectl", "get", "haaddon", "-n", namespace, "-o", "wide")
	output, err := utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- HomeAssistantAddon Describe ---\n")
	cmd = exec.Command("kubectl", "describe", "haaddon", "-n", namespace)
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- Deployments and StatefulSets ---\n")
	cmd = exec.Command("kubectl", "get", "deployment,statefulset,service,ingress", "-n", namespace, "-o", "wide")
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- Pods ---\n")
	cmd = exec.Command("kubectl", "get", "pods", "-n", namespace, "-o", "wide")
	output, err = utils.Run(cmd)
	if err == nil {
		writeDebug("%s\n", output)
	}

	writeDebug("\n--- HomeAssistantConfiguration (for HAIntegration tests) ---\n")
	configName := haName + "-config"
	cmd = exec.Command("kubectl", "get", "haconfig", configName, "-n", namespace, "-o", "yaml")
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

	writeDebug("\n=== END ADDON DEBUG INFO ===\n")
}
