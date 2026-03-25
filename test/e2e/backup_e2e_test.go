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
	backupTestNamespacePrefix = "ha-backup-e2e-"
)

var _ = Describe("Backup E2E", Label("backup"), Ordered, func() {
	var (
		namespace  string
		haName     string
		configName string
	)

	BeforeEach(func() {
		namespace = backupTestNamespacePrefix + utils.RandomString(8)
		haName = "test-ha-" + utils.RandomString(6)
		configName = "test-config-" + utils.RandomString(6)

		By("Creating test namespace: " + namespace)
		Expect(utils.CreateNamespace(namespace)).To(Succeed())
	})

	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Test failed - collecting debug info")
			collectBackupDebugInfo(namespace, haName, configName)
		}

		By("Deleting test namespace: " + namespace)
		if err := utils.DeleteNamespace(namespace); err != nil {
			fmt.Printf("Warning: failed to delete namespace %s: %v\n", namespace, err)
		}
	})

	// BACKUP-FAST-1: CR with backup spec is accepted by the API server
	Context("CR Acceptance", Label("fast", "infra-only"), func() {
		It("should accept HomeAssistant CR with backup spec", func() {
			By("Creating HomeAssistant CR with backup configuration")
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
        name: fake-creds
  backup:
    enabled: true
    recurrence: daily
    time: "03:00:00"
    retentionCopies: 7
    retentionDays: 30
    includeDatabase: true
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

			By("Verifying HA CR was created with backup spec")
			Eventually(func(g Gomega) {
				enabled := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.spec.backup.enabled}")
				g.Expect(enabled).To(Equal("true"))
			}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())

			By("Verifying backup spec fields are stored correctly")
			recurrence := utils.Kubectl("get", "ha", haName, "-n", namespace,
				"-o", "jsonpath={.spec.backup.recurrence}")
			Expect(recurrence).To(Equal("daily"))

			backupTime := utils.Kubectl("get", "ha", haName, "-n", namespace,
				"-o", "jsonpath={.spec.backup.time}")
			Expect(backupTime).To(Equal("03:00:00"))

			retentionCopies := utils.Kubectl("get", "ha", haName, "-n", namespace,
				"-o", "jsonpath={.spec.backup.retentionCopies}")
			Expect(retentionCopies).To(Equal("7"))

			retentionDays := utils.Kubectl("get", "ha", haName, "-n", namespace,
				"-o", "jsonpath={.spec.backup.retentionDays}")
			Expect(retentionDays).To(Equal("30"))

			includeDB := utils.Kubectl("get", "ha", haName, "-n", namespace,
				"-o", "jsonpath={.spec.backup.includeDatabase}")
			Expect(includeDB).To(Equal("true"))

			By("Verifying StatefulSet was created (controller processed the CR)")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.metadata.name}")
				g.Expect(output).To(Equal(haName))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying BackupConfigured condition reflects token not available (no bootstrap Secret)")
			Eventually(func(g Gomega) {
				reason := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='BackupConfigured')].reason}")
				g.Expect(reason).To(Equal("TokenNotAvailable"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())
		})
	})

	// BACKUP-FAST-2: CR with backup disabled has no BackupConfigured condition
	Context("Backup Disabled", Label("fast", "infra-only"), func() {
		It("should not set BackupConfigured condition when backup is disabled", func() {
			By("Creating HomeAssistant CR without backup")
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

			By("Waiting for reconciliation to complete")
			Eventually(func(g Gomega) {
				output := utils.Kubectl("get", "statefulset", haName, "-n", namespace,
					"-o", "jsonpath={.metadata.name}")
				g.Expect(output).To(Equal(haName))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())

			By("Verifying no BackupConfigured condition exists")
			Consistently(func(g Gomega) {
				condType := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='BackupConfigured')].type}")
				g.Expect(condType).To(BeEmpty())
			}, 10*time.Second, 2*time.Second).Should(Succeed())
		})
	})

	// BACKUP-SLOW-1: Full bootstrap + backup configuration
	Context("Full Backup Configuration", Label("slow", "bootstrap", "pod-required"), func() {
		It("should configure automatic backup in HA after bootstrap", func() {
			credentialsSecretName := "backup-creds-" + utils.RandomString(6)

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

			By("Creating HomeAssistant CR with bootstrap and backup enabled")
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
  backup:
    enabled: true
    recurrence: daily
    time: "04:00:00"
    retentionCopies: 3
    includeDatabase: true
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

			By("Waiting for BackupConfigured condition to be True")
			Eventually(func(g Gomega) {
				status := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='BackupConfigured')].status}")
				g.Expect(status).To(Equal("True"))

				reason := utils.Kubectl("get", "ha", haName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='BackupConfigured')].reason}")
				g.Expect(reason).To(Equal("BackupConfigured"))
			}, utils.HotReloadTimeout, 5*time.Second).Should(Succeed())

			By("Verifying BackupConfigured event was emitted")
			Eventually(func(g Gomega) {
				events := utils.Kubectl("get", "events", "-n", namespace,
					"--field-selector", fmt.Sprintf("involvedObject.name=%s,reason=BackupConfigured", haName),
					"-o", "jsonpath={.items[0].reason}")
				g.Expect(events).To(Equal("BackupConfigured"))
			}, utils.ReconciliationTimeout, 2*time.Second).Should(Succeed())
		})
	})
})

