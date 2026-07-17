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

package controller

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

// These exercise reconcileExposure/updateExposureReady against a real envtest API
// server (real optimistic concurrency, real status subresource semantics) rather
// than the fake client used elsewhere — closing the coverage gap noted for the
// exposure/TLS reconcile paths.
var _ = Describe("Exposure reconciliation (envtest)", func() {
	const (
		testNamespace = "default"
		testTimeout   = time.Second * 15
		testInterval  = time.Millisecond * 250
	)

	It("sets ExposureReady=True on a real apiserver for BYO-secret Ingress", func() {
		name := "envtest-expose-byo"

		haConfig := &hav1.HomeAssistantConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-config", Namespace: testNamespace},
			Spec: hav1.HomeAssistantConfigurationSpec{
				HomeAssistantRef: hav1.HomeAssistantReference{Name: name},
				Configuration:    "homeassistant:\n  name: Home\n",
			},
		}
		Expect(k8sClient.Create(ctx, haConfig)).To(Succeed())

		ha := &hav1.HomeAssistant{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
			Spec: hav1.HomeAssistantSpec{
				Version: "2024.1.0",
				Ingress: &hav1.IngressSpec{
					Enabled: true,
					Host:    "envtest.example.com",
					TLS: &hav1.IngressTLSSpec{
						Enabled:    true,
						SecretName: "envtest-byo-tls", // bring-your-own: no cert-manager needed
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, ha)).To(Succeed())

		reconciler := &HomeAssistantReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: events.NewFakeRecorder(64),
		}

		By("Reconciling once")
		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: testNamespace},
		})
		Expect(err).NotTo(HaveOccurred())

		By("Reconciling repeatedly (mimicking real requeues) and checking ExposureReady")
		Eventually(func(g Gomega) {
			// Re-reconcile each poll to mimic the real controller loop retrying.
			_, rerr := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: name, Namespace: testNamespace},
			})
			g.Expect(rerr).NotTo(HaveOccurred())

			fresh := &hav1.HomeAssistant{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, fresh)).To(Succeed())
			var found *metav1.Condition
			for i := range fresh.Status.Conditions {
				if fresh.Status.Conditions[i].Type == conditionExposureReady {
					found = &fresh.Status.Conditions[i]
					break
				}
			}
			g.Expect(found).NotTo(BeNil(), "ExposureReady condition never appeared; conditions=%+v", fresh.Status.Conditions)
			g.Expect(found.Status).To(Equal(metav1.ConditionTrue))
		}, testTimeout, testInterval).Should(Succeed())
	})

	It("sets ExposureReady=True with a cert-manager issuerRef (real Certificate CRD)", func() {
		By("Installing a minimal Certificate CRD (mimics cert-manager)")
		crds, err := envtest.InstallCRDs(cfg, envtest.CRDInstallOptions{
			Paths: []string{"testdata/certificate_crd.yaml"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(crds).To(HaveLen(1))

		name := "envtest-expose-issuer"

		haConfig := &hav1.HomeAssistantConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-config", Namespace: testNamespace},
			Spec: hav1.HomeAssistantConfigurationSpec{
				HomeAssistantRef: hav1.HomeAssistantReference{Name: name},
				Configuration:    "homeassistant:\n  name: Home\n",
			},
		}
		Expect(k8sClient.Create(ctx, haConfig)).To(Succeed())

		ha := &hav1.HomeAssistant{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
			Spec: hav1.HomeAssistantSpec{
				Version: "2024.1.0",
				Ingress: &hav1.IngressSpec{
					Enabled: true,
					Host:    "envtest-issuer.example.com",
					TLS: &hav1.IngressTLSSpec{
						Enabled:   true,
						IssuerRef: &hav1.IssuerReference{Name: "envtest-issuer", Kind: "ClusterIssuer"},
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, ha)).To(Succeed())

		reconciler := &HomeAssistantReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: events.NewFakeRecorder(64),
		}

		By("Reconciling once")
		_, err = reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: testNamespace},
		})
		Expect(err).NotTo(HaveOccurred())

		By("Verifying the Certificate object was created")
		cert := &unstructured.Unstructured{}
		cert.SetGroupVersionKind(certificateGVK)
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{Name: name + "-ingress-tls", Namespace: testNamespace}, cert)
		}, testTimeout, testInterval).Should(Succeed())

		By("Reconciling repeatedly (mimicking real requeues) and checking ExposureReady")
		Eventually(func(g Gomega) {
			_, rerr := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: name, Namespace: testNamespace},
			})
			g.Expect(rerr).NotTo(HaveOccurred())

			fresh := &hav1.HomeAssistant{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, fresh)).To(Succeed())
			var found *metav1.Condition
			for i := range fresh.Status.Conditions {
				if fresh.Status.Conditions[i].Type == conditionExposureReady {
					found = &fresh.Status.Conditions[i]
					break
				}
			}
			g.Expect(found).NotTo(BeNil(), "ExposureReady condition never appeared; conditions=%+v", fresh.Status.Conditions)
			g.Expect(found.Status).To(Equal(metav1.ConditionTrue))
		}, testTimeout, testInterval).Should(Succeed())
	})
})
