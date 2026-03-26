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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/przemekhys/homeassistant-operator/test/utils"
)

const (
	infraTestNamespacePrefix = "hainfra-e2e-"
)

var _ = Describe("HomeAssistant Infrastructure E2E (Floor/Label/Area)", Label("infrastructure"), Ordered, func() {
	var namespace string
	var haName string

	BeforeEach(func() {
		namespace = infraTestNamespacePrefix + utils.RandomString(8)
		haName = "test-ha-" + utils.RandomString(6)

		By("Creating test namespace: " + namespace)
		Expect(utils.CreateNamespace(namespace)).To(Succeed())
	})

	AfterEach(func() {
		if CurrentSpecReport().Failed() {
			collectInfraDebugInfo(namespace, haName)
		}

		By("Deleting test namespace: " + namespace)
		if err := utils.DeleteNamespace(namespace); err != nil {
			fmt.Printf("Warning: failed to delete namespace %s: %v\n", namespace, err)
		}
	})

	createHA := func() {
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
	}

	Context("HomeAssistantFloor Lifecycle", Label("fast"), func() {
		It("should create HomeAssistantFloor CR and add finalizer", func() {
			createHA()

			floorName := "test-floor-" + utils.RandomString(6)
			floorYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantFloor
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  name: "Ground Floor"
  level: 0
  icon: "mdi:home-floor-0"
`, floorName, namespace, haName)
			Expect(utils.ApplyYAML(floorYAML, namespace)).To(Succeed())

			By("Verifying finalizer is added")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hafloor", floorName, "-n", namespace,
					"-o", "jsonpath={.metadata.finalizers}")
				g.Expect(output).To(ContainSubstring("ha.homeassistant.io/floor-finalizer"))
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())
		})

		It("should set Ready=False when HA not bootstrapped (no API token)", func() {
			createHA()

			floorName := "test-floor-" + utils.RandomString(6)
			floorYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantFloor
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  name: "First Floor"
`, floorName, namespace, haName)
			Expect(utils.ApplyYAML(floorYAML, namespace)).To(Succeed())

			By("Verifying Ready=False with TokenNotAvailable")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "hafloor", floorName, "-n", namespace,
					"-o", `jsonpath={.status.conditions[?(@.type=="Ready")].reason}`)
				g.Expect(output).To(Equal("TokenNotAvailable"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())
		})
	})

	Context("HomeAssistantLabel Lifecycle", Label("fast"), func() {
		It("should create HomeAssistantLabel CR and add finalizer", func() {
			createHA()

			labelName := "test-label-" + utils.RandomString(6)
			labelYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantLabel
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  name: "Outdoor"
  icon: "mdi:tree"
  color: "green"
`, labelName, namespace, haName)
			Expect(utils.ApplyYAML(labelYAML, namespace)).To(Succeed())

			By("Verifying finalizer is added")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "halabel", labelName, "-n", namespace,
					"-o", "jsonpath={.metadata.finalizers}")
				g.Expect(output).To(ContainSubstring("ha.homeassistant.io/label-finalizer"))
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())
		})

		It("should set Ready=False when HA not bootstrapped (no API token)", func() {
			createHA()

			labelName := "test-label-" + utils.RandomString(6)
			labelYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantLabel
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  name: "Critical"
`, labelName, namespace, haName)
			Expect(utils.ApplyYAML(labelYAML, namespace)).To(Succeed())

			By("Verifying Ready=False with TokenNotAvailable")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "halabel", labelName, "-n", namespace,
					"-o", `jsonpath={.status.conditions[?(@.type=="Ready")].reason}`)
				g.Expect(output).To(Equal("TokenNotAvailable"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())
		})
	})

	Context("HomeAssistantArea Lifecycle", Label("fast"), func() {
		It("should create HomeAssistantArea CR and add finalizer", func() {
			createHA()

			areaName := "test-area-" + utils.RandomString(6)
			areaYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantArea
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  name: "Living Room"
  icon: "mdi:sofa"
`, areaName, namespace, haName)
			Expect(utils.ApplyYAML(areaYAML, namespace)).To(Succeed())

			By("Verifying finalizer is added")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haarea", areaName, "-n", namespace,
					"-o", "jsonpath={.metadata.finalizers}")
				g.Expect(output).To(ContainSubstring("ha.homeassistant.io/area-finalizer"))
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())
		})

		It("should set Ready=False when HA not bootstrapped (no API token)", func() {
			createHA()

			areaName := "test-area-" + utils.RandomString(6)
			areaYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantArea
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: %s
  name: "Kitchen"
`, areaName, namespace, haName)
			Expect(utils.ApplyYAML(areaYAML, namespace)).To(Succeed())

			By("Verifying Ready=False with TokenNotAvailable")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haarea", areaName, "-n", namespace,
					"-o", `jsonpath={.status.conditions[?(@.type=="Ready")].reason}`)
				g.Expect(output).To(Equal("TokenNotAvailable"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())
		})

		It("should set Ready=False when HA ref does not exist", func() {
			areaName := "test-area-" + utils.RandomString(6)
			areaYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1alpha1
kind: HomeAssistantArea
metadata:
  name: %s
  namespace: %s
spec:
  homeAssistantRef:
    name: nonexistent-ha
  name: "Garage"
`, areaName, namespace)
			Expect(utils.ApplyYAML(areaYAML, namespace)).To(Succeed())

			By("Verifying Ready=False with HANotReady")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "haarea", areaName, "-n", namespace,
					"-o", `jsonpath={.status.conditions[?(@.type=="Ready")].reason}`)
				g.Expect(output).To(Equal("HANotReady"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())
		})
	})
})

func collectInfraDebugInfo(namespace, haName string) {
	fmt.Printf("\n=== Infrastructure Debug Info for namespace %s ===\n", namespace)

	fmt.Println("\n--- HomeAssistantFloor resources ---")
	fmt.Println(utils.Kubectl("get", "hafloor", "-n", namespace, "-o", "wide"))

	fmt.Println("\n--- HomeAssistantLabel resources ---")
	fmt.Println(utils.Kubectl("get", "halabel", "-n", namespace, "-o", "wide"))

	fmt.Println("\n--- HomeAssistantArea resources ---")
	fmt.Println(utils.Kubectl("get", "haarea", "-n", namespace, "-o", "wide"))

	fmt.Println("\n--- HomeAssistant status ---")
	fmt.Println(utils.Kubectl("get", "ha", haName, "-n", namespace, "-o", "jsonpath={.status}"))

	fmt.Println("\n--- Recent events ---")
	fmt.Println(utils.Kubectl("get", "events", "-n", namespace, "--sort-by=.lastTimestamp"))
}