// collectBackupDebugInfo collects debug information when a backup test fails
func collectBackupDebugInfo(namespace, haName, configName string) {
	writeDebug := func(format string, args ...any) {
		_, _ = fmt.Fprintf(GinkgoWriter, format, args...)
	}

	writeDebug("\n=== DEBUG INFO: Backup Test Failed ===\n")

	// HomeAssistant CR
	writeDebug("\n--- HomeAssistant status ---\n")
	statusCmd := exec.Command("kubectl", "get", "ha", haName, "-n", namespace, "-o", "yaml")
	output, err := utils.Run(statusCmd)
	if err == nil {
		writeDebug("%s\n", output)
	} else {
		writeDebug("Error: %v\n", err)
	}

	// HomeAssistantConfiguration
	writeDebug("\n--- HomeAssistantConfiguration ---\n")
	configCmd := exec.Command("kubectl", "describe", "haconfig", configName, "-n", namespace)
	output, err = utils.Run(configCmd)
	if err == nil {
		writeDebug("%s\n", output)
	} else {
		writeDebug("Error: %v\n", err)
	}

	// Pods
	writeDebug("\n--- Pods ---\n")
	podsCmd := exec.Command("kubectl", "get", "pods", "-n", namespace, "-o", "wide")
	output, err = utils.Run(podsCmd)
	if err == nil {
		writeDebug("%s\n", output)
	} else {
		writeDebug("Error: %v\n", err)
	}

	// Pod logs
	writeDebug("\n--- HA Pod logs (last 50 lines) ---\n")
	podName := haName + "-0"
	podLogsCmd := exec.Command("kubectl", "logs", podName, "-n", namespace, "--tail=50")
	output, err = utils.Run(podLogsCmd)
	if err == nil {
		writeDebug("%s\n", output)
	} else {
		writeDebug("Error: %v\n", err)
	}

	// Events
	writeDebug("\n--- Events ---\n")
	eventsCmd := exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
	output, err = utils.Run(eventsCmd)
	if err == nil {
		writeDebug("%s\n", output)
	} else {
		writeDebug("Error: %v\n", err)
	}

	// Operator logs
	writeDebug("\n--- Operator logs (last 50 lines) ---\n")
	logsCmd := exec.Command("kubectl", "logs", "-n", "homeassistant-operator-system",
		"-l", "control-plane=controller-manager", "--tail=50")
	output, err = utils.Run(logsCmd)
	if err == nil {
		writeDebug("%s\n", output)
	} else {
		writeDebug("Error: %v\n", err)
	}

	writeDebug("\n=== END DEBUG INFO ===\n")
}
