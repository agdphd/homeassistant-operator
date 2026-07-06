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

// This suite verifies that spec.alpha.networkPolicy.enabled actually restricts
// traffic to the Home Assistant pod, not just that the NetworkPolicy object
// exists. k3d/k3s's default CNI enforces NetworkPolicy in this project's CI.
var _ = Describe("NetworkPolicy E2E", func() {
	var (
		namespace      string
		probeNamespace string
		haName         string
		configName     string
	)

	BeforeEach(func() {
		suffix := utils.RandomString(8)
		namespace = "ha-e2e-netpol-" + suffix
		probeNamespace = "ha-e2e-netpol-probe-" + suffix
		haName = "ha-netpol"
		configName = "ha-netpol-config"

		By("Creating test namespace: " + namespace)
		Expect(utils.CreateNamespace(namespace)).To(Succeed())

		By("Creating HomeAssistantConfiguration")
		configYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1
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

		By("Creating HomeAssistant CR (no bootstrap needed for this test)")
		haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "%s"
  storage:
    size: "1Gi"
`, haName, namespace, haVersion())
		Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

		By("Waiting for the HA pod to be Ready (web server listening on 8123)")
		Eventually(func(g Gomega) {
			status := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			g.Expect(status).To(Equal("True"))
		}, utils.HAPodReadyTimeout, 10*time.Second).Should(Succeed())
	})

	AfterEach(func() {
		By("Deleting probe namespace: " + probeNamespace)
		_ = utils.DeleteNamespace(probeNamespace)
		By("Deleting test namespace: " + namespace)
		Expect(utils.DeleteNamespace(namespace)).To(Succeed())
	})

	It("blocks traffic from an unrelated namespace and allows same-namespace traffic once enabled", func() {
		By("Verifying no NetworkPolicy exists by default")
		Expect(utils.Kubectl("get", "networkpolicy", haName, "-n", namespace, "--ignore-not-found")).To(BeEmpty())

		By("Enabling spec.alpha.networkPolicy.enabled")
		Expect(utils.PatchResource("homeassistants", haName, namespace, "merge",
			`{"spec":{"alpha":{"networkPolicy":{"enabled":true}}}}`)).To(Succeed())

		By("Waiting for the operator to create the NetworkPolicy")
		Eventually(func(g Gomega) {
			g.Expect(utils.Kubectl("get", "networkpolicy", haName, "-n", namespace, "--ignore-not-found")).NotTo(BeEmpty())
		}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

		haClusterIP := utils.Kubectl("get", "svc", haName, "-n", namespace, "-o", "jsonpath={.spec.clusterIP}")
		Expect(haClusterIP).NotTo(BeEmpty())
		haURL := fmt.Sprintf("http://%s:8123", haClusterIP)

		By("Creating a probe pod in an unrelated namespace — expected to be BLOCKED")
		Expect(utils.CreateNamespace(probeNamespace)).To(Succeed())
		runCmd := exec.Command("kubectl", "run", "probe-blocked", "-n", probeNamespace,
			"--image=busybox", "--restart=Never", "--command", "--", "sleep", "3600")
		_, err := utils.Run(runCmd)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			g.Expect(utils.Kubectl("get", "pod", "probe-blocked", "-n", probeNamespace,
				"-o", "jsonpath={.status.phase}")).To(Equal("Running"))
		}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

		// NetworkPolicy object existing does not mean the CNI has already
		// programmed the dataplane — retry until enforcement is actually active.
		Eventually(func(g Gomega) {
			blockedCmd := exec.Command("kubectl", "exec", "-n", probeNamespace, "probe-blocked", "--",
				"wget", "-q", "-T", "3", "-O-", haURL)
			_, blockedErr := utils.Run(blockedCmd)
			g.Expect(blockedErr).To(HaveOccurred(),
				"a pod in an unrelated namespace should NOT reach the HA Service once the NetworkPolicy is enabled")
		}, utils.ResourceTimeout, 5*time.Second).Should(Succeed())

		By("Creating a probe pod in the same namespace as HA — expected to be ALLOWED")
		runCmd = exec.Command("kubectl", "run", "probe-allowed", "-n", namespace,
			"--image=busybox", "--restart=Never", "--command", "--", "sleep", "3600")
		_, err = utils.Run(runCmd)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			g.Expect(utils.Kubectl("get", "pod", "probe-allowed", "-n", namespace,
				"-o", "jsonpath={.status.phase}")).To(Equal("Running"))
		}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

		allowedCmd := exec.Command("kubectl", "exec", "-n", namespace, "probe-allowed", "--",
			"wget", "-q", "-T", "5", "-O-", haURL)
		_, allowedErr := utils.Run(allowedCmd)
		Expect(allowedErr).NotTo(HaveOccurred(), "a pod in the same namespace as HA should still reach it")

		By("Disabling spec.alpha.networkPolicy.enabled")
		Expect(utils.PatchResource("homeassistants", haName, namespace, "merge",
			`{"spec":{"alpha":{"networkPolicy":{"enabled":false}}}}`)).To(Succeed())

		By("Verifying the NetworkPolicy is removed")
		Eventually(func(g Gomega) {
			g.Expect(utils.Kubectl("get", "networkpolicy", haName, "-n", namespace, "--ignore-not-found")).To(BeEmpty())
		}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())
	})
})
