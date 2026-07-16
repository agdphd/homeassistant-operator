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
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/przemekhys/homeassistant-operator/test/utils"
)

// Webhook TLS E2E. Verifies the validating webhook is serving (over its TLS
// certificate) and enforces TLS
// configuration coherence: an incoherent CR is rejected at admission, a coherent
// one is accepted. Requires the operator to be deployed with the webhook enabled.
var _ = Describe("Webhook TLS E2E", Label("tls", "webhook"), func() {
	var namespace string

	BeforeEach(func() {
		if !validatingWebhookInstalled() {
			Skip("validating webhook not deployed")
		}
		namespace = "ha-e2e-tls-webhook-" + utils.RandomString(8)
		Expect(utils.CreateNamespace(namespace)).To(Succeed())
	})

	AfterEach(func() {
		if namespace != "" {
			Expect(utils.ForceDeleteNamespace(namespace)).To(Succeed())
		}
	})

	It("rejects a HomeAssistant with incoherent TLS config", func() {
		By("Applying native TLS enabled without issuerRef or secretName")
		bad := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1
kind: HomeAssistant
metadata:
  name: ha-bad
  namespace: %s
spec:
  alpha:
    tls:
      native:
        enabled: true
`, namespace)
		// failurePolicy is Ignore, so rejection only occurs once the webhook is
		// actually serving — retry until it rejects (the CR is never created).
		Eventually(func() error {
			return utils.ApplyYAML(bad, namespace)
		}, utils.CertIssueTimeout, utils.DefaultEventuallyPollingInterval).Should(HaveOccurred(),
			"webhook should reject native TLS without issuerRef/secretName")
	})

	It("accepts a coherent HomeAssistant", func() {
		good := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1
kind: HomeAssistant
metadata:
  name: ha-good
  namespace: %s
spec:
  alpha:
    tls:
      native:
        enabled: true
        issuerRef:
          name: some-issuer
          kind: ClusterIssuer
`, namespace)
		Expect(utils.ApplyYAML(good, namespace)).To(Succeed())
	})
})

// validatingWebhookInstalled reports whether the operator's ValidatingWebhookConfiguration
// is present on the cluster.
func validatingWebhookInstalled() bool {
	// Request resource names so "No resources found" is never mistaken for a hit.
	cmd := exec.Command("kubectl", "get", "validatingwebhookconfiguration",
		"-l", "app.kubernetes.io/name=homeassistant-operator", "-o", "name")
	out, err := utils.Run(cmd)
	if err == nil && strings.Contains(out, "validating-webhook-configuration") {
		return true
	}
	// Fall back to matching by name suffix across all VWCs (labels may differ).
	cmd = exec.Command("kubectl", "get", "validatingwebhookconfiguration", "-o", "name")
	out, err = utils.Run(cmd)
	return err == nil && strings.Contains(out, "validating-webhook-configuration")
}
