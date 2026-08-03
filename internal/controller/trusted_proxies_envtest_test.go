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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

// These exercise the full trusted-proxies flow across both reconcilers against a
// real envtest API server: HomeAssistantConfigurationReconciler computes/persists
// TrustedProxiesDefaulted, and HomeAssistantReconciler's ExposureReady condition
// reflects it — proving the cross-CRD status read actually works end to end, not
// just the pure injectTrustedProxies function in isolation.
var _ = Describe("Trusted proxies defaults (envtest)", func() {
	const (
		testNamespace = "default"
		testTimeout   = time.Second * 15
		testInterval  = time.Millisecond * 250
	)

	reconcileBoth := func(name string) {
		configReconciler := &HomeAssistantConfigurationReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
		_, err := configReconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name + "-config", Namespace: testNamespace},
		})
		Expect(err).NotTo(HaveOccurred())

		haReconciler := &HomeAssistantReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: events.NewFakeRecorder(64),
		}
		_, err = haReconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: testNamespace},
		})
		Expect(err).NotTo(HaveOccurred())
	}

	exposureReadyMessage := func(name string) string {
		fresh := &hav1.HomeAssistant{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, fresh)).To(Succeed())
		for i := range fresh.Status.Conditions {
			if fresh.Status.Conditions[i].Type == conditionExposureReady {
				return fresh.Status.Conditions[i].Message
			}
		}
		return ""
	}

	It("applies defaults, persists status, and reflects it in ExposureReady", func() {
		name := "envtest-tp-applied"

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
				Ingress: &hav1.IngressSpec{Enabled: true, Host: "envtest-tp.example.com"},
			},
		}
		Expect(k8sClient.Create(ctx, ha)).To(Succeed())

		Eventually(func(g Gomega) {
			reconcileBoth(name)

			cm := &corev1.ConfigMap{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: name + "-configuration", Namespace: testNamespace,
			}, cm)).To(Succeed())
			g.Expect(cm.Data["configuration.yaml"]).To(ContainSubstring("use_x_forwarded_for: true"))
			g.Expect(cm.Data["configuration.yaml"]).To(ContainSubstring("10.0.0.0/8"))

			updatedConfig := &hav1.HomeAssistantConfiguration{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: name + "-config", Namespace: testNamespace,
			}, updatedConfig)).To(Succeed())
			g.Expect(updatedConfig.Status.TrustedProxiesDefaulted).NotTo(BeNil())
			g.Expect(*updatedConfig.Status.TrustedProxiesDefaulted).To(BeTrue())

			g.Expect(exposureReadyMessage(name)).To(ContainSubstring("default trusted proxies applied"))
		}, testTimeout, testInterval).Should(Succeed())
	})

	It("reflects opt-out in ExposureReady and injects nothing", func() {
		name := "envtest-tp-optout"

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
				Version:                      "2024.1.0",
				Ingress:                      &hav1.IngressSpec{Enabled: true, Host: "envtest-tp-optout.example.com"},
				DisableDefaultTrustedProxies: true,
			},
		}
		Expect(k8sClient.Create(ctx, ha)).To(Succeed())

		Eventually(func(g Gomega) {
			reconcileBoth(name)

			cm := &corev1.ConfigMap{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: name + "-configuration", Namespace: testNamespace,
			}, cm)).To(Succeed())
			g.Expect(cm.Data["configuration.yaml"]).NotTo(ContainSubstring("trusted_proxies"))
			g.Expect(cm.Data["configuration.yaml"]).NotTo(ContainSubstring("use_x_forwarded_for"))

			g.Expect(exposureReadyMessage(name)).To(ContainSubstring("default trusted proxies disabled (opt-out)"))
		}, testTimeout, testInterval).Should(Succeed())
	})

	It("reflects user-managed trusted proxies in ExposureReady when already set", func() {
		name := "envtest-tp-usermanaged"

		haConfig := &hav1.HomeAssistantConfiguration{
			ObjectMeta: metav1.ObjectMeta{Name: name + "-config", Namespace: testNamespace},
			Spec: hav1.HomeAssistantConfigurationSpec{
				HomeAssistantRef: hav1.HomeAssistantReference{Name: name},
				Configuration: "homeassistant:\n  name: Home\n" +
					"http:\n  use_x_forwarded_for: true\n  trusted_proxies:\n    - 203.0.113.0/24\n",
			},
		}
		Expect(k8sClient.Create(ctx, haConfig)).To(Succeed())

		ha := &hav1.HomeAssistant{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
			Spec: hav1.HomeAssistantSpec{
				Version: "2024.1.0",
				Ingress: &hav1.IngressSpec{Enabled: true, Host: "envtest-tp-usermanaged.example.com"},
			},
		}
		Expect(k8sClient.Create(ctx, ha)).To(Succeed())

		Eventually(func(g Gomega) {
			reconcileBoth(name)

			cm := &corev1.ConfigMap{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: name + "-configuration", Namespace: testNamespace,
			}, cm)).To(Succeed())
			g.Expect(cm.Data["configuration.yaml"]).To(ContainSubstring("203.0.113.0/24"))
			g.Expect(cm.Data["configuration.yaml"]).NotTo(ContainSubstring("10.0.0.0/8"))

			updatedConfig := &hav1.HomeAssistantConfiguration{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: name + "-config", Namespace: testNamespace,
			}, updatedConfig)).To(Succeed())
			g.Expect(updatedConfig.Status.TrustedProxiesDefaulted).NotTo(BeNil())
			g.Expect(*updatedConfig.Status.TrustedProxiesDefaulted).To(BeFalse())

			g.Expect(exposureReadyMessage(name)).To(ContainSubstring("using user-configured trusted proxies"))
		}, testTimeout, testInterval).Should(Succeed())
	})

	It("removes previously-applied defaults once exposure is disabled", func() {
		name := "envtest-tp-removal"

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
				Ingress: &hav1.IngressSpec{Enabled: true, Host: "envtest-tp-removal.example.com"},
			},
		}
		Expect(k8sClient.Create(ctx, ha)).To(Succeed())

		By("Phase 1: defaults get applied while exposed")
		Eventually(func(g Gomega) {
			reconcileBoth(name)
			cm := &corev1.ConfigMap{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: name + "-configuration", Namespace: testNamespace,
			}, cm)).To(Succeed())
			g.Expect(cm.Data["configuration.yaml"]).To(ContainSubstring("10.0.0.0/8"))
		}, testTimeout, testInterval).Should(Succeed())

		By("Phase 2: disabling Ingress removes the previously-injected defaults")
		fresh := &hav1.HomeAssistant{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: testNamespace}, fresh)).To(Succeed())
		fresh.Spec.Ingress.Enabled = false
		Expect(k8sClient.Update(ctx, fresh)).To(Succeed())

		Eventually(func(g Gomega) {
			reconcileBoth(name)
			cm := &corev1.ConfigMap{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: name + "-configuration", Namespace: testNamespace,
			}, cm)).To(Succeed())
			g.Expect(cm.Data["configuration.yaml"]).NotTo(ContainSubstring("trusted_proxies"))
			g.Expect(cm.Data["configuration.yaml"]).NotTo(ContainSubstring("use_x_forwarded_for"))

			updatedConfig := &hav1.HomeAssistantConfiguration{}
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name: name + "-config", Namespace: testNamespace,
			}, updatedConfig)).To(Succeed())
			g.Expect(updatedConfig.Status.TrustedProxiesDefaulted).NotTo(BeNil())
			g.Expect(*updatedConfig.Status.TrustedProxiesDefaulted).To(BeFalse())
		}, testTimeout, testInterval).Should(Succeed())
	})
})
