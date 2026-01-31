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
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hav1alpha1 "github.com/przemekhys/homeassistant-operator/api/v1alpha1"
)

var _ = Describe("Bootstrap Controller", func() {
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	Context("When bootstrap is disabled", func() {
		It("Should not perform bootstrap operations", func() {
			By("Creating a HomeAssistant CR without bootstrap enabled")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-no-bootstrap",
					Namespace: "default",
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "2024.1.0",
					// Bootstrap is nil or disabled
					Bootstrap: nil,
				},
			}
			Expect(k8sClient.Create(ctx, ha)).Should(Succeed())

			By("Creating HomeAssistantConfiguration")
			haConfig := &hav1alpha1.HomeAssistantConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ha.Name + "-config",
					Namespace: ha.Namespace,
				},
				Spec: hav1alpha1.HomeAssistantConfigurationSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{
						Name: ha.Name,
					},
					Configuration: "homeassistant:\n  name: Home\n",
				},
			}
			Expect(k8sClient.Create(ctx, haConfig)).Should(Succeed())

			haKey := types.NamespacedName{Name: ha.Name, Namespace: ha.Namespace}

			By("Verifying bootstrap status remains nil")
			Eventually(func(g Gomega) {
				fetchedHA := &hav1alpha1.HomeAssistant{}
				g.Expect(k8sClient.Get(ctx, haKey, fetchedHA)).Should(Succeed())
				// Bootstrap status should remain nil since it's disabled
				g.Expect(fetchedHA.Status.Bootstrap).Should(BeNil())
			}, timeout, interval).Should(Succeed())

			By("Cleaning up")
			Expect(k8sClient.Delete(ctx, haConfig)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, ha)).Should(Succeed())
		})

		It("Should not perform bootstrap when explicitly disabled", func() {
			By("Creating a HomeAssistant CR with bootstrap.enabled=false")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-bootstrap-disabled",
					Namespace: "default",
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "2024.1.0",
					Bootstrap: &hav1alpha1.BootstrapSpec{
						Enabled: false,
					},
				},
			}
			Expect(k8sClient.Create(ctx, ha)).Should(Succeed())

			By("Creating HomeAssistantConfiguration")
			haConfig := &hav1alpha1.HomeAssistantConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ha.Name + "-config",
					Namespace: ha.Namespace,
				},
				Spec: hav1alpha1.HomeAssistantConfigurationSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{
						Name: ha.Name,
					},
					Configuration: "homeassistant:\n  name: Home\n",
				},
			}
			Expect(k8sClient.Create(ctx, haConfig)).Should(Succeed())

			haKey := types.NamespacedName{Name: ha.Name, Namespace: ha.Namespace}

			By("Verifying bootstrap status remains nil")
			Consistently(func(g Gomega) {
				fetchedHA := &hav1alpha1.HomeAssistant{}
				g.Expect(k8sClient.Get(ctx, haKey, fetchedHA)).Should(Succeed())
				g.Expect(fetchedHA.Status.Bootstrap).Should(BeNil())
			}, time.Second*2, interval).Should(Succeed())

			By("Cleaning up")
			Expect(k8sClient.Delete(ctx, haConfig)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, ha)).Should(Succeed())
		})
	})

	Context("When bootstrap is already completed", func() {
		It("Should skip bootstrap operations", func() {
			By("Creating credentials secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bootstrap-completed-creds",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"username": []byte("admin"),
					"password": []byte("testpass123"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).Should(Succeed())

			By("Creating a HomeAssistant CR with bootstrap enabled")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-bootstrap-completed",
					Namespace: "default",
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "2024.1.0",
					Bootstrap: &hav1alpha1.BootstrapSpec{
						Enabled: true,
						Credentials: &hav1alpha1.BootstrapCredentials{
							SecretRef: &hav1alpha1.CredentialsSecretRef{
								Name: secret.Name,
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, ha)).Should(Succeed())

			By("Creating HomeAssistantConfiguration")
			haConfig := &hav1alpha1.HomeAssistantConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ha.Name + "-config",
					Namespace: ha.Namespace,
				},
				Spec: hav1alpha1.HomeAssistantConfigurationSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{
						Name: ha.Name,
					},
					Configuration: "homeassistant:\n  name: Home\n",
				},
			}
			Expect(k8sClient.Create(ctx, haConfig)).Should(Succeed())

			haKey := types.NamespacedName{Name: ha.Name, Namespace: ha.Namespace}

			By("Manually marking bootstrap as completed")
			Eventually(func(g Gomega) {
				fetchedHA := &hav1alpha1.HomeAssistant{}
				g.Expect(k8sClient.Get(ctx, haKey, fetchedHA)).Should(Succeed())

				// Initialize bootstrap status and mark as completed
				now := metav1.Now()
				fetchedHA.Status.Bootstrap = &hav1alpha1.BootstrapStatus{
					Completed:   true,
					LastAttempt: &now,
					Message:     "Bootstrap already completed",
				}
				meta.SetStatusCondition(&fetchedHA.Status.Conditions, metav1.Condition{
					Type:               "BootstrapReady",
					Status:             metav1.ConditionTrue,
					ObservedGeneration: fetchedHA.Generation,
					Reason:             reasonBootstrapCompleted,
					Message:            "Bootstrap already completed",
				})
				g.Expect(k8sClient.Status().Update(ctx, fetchedHA)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			By("Verifying bootstrap status remains completed")
			Consistently(func(g Gomega) {
				fetchedHA := &hav1alpha1.HomeAssistant{}
				g.Expect(k8sClient.Get(ctx, haKey, fetchedHA)).Should(Succeed())
				g.Expect(fetchedHA.Status.Bootstrap).NotTo(BeNil())
				g.Expect(fetchedHA.Status.Bootstrap.Completed).Should(BeTrue())
			}, time.Second*2, interval).Should(Succeed())

			By("Cleaning up")
			Expect(k8sClient.Delete(ctx, haConfig)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, ha)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).Should(Succeed())
		})
	})

	Context("When validating bootstrap configuration", func() {
		It("Should fail when credentials secretRef is nil", func() {
			By("Creating a HomeAssistant CR with nil secretRef")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-nil-secretref",
					Namespace: "default",
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "2024.1.0",
					Bootstrap: &hav1alpha1.BootstrapSpec{
						Enabled: true,
						Credentials: &hav1alpha1.BootstrapCredentials{
							SecretRef: nil, // Invalid: nil secretRef
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, ha)).Should(Succeed())

			By("Creating HomeAssistantConfiguration")
			haConfig := &hav1alpha1.HomeAssistantConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ha.Name + "-config",
					Namespace: ha.Namespace,
				},
				Spec: hav1alpha1.HomeAssistantConfigurationSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{
						Name: ha.Name,
					},
					Configuration: "homeassistant:\n  name: Home\n",
				},
			}
			Expect(k8sClient.Create(ctx, haConfig)).Should(Succeed())

			haKey := types.NamespacedName{Name: ha.Name, Namespace: ha.Namespace}

			By("Reconciling the resource to create StatefulSet")
			reconciler := &HomeAssistantReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: haKey,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Marking HomeAssistant as ready to trigger bootstrap")
			Eventually(func(g Gomega) {
				fetchedHA := &hav1alpha1.HomeAssistant{}
				g.Expect(k8sClient.Get(ctx, haKey, fetchedHA)).Should(Succeed())
				fetchedHA.Status.Ready = true
				fetchedHA.Status.Phase = hav1alpha1.PhaseRunning
				g.Expect(k8sClient.Status().Update(ctx, fetchedHA)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			By("Reconciling again to trigger bootstrap logic")
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: haKey,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying bootstrap fails with validation error")
			Eventually(func(g Gomega) {
				fetchedHA := &hav1alpha1.HomeAssistant{}
				g.Expect(k8sClient.Get(ctx, haKey, fetchedHA)).Should(Succeed())

				// Should have bootstrap status with error
				g.Expect(fetchedHA.Status.Bootstrap).NotTo(BeNil())
				g.Expect(fetchedHA.Status.Bootstrap.Completed).Should(BeFalse())
				expectedMsg := "bootstrap credentials secretRef required when enabled"
				g.Expect(fetchedHA.Status.Bootstrap.Message).Should(ContainSubstring(expectedMsg))

				// Check condition
				condition := meta.FindStatusCondition(fetchedHA.Status.Conditions, "BootstrapReady")
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).Should(Equal(metav1.ConditionFalse))
				g.Expect(condition.Reason).Should(Equal(reasonBootstrapMissingCredentials))
			}, timeout, interval).Should(Succeed())

			By("Cleaning up")
			Expect(k8sClient.Delete(ctx, haConfig)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, ha)).Should(Succeed())
		})

		It("Should fail when secret name is empty", func() {
			By("Creating a HomeAssistant CR with empty secret name")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-empty-secret-name",
					Namespace: "default",
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "2024.1.0",
					Bootstrap: &hav1alpha1.BootstrapSpec{
						Enabled: true,
						Credentials: &hav1alpha1.BootstrapCredentials{
							SecretRef: &hav1alpha1.CredentialsSecretRef{
								Name: "", // Invalid: empty name
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, ha)).Should(Succeed())

			By("Creating HomeAssistantConfiguration")
			haConfig := &hav1alpha1.HomeAssistantConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ha.Name + "-config",
					Namespace: ha.Namespace,
				},
				Spec: hav1alpha1.HomeAssistantConfigurationSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{
						Name: ha.Name,
					},
					Configuration: "homeassistant:\n  name: Home\n",
				},
			}
			Expect(k8sClient.Create(ctx, haConfig)).Should(Succeed())

			haKey := types.NamespacedName{Name: ha.Name, Namespace: ha.Namespace}

			By("Reconciling the resource to create StatefulSet")
			reconciler := &HomeAssistantReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: haKey,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Marking HomeAssistant as ready to trigger bootstrap")
			Eventually(func(g Gomega) {
				fetchedHA := &hav1alpha1.HomeAssistant{}
				g.Expect(k8sClient.Get(ctx, haKey, fetchedHA)).Should(Succeed())
				fetchedHA.Status.Ready = true
				fetchedHA.Status.Phase = hav1alpha1.PhaseRunning
				g.Expect(k8sClient.Status().Update(ctx, fetchedHA)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			By("Reconciling again to trigger bootstrap logic")
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: haKey,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying bootstrap fails with validation error")
			Eventually(func(g Gomega) {
				fetchedHA := &hav1alpha1.HomeAssistant{}
				g.Expect(k8sClient.Get(ctx, haKey, fetchedHA)).Should(Succeed())

				g.Expect(fetchedHA.Status.Bootstrap).NotTo(BeNil())
				g.Expect(fetchedHA.Status.Bootstrap.Completed).Should(BeFalse())
				g.Expect(fetchedHA.Status.Bootstrap.Message).Should(ContainSubstring("secret name cannot be empty"))

				condition := meta.FindStatusCondition(fetchedHA.Status.Conditions, "BootstrapReady")
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).Should(Equal(metav1.ConditionFalse))
				g.Expect(condition.Reason).Should(Equal(reasonBootstrapMissingCredentials))
			}, timeout, interval).Should(Succeed())

			By("Cleaning up")
			Expect(k8sClient.Delete(ctx, haConfig)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, ha)).Should(Succeed())
		})

		It("Should fail when credentials secret is missing", func() {
			By("Creating a HomeAssistant CR referencing non-existent secret")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-missing-secret",
					Namespace: "default",
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "2024.1.0",
					Bootstrap: &hav1alpha1.BootstrapSpec{
						Enabled: true,
						Credentials: &hav1alpha1.BootstrapCredentials{
							SecretRef: &hav1alpha1.CredentialsSecretRef{
								Name: "non-existent-secret",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, ha)).Should(Succeed())

			By("Creating HomeAssistantConfiguration")
			haConfig := &hav1alpha1.HomeAssistantConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ha.Name + "-config",
					Namespace: ha.Namespace,
				},
				Spec: hav1alpha1.HomeAssistantConfigurationSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{
						Name: ha.Name,
					},
					Configuration: "homeassistant:\n  name: Home\n",
				},
			}
			Expect(k8sClient.Create(ctx, haConfig)).Should(Succeed())

			haKey := types.NamespacedName{Name: ha.Name, Namespace: ha.Namespace}

			By("Reconciling the resource to create StatefulSet")
			reconciler := &HomeAssistantReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: haKey,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Marking HomeAssistant as ready to trigger bootstrap")
			Eventually(func(g Gomega) {
				fetchedHA := &hav1alpha1.HomeAssistant{}
				g.Expect(k8sClient.Get(ctx, haKey, fetchedHA)).Should(Succeed())
				fetchedHA.Status.Ready = true
				fetchedHA.Status.Phase = hav1alpha1.PhaseRunning
				g.Expect(k8sClient.Status().Update(ctx, fetchedHA)).Should(Succeed())
			}, timeout, interval).Should(Succeed())

			By("Reconciling again to trigger bootstrap logic")
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: haKey,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying bootstrap fails with missing secret error")
			Eventually(func(g Gomega) {
				fetchedHA := &hav1alpha1.HomeAssistant{}
				g.Expect(k8sClient.Get(ctx, haKey, fetchedHA)).Should(Succeed())

				g.Expect(fetchedHA.Status.Bootstrap).NotTo(BeNil())
				g.Expect(fetchedHA.Status.Bootstrap.Completed).Should(BeFalse())
				g.Expect(fetchedHA.Status.Bootstrap.Message).Should(ContainSubstring("not found"))

				condition := meta.FindStatusCondition(fetchedHA.Status.Conditions, "BootstrapReady")
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).Should(Equal(metav1.ConditionFalse))
				g.Expect(condition.Reason).Should(Equal(reasonBootstrapFailed))
			}, timeout, interval).Should(Succeed())

			By("Cleaning up")
			Expect(k8sClient.Delete(ctx, haConfig)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, ha)).Should(Succeed())
		})
	})

	Context("When retrieving bootstrap credentials", func() {
		It("Should retrieve credentials with default key names", func() {
			By("Creating credentials secret with default keys")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-default-keys",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"username": []byte("testuser"),
					"password": []byte("testpass"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).Should(Succeed())

			By("Creating a HomeAssistant CR")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-default-keys",
					Namespace: "default",
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "2024.1.0",
					Bootstrap: &hav1alpha1.BootstrapSpec{
						Enabled: true,
						Credentials: &hav1alpha1.BootstrapCredentials{
							SecretRef: &hav1alpha1.CredentialsSecretRef{
								Name: secret.Name,
								// Using default keys (username, password)
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, ha)).Should(Succeed())

			By("Creating HomeAssistantConfiguration")
			haConfig := &hav1alpha1.HomeAssistantConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ha.Name + "-config",
					Namespace: ha.Namespace,
				},
				Spec: hav1alpha1.HomeAssistantConfigurationSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{
						Name: ha.Name,
					},
					Configuration: "homeassistant:\n  name: Home\n",
				},
			}
			Expect(k8sClient.Create(ctx, haConfig)).Should(Succeed())

			By("Creating reconciler to test credential retrieval")
			reconciler := &HomeAssistantReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			By("Verifying credentials can be retrieved")
			username, password, err := reconciler.getBootstrapCredentials(ctx, ha)
			Expect(err).NotTo(HaveOccurred())
			Expect(username).Should(Equal("testuser"))
			Expect(password).Should(Equal("testpass"))

			By("Cleaning up")
			Expect(k8sClient.Delete(ctx, haConfig)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, ha)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).Should(Succeed())
		})

		It("Should retrieve credentials with custom key names", func() {
			By("Creating credentials secret with custom keys")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-custom-keys",
					Namespace: "default",
				},
				Data: map[string][]byte{
					"admin-user": []byte("customuser"),
					"admin-pass": []byte("custompass"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).Should(Succeed())

			By("Creating a HomeAssistant CR with custom key names")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-custom-keys",
					Namespace: "default",
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "2024.1.0",
					Bootstrap: &hav1alpha1.BootstrapSpec{
						Enabled: true,
						Credentials: &hav1alpha1.BootstrapCredentials{
							SecretRef: &hav1alpha1.CredentialsSecretRef{
								Name:        secret.Name,
								UsernameKey: "admin-user",
								PasswordKey: "admin-pass",
							},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, ha)).Should(Succeed())

			By("Creating HomeAssistantConfiguration")
			haConfig := &hav1alpha1.HomeAssistantConfiguration{
				ObjectMeta: metav1.ObjectMeta{
					Name:      ha.Name + "-config",
					Namespace: ha.Namespace,
				},
				Spec: hav1alpha1.HomeAssistantConfigurationSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{
						Name: ha.Name,
					},
					Configuration: "homeassistant:\n  name: Home\n",
				},
			}
			Expect(k8sClient.Create(ctx, haConfig)).Should(Succeed())

			By("Creating reconciler to test credential retrieval")
			reconciler := &HomeAssistantReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			By("Verifying credentials can be retrieved with custom keys")
			username, password, err := reconciler.getBootstrapCredentials(ctx, ha)
			Expect(err).NotTo(HaveOccurred())
			Expect(username).Should(Equal("customuser"))
			Expect(password).Should(Equal("custompass"))

			By("Cleaning up")
			Expect(k8sClient.Delete(ctx, haConfig)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, ha)).Should(Succeed())
			Expect(k8sClient.Delete(ctx, secret)).Should(Succeed())
		})
	})

	Context("When building Home Assistant URL", func() {
		It("Should build correct URL with default port", func() {
			By("Creating a HomeAssistant CR with default port")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-default-port",
					Namespace: "test-namespace",
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "2024.1.0",
					// No service spec = default port 8123
				},
			}

			By("Creating reconciler to test URL building")
			reconciler := &HomeAssistantReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			By("Verifying URL is built correctly")
			url := reconciler.buildHomeAssistantURL(ha)
			Expect(url).Should(Equal("http://test-ha-default-port.test-namespace.svc.cluster.local:8123"))
		})

		It("Should build correct URL with custom port", func() {
			By("Creating a HomeAssistant CR with custom port")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-custom-port",
					Namespace: "custom-ns",
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "2024.1.0",
					Service: &hav1alpha1.ServiceSpec{
						Port: 9999,
					},
				},
			}

			By("Creating reconciler to test URL building")
			reconciler := &HomeAssistantReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			By("Verifying URL is built with custom port")
			url := reconciler.buildHomeAssistantURL(ha)
			Expect(url).Should(Equal("http://test-ha-custom-port.custom-ns.svc.cluster.local:9999"))
		})

		It("Should build URL with correct namespace and service name", func() {
			By("Creating a HomeAssistant CR")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-home-assistant",
					Namespace: "production",
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "2024.1.0",
					Service: &hav1alpha1.ServiceSpec{
						Port: 8080,
					},
				},
			}

			By("Creating reconciler to test URL building")
			reconciler := &HomeAssistantReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			By("Verifying URL format")
			url := reconciler.buildHomeAssistantURL(ha)
			Expect(url).Should(Equal("http://my-home-assistant.production.svc.cluster.local:8080"))
			Expect(url).Should(HavePrefix("http://"))
			Expect(url).Should(ContainSubstring(".svc.cluster.local"))
		})
	})

	Context("When validating bootstrap configuration helper", func() {
		It("Should validate correctly with valid config", func() {
			By("Creating a valid bootstrap config")
			ha := &hav1alpha1.HomeAssistant{
				Spec: hav1alpha1.HomeAssistantSpec{
					Bootstrap: &hav1alpha1.BootstrapSpec{
						Enabled: true,
						Credentials: &hav1alpha1.BootstrapCredentials{
							SecretRef: &hav1alpha1.CredentialsSecretRef{
								Name: "valid-secret",
							},
						},
					},
				},
			}

			By("Creating reconciler to test validation")
			reconciler := &HomeAssistantReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			By("Verifying validation passes")
			err := reconciler.validateBootstrapConfig(ha)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should fail validation when credentials is nil", func() {
			By("Creating config with nil credentials")
			ha := &hav1alpha1.HomeAssistant{
				Spec: hav1alpha1.HomeAssistantSpec{
					Bootstrap: &hav1alpha1.BootstrapSpec{
						Enabled:     true,
						Credentials: nil,
					},
				},
			}

			By("Creating reconciler to test validation")
			reconciler := &HomeAssistantReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			By("Verifying validation fails")
			err := reconciler.validateBootstrapConfig(ha)
			Expect(err).Should(HaveOccurred())
			Expect(err.Error()).Should(ContainSubstring("bootstrap credentials secretRef required when enabled"))
		})

		It("Should fail validation when secretRef is nil", func() {
			By("Creating config with nil secretRef")
			ha := &hav1alpha1.HomeAssistant{
				Spec: hav1alpha1.HomeAssistantSpec{
					Bootstrap: &hav1alpha1.BootstrapSpec{
						Enabled: true,
						Credentials: &hav1alpha1.BootstrapCredentials{
							SecretRef: nil,
						},
					},
				},
			}

			By("Creating reconciler to test validation")
			reconciler := &HomeAssistantReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			By("Verifying validation fails")
			err := reconciler.validateBootstrapConfig(ha)
			Expect(err).Should(HaveOccurred())
			Expect(err.Error()).Should(ContainSubstring("bootstrap credentials secretRef required when enabled"))
		})

		It("Should fail validation when secret name is empty", func() {
			By("Creating config with empty secret name")
			ha := &hav1alpha1.HomeAssistant{
				Spec: hav1alpha1.HomeAssistantSpec{
					Bootstrap: &hav1alpha1.BootstrapSpec{
						Enabled: true,
						Credentials: &hav1alpha1.BootstrapCredentials{
							SecretRef: &hav1alpha1.CredentialsSecretRef{
								Name: "",
							},
						},
					},
				},
			}

			By("Creating reconciler to test validation")
			reconciler := &HomeAssistantReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			By("Verifying validation fails")
			err := reconciler.validateBootstrapConfig(ha)
			Expect(err).Should(HaveOccurred())
			Expect(err.Error()).Should(ContainSubstring("secret name cannot be empty"))
		})
	})
})
