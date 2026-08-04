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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/przemekhys/homeassistant-operator/test/utils"
)

// This suite needs a real multi-node cluster (k3d --agents 1, unlike every
// other e2e job's --agents 0) — proving a node selector/toleration actually
// includes or excludes the pod requires an alternative node it must (or must
// not) land on, which a single-node cluster cannot provide. Follows
// e2e_device_passthrough_test.go's no-shared-bootstrap pattern: the pod only
// needs to reach Ready, no onboarding is exercised here.
//
// Manual validation against a real k3d cluster caught an important
// constraint this design works around: k3d's default StorageClass
// ("local-path", WaitForFirstConsumer) binds the PVC's PV to whichever node
// the pod *first* lands on — permanently, for that PVC's lifetime. A
// resource already Ready cannot be relocated to a *different* node via
// nodeSelector/affinity/toleration after the fact (the scheduler correctly
// refuses: "didn't match PersistentVolume's node affinity"). So every
// scenario below that operates on the long-lived `haName` instance
// constrains it to the node it is *already* bound to (`boundNode`) rather
// than trying to move it — this is not a limitation of spec.scheduling
// itself, just of this suite's storage backend. Proving genuine placement
// *choice* between nodes (a preferred affinity rule) instead uses a second,
// freshly-created instance with affinity set at creation time, so
// WaitForFirstConsumer binds its PV wherever that preference resolves.
var _ = Describe("Pod Scheduling Controls E2E", Label("scheduling"), func() {
	var (
		namespace  string
		haName     string
		configName string
		boundNode  string
		otherNode  string
	)

	const nodeLabelKey = "ha-e2e-scheduling"

	BeforeEach(func() {
		suffix := utils.RandomString(8)
		namespace = "ha-e2e-scheduling-" + suffix
		haName = "ha-scheduling"
		configName = "ha-scheduling-config"

		By("Discovering two schedulable nodes")
		nodeNames := strings.Fields(utils.Kubectl("get", "nodes", "-o", "jsonpath={.items[*].metadata.name}"))
		Expect(len(nodeNames)).To(BeNumerically(">=", 2),
			"this suite requires a multi-node cluster (k3d cluster create --agents 1 or more)")

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

		By("Recording the node this instance's PV is now permanently bound to")
		boundNode = utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.spec.nodeName}")
		Expect(boundNode).NotTo(BeEmpty())
		for _, n := range nodeNames {
			if n != boundNode {
				otherNode = n
				break
			}
		}
		Expect(otherNode).NotTo(BeEmpty())
	})

	AfterEach(func() {
		if CurrentSpecReport().Failed() {
			By("Collecting debug info for namespace: " + namespace)
			_, _ = fmt.Fprintf(GinkgoWriter, "\n--- HomeAssistant CRs ---\n%s\n",
				utils.Kubectl("get", "ha", "-n", namespace, "-o", "yaml"))
			_, _ = fmt.Fprintf(GinkgoWriter, "\n--- Pods ---\n%s\n",
				utils.Kubectl("get", "pods", "-n", namespace, "-o", "wide"))
			_, _ = fmt.Fprintf(GinkgoWriter, "\n--- Nodes ---\n%s\n",
				utils.Kubectl("get", "nodes", "--show-labels"))
		}

		By("Removing node label/taint added for this spec")
		_ = utils.Kubectl("label", "node", boundNode, nodeLabelKey+"-")
		_ = utils.Kubectl("label", "node", otherNode, nodeLabelKey+"-")
		_ = utils.Kubectl("taint", "node", boundNode, "ha-dedicated:NoSchedule-")

		By("Deleting test namespace: " + namespace)
		if err := utils.DeleteNamespace(namespace); err != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Warning: failed to delete namespace %s: %v\n", namespace, err)
		}
	})

	It("Pod scheduling controls (spec.scheduling) — nodeSelector, unschedulable "+
		"diagnostics, tolerations, and preferred affinity", func() {
		By("(a) Verifying no scheduling fields are set by default")
		Expect(utils.Kubectl("get", "statefulset", haName, "-n", namespace,
			"-o", "jsonpath={.metadata.name}")).To(Equal(haName), "sanity check that the get command itself succeeded")
		for _, jp := range []string{
			"{.spec.template.spec.nodeSelector}",
			"{.spec.template.spec.affinity}",
			"{.spec.template.spec.tolerations}",
			"{.spec.template.spec.priorityClassName}",
		} {
			Expect(utils.Kubectl("get", "statefulset", haName, "-n", namespace, "-o", jp)).To(BeEmpty())
		}

		By("(b) Labeling the pod's own bound node (" + boundNode +
			") and pinning it there via nodeSelector")
		Expect(utils.Kubectl("label", "node", boundNode, nodeLabelKey+"="+boundNode, "--overwrite")).NotTo(BeEmpty())

		originalUID := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
		Expect(originalUID).NotTo(BeEmpty())

		Expect(utils.PatchResource("homeassistants", haName, namespace, "merge",
			fmt.Sprintf(`{"spec":{"scheduling":{"nodeSelector":{"%s":"%s"}}}}`, nodeLabelKey, boundNode))).To(Succeed())

		Eventually(func(g Gomega) {
			uid := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.metadata.uid}")
			g.Expect(uid).NotTo(BeEmpty())
			g.Expect(uid).NotTo(Equal(originalUID),
				"pod must be recreated automatically when spec.scheduling changes, no manual intervention")

			nodeName := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.spec.nodeName}")
			g.Expect(nodeName).To(Equal(boundNode))

			ready := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			g.Expect(ready).To(Equal("True"))
		}, utils.RestartTimeout, reconcileInterval).Should(Succeed())

		By("(c) Patching nodeSelector to match no node — SchedulingReady=False names the reason")
		Expect(utils.PatchResource("homeassistants", haName, namespace, "merge",
			fmt.Sprintf(`{"spec":{"scheduling":{"nodeSelector":{"%s":"does-not-exist"}}}}`, nodeLabelKey))).To(Succeed())

		Eventually(func(g Gomega) {
			status := utils.Kubectl("get", "ha", haName, "-n", namespace,
				"-o", `jsonpath={.status.conditions[?(@.type=="SchedulingReady")].status}`)
			g.Expect(status).To(Equal("False"))
			reason := utils.Kubectl("get", "ha", haName, "-n", namespace,
				"-o", `jsonpath={.status.conditions[?(@.type=="SchedulingReady")].reason}`)
			g.Expect(reason).To(Equal("Unschedulable"))
		}, utils.RestartTimeout, reconcileInterval).Should(Succeed())

		By("(d) Tainting the bound node — nodeSelector+toleration together let the pod stay scheduled there; " +
			"an untolerating probe pod targeting the same node does not")
		Expect(utils.Kubectl("taint", "node", boundNode, "ha-dedicated=true:NoSchedule", "--overwrite")).NotTo(BeEmpty())

		Expect(utils.PatchResource("homeassistants", haName, namespace, "merge", fmt.Sprintf(
			`{"spec":{"scheduling":{"nodeSelector":{"%s":"%s"},`+
				`"tolerations":[{"key":"ha-dedicated","operator":"Equal","value":"true","effect":"NoSchedule"}]}}}`,
			nodeLabelKey, boundNode))).To(Succeed())

		Eventually(func(g Gomega) {
			nodeName := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace, "-o", "jsonpath={.spec.nodeName}")
			g.Expect(nodeName).To(Equal(boundNode))
			ready := utils.Kubectl("get", "pod", haName+"-0", "-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			g.Expect(ready).To(Equal("True"))
		}, utils.RestartTimeout, reconcileInterval).Should(Succeed())

		probeYAML := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: probe-no-toleration
  namespace: %s
