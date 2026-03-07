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
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hav1alpha1 "github.com/przemekhys/homeassistant-operator/api/v1alpha1"
)

var _ = Describe("HomeAssistantAddon Controller", func() {
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	var (
		testCtx    context.Context
		ns         string
		reconciler *HomeAssistantAddonReconciler
	)

	// reconcileAddon drives one reconciliation cycle for an addon by name.
	reconcileAddon := func(name string) (reconcile.Result, error) {
		return reconciler.Reconcile(testCtx, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: ns},
		})
	}

	// newHA is a minimal HomeAssistant CR.
	newHA := func(name string) *hav1alpha1.HomeAssistant {
		return &hav1alpha1.HomeAssistant{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: hav1alpha1.HomeAssistantSpec{
				Image:   "homeassistant/home-assistant",
				Version: "2025.1",
			},
		}
	}

	// newHAConfig is a minimal HomeAssistantConfiguration CR.
	newHAConfig := func(haName string) *hav1alpha1.HomeAssistantConfiguration {
		return &hav1alpha1.HomeAssistantConfiguration{
			ObjectMeta: metav1.ObjectMeta{
				Name:      haName + "-config",
				Namespace: ns,
			},
			Spec: hav1alpha1.HomeAssistantConfigurationSpec{
				HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: haName},
				Configuration:    "homeassistant:\n  name: Test\n",
			},
		}
	}

	// newAddon builds a basic HomeAssistantAddon CR with profile and optional overrides.
	newAddon := func(name, haName, profile string) *hav1alpha1.HomeAssistantAddon {
		return &hav1alpha1.HomeAssistantAddon{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: hav1alpha1.HomeAssistantAddonSpec{
				HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: haName},
				Profile:          profile,
			},
		}
	}

	BeforeEach(func() {
		testCtx = context.Background()
		ns = "default"
		reconciler = &HomeAssistantAddonReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: events.NewFakeRecorder(100),
		}
	})

	// -------------------------------------------------------------------------
	// Workload creation
	// -------------------------------------------------------------------------
	Context("Workload creation", func() {
		var ha *hav1alpha1.HomeAssistant

		BeforeEach(func() {
			ha = newHA("ha-workload")
			Expect(k8sClient.Create(testCtx, ha)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(testCtx, ha)
		})

		It("creates Deployment when addon has no storage", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-no-storage", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-workload"},
					Image:            "my-app:1.0",
					Ports: []hav1alpha1.AddonPort{
						{Name: "http", Port: 8080, Protocol: "TCP"},
					},
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			dep := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-workload-addon-no-storage", Namespace: ns,
				}, dep)
			}, timeout, interval).Should(Succeed())

			Expect(dep.Spec.Template.Spec.Containers).To(HaveLen(1))
			Expect(dep.Spec.Template.Spec.Containers[0].Image).To(Equal("my-app:1.0"))
			Expect(*dep.Spec.Replicas).To(Equal(int32(1)))
		})

		It("creates StatefulSet when addon has storage", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-with-storage", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-workload"},
					Image:            "my-db:1.0",
					Storage: &hav1alpha1.AddonStorage{
						Size:      resource.MustParse("2Gi"),
						MountPath: "/data",
					},
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			sts := &appsv1.StatefulSet{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-workload-addon-with-storage", Namespace: ns,
				}, sts)
			}, timeout, interval).Should(Succeed())

			Expect(sts.Spec.VolumeClaimTemplates).To(HaveLen(1))
			Expect(*sts.Spec.Replicas).To(Equal(int32(1)))
		})

		It("creates StatefulSet for mosquitto profile (has storage → StatefulSet)", func() {
			addon := newAddon("addon-mosquitto", "ha-workload", "mosquitto")
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			// mosquitto profile has storage → StatefulSet
			sts := &appsv1.StatefulSet{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-workload-addon-mosquitto", Namespace: ns,
				}, sts)
			}, timeout, interval).Should(Succeed())

			Expect(sts.Spec.Template.Spec.Containers[0].Image).To(Equal("eclipse-mosquitto:2"))
		})

		It("creates StatefulSet for mariadb profile", func() {
			addon := newAddon("addon-mariadb", "ha-workload", "mariadb")
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			sts := &appsv1.StatefulSet{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-workload-addon-mariadb", Namespace: ns,
				}, sts)
			}, timeout, interval).Should(Succeed())

			Expect(sts.Spec.Template.Spec.Containers[0].Image).To(Equal("mariadb:11"))
			Expect(sts.Spec.VolumeClaimTemplates).To(HaveLen(1))
		})

		It("creates StatefulSet for node-red profile", func() {
			addon := newAddon("addon-node-red", "ha-workload", "node-red")
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			sts := &appsv1.StatefulSet{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-workload-addon-node-red", Namespace: ns,
				}, sts)
			}, timeout, interval).Should(Succeed())

			Expect(sts.Spec.Template.Spec.Containers[0].Image).To(Equal("nodered/node-red:latest"))
		})

		It("sets env vars on the container", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-env", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-workload"},
					Image:            "my-app:1.0",
					Env: []corev1.EnvVar{
						{Name: "MY_VAR", Value: "hello"},
						{Name: "OTHER_VAR", Value: "world"},
					},
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			dep := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-workload-addon-env", Namespace: ns,
				}, dep)
			}, timeout, interval).Should(Succeed())

			envVars := dep.Spec.Template.Spec.Containers[0].Env
			Expect(envVars).To(ContainElement(corev1.EnvVar{Name: "MY_VAR", Value: "hello"}))
			Expect(envVars).To(ContainElement(corev1.EnvVar{Name: "OTHER_VAR", Value: "world"}))
		})

		It("sets hostNetwork on pod spec when requested", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-hostnet", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-workload"},
					Image:            "my-app:1.0",
					HostNetwork:      true,
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			dep := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-workload-addon-hostnet", Namespace: ns,
				}, dep)
			}, timeout, interval).Should(Succeed())

			Expect(dep.Spec.Template.Spec.HostNetwork).To(BeTrue())
		})
	})

	// -------------------------------------------------------------------------
	// Service creation
	// -------------------------------------------------------------------------
	Context("Service creation", func() {
		var ha *hav1alpha1.HomeAssistant

		BeforeEach(func() {
			ha = newHA("ha-svc")
			Expect(k8sClient.Create(testCtx, ha)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(testCtx, ha)
		})

		It("creates Service with correct ports", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-svc", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-svc"},
					Image:            "my-app:1.0",
					Ports: []hav1alpha1.AddonPort{
						{Name: "http", Port: 8080, Protocol: "TCP"},
						{Name: "grpc", Port: 9090, Protocol: "TCP"},
					},
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			svc := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-svc-addon-svc", Namespace: ns,
				}, svc)
			}, timeout, interval).Should(Succeed())

			Expect(svc.Spec.Ports).To(HaveLen(2))
			Expect(svc.Spec.Ports[0].Name).To(Equal("http"))
			Expect(svc.Spec.Ports[0].Port).To(Equal(int32(8080)))
			Expect(svc.Spec.Ports[1].Name).To(Equal("grpc"))
			Expect(svc.Spec.Ports[1].Port).To(Equal(int32(9090)))
			Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
		})

		It("creates NodePort Service when nodePort is specified", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-nodeport", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-svc"},
					Image:            "my-app:1.0",
					Ports: []hav1alpha1.AddonPort{
						{Name: "mqtt", Port: 1883, Protocol: "TCP", NodePort: 31883},
					},
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			svc := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-svc-addon-nodeport", Namespace: ns,
				}, svc)
			}, timeout, interval).Should(Succeed())

			Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeNodePort))
			Expect(svc.Spec.Ports[0].NodePort).To(Equal(int32(31883)))
		})

		It("does not create Service when no ports specified", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-no-svc", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-svc"},
					Image:            "my-app:1.0",
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			svc := &corev1.Service{}
			Consistently(func() bool {
				err := k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-svc-addon-no-svc", Namespace: ns,
				}, svc)
				return err == nil
			}, time.Second*2, interval).Should(BeFalse())
		})
	})

	// -------------------------------------------------------------------------
	// ConfigMap creation
	// -------------------------------------------------------------------------
	Context("ConfigMap creation", func() {
		var ha *hav1alpha1.HomeAssistant

		BeforeEach(func() {
			ha = newHA("ha-cm")
			Expect(k8sClient.Create(testCtx, ha)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(testCtx, ha)
		})

		It("creates ConfigMap with addon config files", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-config-cm", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-cm"},
					Image:            "my-app:1.0",
					Config: &hav1alpha1.AddonConfig{
						MountPath: "/app/config",
						Data: map[string]string{
							"app.conf":   "key=value\n",
							"extra.conf": "foo=bar\n",
						},
					},
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			cm := &corev1.ConfigMap{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-cm-addon-config-cm-config", Namespace: ns,
				}, cm)
			}, timeout, interval).Should(Succeed())

			Expect(cm.Data).To(HaveKeyWithValue("app.conf", "key=value\n"))
			Expect(cm.Data).To(HaveKeyWithValue("extra.conf", "foo=bar\n"))
		})

		It("mounts ConfigMap into container at correct path", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-cm-mount", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-cm"},
					Image:            "my-app:1.0",
					Config: &hav1alpha1.AddonConfig{
						MountPath: "/app/cfg",
						Data:      map[string]string{"cfg.yaml": "x: 1\n"},
					},
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			dep := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-cm-addon-cm-mount", Namespace: ns,
				}, dep)
			}, timeout, interval).Should(Succeed())

			mounts := dep.Spec.Template.Spec.Containers[0].VolumeMounts
			var found bool
			for _, m := range mounts {
				if m.MountPath == "/app/cfg" {
					found = true
				}
			}
			Expect(found).To(BeTrue(), "expected volume mount at /app/cfg")
		})

		It("does not create ConfigMap when no config specified", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-no-cm", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-cm"},
					Image:            "my-app:1.0",
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			cm := &corev1.ConfigMap{}
			Consistently(func() bool {
				err := k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-cm-addon-no-cm-config", Namespace: ns,
				}, cm)
				return err == nil
			}, time.Second*2, interval).Should(BeFalse())
		})
	})

	// -------------------------------------------------------------------------
	// Ingress creation
	// -------------------------------------------------------------------------
	Context("Ingress creation", func() {
		var ha *hav1alpha1.HomeAssistant

		BeforeEach(func() {
			ha = newHA("ha-ingress")
			Expect(k8sClient.Create(testCtx, ha)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(testCtx, ha)
		})

		It("creates Ingress when enabled", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-ing", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-ingress"},
					Image:            "my-app:1.0",
					Ports: []hav1alpha1.AddonPort{
						{Name: "http", Port: 8080, Protocol: "TCP"},
					},
					Ingress: &hav1alpha1.AddonIngress{
						Enabled: true,
						Host:    "app.example.com",
					},
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			ing := &networkingv1.Ingress{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-ingress-addon-ing", Namespace: ns,
				}, ing)
			}, timeout, interval).Should(Succeed())

			Expect(ing.Spec.Rules).To(HaveLen(1))
			Expect(ing.Spec.Rules[0].Host).To(Equal("app.example.com"))
		})

		It("does not create Ingress when disabled", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-no-ing", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-ingress"},
					Image:            "my-app:1.0",
					Ports: []hav1alpha1.AddonPort{
						{Name: "http", Port: 8080, Protocol: "TCP"},
					},
					Ingress: &hav1alpha1.AddonIngress{
						Enabled: false,
					},
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			ing := &networkingv1.Ingress{}
			Consistently(func() bool {
				err := k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-ingress-addon-no-ing", Namespace: ns,
				}, ing)
				return err == nil
			}, time.Second*2, interval).Should(BeFalse())
		})

		It("deletes Ingress when ingress is disabled after being enabled", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-ing-toggle", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-ingress"},
					Image:            "my-app:1.0",
					Ports: []hav1alpha1.AddonPort{
						{Name: "http", Port: 8080, Protocol: "TCP"},
					},
					Ingress: &hav1alpha1.AddonIngress{
						Enabled: true,
						Host:    "toggle.example.com",
					},
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			// First reconcile: Ingress should be created
			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			ing := &networkingv1.Ingress{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-ingress-addon-ing-toggle", Namespace: ns,
				}, ing)
			}, timeout, interval).Should(Succeed())

			// Update addon to disable Ingress
			Eventually(func() error {
				if err := k8sClient.Get(testCtx, types.NamespacedName{
					Name: addon.Name, Namespace: ns,
				}, addon); err != nil {
					return err
				}
				addon.Spec.Ingress.Enabled = false
				return k8sClient.Update(testCtx, addon)
			}, timeout, interval).Should(Succeed())

			_, err = reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			// Ingress should be deleted
			Eventually(func() bool {
				err := k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-ingress-addon-ing-toggle", Namespace: ns,
				}, ing)
				return err != nil
			}, timeout, interval).Should(BeTrue())
		})
	})

	// -------------------------------------------------------------------------
	// HAIntegration
	// -------------------------------------------------------------------------
	Context("HAIntegration", func() {
		var ha *hav1alpha1.HomeAssistant
		var haConfig *hav1alpha1.HomeAssistantConfiguration

		BeforeEach(func() {
			ha = newHA("ha-integration")
			Expect(k8sClient.Create(testCtx, ha)).To(Succeed())
			haConfig = newHAConfig("ha-integration")
			Expect(k8sClient.Create(testCtx, haConfig)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(testCtx, ha)
			_ = k8sClient.Delete(testCtx, haConfig)
		})

		It("does NOT modify HomeAssistantConfiguration for mosquitto profile (HA 2025.x)", func() {
			// HA 2025.x dropped support for 'broker' in configuration.yaml.
			// The mosquitto profile no longer sets HAIntegration — users configure
			// MQTT via the HA UI or Config Flow API (Phase 6).
			addon := newAddon("addon-mqtt", "ha-integration", "mosquitto")
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			updatedConfig := &hav1alpha1.HomeAssistantConfiguration{}
			Consistently(func() string {
				_ = k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-integration-config", Namespace: ns,
				}, updatedConfig)
				return updatedConfig.Spec.Configuration
			}, "2s", interval).ShouldNot(ContainSubstring("mqtt:"))
		})

		It("removes stale mqtt section when mosquitto addon previously managed it (migration path)", func() {
			// Migration scenario: an existing mosquitto addon CR carries the
			// ha.homeassistant.io/managed-integration: mqtt annotation from the
			// old operator version. The HAConfig still has an mqtt: section.
			// After upgrade the profile has HAIntegration=nil, so reconcileHAIntegration
			// calls cleanupStaleHAIntegration which must remove the section automatically.
			haConfigWithMQTT := &hav1alpha1.HomeAssistantConfiguration{}
			Expect(k8sClient.Get(testCtx, types.NamespacedName{
				Name: "ha-integration-config", Namespace: ns,
			}, haConfigWithMQTT)).To(Succeed())
			haConfigWithMQTT.Spec.Configuration += "mqtt:\n  broker: old-broker.svc\n"
			Expect(k8sClient.Update(testCtx, haConfigWithMQTT)).To(Succeed())

			// Addon has the managed-integration annotation (set by old operator version).
			addon := newAddon("addon-mqtt-stale", "ha-integration", "mosquitto")
			addon.Annotations = map[string]string{
				"ha.homeassistant.io/managed-integration": "mqtt",
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			// Stale mqtt section must be removed from HAConfig.
			updatedConfig := &hav1alpha1.HomeAssistantConfiguration{}
			Eventually(func() string {
				_ = k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-integration-config", Namespace: ns,
				}, updatedConfig)
				return updatedConfig.Spec.Configuration
			}, timeout, interval).ShouldNot(ContainSubstring("mqtt:"))
		})

		It("injects Service DNS as broker when mqtt section has no broker set", func() {
			// Uses explicit haIntegration in spec (not mosquitto profile) to test
			// the broker auto-injection logic in reconcileHAIntegration.
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-mqtt-dns", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-integration"},
					Image:            "eclipse-mosquitto:2",
					HAIntegration: &hav1alpha1.HAIntegration{
						Section: "mqtt",
					},
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			updatedConfig := &hav1alpha1.HomeAssistantConfiguration{}
			Eventually(func() string {
				_ = k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-integration-config", Namespace: ns,
				}, updatedConfig)
				return updatedConfig.Spec.Configuration
			}, timeout, interval).Should(ContainSubstring("ha-integration-addon-mqtt-dns"))
		})

		It("skips HAIntegration gracefully when HomeAssistantConfiguration does not exist", func() {
			ha2 := newHA("ha-no-config")
			Expect(k8sClient.Create(testCtx, ha2)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, ha2) }()

			// Uses explicit haIntegration to test graceful skip when HAConfig is missing.
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-no-haconfig", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-no-config"},
					Image:            "eclipse-mosquitto:2",
					HAIntegration: &hav1alpha1.HAIntegration{
						Section: "mqtt",
					},
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			// Should NOT return error
			result, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))
		})

		It("removes integration section when addon is deleted", func() {
			// Uses explicit haIntegration to test finalizer cleanup logic.
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-mqtt-del", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-integration"},
					Image:            "eclipse-mosquitto:2",
					HAIntegration: &hav1alpha1.HAIntegration{
						Section: "mqtt",
					},
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())

			// Reconcile to add the section
			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			// Wait for mqtt section to appear
			updatedConfig := &hav1alpha1.HomeAssistantConfiguration{}
			Eventually(func() string {
				_ = k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-integration-config", Namespace: ns,
				}, updatedConfig)
				return updatedConfig.Spec.Configuration
			}, timeout, interval).Should(ContainSubstring("mqtt:"))

			// Delete addon → triggers finalizer
			Expect(k8sClient.Delete(testCtx, addon)).To(Succeed())
			Eventually(func() bool {
				fetched := &hav1alpha1.HomeAssistantAddon{}
				if err := k8sClient.Get(testCtx, types.NamespacedName{Name: addon.Name, Namespace: ns}, fetched); err != nil {
					return false
				}
				return fetched.GetDeletionTimestamp() != nil
			}, timeout, interval).Should(BeTrue())

			_, err = reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			// mqtt section should be removed
			Eventually(func() string {
				_ = k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-integration-config", Namespace: ns,
				}, updatedConfig)
				return updatedConfig.Spec.Configuration
			}, timeout, interval).ShouldNot(ContainSubstring("mqtt:"))
		})
	})

	// -------------------------------------------------------------------------
	// Owner references
	// -------------------------------------------------------------------------
	Context("Owner references", func() {
		var ha *hav1alpha1.HomeAssistant

		BeforeEach(func() {
			ha = newHA("ha-owners")
			Expect(k8sClient.Create(testCtx, ha)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(testCtx, ha)
		})

		It("sets owner reference on Deployment to HomeAssistantAddon", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-owner", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-owners"},
					Image:            "my-app:1.0",
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			dep := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-owners-addon-owner", Namespace: ns,
				}, dep)
			}, timeout, interval).Should(Succeed())

			Expect(dep.OwnerReferences).To(HaveLen(1))
			Expect(dep.OwnerReferences[0].Kind).To(Equal("HomeAssistantAddon"))
			Expect(dep.OwnerReferences[0].Name).To(Equal("addon-owner"))
		})

		It("sets owner reference on Service to HomeAssistantAddon", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-owner-svc", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-owners"},
					Image:            "my-app:1.0",
					Ports: []hav1alpha1.AddonPort{
						{Name: "http", Port: 8080, Protocol: "TCP"},
					},
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			svc := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-owners-addon-owner-svc", Namespace: ns,
				}, svc)
			}, timeout, interval).Should(Succeed())

			Expect(svc.OwnerReferences).To(HaveLen(1))
			Expect(svc.OwnerReferences[0].Kind).To(Equal("HomeAssistantAddon"))
		})
	})

	// -------------------------------------------------------------------------
	// Validation — missing HA ref
	// -------------------------------------------------------------------------
	Context("Validation", func() {
		It("requeues when HomeAssistant CR does not exist", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-no-ha", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "non-existent-ha"},
					Image:            "my-app:1.0",
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			result, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(30 * time.Second))

			// Status should reflect the failure
			Eventually(func() bool {
				_ = k8sClient.Get(testCtx, types.NamespacedName{
					Name: addon.Name, Namespace: ns,
				}, addon)
				for _, c := range addon.Status.Conditions {
					if c.Type == conditionTypeReady && c.Status == metav1.ConditionFalse {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())
		})

		It("sets phase Failed and does not requeue on invalid spec (no image, no profile)", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-bad-spec", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-bad-spec"},
				},
			}
			ha := newHA("ha-bad-spec")
			Expect(k8sClient.Create(testCtx, ha)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, ha) }()

			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			result, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			Eventually(func() hav1alpha1.AddonPhase {
				_ = k8sClient.Get(testCtx, types.NamespacedName{
					Name: addon.Name, Namespace: ns,
				}, addon)
				return addon.Status.Phase
			}, timeout, interval).Should(Equal(hav1alpha1.AddonPhaseFailed))
		})
	})

	// -------------------------------------------------------------------------
	// Status updates
	// -------------------------------------------------------------------------
	Context("Status updates", func() {
		var ha *hav1alpha1.HomeAssistant

		BeforeEach(func() {
			ha = newHA("ha-status")
			Expect(k8sClient.Create(testCtx, ha)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(testCtx, ha)
		})

		It("sets Ready=True, phase Running, resolvedImage and workloadType after reconcile", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-status", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-status"},
					Image:            "my-app:1.0",
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				_ = k8sClient.Get(testCtx, types.NamespacedName{
					Name: addon.Name, Namespace: ns,
				}, addon)
				for _, c := range addon.Status.Conditions {
					if c.Type == conditionTypeReady && c.Status == metav1.ConditionTrue {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			Expect(addon.Status.Phase).To(Equal(hav1alpha1.AddonPhaseRunning))
			Expect(addon.Status.ResolvedImage).To(Equal("my-app:1.0"))
			Expect(addon.Status.WorkloadType).To(Equal("Deployment"))
			Expect(addon.Status.AddonHash).NotTo(BeEmpty())
			Expect(addon.Status.ObservedGeneration).To(Equal(addon.Generation))
		})

		It("sets WorkloadType=StatefulSet when storage is configured", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-status-sts", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-status"},
					Image:            "db:1.0",
					Storage: &hav1alpha1.AddonStorage{
						Size:      resource.MustParse("1Gi"),
						MountPath: "/data",
					},
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() string {
				_ = k8sClient.Get(testCtx, types.NamespacedName{
					Name: addon.Name, Namespace: ns,
				}, addon)
				return addon.Status.WorkloadType
			}, timeout, interval).Should(Equal("StatefulSet"))
		})
	})

	// -------------------------------------------------------------------------
	// Idempotency
	// -------------------------------------------------------------------------
	Context("Idempotency", func() {
		var ha *hav1alpha1.HomeAssistant

		BeforeEach(func() {
			ha = newHA("ha-idem")
			Expect(k8sClient.Create(testCtx, ha)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(testCtx, ha)
		})

		It("does not update Deployment on repeated reconciles when spec is unchanged", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-idem", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-idem"},
					Image:            "my-app:1.0",
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			dep := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-idem-addon-idem", Namespace: ns,
				}, dep)
			}, timeout, interval).Should(Succeed())

			initialRV := dep.ResourceVersion

			// Second reconcile
			_, err = reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			Consistently(func() string {
				_ = k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-idem-addon-idem", Namespace: ns,
				}, dep)
				return dep.ResourceVersion
			}, time.Second*2, interval).Should(Equal(initialRV))
		})

		It("updates Deployment when image changes", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-img-change", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-idem"},
					Image:            "my-app:1.0",
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			dep := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-idem-addon-img-change", Namespace: ns,
				}, dep)
			}, timeout, interval).Should(Succeed())
			Expect(dep.Spec.Template.Spec.Containers[0].Image).To(Equal("my-app:1.0"))

			// Update image
			Eventually(func() error {
				if err := k8sClient.Get(testCtx, types.NamespacedName{
					Name: addon.Name, Namespace: ns,
				}, addon); err != nil {
					return err
				}
				addon.Spec.Image = "my-app:2.0"
				return k8sClient.Update(testCtx, addon)
			}, timeout, interval).Should(Succeed())

			_, err = reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() string {
				_ = k8sClient.Get(testCtx, types.NamespacedName{
					Name: "ha-idem-addon-img-change", Namespace: ns,
				}, dep)
				return dep.Spec.Template.Spec.Containers[0].Image
			}, timeout, interval).Should(Equal("my-app:2.0"))
		})
	})

	// -------------------------------------------------------------------------
	// Finalizer & lifecycle
	// -------------------------------------------------------------------------
	Context("Finalizer", func() {
		var ha *hav1alpha1.HomeAssistant

		BeforeEach(func() {
			ha = newHA("ha-fin")
			Expect(k8sClient.Create(testCtx, ha)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(testCtx, ha)
		})

		It("adds finalizer on first reconcile", func() {
			addon := &hav1alpha1.HomeAssistantAddon{
				ObjectMeta: metav1.ObjectMeta{Name: "addon-fin", Namespace: ns},
				Spec: hav1alpha1.HomeAssistantAddonSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "ha-fin"},
					Image:            "my-app:1.0",
				},
			}
			Expect(k8sClient.Create(testCtx, addon)).To(Succeed())
			defer func() { _ = k8sClient.Delete(testCtx, addon) }()

			_, err := reconcileAddon(addon.Name)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() bool {
				_ = k8sClient.Get(testCtx, types.NamespacedName{
					Name: addon.Name, Namespace: ns,
				}, addon)
				for _, f := range addon.Finalizers {
					if f == addonFinalizerName {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())
		})
	})

	// -------------------------------------------------------------------------
	// SetupWithManager
	// -------------------------------------------------------------------------
	Context("SetupWithManager", func() {
		It("registers the controller without error", func() {
			mgr, err := ctrl.NewManager(cfg, ctrl.Options{
				Scheme: k8sClient.Scheme(),
			})
			Expect(err).NotTo(HaveOccurred())

			rec := &HomeAssistantAddonReconciler{
				Client: mgr.GetClient(),
				Scheme: mgr.GetScheme(),
			}
			Expect(rec.SetupWithManager(mgr)).To(Succeed())
		})
	})
})
