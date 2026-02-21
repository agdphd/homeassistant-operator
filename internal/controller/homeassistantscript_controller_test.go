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
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hav1alpha1 "github.com/przemekhys/homeassistant-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ = Describe("HomeAssistantScript Controller", func() {
	const (
		timeout  = time.Second * 10
		interval = time.Millisecond * 250
	)

	var (
		ctx        context.Context
		namespace  string
		reconciler *HomeAssistantScriptReconciler
	)

	// Helper to create test script
	createTestScript := func(
		name, haRef, alias string,
		sequence []hav1alpha1.ScriptAction,
	) *hav1alpha1.HomeAssistantScript {
		return &hav1alpha1.HomeAssistantScript{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: hav1alpha1.HomeAssistantScriptSpec{
				HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: haRef},
				Alias:            alias,
				Sequence:         sequence,
			},
		}
	}

	// Helper to create test action
	createTestAction := func(data map[string]interface{}) hav1alpha1.ScriptAction {
		raw, err := json.Marshal(data)
		Expect(err).NotTo(HaveOccurred())
		return hav1alpha1.ScriptAction{
			RawExtension: runtime.RawExtension{Raw: raw},
		}
	}

	// Helper to reconcile
	reconcileScript := func(name string) (reconcile.Result, error) {
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

		reconciler = &HomeAssistantScriptReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: events.NewFakeRecorder(100),
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
				Spec: hav1alpha1.HomeAssistantSpec{},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())

			// Wait for HA to be created
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      ha.Name,
					Namespace: ha.Namespace,
				}, ha)
			}, timeout, interval).Should(Succeed())
		})

		AfterEach(func() {
			// Cleanup
			_ = k8sClient.Delete(ctx, ha)
		})

		It("should create ConfigMap when script is created", func() {
			actions := []hav1alpha1.ScriptAction{
				createTestAction(map[string]interface{}{
					"service": "notify.mobile_app",
					"data": map[string]interface{}{
						"message": "Test",
					},
				}),
			}

			script := createTestScript("test-script", ha.Name, "Test Script", actions)
			Expect(k8sClient.Create(ctx, script)).To(Succeed())

			// Reconcile
			result, err := reconcileScript(script.Name)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeZero())

			// Verify ConfigMap was created
			cm := &corev1.ConfigMap{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      ha.Name + generatedScriptsSuffix,
					Namespace: namespace,
				}, cm)
			}, timeout, interval).Should(Succeed())

			// Verify ConfigMap contains script
			Expect(cm.Data).To(HaveKey(scriptsYamlKey))
			Expect(cm.Data[scriptsYamlKey]).To(ContainSubstring("Test Script"))
			Expect(cm.Data[scriptsYamlKey]).To(ContainSubstring("notify.mobile_app"))

			// Cleanup
			_ = k8sClient.Delete(ctx, script)
		})

		It("should aggregate multiple scripts into one ConfigMap", func() {
			actions := []hav1alpha1.ScriptAction{
				createTestAction(map[string]interface{}{
					"service": "light.turn_on",
				}),
			}

			script1 := createTestScript("script-1", ha.Name, "Script One", actions)
			script2 := createTestScript("script-2", ha.Name, "Script Two", actions)

			Expect(k8sClient.Create(ctx, script1)).To(Succeed())
			Expect(k8sClient.Create(ctx, script2)).To(Succeed())

			// Reconcile both
			_, err := reconcileScript(script1.Name)
			Expect(err).NotTo(HaveOccurred())
			_, err = reconcileScript(script2.Name)
			Expect(err).NotTo(HaveOccurred())

			// Verify single ConfigMap contains both
			cm := &corev1.ConfigMap{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      ha.Name + generatedScriptsSuffix,
					Namespace: namespace,
				}, cm)
			}, timeout, interval).Should(Succeed())

			yamlContent := cm.Data[scriptsYamlKey]
			Expect(yamlContent).To(ContainSubstring("Script One"))
			Expect(yamlContent).To(ContainSubstring("Script Two"))

			// Cleanup
			_ = k8sClient.Delete(ctx, script1)
			_ = k8sClient.Delete(ctx, script2)
		})

		It("should handle script with fields (input parameters)", func() {
			fieldSpec := hav1alpha1.ScriptField{}
			raw, _ := json.Marshal(map[string]interface{}{
				"description": "Message to send",
				"example":     "Hello",
			})
			fieldSpec.Raw = raw

			actions := []hav1alpha1.ScriptAction{
				createTestAction(map[string]interface{}{
					"service": "notify.mobile_app",
					"data": map[string]interface{}{
						"message": "{{ message }}",
					},
				}),
			}

			script := &hav1alpha1.HomeAssistantScript{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "script-with-fields",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantScriptSpec{
					HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: ha.Name},
					Alias:            "Script With Fields",
					Fields: map[string]hav1alpha1.ScriptField{
						"message": fieldSpec,
					},
					Sequence: actions,
				},
			}

			Expect(k8sClient.Create(ctx, script)).To(Succeed())

			// Reconcile
			_, err := reconcileScript(script.Name)
			Expect(err).NotTo(HaveOccurred())

			// Verify ConfigMap contains fields
			cm := &corev1.ConfigMap{}
			Eventually(func() error {
				return k8sClient.Get(ctx, types.NamespacedName{
					Name:      ha.Name + generatedScriptsSuffix,
					Namespace: namespace,
				}, cm)
			}, timeout, interval).Should(Succeed())

			yamlContent := cm.Data[scriptsYamlKey]
			Expect(yamlContent).To(ContainSubstring("fields"))
			Expect(yamlContent).To(ContainSubstring("message"))

			// Cleanup
			_ = k8sClient.Delete(ctx, script)
		})

		It("should handle different script modes", func() {
			actions := []hav1alpha1.ScriptAction{
				createTestAction(map[string]interface{}{
					"delay": map[string]interface{}{
						"seconds": 1,
					},
				}),
			}

			modes := []hav1alpha1.ScriptMode{
				hav1alpha1.ScriptModeSingle,
				hav1alpha1.ScriptModeRestart,
				hav1alpha1.ScriptModeQueued,
				hav1alpha1.ScriptModeParallel,
			}

			for _, mode := range modes {
				script := &hav1alpha1.HomeAssistantScript{
					ObjectMeta: metav1.ObjectMeta{
						Name:      string(mode) + "-script",
						Namespace: namespace,
					},
					Spec: hav1alpha1.HomeAssistantScriptSpec{
						HomeAssistantRef: hav1alpha1.HomeAssistantReference{Name: ha.Name},
						Alias:            string(mode) + " Script",
						Mode:             mode,
						Sequence:         actions,
					},
				}

				Expect(k8sClient.Create(ctx, script)).To(Succeed())
				_, err := reconcileScript(script.Name)
				Expect(err).NotTo(HaveOccurred())

				// Verify ConfigMap contains mode
				cm := &corev1.ConfigMap{}
				Eventually(func() error {
					if err := k8sClient.Get(ctx, types.NamespacedName{
						Name:      ha.Name + generatedScriptsSuffix,
						Namespace: namespace,
					}, cm); err != nil {
						return err
					}
					if cm.Data[scriptsYamlKey] == "" {
						return fmt.Errorf("scriptsYamlKey is empty")
					}
					return nil
				}, timeout, interval).Should(Succeed())

				yamlContent := cm.Data[scriptsYamlKey]
				Expect(yamlContent).To(ContainSubstring(string(mode)))

				// Cleanup: delete then reconcile to trigger finalizer removal
				_ = k8sClient.Delete(ctx, script)
				_, _ = reconcileScript(script.Name)
			}
		})
	})

	Context("Status Updates", func() {
		var ha *hav1alpha1.HomeAssistant

		BeforeEach(func() {
			ha = &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-status",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSpec{},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, ha)
		})

		It("should set Ready condition after successful reconciliation", func() {
			actions := []hav1alpha1.ScriptAction{
				createTestAction(map[string]interface{}{
					"service": "test.service",
				}),
			}

			script := createTestScript("status-script", ha.Name, "Status Test", actions)
			Expect(k8sClient.Create(ctx, script)).To(Succeed())

			// Reconcile
			_, err := reconcileScript(script.Name)
			Expect(err).NotTo(HaveOccurred())

			// Check status
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      script.Name,
					Namespace: namespace,
				}, script); err != nil {
					return false
				}

				for _, cond := range script.Status.Conditions {
					if cond.Type == conditionTypeReady && cond.Status == metav1.ConditionTrue {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			// Cleanup
			_ = k8sClient.Delete(ctx, script)
		})
	})

	Context("Finalizer Handling", func() {
		var ha *hav1alpha1.HomeAssistant

		BeforeEach(func() {
			ha = &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-finalizer",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSpec{},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())
		})

		AfterEach(func() {
			_ = k8sClient.Delete(ctx, ha)
		})

		It("should add finalizer on creation", func() {
			actions := []hav1alpha1.ScriptAction{
				createTestAction(map[string]interface{}{
					"service": "test.service",
				}),
			}

			script := createTestScript("finalizer-script", ha.Name, "Finalizer Test", actions)
			Expect(k8sClient.Create(ctx, script)).To(Succeed())

			// Reconcile
			_, err := reconcileScript(script.Name)
			Expect(err).NotTo(HaveOccurred())

			// Verify finalizer was added
			Eventually(func() bool {
				if err := k8sClient.Get(ctx, types.NamespacedName{
					Name:      script.Name,
					Namespace: namespace,
				}, script); err != nil {
					return false
				}

				for _, f := range script.Finalizers {
					if f == scriptFinalizerName {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			// Cleanup
			_ = k8sClient.Delete(ctx, script)
		})
	})
})

// Test helper functions
var _ = Describe("HomeAssistantScript Helper Functions", func() {
	var reconciler *HomeAssistantScriptReconciler

	BeforeEach(func() {
		reconciler = &HomeAssistantScriptReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: events.NewFakeRecorder(100),
		}
	})

	Context("scriptToYaml", func() {
		It("should convert script with basic fields", func() {
			actions := []hav1alpha1.ScriptAction{
				{RawExtension: runtime.RawExtension{
					Raw: []byte(`{"service":"light.turn_on"}`),
				}},
			}

			script := &hav1alpha1.HomeAssistantScript{
				Spec: hav1alpha1.HomeAssistantScriptSpec{
					Alias:       "Test Script",
					Description: "Test description",
					Icon:        "mdi:script",
					Mode:        hav1alpha1.ScriptModeSingle,
					Sequence:    actions,
				},
			}

			yaml, err := reconciler.scriptToYaml(script)
			Expect(err).NotTo(HaveOccurred())
			Expect(yaml).To(HaveKey("alias"))
			Expect(yaml["alias"]).To(Equal("Test Script"))
			Expect(yaml).To(HaveKey("description"))
			Expect(yaml).To(HaveKey("icon"))
			Expect(yaml).To(HaveKey("mode"))
			Expect(yaml).To(HaveKey("sequence"))
		})
	})

	Context("calculateScriptHash", func() {
		It("should generate consistent hash for same script", func() {
			actions := []hav1alpha1.ScriptAction{
				{RawExtension: runtime.RawExtension{
					Raw: []byte(`{"service":"test.service"}`),
				}},
			}

			script := &hav1alpha1.HomeAssistantScript{
				Spec: hav1alpha1.HomeAssistantScriptSpec{
					Alias:    "Hash Test",
					Sequence: actions,
				},
			}

			hash1, err1 := reconciler.calculateScriptHash(script)
			hash2, err2 := reconciler.calculateScriptHash(script)

			Expect(err1).NotTo(HaveOccurred())
			Expect(err2).NotTo(HaveOccurred())
			Expect(hash1).To(Equal(hash2))
			Expect(hash1).NotTo(BeEmpty())
		})
	})
})
