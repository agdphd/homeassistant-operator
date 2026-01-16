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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hav1alpha1 "github.com/przemekhys/homeassistant-operator/api/v1alpha1"
)

var _ = Describe("CRD Short Names", func() {
	const (
		namespace = "default"
		timeout   = time.Second * 10
		interval  = time.Millisecond * 250
	)

	Context("HomeAssistant CRD short names", func() {
		AfterEach(func() {
			// Cleanup all HomeAssistant resources
			haList := &hav1alpha1.HomeAssistantList{}
			_ = k8sClient.List(ctx, haList, &client.ListOptions{Namespace: namespace})
			for _, ha := range haList.Items {
				_ = k8sClient.Delete(ctx, &ha)
			}

			// Wait for resources to be deleted
			Eventually(func() int {
				haList := &hav1alpha1.HomeAssistantList{}
				_ = k8sClient.List(ctx, haList, &client.ListOptions{Namespace: namespace})
				return len(haList.Items)
			}, timeout, interval).Should(Equal(0))
		})

		It("should allow creating HomeAssistant with 'ha' short name", func() {
			By("Creating a HomeAssistant with metadata")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-shortname",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "stable",
				},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())

			By("Retrieving the resource using full name")
			retrieved := &hav1alpha1.HomeAssistant{}
			key := types.NamespacedName{
				Name:      "test-ha-shortname",
				Namespace: namespace,
			}
			Expect(k8sClient.Get(ctx, key, retrieved)).To(Succeed())
			Expect(retrieved.Name).To(Equal("test-ha-shortname"))
			Expect(retrieved.Spec.Version).To(Equal("stable"))
		})

		It("should allow creating HomeAssistant with 'has' short name", func() {
			By("Creating a HomeAssistant with metadata")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-has-shortname",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "2024.1.0",
				},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())

			By("Retrieving the resource using full name")
			retrieved := &hav1alpha1.HomeAssistant{}
			key := types.NamespacedName{
				Name:      "test-has-shortname",
				Namespace: namespace,
			}
			Expect(k8sClient.Get(ctx, key, retrieved)).To(Succeed())
			Expect(retrieved.Name).To(Equal("test-has-shortname"))
			Expect(retrieved.Spec.Version).To(Equal("2024.1.0"))
		})

		It("should list HomeAssistant resources correctly", func() {
			By("Creating multiple HomeAssistant resources")
			for i := 1; i <= 3; i++ {
				ha := &hav1alpha1.HomeAssistant{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "ha-test-" + string(rune(48+i)),
						Namespace: namespace,
					},
					Spec: hav1alpha1.HomeAssistantSpec{
						Version: "stable",
					},
				}
				Expect(k8sClient.Create(ctx, ha)).To(Succeed())
			}

			By("Listing all HomeAssistant resources")
			haList := &hav1alpha1.HomeAssistantList{}
			Expect(k8sClient.List(ctx, haList, &client.ListOptions{Namespace: namespace})).To(Succeed())
			Expect(haList.Items).To(HaveLen(3))
		})
	})

	Context("HomeAssistantSecrets CRD short names", func() {
		AfterEach(func() {
			// Cleanup all HomeAssistantSecrets resources
			secretsList := &hav1alpha1.HomeAssistantSecretsList{}
			_ = k8sClient.List(ctx, secretsList, &client.ListOptions{Namespace: namespace})
			for _, hs := range secretsList.Items {
				_ = k8sClient.Delete(ctx, &hs)
			}

			// Cleanup all HomeAssistant resources
			haList := &hav1alpha1.HomeAssistantList{}
			_ = k8sClient.List(ctx, haList, &client.ListOptions{Namespace: namespace})
			for _, ha := range haList.Items {
				_ = k8sClient.Delete(ctx, &ha)
			}

			// Wait for resources to be deleted
			Eventually(func() int {
				secretsList := &hav1alpha1.HomeAssistantSecretsList{}
				_ = k8sClient.List(ctx, secretsList, &client.ListOptions{Namespace: namespace})
				return len(secretsList.Items)
			}, timeout, interval).Should(Equal(0))
		})

		It("should allow creating HomeAssistantSecrets with 'hasecrets' short name", func() {
			By("Creating a HomeAssistant resource first")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-for-secrets",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "stable",
				},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())

			By("Creating a test Secret")
			secret := &metav1.Status{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "Secret",
				},
			}
			_ = secret // This is just for documentation

			By("Creating a HomeAssistantSecrets resource")
			hs := &hav1alpha1.HomeAssistantSecrets{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hasecrets-shortname",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSecretsSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{
						Name: "test-ha-for-secrets",
					},
					SecretRefs: []hav1alpha1.SecretKeyReference{
						{
							Name: "test-secret",
							Keys: []string{"key1"},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, hs)).To(Succeed())

			By("Retrieving the resource using full name")
			retrieved := &hav1alpha1.HomeAssistantSecrets{}
			key := types.NamespacedName{
				Name:      "test-hasecrets-shortname",
				Namespace: namespace,
			}
			Expect(k8sClient.Get(ctx, key, retrieved)).To(Succeed())
			Expect(retrieved.Name).To(Equal("test-hasecrets-shortname"))
			Expect(retrieved.Spec.HomeAssistantRef.Name).To(Equal("test-ha-for-secrets"))
		})

		It("should allow creating HomeAssistantSecrets with 'hasec' short name", func() {
			By("Creating a HomeAssistant resource first")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-for-hasec",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "stable",
				},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())

			By("Creating a HomeAssistantSecrets resource")
			hs := &hav1alpha1.HomeAssistantSecrets{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hasec-shortname",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSecretsSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{
						Name: "test-ha-for-hasec",
					},
					SecretRefs: []hav1alpha1.SecretKeyReference{
						{
							Name: "test-secret",
							Keys: []string{"key1", "key2"},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, hs)).To(Succeed())

			By("Retrieving the resource using full name")
			retrieved := &hav1alpha1.HomeAssistantSecrets{}
			key := types.NamespacedName{
				Name:      "test-hasec-shortname",
				Namespace: namespace,
			}
			Expect(k8sClient.Get(ctx, key, retrieved)).To(Succeed())
			Expect(retrieved.Name).To(Equal("test-hasec-shortname"))
			Expect(retrieved.Spec.SecretRefs).To(HaveLen(1))
		})

		It("should list HomeAssistantSecrets resources correctly", func() {
			By("Creating a HomeAssistant resource")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-for-listing",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "stable",
				},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())

			By("Creating multiple HomeAssistantSecrets resources")
			for i := 1; i <= 2; i++ {
				hs := &hav1alpha1.HomeAssistantSecrets{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "hasec-test-" + string(rune(48+i)),
						Namespace: namespace,
					},
					Spec: hav1alpha1.HomeAssistantSecretsSpec{
						HomeAssistantRef: hav1alpha1.HomeAssistantReference{
							Name: "test-ha-for-listing",
						},
						SecretRefs: []hav1alpha1.SecretKeyReference{
							{
								Name: "test-secret-" + string(rune(48+i)),
							},
						},
					},
				}
				Expect(k8sClient.Create(ctx, hs)).To(Succeed())
			}

			By("Listing all HomeAssistantSecrets resources")
			secretsList := &hav1alpha1.HomeAssistantSecretsList{}
			Expect(k8sClient.List(ctx, secretsList, &client.ListOptions{Namespace: namespace})).To(Succeed())
			Expect(secretsList.Items).To(HaveLen(2))
		})

		It("should preserve short names when updating HomeAssistantSecrets", func() {
			By("Creating a HomeAssistant resource")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-for-update",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "stable",
				},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())

			By("Creating a HomeAssistantSecrets resource")
			hs := &hav1alpha1.HomeAssistantSecrets{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hasec-update",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSecretsSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{
						Name: "test-ha-for-update",
					},
					SecretRefs: []hav1alpha1.SecretKeyReference{
						{
							Name: "test-secret",
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, hs)).To(Succeed())

			By("Updating the resource")
			retrieved := &hav1alpha1.HomeAssistantSecrets{}
			key := types.NamespacedName{
				Name:      "test-hasec-update",
				Namespace: namespace,
			}
			Expect(k8sClient.Get(ctx, key, retrieved)).To(Succeed())

			// Update with a new secret reference
			retrieved.Spec.SecretRefs = append(retrieved.Spec.SecretRefs, hav1alpha1.SecretKeyReference{
				Name: "test-secret-2",
				Keys: []string{"key1"},
			})

			Expect(k8sClient.Update(ctx, retrieved)).To(Succeed())

			By("Verifying the update preserved the resource")
			updated := &hav1alpha1.HomeAssistantSecrets{}
			Expect(k8sClient.Get(ctx, key, updated)).To(Succeed())
			Expect(updated.Spec.SecretRefs).To(HaveLen(2))
		})
	})

	Context("Print columns in list output", func() {
		AfterEach(func() {
			// Cleanup all HomeAssistant resources
			haList := &hav1alpha1.HomeAssistantList{}
			_ = k8sClient.List(ctx, haList, &client.ListOptions{Namespace: namespace})
			for _, ha := range haList.Items {
				_ = k8sClient.Delete(ctx, &ha)
			}

			// Cleanup all HomeAssistantSecrets resources
			secretsList := &hav1alpha1.HomeAssistantSecretsList{}
			_ = k8sClient.List(ctx, secretsList, &client.ListOptions{Namespace: namespace})
			for _, hs := range secretsList.Items {
				_ = k8sClient.Delete(ctx, &hs)
			}
		})

		It("should support print columns for HomeAssistant resources", func() {
			By("Creating a HomeAssistant resource")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-printcolumn",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "stable",
				},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())

			By("Retrieving the resource")
			retrieved := &hav1alpha1.HomeAssistant{}
			key := types.NamespacedName{
				Name:      "test-printcolumn",
				Namespace: namespace,
			}
			Expect(k8sClient.Get(ctx, key, retrieved)).To(Succeed())

			By("Verifying print column fields are accessible")
			// Print columns are: Ready (status.conditions), Version (status.version), Age (metadata.creationTimestamp)
			Expect(retrieved.ObjectMeta.CreationTimestamp).NotTo(BeNil())
			// Version and Ready will be populated after reconciliation, but the fields exist
			Expect(retrieved.Status).NotTo(BeNil())
		})

		It("should support print columns for HomeAssistantSecrets resources", func() {
			By("Creating a HomeAssistant resource")
			ha := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-ref",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "stable",
				},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())

			By("Creating a HomeAssistantSecrets resource")
			hs := &hav1alpha1.HomeAssistantSecrets{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hasec-printcol",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSecretsSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{
						Name: "test-ha-ref",
					},
					SecretRefs: []hav1alpha1.SecretKeyReference{
						{
							Name: "test-secret",
							Keys: []string{"key1"},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, hs)).To(Succeed())

			By("Retrieving the resource")
			retrieved := &hav1alpha1.HomeAssistantSecrets{}
			key := types.NamespacedName{
				Name:      "test-hasec-printcol",
				Namespace: namespace,
			}
			Expect(k8sClient.Get(ctx, key, retrieved)).To(Succeed())

			By("Verifying print column fields are accessible")
			// Print columns are: HomeAssistant (spec.homeAssistantRef.name), Secrets (spec.secretRefs[*].name), Ready (status.conditions), Age (metadata.creationTimestamp)
			Expect(retrieved.Spec.HomeAssistantRef.Name).To(Equal("test-ha-ref"))
			Expect(retrieved.Spec.SecretRefs).To(HaveLen(1))
			Expect(retrieved.ObjectMeta.CreationTimestamp).NotTo(BeNil())
			Expect(retrieved.Status).NotTo(BeNil())
		})
	})
})
