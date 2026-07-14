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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/przemekhys/homeassistant-operator/test/utils"
)

// Native TLS E2E. Verifies that the operator provisions a cert-manager
// Certificate for native TLS and reflects
// issuance in the HomeAssistant status. Uses a self-signed ClusterIssuer so the
// certificate is issued quickly and deterministically (no ACME).
var _ = Describe("Native TLS E2E", Label("tls", "native"), func() {
	const clusterIssuer = "ha-e2e-selfsigned"

	var (
		namespace  string
		haName     string
		configName string
	)

	BeforeEach(func() {
		if !utils.IsCertManagerCRDsInstalled() {
			By("Installing cert-manager (not present)")
			Expect(utils.InstallCertManager()).To(Succeed())
		}

		By("Ensuring a self-signed ClusterIssuer exists")
		issuerYAML := fmt.Sprintf(`apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: %s
spec:
  selfSigned: {}
`, clusterIssuer)
		Expect(utils.ApplyYAML(issuerYAML, "")).To(Succeed())

		suffix := utils.RandomString(8)
		namespace = "ha-e2e-tls-native-" + suffix
		haName = "ha-native"
		configName = "ha-native-config"

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
	})

	AfterEach(func() {
		Expect(utils.ForceDeleteNamespace(namespace)).To(Succeed())
	})

	It("issues a native TLS certificate and reports TLSReady", func() {
		By("Creating a HomeAssistant with native TLS enabled")
		haYAML := fmt.Sprintf(`apiVersion: ha.homeassistant.io/v1
kind: HomeAssistant
metadata:
  name: %s
  namespace: %s
spec:
  storage:
    size: 1Gi
  service:
    type: ClusterIP
    port: 8123
  alpha:
    tls:
      native:
        enabled: true
        issuerRef:
          name: %s
          kind: ClusterIssuer
        dnsNames:
          - %s.example.com
  %s`, haName, namespace, clusterIssuer, haName, utils.GetDefaultHAResourceRequests())
		Expect(utils.ApplyYAML(haYAML, namespace)).To(Succeed())

		By("Waiting for the operator to report cert-manager available")
		Eventually(func() string {
			return utils.GetResourceStatus("homeassistant", haName, namespace,
				"{.status.conditions[?(@.type=='CertManagerAvailable')].status}")
		}, utils.CertIssueTimeout, utils.DefaultEventuallyPollingInterval).Should(Equal("True"))

		By("Waiting for the cert-manager Certificate to be created and Ready")
		certName := haName + "-native-tls"
		Eventually(func() string {
			return utils.GetResourceStatus("certificate", certName, namespace,
				"{.status.conditions[?(@.type=='Ready')].status}")
		}, utils.CertIssueTimeout, utils.DefaultEventuallyPollingInterval).Should(Equal("True"))

		By("Waiting for the HomeAssistant to report TLSReady")
		Eventually(func() string {
			return utils.GetResourceStatus("homeassistant", haName, namespace,
				"{.status.conditions[?(@.type=='TLSReady')].status}")
		}, utils.CertIssueTimeout, utils.DefaultEventuallyPollingInterval).Should(Equal("True"))

		By("Verifying the TLS Secret carries certificate material")
		Eventually(func() string {
			return utils.GetResourceStatus("secret", certName, namespace, "{.data.tls\\.crt}")
		}, utils.CertIssueTimeout, utils.DefaultEventuallyPollingInterval).ShouldNot(BeEmpty())
	})
})
