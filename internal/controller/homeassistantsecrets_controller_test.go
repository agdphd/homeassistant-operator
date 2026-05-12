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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

var _ = Describe("HomeAssistantSecrets Controller", func() {
	Context("When reconciling a HomeAssistantSecrets resource", func() {
		const (
			haName       = "test-ha"
			resourceName = "test-hasecrets"
			secretName   = "test-secret"
			namespace    = "default"
			timeout      = time.Second * 10
			interval     = time.Millisecond * 250
		)

		BeforeEach(func() {
			// Create HomeAssistant resource for tests
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      haName,
					Namespace: namespace,
				},
				Spec: hav1.HomeAssistantSpec{
					Version: "stable",
				},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())
		})

		AfterEach(func() {
			// Cleanup HomeAssistantSecrets resources
			haSecretsList := &hav1.HomeAssistantSecretsList{}
			_ = k8sClient.List(ctx, haSecretsList, &client.ListOptions{Namespace: namespace})
			for _, haSecrets := range haSecretsList.Items {
				_ = k8sClient.Delete(ctx, &haSecrets)
			}

			// Cleanup secrets
			secretList := &corev1.SecretList{}
			_ = k8sClient.List(ctx, secretList, &client.ListOptions{Namespace: namespace})
			for _, secret := range secretList.Items {
				_ = k8sClient.Delete(ctx, &secret)
			}

			// Cleanup StatefulSets
			stsList := &appsv1.StatefulSetList{}
			_ = k8sClient.List(ctx, stsList, &client.ListOptions{Namespace: namespace})
			for _, sts := range stsList.Items {
				_ = k8sClient.Delete(ctx, &sts)
			}

			// Cleanup HomeAssistant
			haList := &hav1.HomeAssistantList{}
			_ = k8sClient.List(ctx, haList, &client.ListOptions{Namespace: namespace})
			for _, ha := range haList.Items {
				_ = k8sClient.Delete(ctx, &ha)
			}

			// Wait for resources to be deleted
			Eventually(func() int {
				haSecretsList := &hav1.HomeAssistantSecretsList{}
				_ = k8sClient.List(ctx, haSecretsList, &client.ListOptions{Namespace: namespace})
				return len(haSecretsList.Items)
			}, timeout, interval).Should(Equal(0))
		})

		It("should create generated Secret when HomeAssistantSecrets is created", func() {
			By("Creating a source Secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
				},
				StringData: map[string]string{
					"mqtt_user":     "testuser",
					"mqtt_password": "testpass",
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			By("Creating a HomeAssistantSecrets")
			haSecrets := &hav1.HomeAssistantSecrets{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace,
				},
				Spec: hav1.HomeAssistantSecretsSpec{
					HomeAssistantRef: hav1.HomeAssistantReference{
						Name: haName,
					},
					SecretRefs: []hav1.SecretKeyReference{
						{
							Name: secretName,
							Keys: []string{"mqtt_user", "mqtt_password"},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, haSecrets)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &HomeAssistantSecretsReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying generated Secret was created")
			Eventually(func(g Gomega) {
				generatedSecret := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-generated-secrets",
					Namespace: namespace,
				}, generatedSecret)).To(Succeed())
				g.Expect(generatedSecret.Data).To(HaveKey("secrets.yaml"))
			}, timeout, interval).Should(Succeed())
		})

		It("should filter keys when specific keys are provided", func() {
			By("Creating a source Secret with multiple keys")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
				},
				StringData: map[string]string{
					"mqtt_user":     "testuser",
					"mqtt_password": "testpass",
					"db_password":   "dbpass",
					"api_key":       "apikey",
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			By("Creating a HomeAssistantSecrets with key filtering")
			haSecrets := &hav1.HomeAssistantSecrets{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace,
				},
				Spec: hav1.HomeAssistantSecretsSpec{
					HomeAssistantRef: hav1.HomeAssistantReference{
						Name: haName,
					},
					SecretRefs: []hav1.SecretKeyReference{
						{
							Name: secretName,
							Keys: []string{"mqtt_user", "mqtt_password"}, // Only 2 of 4 keys
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, haSecrets)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &HomeAssistantSecretsReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying only specified keys are included")
			Eventually(func(g Gomega) {
				generatedSecret := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-generated-secrets",
					Namespace: namespace,
				}, generatedSecret)).To(Succeed())

				secretsYaml := string(generatedSecret.Data["secrets.yaml"])
				g.Expect(secretsYaml).To(ContainSubstring("mqtt_user"))
				g.Expect(secretsYaml).To(ContainSubstring("mqtt_password"))
				g.Expect(secretsYaml).NotTo(ContainSubstring("db_password"))
				g.Expect(secretsYaml).NotTo(ContainSubstring("api_key"))
			}, timeout, interval).Should(Succeed())
		})

		It("should include all keys when keys list is empty", func() {
			By("Creating a source Secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
				},
				StringData: map[string]string{
					"key1": "value1",
					"key2": "value2",
					"key3": "value3",
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			By("Creating a HomeAssistantSecrets without key filtering")
			haSecrets := &hav1.HomeAssistantSecrets{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace,
				},
				Spec: hav1.HomeAssistantSecretsSpec{
					HomeAssistantRef: hav1.HomeAssistantReference{
						Name: haName,
					},
					SecretRefs: []hav1.SecretKeyReference{
						{
							Name: secretName,
							// No Keys specified
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, haSecrets)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &HomeAssistantSecretsReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying all keys are included")
			Eventually(func(g Gomega) {
				generatedSecret := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-generated-secrets",
					Namespace: namespace,
				}, generatedSecret)).To(Succeed())

				secretsYaml := string(generatedSecret.Data["secrets.yaml"])
				g.Expect(secretsYaml).To(ContainSubstring("key1"))
				g.Expect(secretsYaml).To(ContainSubstring("key2"))
				g.Expect(secretsYaml).To(ContainSubstring("key3"))
			}, timeout, interval).Should(Succeed())
		})

		It("should update hash when source secret changes", func() {
			By("Creating a source Secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
				},
				StringData: map[string]string{
					"test_key": "initial_value",
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			By("Creating a HomeAssistantSecrets")
			haSecrets := &hav1.HomeAssistantSecrets{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace,
				},
				Spec: hav1.HomeAssistantSecretsSpec{
					HomeAssistantRef: hav1.HomeAssistantReference{
						Name: haName,
					},
					SecretRefs: []hav1.SecretKeyReference{
						{
							Name: secretName,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, haSecrets)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &HomeAssistantSecretsReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Getting initial hash")
			var initialHash string
			Eventually(func(g Gomega) {
				updatedHASecrets := &hav1.HomeAssistantSecrets{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				}, updatedHASecrets)).To(Succeed())
				initialHash = updatedHASecrets.Status.SecretsHash
				g.Expect(initialHash).NotTo(BeEmpty())
			}, timeout, interval).Should(Succeed())

			By("Updating source Secret")
			Eventually(func() error {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      secretName,
					Namespace: namespace,
				}, secret)
				if err != nil {
					return err
				}
				secret.StringData = map[string]string{
					"test_key": "updated_value",
				}
				return k8sClient.Update(ctx, secret)
			}, timeout, interval).Should(Succeed())

			By("Reconciling after update")
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying hash changed")
			Eventually(func(g Gomega) {
				updatedHASecrets := &hav1.HomeAssistantSecrets{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				}, updatedHASecrets)).To(Succeed())
				g.Expect(updatedHASecrets.Status.SecretsHash).NotTo(Equal(initialHash))
			}, timeout, interval).Should(Succeed())
		})

		It("should set owner reference on generated Secret", func() {
			By("Creating a source Secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
				},
				StringData: map[string]string{
					"test_key": "test_value",
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			By("Creating a HomeAssistantSecrets")
			haSecrets := &hav1.HomeAssistantSecrets{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace,
				},
				Spec: hav1.HomeAssistantSecretsSpec{
					HomeAssistantRef: hav1.HomeAssistantReference{
						Name: haName,
					},
					SecretRefs: []hav1.SecretKeyReference{
						{
							Name: secretName,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, haSecrets)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &HomeAssistantSecretsReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying owner reference is set")
			Eventually(func(g Gomega) {
				generatedSecret := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-generated-secrets",
					Namespace: namespace,
				}, generatedSecret)).To(Succeed())
				g.Expect(generatedSecret.OwnerReferences).To(HaveLen(1))
				g.Expect(generatedSecret.OwnerReferences[0].Kind).To(Equal("HomeAssistantSecrets"))
				g.Expect(generatedSecret.OwnerReferences[0].Name).To(Equal(resourceName))
			}, timeout, interval).Should(Succeed())
		})

		It("should set status to not ready when HomeAssistant is missing", func() {
			By("Creating a HomeAssistantSecrets without HomeAssistant")
			haSecrets := &hav1.HomeAssistantSecrets{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace,
				},
				Spec: hav1.HomeAssistantSecretsSpec{
					HomeAssistantRef: hav1.HomeAssistantReference{
						Name: "non-existent-ha",
					},
					SecretRefs: []hav1.SecretKeyReference{
						{
							Name: secretName,
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, haSecrets)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &HomeAssistantSecretsReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				},
			})
			// Expect requeue, not error
			Expect(err).NotTo(HaveOccurred())

			By("Verifying status is not ready")
			Eventually(func(g Gomega) {
				updatedHASecrets := &hav1.HomeAssistantSecrets{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				}, updatedHASecrets)).To(Succeed())

				condition := meta.FindStatusCondition(updatedHASecrets.Status.Conditions, "Ready")
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(condition.Reason).To(Equal("InvalidConfiguration"))
			}, timeout, interval).Should(Succeed())
		})

		It("should handle AutoRestart=false correctly", func() {
			By("Creating a source Secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
				},
				StringData: map[string]string{
					"test_key": "test_value",
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			By("Creating a HomeAssistantSecrets with AutoRestart=false")
			haSecrets := &hav1.HomeAssistantSecrets{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace,
				},
				Spec: hav1.HomeAssistantSecretsSpec{
					HomeAssistantRef: hav1.HomeAssistantReference{
						Name: haName,
					},
					SecretRefs: []hav1.SecretKeyReference{
						{
							Name: secretName,
						},
					},
					AutoRestart: ptr.To(false),
				},
			}
			Expect(k8sClient.Create(ctx, haSecrets)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &HomeAssistantSecretsReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying generated Secret was created despite AutoRestart=false")
			Eventually(func(g Gomega) {
				generatedSecret := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-generated-secrets",
					Namespace: namespace,
				}, generatedSecret)).To(Succeed())
			}, timeout, interval).Should(Succeed())
		})

		It("should skip missing keys and continue processing other keys", func() {
			By("Creating a source Secret with some keys")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
				},
				StringData: map[string]string{
					"existing_key1": "value1",
					"existing_key2": "value2",
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			By("Creating a HomeAssistantSecrets requesting both existing and missing keys")
			haSecrets := &hav1.HomeAssistantSecrets{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace,
				},
				Spec: hav1.HomeAssistantSecretsSpec{
					HomeAssistantRef: hav1.HomeAssistantReference{
						Name: haName,
					},
					SecretRefs: []hav1.SecretKeyReference{
						{
							Name: secretName,
							Keys: []string{"existing_key1", "missing_key", "existing_key2"},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, haSecrets)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &HomeAssistantSecretsReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying only existing keys are in generated Secret")
			Eventually(func(g Gomega) {
				generatedSecret := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-generated-secrets",
					Namespace: namespace,
				}, generatedSecret)).To(Succeed())

				secretsYaml := string(generatedSecret.Data["secrets.yaml"])
				g.Expect(secretsYaml).To(ContainSubstring("existing_key1"))
				g.Expect(secretsYaml).To(ContainSubstring("existing_key2"))
				g.Expect(secretsYaml).NotTo(ContainSubstring("missing_key"))
			}, timeout, interval).Should(Succeed())

			By("Verifying reconciliation succeeded")
			Eventually(func(g Gomega) {
				updatedHASecrets := &hav1.HomeAssistantSecrets{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				}, updatedHASecrets)).To(Succeed())

				condition := meta.FindStatusCondition(updatedHASecrets.Status.Conditions, "Ready")
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			}, timeout, interval).Should(Succeed())
		})

		It("should return error when source Secret is missing", func() {
			By("Creating a HomeAssistantSecrets without creating the source Secret")
			haSecrets := &hav1.HomeAssistantSecrets{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace,
				},
				Spec: hav1.HomeAssistantSecretsSpec{
					HomeAssistantRef: hav1.HomeAssistantReference{
						Name: haName,
					},
					SecretRefs: []hav1.SecretKeyReference{
						{
							Name: "non-existent-secret",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, haSecrets)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &HomeAssistantSecretsReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				},
			})
			Expect(err).To(HaveOccurred())

			By("Verifying status is not ready with appropriate error")
			Eventually(func(g Gomega) {
				updatedHASecrets := &hav1.HomeAssistantSecrets{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				}, updatedHASecrets)).To(Succeed())

				condition := meta.FindStatusCondition(updatedHASecrets.Status.Conditions, "Ready")
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(condition.Reason).To(Equal("ReconciliationFailed"))
				g.Expect(condition.Message).To(ContainSubstring("not found"))
			}, timeout, interval).Should(Succeed())
		})

		It("should update StatefulSet annotation when AutoRestart=true and secrets change", func() {
			By("Creating a source Secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
				},
				StringData: map[string]string{
					"test_key": "initial_value",
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			By("Creating a StatefulSet for the HomeAssistant")
			sts := createTestStatefulSet(haName, namespace)
			Expect(k8sClient.Create(ctx, sts)).To(Succeed())

			By("Creating a HomeAssistantSecrets with AutoRestart=true (default)")
			haSecrets := &hav1.HomeAssistantSecrets{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace,
				},
				Spec: hav1.HomeAssistantSecretsSpec{
					HomeAssistantRef: hav1.HomeAssistantReference{
						Name: haName,
					},
					SecretRefs: []hav1.SecretKeyReference{
						{
							Name: secretName,
						},
					},
					// AutoRestart defaults to true when nil
				},
			}
			Expect(k8sClient.Create(ctx, haSecrets)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &HomeAssistantSecretsReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying StatefulSet annotation was updated")
			var initialHash string
			Eventually(func(g Gomega) {
				updatedSts := &appsv1.StatefulSet{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName,
					Namespace: namespace,
				}, updatedSts)).To(Succeed())

				g.Expect(updatedSts.Spec.Template.Annotations).NotTo(BeNil())
				hash, ok := updatedSts.Spec.Template.Annotations["ha.homeassistant.io/secrets-hash"]
				g.Expect(ok).To(BeTrue())
				g.Expect(hash).NotTo(BeEmpty())
				initialHash = hash
			}, timeout, interval).Should(Succeed())

			By("Updating the source Secret")
			Eventually(func() error {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      secretName,
					Namespace: namespace,
				}, secret)
				if err != nil {
					return err
				}
				secret.StringData = map[string]string{
					"test_key": "updated_value",
				}
				return k8sClient.Update(ctx, secret)
			}, timeout, interval).Should(Succeed())

			By("Reconciling after secret change")
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying StatefulSet annotation was updated with new hash")
			Eventually(func(g Gomega) {
				updatedSts := &appsv1.StatefulSet{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName,
					Namespace: namespace,
				}, updatedSts)).To(Succeed())

				newHash := updatedSts.Spec.Template.Annotations["ha.homeassistant.io/secrets-hash"]
				g.Expect(newHash).NotTo(BeEmpty())
				g.Expect(newHash).NotTo(Equal(initialHash))
			}, timeout, interval).Should(Succeed())
		})

		It("should NOT update StatefulSet annotation when AutoRestart=false", func() {
			By("Creating a source Secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
				},
				StringData: map[string]string{
					"test_key": "initial_value",
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			By("Creating a StatefulSet for the HomeAssistant")
			sts := createTestStatefulSet(haName, namespace)
			Expect(k8sClient.Create(ctx, sts)).To(Succeed())

			By("Creating a HomeAssistantSecrets with AutoRestart=false")
			haSecrets := &hav1.HomeAssistantSecrets{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace,
				},
				Spec: hav1.HomeAssistantSecretsSpec{
					HomeAssistantRef: hav1.HomeAssistantReference{
						Name: haName,
					},
					SecretRefs: []hav1.SecretKeyReference{
						{
							Name: secretName,
						},
					},
					AutoRestart: ptr.To(false),
				},
			}
			Expect(k8sClient.Create(ctx, haSecrets)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &HomeAssistantSecretsReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying StatefulSet annotation was NOT updated")
			Consistently(func(g Gomega) {
				updatedSts := &appsv1.StatefulSet{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName,
					Namespace: namespace,
				}, updatedSts)).To(Succeed())

				// Either annotations is nil or doesn't contain the hash key
				if updatedSts.Spec.Template.Annotations != nil {
					_, hasHash := updatedSts.Spec.Template.Annotations["ha.homeassistant.io/secrets-hash"]
					g.Expect(hasHash).To(BeFalse())
				}
			}, time.Second*2, interval).Should(Succeed())
		})

		It("should gracefully handle missing StatefulSet when AutoRestart is enabled", func() {
			By("Creating a source Secret")
			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: namespace,
				},
				StringData: map[string]string{
					"test_key": "test_value",
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())

			By("Creating a HomeAssistantSecrets WITHOUT creating StatefulSet")
			haSecrets := &hav1.HomeAssistantSecrets{
				ObjectMeta: metav1.ObjectMeta{
					Name:      resourceName,
					Namespace: namespace,
				},
				Spec: hav1.HomeAssistantSecretsSpec{
					HomeAssistantRef: hav1.HomeAssistantReference{
						Name: haName,
					},
					SecretRefs: []hav1.SecretKeyReference{
						{
							Name: secretName,
						},
					},
					AutoRestart: ptr.To(true),
				},
			}
			Expect(k8sClient.Create(ctx, haSecrets)).To(Succeed())

			By("Reconciling the resource")
			reconciler := &HomeAssistantSecretsReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				},
			})
			// Should not return error - StatefulSet update is best-effort
			Expect(err).NotTo(HaveOccurred())

			By("Verifying reconciliation succeeded despite missing StatefulSet")
			Eventually(func(g Gomega) {
				updatedHASecrets := &hav1.HomeAssistantSecrets{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      resourceName,
					Namespace: namespace,
				}, updatedHASecrets)).To(Succeed())

				condition := meta.FindStatusCondition(updatedHASecrets.Status.Conditions, "Ready")
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			}, timeout, interval).Should(Succeed())

			By("Verifying generated Secret was still created")
			Eventually(func(g Gomega) {
				generatedSecret := &corev1.Secret{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-generated-secrets",
					Namespace: namespace,
				}, generatedSecret)).To(Succeed())
			}, timeout, interval).Should(Succeed())
		})
	})
})

// createTestStatefulSet creates a minimal StatefulSet for testing
func createTestStatefulSet(name, namespace string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": name,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": name,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "homeassistant",
							Image: "ghcr.io/home-assistant/home-assistant:stable",
						},
					},
				},
			},
		},
	}
}
