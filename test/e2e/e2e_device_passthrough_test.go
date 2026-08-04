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

// This suite runs in its own job/cluster (rather than reusing critical-path's
// shared HA instance) so it can be split across CI jobs independently — see
// docs/development/testing.md. It intentionally skips spec.bootstrap: the pod
// only needs to reach Ready (readiness probe just checks HTTP 200 on "/",
// which HA serves before onboarding) for every assertion here, following the
// same no-bootstrap pattern as network_policy_test.go.
//
// Labeled "group-b" rather than a one-off job name: unlike
// community-repository-a/b (which share one BeforeAll because both groups'
// setup cost is similar), this group deliberately does NOT share critical-path
// group-a's Ordered/BeforeAll block, since group-a's real-onboarding bootstrap
// is expensive and this suite doesn't need it at all. Future specs that also
// don't need a fully-onboarded instance belong here (new file, same
// "critical-path"+"group-b" labels); specs that do belong in group-a's file
// instead, reusing its shared bootstrap.
var _ = Describe("Device Passthrough E2E", Label("critical-path", "group-b"), func() {
	var (
		namespace  string
		haName     string
		configName string
	)

	BeforeEach(func() {
		suffix := utils.RandomString(8)
		namespace = "ha-e2e-critical-b-" + suffix
		haName = "ha-devices"
		configName = "ha-devices-config"

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
  %s
`, haName, namespace, haVersion(), utils.GetEnhancedHAResourceRequests())
		Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

		By("Waiting for the HA pod to be Ready (web server listening on 8123)")
		Eventually(func(g Gomega) {
			status := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			g.Expect(status).To(Equal("True"))
		}, utils.HAPodReadyTimeout, 10*time.Second).Should(Succeed())
	})

	AfterEach(func() {
		if CurrentSpecReport().Failed() {
			By("Collecting debug info for namespace: " + namespace)
			_, _ = fmt.Fprintf(GinkgoWriter, "\n--- HomeAssistant CR ---\n%s\n",
				utils.Kubectl("get", "ha", haName, "-n", namespace, "-o", "yaml"))
			_, _ = fmt.Fprintf(GinkgoWriter, "\n--- Pods ---\n%s\n",
				utils.Kubectl("get", "pods", "-n", namespace, "-o", "wide"))
		}
		By("Deleting test namespace: " + namespace)
		if err := utils.DeleteNamespace(namespace); err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Warning: failed to delete namespace %s: %v\n", namespace, err)
		}
	})

	// Real hardware cannot be tested in CI, so /dev/null and /dev/zero
	// (present on every Linux node) stand in for a real Zigbee/Z-Wave
	// coordinator.
	It("Device passthrough (spec.alpha.devices) — mount, multi-device, and missing-device diagnostics", func() {
		By("Verifying the StatefulSet is reachable and has no device volume by default")
		Expect(utils.Kubectl("get", "statefulset", haName, "-n", namespace,
			"-o", "jsonpath={.metadata.name}")).To(Equal(haName), "sanity check that the get command itself succeeded")
		Expect(utils.Kubectl("get", "statefulset", haName, "-n", namespace,
			"-o", "jsonpath={.spec.template.spec.volumes[?(@.name=='device-0')].name}")).To(BeEmpty())

		By("Recording the pre-patch pod UID to detect the restart")
		originalUID := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
		Expect(originalUID).NotTo(BeEmpty())

		By("Declaring two devices: /dev/null and /dev/zero")
		Expect(utils.PatchResource("homeassistants", haName, namespace, "merge",
			`{"spec":{"alpha":{"devices":[{"hostPath":"/dev/null"},{"hostPath":"/dev/zero"}]}}}`)).To(Succeed())

		By("Waiting for the pod to restart onto the new template and become Ready with both devices mounted")
		Eventually(func(g Gomega) {
			uid := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
			g.Expect(uid).NotTo(BeEmpty())
			g.Expect(uid).NotTo(Equal(originalUID), "pod must have been recreated onto the new template")

			deviceVol := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
				"-o", "jsonpath={.spec.volumes[?(@.name=='device-0')].name}")
			g.Expect(deviceVol).To(Equal("device-0"), "new pod template must carry the declared device volume")

			phase := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.status.phase}")
			g.Expect(phase).To(Equal("Running"))
			ready := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			g.Expect(ready).To(Equal("True"))
		}, utils.RestartTimeout, reconcileInterval).Should(Succeed())

		By("Verifying the container is never privileged")
		privileged := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
			"-o", `jsonpath={.spec.containers[?(@.name=="home-assistant")].securityContext.privileged}`)
		Expect(privileged).To(Equal("false"), "spec.alpha.devices must set privileged:false explicitly, never leave it unset")

		By("Verifying both devices are present inside the container")
		cmd := exec.Command("kubectl", "exec", "-n", namespace, haName+"-0", "-c", "home-assistant",
			"--", "sh", "-c", "test -c /dev/null && test -c /dev/zero")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "/dev/null and /dev/zero should both be character devices inside the container")

		By("Declaring a device path that does not exist on this node")
		Expect(utils.PatchResource("homeassistants", haName, namespace, "merge",
			`{"spec":{"alpha":{"devices":[{"hostPath":"/dev/does-not-exist-e2e-0"}]}}}`)).To(Succeed())

		By("Verifying DevicesReady=False names the missing path")
		Eventually(func(g Gomega) {
			status := utils.Kubectl("get", "ha", haName, "-n", namespace,
				"-o", `jsonpath={.status.conditions[?(@.type=="DevicesReady")].status}`)
			g.Expect(status).To(Equal("False"))
			message := utils.Kubectl("get", "ha", haName, "-n", namespace,
				"-o", `jsonpath={.status.conditions[?(@.type=="DevicesReady")].message}`)
			g.Expect(message).To(ContainSubstring("/dev/does-not-exist-e2e-0"))
		}, utils.RestartTimeout, reconcileInterval).Should(Succeed())
	})
})
