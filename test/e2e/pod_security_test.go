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

// This suite verifies that the operator's own namespace enforces the "restricted"
// Pod Security Standard shipped in config/manager/manager.yaml. It is deliberately
// lightweight: it does not deploy Home Assistant. PSA is a built-in, deterministic
// admission controller, so this only needs to confirm the shipped labels are present
// and that enforcement is actually live (a non-compliant pod is rejected while the
// operator's own restricted securityContext shape is admitted).
//
// Prereq: the operator is already deployed by the suite via `make deploy`, which
// applies the Namespace object (with the PSA labels) onto homeassistant-operator-system.
var _ = Describe("Pod Security Standards E2E", func() {
	const operatorNamespace = "homeassistant-operator-system"

	AfterEach(func() {
		By("Removing probe pods")
		_ = utils.Kubectl("delete", "pod", "pss-probe-violator", "-n", operatorNamespace, "--ignore-not-found")
		_ = utils.Kubectl("delete", "pod", "pss-probe-compliant", "-n", operatorNamespace, "--ignore-not-found")
	})

	It("labels the operator namespace to enforce restricted", func() {
		By("Checking the 6 PSA labels are present on the operator namespace")
		for label, want := range map[string]string{
			"enforce":         "restricted",
			"enforce-version": "latest",
			"audit":           "restricted",
			"audit-version":   "latest",
			"warn":            "restricted",
			"warn-version":    "latest",
		} {
			jsonPath := fmt.Sprintf("jsonpath={.metadata.labels.pod-security\\.kubernetes\\.io/%s}", label)
			got := utils.Kubectl("get", "ns", operatorNamespace, "-o", jsonPath)
			Expect(got).To(Equal(want),
				fmt.Sprintf("namespace label pod-security.kubernetes.io/%s should be %q", label, want))
		}
	})

	It("rejects a non-compliant pod and admits the operator's restricted securityContext", func() {
		By("Attempting to create a privileged pod — expected to be REJECTED by admission")
		violatorYAML := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: pss-probe-violator
  namespace: %s
spec:
  restartPolicy: Never
  containers:
  - name: c
    image: busybox
    command: ["sleep", "3600"]
    securityContext:
      privileged: true
`, operatorNamespace)
		err := utils.ApplyYAML(violatorYAML, operatorNamespace)
		Expect(err).To(HaveOccurred(),
			"a privileged pod must be rejected in a namespace that enforces restricted")
		Expect(err.Error()).To(ContainSubstring("violates PodSecurity"),
			"rejection must come from Pod Security Admission")

		By("Creating a pod with the operator's restricted securityContext — expected to be ADMITTED")
		compliantYAML := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: pss-probe-compliant
  namespace: %s
spec:
  restartPolicy: Never
  securityContext:
    runAsNonRoot: true
    runAsUser: 65532
    seccompProfile:
      type: RuntimeDefault
  containers:
  - name: c
    image: busybox
    command: ["sleep", "3600"]
    securityContext:
      allowPrivilegeEscalation: false
      runAsNonRoot: true
      capabilities:
        drop:
        - ALL
`, operatorNamespace)
		Expect(utils.ApplyYAML(compliantYAML, operatorNamespace)).To(Succeed(),
			"a pod mirroring the operator's restricted securityContext must be admitted")

		By("Waiting for the compliant probe pod to reach Running")
		Eventually(func(g Gomega) {
			g.Expect(utils.Kubectl("get", "pod", "pss-probe-compliant", "-n", operatorNamespace,
				"-o", "jsonpath={.status.phase}")).To(Equal("Running"))
		}, utils.ResourceTimeout, 2*time.Second).Should(Succeed())
	})
})
