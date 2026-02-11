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
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hav1alpha1 "github.com/przemekhys/homeassistant-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("HomeAssistantScene Controller", func() {
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	var (
		ctx        context.Context
		namespace  string
		reconciler *HomeAssistantSceneReconciler
	)

	// Helper to create test scene
	createTestScene := func(
		name, haRef, sceneName string,
		entities []hav1alpha1.SceneEntity,
	) *hav1alpha1.HomeAssistantScene {
		return &hav1alpha1.HomeAssistantScene{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: hav1alpha1.HomeAssistantSceneSpec{
				HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: haRef},
				Name:             sceneName,
				Entities:         entities,
			},
		}
	}

	// Helper to create test entity
	createTestEntity := func(entityID, state string, attrs map[string]interface{}) hav1alpha1.SceneEntity {
		entity := hav1alpha1.SceneEntity{
			EntityID: entityID,
			State:    state,
		}
		if attrs != nil {
			raw, _ := json.Marshal(attrs)
			entity.Attributes = runtime.RawExtension{Raw: raw}
		}
		return entity
	}

	// Helper to reconcile
	reconcileScene := func(name string) (reconcile.Result, error) {
		return reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      name,
				Namespace: namespace,
			},
		})
	}

	BeforeEach(func() {
		ctx = context.Background()
		namespace = "default"

		reconciler = &HomeAssistantSceneReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
	})

	Context("ConfigMap Aggregation", func() {
		var ha *hav1alpha1.HomeAssistant

		BeforeEach(func() {
			ha = &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Image:   "homeassistant/home-assistant:latest",
					Version: "2024.1.0",
				},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())
		})

		AfterEach(func() {
			// Cleanup HA
			_ = k8sClient.Delete(ctx, ha)
		})

		It("should create ConfigMap with single scene", func() {
			scene := createTestScene(
				"test-scene-single",
				"test-ha",
				"Movie Night",
				[]hav1alpha1.SceneEntity{
					createTestEntity("light.living_room", "on", map[string]interface{}{"brightness": 30}),
					createTestEntity("cover.blinds", "closed", nil),
				},
			)

			Expect(k8sClient.Create(ctx, scene)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, scene) }()

			// Reconcile
			_, err := reconcileScene(scene.Name)
			Expect(err).NotTo(HaveOccurred())

			// Check ConfigMap created
			configMapName := "test-ha-scenes"
			cm := &corev1.ConfigMap{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      configMapName,
					Namespace: namespace,
				}, cm)
			}, timeout, interval).Should(Succeed())

			// Verify ConfigMap data
			Expect(cm.Data).To(HaveKey("scenes.yaml"))
			Expect(cm.Data["scenes.yaml"]).To(ContainSubstring("id: test-scene-single"))
			Expect(cm.Data["scenes.yaml"]).To(ContainSubstring("name: Movie Night"))
			Expect(cm.Data["scenes.yaml"]).To(ContainSubstring("light.living_room"))
		})

		It("should aggregate multiple scenes into single ConfigMap", func() {
			scene1 := createTestScene(
				"scene-one",
				"test-ha",
				"Scene One",
				[]hav1alpha1.SceneEntity{
					createTestEntity("light.room1", "on", nil),
				},
			)
			scene2 := createTestScene(
				"scene-two",
				"test-ha",
				"Scene Two",
				[]hav1alpha1.SceneEntity{
					createTestEntity("light.room2", "off", nil),
				},
			)

			Expect(k8sClient.Create(ctx, scene1)).To(Succeed())
			Expect(k8sClient.Create(ctx, scene2)).To(Succeed())
			defer func() {
				_ = k8sClient.Delete(ctx, scene1)
				_ = k8sClient.Delete(ctx, scene2)
			}()

			// Reconcile both
			_, err := reconcileScene(scene1.Name)
			Expect(err).NotTo(HaveOccurred())
			_, err = reconcileScene(scene2.Name)
			Expect(err).NotTo(HaveOccurred())

			// Check ConfigMap contains both
			cm := &corev1.ConfigMap{}
			Eventually(func() string {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-ha-scenes",
					Namespace: namespace,
				}, cm)
				if err != nil {
					return ""
				}
				return cm.Data["scenes.yaml"]
			}, timeout, interval).Should(And(
				ContainSubstring("scene-one"),
				ContainSubstring("scene-two"),
			))
		})

		It("should regenerate ConfigMap when scene is updated", func() {
			scene := createTestScene(
				"scene-update",
				"test-ha",
				"Original Name",
				[]hav1alpha1.SceneEntity{
					createTestEntity("light.test", "on", nil),
				},
			)

			Expect(k8sClient.Create(ctx, scene)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, scene) }()

			// Initial reconcile
			_, err := reconcileScene(scene.Name)
			Expect(err).NotTo(HaveOccurred())

			// Verify initial ConfigMap
			cm := &corev1.ConfigMap{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-ha-scenes",
					Namespace: namespace,
				}, cm)
				return cm.Data["scenes.yaml"]
			}, timeout, interval).Should(ContainSubstring("Original Name"))

			// Update scene
			Eventually(func() error {
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      scene.Name,
					Namespace: namespace,
				}, scene); err != nil {
					return err
				}
				scene.Spec.Name = "Updated Name"
				return k8sClient.Update(ctx, scene)
			}, timeout, interval).Should(Succeed())

			// Reconcile again
			_, err = reconcileScene(scene.Name)
			Expect(err).NotTo(HaveOccurred())

			// Verify ConfigMap updated
			Eventually(func() string {
				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-ha-scenes",
					Namespace: namespace,
				}, cm)
				return cm.Data["scenes.yaml"]
			}, timeout, interval).Should(ContainSubstring("Updated Name"))
		})

		It("should remove scene from ConfigMap when deleted (finalizer)", func() {
			// Create dedicated HA for this test to avoid interference
			haDelete := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-delete",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Image:   "homeassistant/home-assistant:latest",
					Version: "2024.1.0",
				},
			}
			Expect(k8sClient.Create(ctx, haDelete)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, haDelete) }()

			scene1 := createTestScene(
				"scene-keep",
				"test-ha-delete",
				"Keep",
				[]hav1alpha1.SceneEntity{
					createTestEntity("light.keep", "on", nil),
				},
			)
			scene2 := createTestScene(
				"scene-delete",
				"test-ha-delete",
				"Delete",
				[]hav1alpha1.SceneEntity{
					createTestEntity("light.delete", "on", nil),
				},
			)

			Expect(k8sClient.Create(ctx, scene1)).To(Succeed())
			Expect(k8sClient.Create(ctx, scene2)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, scene1) }()

			// Reconcile both
			_, err := reconcileScene(scene1.Name)
			Expect(err).NotTo(HaveOccurred())
			_, err = reconcileScene(scene2.Name)
			Expect(err).NotTo(HaveOccurred())

			// Verify both in ConfigMap
			cm := &corev1.ConfigMap{}
			Eventually(func() string {
				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-ha-delete-scenes",
					Namespace: namespace,
				}, cm)
				return cm.Data["scenes.yaml"]
			}, timeout, interval).Should(And(
				ContainSubstring("scene-keep"),
				ContainSubstring("scene-delete"),
			))

			// Delete scene2
			Expect(k8sClient.Delete(ctx, scene2)).To(Succeed())

			// Wait for deletion to trigger finalizer reconciliation
			time.Sleep(time.Second)
			_, err = reconcileScene(scene2.Name)
			Expect(err).NotTo(HaveOccurred())

			// Verify only scene1 remains
			Eventually(func() string {
				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-ha-delete-scenes",
					Namespace: namespace,
				}, cm)
				return cm.Data["scenes.yaml"]
			}, timeout, interval).Should(And(
				ContainSubstring("scene-keep"),
				Not(ContainSubstring("scene-delete")),
			))
		})

		It("should set owner reference to HomeAssistant CR (not scene)", func() {
			scene := createTestScene(
				"scene-owner",
				"test-ha",
				"Owner Test",
				[]hav1alpha1.SceneEntity{
					createTestEntity("light.test", "on", nil),
				},
			)

			Expect(k8sClient.Create(ctx, scene)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, scene) }()

			// Reconcile
			_, err := reconcileScene(scene.Name)
			Expect(err).NotTo(HaveOccurred())

			// Check ConfigMap owner reference
			cm := &corev1.ConfigMap{}
			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-ha-scenes",
					Namespace: namespace,
				}, cm)
				if err != nil {
					return false
				}
				// Owner should be HomeAssistant, not Scene
				if len(cm.OwnerReferences) == 0 {
					return false
				}
				return cm.OwnerReferences[0].Kind == "HomeAssistant" &&
					cm.OwnerReferences[0].Name == "test-ha"
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("YAML Conversion", func() {
		It("should convert scene to YAML with all fields", func() {
			scene := &hav1alpha1.HomeAssistantScene{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-scene",
				},
				Spec: hav1alpha1.HomeAssistantSceneSpec{
					ID:   "custom_id",
					Name: "Test Scene",
					Icon: "mdi:test",
					Entities: []hav1alpha1.SceneEntity{
						createTestEntity("light.test", "on", map[string]interface{}{
							"brightness": 100,
							"color_temp": 400,
						}),
					},
				},
			}

			yamlMap, err := reconciler.sceneToYaml(scene)
			Expect(err).NotTo(HaveOccurred())
			Expect(yamlMap).To(HaveKey("id"))
			Expect(yamlMap["id"]).To(Equal("custom_id"))
			Expect(yamlMap).To(HaveKey("name"))
			Expect(yamlMap["name"]).To(Equal("Test Scene"))
			Expect(yamlMap).To(HaveKey("icon"))
			Expect(yamlMap["icon"]).To(Equal("mdi:test"))
			Expect(yamlMap).To(HaveKey("entities"))

			// Check entities format (map, not list)
			entities := yamlMap["entities"].(map[string]interface{})
			Expect(entities).To(HaveKey("light.test"))
			lightData := entities["light.test"].(map[string]interface{})
			Expect(lightData["state"]).To(Equal("on"))
			Expect(lightData["brightness"]).To(Equal(100.0))
			Expect(lightData["color_temp"]).To(Equal(400.0))
		})

		It("should omit optional fields when not set", func() {
			scene := &hav1alpha1.HomeAssistantScene{
				ObjectMeta: metav1.ObjectMeta{
					Name: "minimal-scene",
				},
				Spec: hav1alpha1.HomeAssistantSceneSpec{
					Entities: []hav1alpha1.SceneEntity{
						createTestEntity("light.test", "on", nil),
					},
				},
			}

			yamlMap, err := reconciler.sceneToYaml(scene)
			Expect(err).NotTo(HaveOccurred())
			Expect(yamlMap).To(HaveKey("id")) // Auto-generated from CR name
			Expect(yamlMap).NotTo(HaveKey("name"))
			Expect(yamlMap).NotTo(HaveKey("icon"))
		})

		It("should auto-generate ID from CR name", func() {
			scene := &hav1alpha1.HomeAssistantScene{
				ObjectMeta: metav1.ObjectMeta{
					Name: "auto-id-scene",
				},
				Spec: hav1alpha1.HomeAssistantSceneSpec{
					Entities: []hav1alpha1.SceneEntity{
						createTestEntity("light.test", "on", nil),
					},
				},
			}

			yamlMap, err := reconciler.sceneToYaml(scene)
			Expect(err).NotTo(HaveOccurred())
			Expect(yamlMap["id"]).To(Equal("auto-id-scene"))
		})

		It("should handle entities with state only (no attributes)", func() {
			scene := &hav1alpha1.HomeAssistantScene{
				ObjectMeta: metav1.ObjectMeta{
					Name: "state-only-scene",
				},
				Spec: hav1alpha1.HomeAssistantSceneSpec{
					Entities: []hav1alpha1.SceneEntity{
						{
							EntityID: "light.simple",
							State:    "off",
						},
					},
				},
			}

			yamlMap, err := reconciler.sceneToYaml(scene)
			Expect(err).NotTo(HaveOccurred())

			entities := yamlMap["entities"].(map[string]interface{})
			Expect(entities).To(HaveKey("light.simple"))
			lightData := entities["light.simple"].(map[string]interface{})
			Expect(lightData).To(HaveKey("state"))
			Expect(lightData["state"]).To(Equal("off"))
			// Only state, no other attributes
			Expect(lightData).To(HaveLen(1))
		})
	})

	Context("Hash Tracking", func() {
		It("should calculate stable hash (idempotent)", func() {
			scene := &hav1alpha1.HomeAssistantScene{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hash-test",
				},
				Spec: hav1alpha1.HomeAssistantSceneSpec{
					Entities: []hav1alpha1.SceneEntity{
						createTestEntity("light.test", "on", map[string]interface{}{"brightness": 50}),
					},
				},
			}

			hash1, err := reconciler.calculateSceneHash(scene)
			Expect(err).NotTo(HaveOccurred())

			hash2, err := reconciler.calculateSceneHash(scene)
			Expect(err).NotTo(HaveOccurred())

			Expect(hash1).To(Equal(hash2))
		})

		It("should change hash when entities change", func() {
			scene := &hav1alpha1.HomeAssistantScene{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hash-change-test",
				},
				Spec: hav1alpha1.HomeAssistantSceneSpec{
					Entities: []hav1alpha1.SceneEntity{
						createTestEntity("light.test", "on", nil),
					},
				},
			}

			hash1, err := reconciler.calculateSceneHash(scene)
			Expect(err).NotTo(HaveOccurred())

			// Change entities
			scene.Spec.Entities = []hav1alpha1.SceneEntity{
				createTestEntity("light.test", "off", nil),
			}

			hash2, err := reconciler.calculateSceneHash(scene)
			Expect(err).NotTo(HaveOccurred())

			Expect(hash1).NotTo(Equal(hash2))
		})

		It("should change hash when entity attributes change", func() {
			scene := &hav1alpha1.HomeAssistantScene{
				ObjectMeta: metav1.ObjectMeta{
					Name: "hash-attr-test",
				},
				Spec: hav1alpha1.HomeAssistantSceneSpec{
					Entities: []hav1alpha1.SceneEntity{
						createTestEntity("light.test", "on", map[string]interface{}{"brightness": 30}),
					},
				},
			}

			hash1, err := reconciler.calculateSceneHash(scene)
			Expect(err).NotTo(HaveOccurred())

			// Change attribute
			scene.Spec.Entities = []hav1alpha1.SceneEntity{
				createTestEntity("light.test", "on", map[string]interface{}{"brightness": 50}),
			}

			hash2, err := reconciler.calculateSceneHash(scene)
			Expect(err).NotTo(HaveOccurred())

			Expect(hash1).NotTo(Equal(hash2))
		})
	})

	Context("Hot-Reload", func() {
		var ha *hav1alpha1.HomeAssistant

		BeforeEach(func() {
			ha = &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-reload",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Image:   "homeassistant/home-assistant:latest",
					Version: "2024.1.0",
				},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, ha)
		})

		It("should skip reload when autoReload=false", func() {
			autoReload := false
			scene := &hav1alpha1.HomeAssistantScene{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scene-no-reload",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSceneSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "test-ha-reload"},
					AutoReload:       &autoReload,
					Entities: []hav1alpha1.SceneEntity{
						createTestEntity("light.test", "on", nil),
					},
				},
			}

			Expect(k8sClient.Create(ctx, scene)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, scene) }()

			// Reconcile
			_, err := reconcileScene(scene.Name)
			Expect(err).NotTo(HaveOccurred())

			// Check status - LastError should be empty (graceful skip)
			Eventually(func() string {
				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      scene.Name,
					Namespace: namespace,
				}, scene)
				return scene.Status.LastError
			}, timeout, interval).Should(BeEmpty())
		})

		It("should skip reload gracefully when API token missing", func() {
			scene := &hav1alpha1.HomeAssistantScene{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scene-no-token",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSceneSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "test-ha-reload"},
					Entities: []hav1alpha1.SceneEntity{
						createTestEntity("light.test", "on", nil),
					},
				},
			}

			Expect(k8sClient.Create(ctx, scene)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, scene) }()

			// Reconcile (no token secret exists)
			_, err := reconcileScene(scene.Name)
			Expect(err).NotTo(HaveOccurred())

			// Check status - should have error about missing token
			Eventually(func() string {
				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      scene.Name,
					Namespace: namespace,
				}, scene)
				return scene.Status.LastError
			}, timeout, interval).Should(ContainSubstring("API token not found"))
		})
	})

	Context("Validation", func() {
		It("should set condition when HomeAssistant not found", func() {
			scene := &hav1alpha1.HomeAssistantScene{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scene-no-ha",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSceneSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "non-existent"},
					Entities: []hav1alpha1.SceneEntity{
						createTestEntity("light.test", "on", nil),
					},
				},
			}

			Expect(k8sClient.Create(ctx, scene)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, scene) }()

			// Reconcile
			result, err := reconcileScene(scene.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(30 * time.Second))

			// Check status condition
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      scene.Name,
					Namespace: namespace,
				}, scene)
				for _, cond := range scene.Status.Conditions {
					if cond.Type == conditionTypeReady && cond.Status == metav1.ConditionFalse {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("Status Conditions", func() {
		var ha *hav1alpha1.HomeAssistant

		BeforeEach(func() {
			ha = &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-status",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Image:   "homeassistant/home-assistant:latest",
					Version: "2024.1.0",
				},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, ha)
		})

		It("should set Ready=True when successful", func() {
			scene := &hav1alpha1.HomeAssistantScene{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scene-ready",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSceneSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "test-ha-status"},
					Entities: []hav1alpha1.SceneEntity{
						createTestEntity("light.test", "on", nil),
					},
				},
			}

			Expect(k8sClient.Create(ctx, scene)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, scene) }()

			// Reconcile
			_, err := reconcileScene(scene.Name)
			Expect(err).NotTo(HaveOccurred())

			// Check status condition
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      scene.Name,
					Namespace: namespace,
				}, scene)
				for _, cond := range scene.Status.Conditions {
					if cond.Type == conditionTypeReady && cond.Status == metav1.ConditionTrue {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())
		})

		It("should update ObservedGeneration", func() {
			scene := &hav1alpha1.HomeAssistantScene{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scene-generation",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSceneSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "test-ha-status"},
					Entities: []hav1alpha1.SceneEntity{
						createTestEntity("light.test", "on", nil),
					},
				},
			}

			Expect(k8sClient.Create(ctx, scene)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, scene) }()

			// Reconcile
			_, err := reconcileScene(scene.Name)
			Expect(err).NotTo(HaveOccurred())

			// Check ObservedGeneration matches metadata.generation
			Eventually(func() bool {
				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      scene.Name,
					Namespace: namespace,
				}, scene)
				return scene.Status.ObservedGeneration == scene.Generation
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("Idempotency", func() {
		var ha *hav1alpha1.HomeAssistant

		BeforeEach(func() {
			ha = &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-idempotent",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Image:   "homeassistant/home-assistant:latest",
					Version: "2024.1.0",
				},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, ha)
		})

		It("should not update ConfigMap on repeated reconciles (deep equality)", func() {
			scene := &hav1alpha1.HomeAssistantScene{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "scene-idempotent",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSceneSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: "test-ha-idempotent"},
					Entities: []hav1alpha1.SceneEntity{
						createTestEntity("light.test", "on", nil),
					},
				},
			}

			Expect(k8sClient.Create(ctx, scene)).To(Succeed())
			defer func() { _ = k8sClient.Delete(ctx, scene) }()

			// Initial reconcile
			_, err := reconcileScene(scene.Name)
			Expect(err).NotTo(HaveOccurred())

			// Get ConfigMap resourceVersion
			cm := &corev1.ConfigMap{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-ha-idempotent-scenes",
					Namespace: namespace,
				}, cm)
			}, timeout, interval).Should(Succeed())

			initialRV := cm.ResourceVersion

			// Reconcile again (no changes)
			_, err = reconcileScene(scene.Name)
			Expect(err).NotTo(HaveOccurred())

			// ResourceVersion should NOT change
			Consistently(func() string {
				_ = k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-ha-idempotent-scenes",
					Namespace: namespace,
				}, cm)
				return cm.ResourceVersion
			}, time.Second*2, interval).Should(Equal(initialRV))
		})
	})

	Context("SetupWithManager", func() {
		It("should setup controller successfully", func() {
			// Create a test manager (simplified version)
			mgr, err := ctrl.NewManager(cfg, ctrl.Options{
				Scheme: k8sClient.Scheme(),
			})
			Expect(err).NotTo(HaveOccurred())

			// Setup controller
			reconciler := &HomeAssistantSceneReconciler{
				Client: mgr.GetClient(),
				Scheme: mgr.GetScheme(),
			}
			err = reconciler.SetupWithManager(mgr)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