spec:
  nodeSelector:
    %s: %s
  containers:
    - name: probe
      image: busybox
      command: ["sleep", "3600"]
`, namespace, nodeLabelKey, boundNode)
		Expect(utils.ApplyYAML(probeYAML, namespace)).To(Succeed())
		Consistently(func(g Gomega) {
			phase := utils.Kubectl("get", "pod", "probe-no-toleration", "-n", namespace, "-o", "jsonpath={.status.phase}")
			g.Expect(phase).To(Equal("Pending"),
				"a pod without the taint's toleration must never schedule onto the dedicated node")
		}, 15*time.Second, 3*time.Second).Should(Succeed())

		By("(e) A fresh instance with preferred node affinity set at creation lands on the favored node " +
			"— a second instance, not the long-lived one above, since this needs a genuine choice between " +
			"nodes that an already-PV-bound instance cannot offer (see suite comment)")
		preferredConfigName := "ha-scheduling-preferred-config"
		preferredHAName := "ha-scheduling-preferred"
		Expect(utils.Kubectl("label", "node", otherNode, nodeLabelKey+"="+otherNode, "--overwrite")).NotTo(BeEmpty())

		preferredConfigYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1
kind: HomeAssistantConfiguration
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  configuration: |
    default_config:
`, preferredConfigName, namespace, preferredHAName)
		Expect(utils.ApplyYAML(preferredConfigYAML, namespace)).To(Succeed())

		preferredHAYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  version: "%s"
  storage:
    size: "1Gi"
  scheduling:
    affinity:
      nodeAffinity:
        preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 1
            preference:
              matchExpressions:
                - key: %s
                  operator: In
                  values: ["%s"]
  %s
`, preferredHAName, namespace, haVersion(), nodeLabelKey, otherNode, utils.GetEnhancedHAResourceRequests())
		Expect(utils.ApplyYAML(preferredHAYAML, namespace)).To(Succeed())

		Eventually(func(g Gomega) {
			nodeName := utils.Kubectl("get", "pod", preferredHAName+"-0", "-n", namespace, "-o", "jsonpath={.spec.nodeName}")
			g.Expect(nodeName).To(Equal(otherNode), "a preferred rule must be honored when the favored node is available")
			ready := utils.Kubectl("get", "pod", preferredHAName+"-0", "-n", namespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			g.Expect(ready).To(Equal("True"))
		}, utils.HAPodReadyTimeout, 10*time.Second).Should(Succeed())
	})
})
