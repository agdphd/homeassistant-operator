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

package v1

import (
	"context"
	"crypto/tls"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	schedulingv1 "k8s.io/api/scheduling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

// setupWebhookTestEnv spins up a real envtest API server, a real
// ValidatingWebhookConfiguration, and a real webhook server (the same wiring
// cmd/main.go uses via SetupHomeAssistantWebhookWithManager), then waits for
// the webhook server to accept TLS connections before returning a client
// pointed at it. Callers must invoke the returned cleanup function (e.g. via
// defer) to stop the manager and the envtest environment.
func setupWebhookTestEnv(t *testing.T) (client.Client, func()) {
	t.Helper()
	g := NewWithT(t)
	ctx, cancel := context.WithCancel(context.Background())

	g.Expect(hav1.AddToScheme(scheme.Scheme)).To(Succeed())

	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{filepath.Join("..", "..", "..", "config", "webhook")},
		},
	}

	cfg, err := testEnv.Start()
	g.Expect(err).NotTo(HaveOccurred())

	whOpts := testEnv.WebhookInstallOptions
	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
		WebhookServer: webhook.NewServer(webhook.Options{
			Host:    whOpts.LocalServingHost,
			Port:    whOpts.LocalServingPort,
			CertDir: whOpts.LocalServingCertDir,
		}),
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		LeaderElection:         false,
	})
	g.Expect(err).NotTo(HaveOccurred())

	g.Expect(SetupHomeAssistantWebhookWithManager(mgr)).To(Succeed())

	go func() {
		_ = mgr.Start(ctx)
	}()

	// Standard kubebuilder pattern: wait until the webhook server actually
	// accepts TLS connections before exercising it.
	g.Eventually(func(g Gomega) {
		conn, err := tls.Dial("tcp",
			fmt.Sprintf("%s:%d", whOpts.LocalServingHost, whOpts.LocalServingPort),
			&tls.Config{InsecureSkipVerify: true}) //nolint:gosec // test-only
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(conn.Close()).To(Succeed())
	}, 10*time.Second, 100*time.Millisecond).Should(Succeed())

	k8sClient, err := client.New(cfg, client.Options{Scheme: scheme.Scheme})
	g.Expect(err).NotTo(HaveOccurred())

	cleanup := func() {
		cancel()
		g.Expect(testEnv.Stop()).To(Succeed())
	}
	return k8sClient, cleanup
}

// TestAdmissionWebhookRejectsInvalidNativeTLS exercises the real HTTP admission
// path end to end. The unit tests in homeassistant_webhook_test.go only
// exercise validateHomeAssistantTLS as a pure function and would not catch a
// registration/wiring bug.
func TestAdmissionWebhookRejectsInvalidNativeTLS(t *testing.T) {
	g := NewWithT(t)
	k8sClient, cleanup := setupWebhookTestEnv(t)
	defer cleanup()

	bad := &hav1.HomeAssistant{
		ObjectMeta: metav1.ObjectMeta{Name: "ha-bad", Namespace: "default"},
		Spec: hav1.HomeAssistantSpec{
			Alpha: &hav1.AlphaSpec{
				TLS: &hav1.TLSAlphaSpec{
					Native: &hav1.NativeTLSAlphaSpec{Enabled: true},
				},
			},
		},
	}

	err := k8sClient.Create(context.Background(), bad)
	g.Expect(err).To(HaveOccurred(), "webhook should reject native TLS without issuerRef/secretName")
	g.Expect(err.Error()).To(ContainSubstring("requires issuerRef or secretName"))
}

// TestAdmissionWebhookRejectsInvalidGatewayFilter exercises the real HTTP
// admission path end to end for spec.gateway.filters, the same way
// TestAdmissionWebhookRejectsInvalidNativeTLS does for native TLS. The unit
// tests in homeassistant_webhook_test.go only exercise validateGatewayFilters
// as a pure function and would not catch a registration/wiring bug.
func TestAdmissionWebhookRejectsInvalidGatewayFilter(t *testing.T) {
	g := NewWithT(t)
	k8sClient, cleanup := setupWebhookTestEnv(t)
	defer cleanup()

	// type is a valid enum value (passes the CRD's own OpenAPI schema check),
	// but the required requestHeaderModifier sub-object is missing — only the
	// webhook's own validateGatewayFilters catches this, since the CRD schema
	// treats every sub-object as optional (there is no CEL cross-field rule).
	bad := &hav1.HomeAssistant{
		ObjectMeta: metav1.ObjectMeta{Name: "ha-bad-filter", Namespace: "default"},
		Spec: hav1.HomeAssistantSpec{
			Gateway: &hav1.GatewaySpec{
				Filters: []hav1.HTTPRouteFilter{{Type: "RequestHeaderModifier"}},
			},
		},
	}

	err := k8sClient.Create(context.Background(), bad)
	g.Expect(err).To(HaveOccurred(), "webhook should reject a filter missing its declared type's sub-object")
	g.Expect(err.Error()).To(ContainSubstring("requestHeaderModifier is required"))
}

// TestAdmissionWebhookRejectsNonexistentPriorityClass exercises the real HTTP
// admission path end to end for spec.scheduling.priorityClassName. This is
// the one validateScheduling case that genuinely needs a real API server
// (validateNodeSelector's cases are covered as pure-function unit tests in
// homeassistant_webhook_test.go instead) — it must actually list real
// PriorityClass objects, which only exists here, not in a table test.
func TestAdmissionWebhookRejectsNonexistentPriorityClass(t *testing.T) {
	g := NewWithT(t)
	k8sClient, cleanup := setupWebhookTestEnv(t)
	defer cleanup()

	bad := &hav1.HomeAssistant{
		ObjectMeta: metav1.ObjectMeta{Name: "ha-bad-priorityclass", Namespace: "default"},
		Spec: hav1.HomeAssistantSpec{
			Scheduling: &hav1.SchedulingSpec{PriorityClassName: "does-not-exist"},
		},
	}

	err := k8sClient.Create(context.Background(), bad)
	g.Expect(err).To(HaveOccurred(), "webhook should reject a priorityClassName naming a nonexistent PriorityClass")
	g.Expect(err.Error()).To(ContainSubstring("does-not-exist"))
}

// TestAdmissionWebhookAcceptsExistingPriorityClass is the accepting
// counterpart to TestAdmissionWebhookRejectsNonexistentPriorityClass: a
// priorityClassName naming a real PriorityClass must be admitted.
func TestAdmissionWebhookAcceptsExistingPriorityClass(t *testing.T) {
	g := NewWithT(t)
	k8sClient, cleanup := setupWebhookTestEnv(t)
	defer cleanup()

	pc := &schedulingv1.PriorityClass{
		ObjectMeta: metav1.ObjectMeta{Name: "ha-critical-test"},
		Value:      1000000,
	}
	g.Expect(k8sClient.Create(context.Background(), pc)).To(Succeed())

	good := &hav1.HomeAssistant{
		ObjectMeta: metav1.ObjectMeta{Name: "ha-good-priorityclass", Namespace: "default"},
		Spec: hav1.HomeAssistantSpec{
			Scheduling: &hav1.SchedulingSpec{PriorityClassName: "ha-critical-test"},
		},
	}

	g.Expect(k8sClient.Create(context.Background(), good)).To(Succeed())
}
