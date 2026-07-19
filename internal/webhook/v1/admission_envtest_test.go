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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

// TestAdmissionWebhookRejectsInvalidNativeTLS exercises the real HTTP admission
// path end to end (real API server + real ValidatingWebhookConfiguration + real
// webhook server), the same wiring cmd/main.go uses via
// SetupHomeAssistantWebhookWithManager. The unit tests in
// homeassistant_webhook_test.go only exercise validateHomeAssistantTLS as a pure
// function and would not catch a registration/wiring bug.
func TestAdmissionWebhookRejectsInvalidNativeTLS(t *testing.T) {
	g := NewWithT(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	g.Expect(hav1.AddToScheme(scheme.Scheme)).To(Succeed())

	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		WebhookInstallOptions: envtest.WebhookInstallOptions{
			Paths: []string{filepath.Join("..", "..", "..", "config", "webhook")},
		},
	}

	cfg, err := testEnv.Start()
	g.Expect(err).NotTo(HaveOccurred())
	defer func() { g.Expect(testEnv.Stop()).To(Succeed()) }()

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

	err = k8sClient.Create(ctx, bad)
	g.Expect(err).To(HaveOccurred(), "webhook should reject native TLS without issuerRef/secretName")
	g.Expect(err.Error()).To(ContainSubstring("requires issuerRef or secretName"))
}
