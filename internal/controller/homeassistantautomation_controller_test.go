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
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hav1alpha1 "github.com/przemekhys/homeassistant-operator/api/v1alpha1"
)

var _ = Describe("HomeAssistantAutomation Controller", func() {
	const (
		haName    = "test-ha-auto"
		namespace = "default"
		timeout   = time.Second * 10
		interval  = time.Millisecond * 250
	)

	var reconciler *HomeAssistantAutomationReconciler

	// Helper to create a test automation CR
	createTestAutomation := func(
		name string,
		haRef string,
		alias string,
		triggers []runtime.RawExtension,
		actions []runtime.RawExtension,
	) *hav1alpha1.HomeAssistantAutomation {
		triggerList := make([]hav1alpha1.AutomationTrigger, len(triggers))
		for i, t := range triggers {
			triggerList[i] = hav1alpha1.AutomationTrigger{RawExtension: t}
		}
		actionList := make([]hav1alpha1.AutomationAction, len(actions))
		for i, a := range actions {
			actionList[i] = hav1alpha1.AutomationAction{RawExtension: a}
		}
		return &hav1alpha1.HomeAssistantAutomation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: hav1alpha1.HomeAssistantAutomationSpec{
				HomeAssistantRef: hav1alpha1.HomeAssistantReference{
					Name: haRef,
				},
				Alias:    alias,
				Triggers: triggerList,
				Actions:  actionList,
			},
		}
	}

	// Helper for simple trigger/action raw extensions
	rawExt := func(data map[string]interface{}) runtime.RawExtension {
		raw, err := json.Marshal(data)
		Expect(err).NotTo(HaveOccurred())
		return runtime.RawExtension{Raw: raw}
	}

	// Default trigger and action
	defaultTrigger := func() runtime.RawExtension {
		return rawExt(map[string]interface{}{"platform": "sun", "event": "sunset"})
	}

	defaultAction := func() runtime.RawExtension {
		return rawExt(map[string]interface{}{
			"service": "light.turn_on",
			"target":  map[string]interface{}{"entity_id": "light.living_room"},
		})
	}

	reconcileAutomation := func(name string) (reconcile.Result, error) {
		return reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      name,
				Namespace: namespace,
			},
		})
	}

	// reconcileAutomationTwice calls reconcile twice - first for finalizer, second for actual reconciliation
	// This is needed because after adding finalizer, the reconciler returns early
	reconcileAutomationTwice := func(name string) error {
		// First reconcile - adds finalizer
		_, err := reconcileAutomation(name)
		if err != nil {
			return err
		}
		// Second reconcile - actual reconciliation
		_, err = reconcileAutomation(name)
		return err
	}

	BeforeEach(func() {
		reconciler = &HomeAssistantAutomationReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			Recorder: events.NewFakeRecorder(100),
		}

		// Create HomeAssistant resource
		ha := &hav1alpha1.HomeAssistant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      haName,
				Namespace: namespace,
			},
			Spec: hav1alpha1.HomeAssistantSpec{
				Version: "stable",
			},
		}
		Expect(k8sClient.Create(ctx, ha)).To(Succeed())
	})

	AfterEach(func() {
		// Cleanup HomeAssistantAutomation resources first (before HA)
		autoList := &hav1alpha1.HomeAssistantAutomationList{}
		_ = k8sClient.List(ctx, autoList, &client.ListOptions{Namespace: namespace})
		for _, auto := range autoList.Items {
			autoToDelete := auto // Create a copy for the closure
			_ = k8sClient.Delete(ctx, &autoToDelete)
			// Trigger reconcile to handle finalizer cleanup
			_, _ = reconcileAutomation(autoToDelete.Name)
		}

		// Wait for automations to be fully deleted
		Eventually(func() int {
			autoList := &hav1alpha1.HomeAssistantAutomationList{}
			_ = k8sClient.List(ctx, autoList, &client.ListOptions{Namespace: namespace})
			return len(autoList.Items)
		}, timeout, interval).Should(Equal(0))

		// Cleanup ConfigMaps
		cmList := &corev1.ConfigMapList{}
		_ = k8sClient.List(ctx, cmList, &client.ListOptions{Namespace: namespace})
		for _, cm := range cmList.Items {
			_ = k8sClient.Delete(ctx, &cm)
		}

		// Cleanup Secrets
		secretList := &corev1.SecretList{}
		_ = k8sClient.List(ctx, secretList, &client.ListOptions{Namespace: namespace})
		for _, secret := range secretList.Items {
			_ = k8sClient.Delete(ctx, &secret)
		}

		// Cleanup HomeAssistant last (after automations are gone)
		haList := &hav1alpha1.HomeAssistantList{}
		_ = k8sClient.List(ctx, haList, &client.ListOptions{Namespace: namespace})
		for _, ha := range haList.Items {
			_ = k8sClient.Delete(ctx, &ha)
		}
	})

	Context("When validating HomeAssistant reference", func() {
		It("should set Ready=False when referenced HomeAssistant does not exist", func() {
			By("Creating an automation referencing non-existent HA")
			auto := createTestAutomation("auto-no-ha", "non-existent-ha", "Test Auto",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling the resource")
			// First reconcile - adds finalizer
			_, err := reconcileAutomation("auto-no-ha")
			Expect(err).NotTo(HaveOccurred())
			// Second reconcile - validates HA ref and sets RequeueAfter
			result, err := reconcileAutomation("auto-no-ha")
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(5 * time.Second))

			By("Verifying status is Ready=False with InvalidAutomation reason")
			Eventually(func(g Gomega) {
				updated := &hav1alpha1.HomeAssistantAutomation{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "auto-no-ha", Namespace: namespace}, updated)).To(Succeed())
				condition := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(condition.Reason).To(Equal(reasonInvalidAutomation))
				g.Expect(condition.Message).To(ContainSubstring("not found"))
			}, timeout, interval).Should(Succeed())
		})

		It("should set Ready=True when HomeAssistant exists", func() {
			By("Creating an automation referencing existing HA")
			auto := createTestAutomation("auto-with-ha", haName, "Test Auto",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling the resource")
			Expect(reconcileAutomationTwice("auto-with-ha")).To(Succeed())

			By("Verifying status is Ready=True")
			Eventually(func(g Gomega) {
				updated := &hav1alpha1.HomeAssistantAutomation{}
				g.Expect(k8sClient.Get(
					ctx,
					types.NamespacedName{Name: "auto-with-ha", Namespace: namespace},
					updated,
				)).To(Succeed())
				condition := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(condition.Reason).To(Equal(reasonAutomationGenerated))
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("When generating ConfigMap (aggregation)", func() {
		It("should create ConfigMap with a single automation", func() {
			By("Creating one automation")
			auto := createTestAutomation("auto-single", haName, "Sunset Lights",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling")
			Expect(reconcileAutomationTwice("auto-single")).To(Succeed())

			By("Verifying ConfigMap was created with automation content")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				g.Expect(cm.Data).To(HaveKey("automations.yaml"))
				yamlContent := cm.Data["automations.yaml"]
				g.Expect(yamlContent).To(ContainSubstring("alias: Sunset Lights"))
				g.Expect(yamlContent).To(ContainSubstring("platform: sun"))
				g.Expect(yamlContent).To(ContainSubstring("light.turn_on"))
			}, timeout, interval).Should(Succeed())
		})

		It("should aggregate two automations into one ConfigMap", func() {
			By("Creating first automation")
			auto1 := createTestAutomation("auto-agg-1", haName, "Sunrise Lights",
				[]runtime.RawExtension{rawExt(map[string]interface{}{"platform": "sun", "event": "sunrise"})},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto1)).To(Succeed())

			By("Creating second automation")
			auto2 := createTestAutomation("auto-agg-2", haName, "Motion Sensor",
				[]runtime.RawExtension{rawExt(map[string]interface{}{
					"platform": "state", "entity_id": "binary_sensor.motion",
				})},
				[]runtime.RawExtension{rawExt(map[string]interface{}{
					"service": "notify.notify",
					"data":    map[string]interface{}{"message": "Motion detected"},
				})},
			)
			Expect(k8sClient.Create(ctx, auto2)).To(Succeed())

			By("Reconciling both automations")
			Expect(reconcileAutomationTwice("auto-agg-1")).To(Succeed())
			Expect(reconcileAutomationTwice("auto-agg-2")).To(Succeed())

			By("Verifying ConfigMap contains both automations")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				yamlContent := cm.Data["automations.yaml"]
				g.Expect(yamlContent).To(ContainSubstring("Sunrise Lights"))
				g.Expect(yamlContent).To(ContainSubstring("Motion Sensor"))
			}, timeout, interval).Should(Succeed())
		})

		It("should skip disabled automations in ConfigMap", func() {
			By("Creating an enabled automation")
			auto1 := createTestAutomation("auto-enabled", haName, "Enabled Auto",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto1)).To(Succeed())

			By("Creating a disabled automation")
			auto2 := createTestAutomation("auto-disabled", haName, "Disabled Auto",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			auto2.Spec.Enabled = ptr.To(false)
			Expect(k8sClient.Create(ctx, auto2)).To(Succeed())

			By("Reconciling both")
			Expect(reconcileAutomationTwice("auto-enabled")).To(Succeed())
			Expect(reconcileAutomationTwice("auto-disabled")).To(Succeed())

			By("Verifying ConfigMap contains only the enabled automation")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				yamlContent := cm.Data["automations.yaml"]
				g.Expect(yamlContent).To(ContainSubstring("Enabled Auto"))
				g.Expect(yamlContent).NotTo(ContainSubstring("Disabled Auto"))
			}, timeout, interval).Should(Succeed())
		})

		It("should update ConfigMap when automation spec changes (two-phase)", func() {
			By("Creating an automation")
			auto := createTestAutomation("auto-update", haName, "Original Alias",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling (phase 1)")
			Expect(reconcileAutomationTwice("auto-update")).To(Succeed())

			By("Capturing initial ConfigMap content")
			var initialContent string
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				initialContent = cm.Data["automations.yaml"]
				g.Expect(initialContent).To(ContainSubstring("Original Alias"))
			}, timeout, interval).Should(Succeed())

			By("Updating the automation alias")
			Eventually(func() error {
				updated := &hav1alpha1.HomeAssistantAutomation{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: "auto-update", Namespace: namespace}, updated); err != nil {
					return err
				}
				updated.Spec.Alias = "Updated Alias"
				return k8sClient.Update(ctx, updated)
			}, timeout, interval).Should(Succeed())

			By("Reconciling (phase 2)")
			Expect(reconcileAutomationTwice("auto-update")).To(Succeed())

			By("Verifying ConfigMap content changed")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				g.Expect(cm.Data["automations.yaml"]).To(ContainSubstring("Updated Alias"))
				g.Expect(cm.Data["automations.yaml"]).NotTo(ContainSubstring("Original Alias"))
			}, timeout, interval).Should(Succeed())
		})

		It("should set owner reference to HomeAssistant on ConfigMap", func() {
			By("Creating an automation")
			auto := createTestAutomation("auto-owner", haName, "Owner Test",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling")
			Expect(reconcileAutomationTwice("auto-owner")).To(Succeed())

			By("Verifying owner reference points to HomeAssistant")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				g.Expect(cm.OwnerReferences).To(HaveLen(1))
				g.Expect(cm.OwnerReferences[0].Kind).To(Equal("HomeAssistant"))
				g.Expect(cm.OwnerReferences[0].Name).To(Equal(haName))
			}, timeout, interval).Should(Succeed())
		})

		It("should auto-generate ID from CR name when spec.id is not set", func() {
			By("Creating an automation without explicit ID")
			auto := createTestAutomation("my-sunset-automation", haName, "Sunset Lights",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling")
			Expect(reconcileAutomationTwice("my-sunset-automation")).To(Succeed())

			By("Verifying ID in ConfigMap equals CR name")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				g.Expect(cm.Data["automations.yaml"]).To(ContainSubstring("id: my-sunset-automation"))
			}, timeout, interval).Should(Succeed())
		})

		It("should use explicit spec.id when set", func() {
			By("Creating an automation with explicit ID")
			auto := createTestAutomation("auto-explicit-id", haName, "Custom ID Auto",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			auto.Spec.ID = "custom-automation-id"
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling")
			Expect(reconcileAutomationTwice("auto-explicit-id")).To(Succeed())

			By("Verifying ID in ConfigMap uses explicit ID")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				g.Expect(cm.Data["automations.yaml"]).To(ContainSubstring("id: custom-automation-id"))
				g.Expect(cm.Data["automations.yaml"]).NotTo(ContainSubstring("id: auto-explicit-id"))
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("When converting automationToYaml", func() {
		It("should correctly convert triggers, conditions, and actions", func() {
			By("Creating automation with trigger, condition, and action")
			auto := createTestAutomation("auto-full", haName, "Full Auto",
				[]runtime.RawExtension{
					rawExt(map[string]interface{}{
						"platform": "sun", "event": "sunset", "offset": "-00:30:00",
					}),
				},
				[]runtime.RawExtension{
					rawExt(map[string]interface{}{
						"service": "light.turn_on",
						"target":  map[string]interface{}{"entity_id": "light.porch"},
					}),
				},
			)
			auto.Spec.Conditions = []hav1alpha1.AutomationCondition{
				{RawExtension: rawExt(map[string]interface{}{
					"condition": "state",
					"entity_id": "input_boolean.guest_mode",
					"state":     "on",
				})},
			}
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling")
			Expect(reconcileAutomationTwice("auto-full")).To(Succeed())

			By("Verifying all sections are present in ConfigMap")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				yamlContent := cm.Data["automations.yaml"]
				g.Expect(yamlContent).To(ContainSubstring("trigger:"))
				g.Expect(yamlContent).To(ContainSubstring("platform: sun"))
				g.Expect(yamlContent).To(ContainSubstring("condition:"))
				g.Expect(yamlContent).To(ContainSubstring("input_boolean.guest_mode"))
				g.Expect(yamlContent).To(ContainSubstring("action:"))
				g.Expect(yamlContent).To(ContainSubstring("light.turn_on"))
			}, timeout, interval).Should(Succeed())
		})

		It("should omit conditions key when no conditions specified", func() {
			By("Creating automation without conditions")
			auto := createTestAutomation("auto-no-cond", haName, "No Conditions",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling")
			Expect(reconcileAutomationTwice("auto-no-cond")).To(Succeed())

			By("Verifying conditions key is absent")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				yamlContent := cm.Data["automations.yaml"]
				g.Expect(yamlContent).NotTo(ContainSubstring("conditions:"))
			}, timeout, interval).Should(Succeed())
		})

		It("should map Go field names to Home Assistant format", func() {
			By("Creating automation with maxExceeded and initialState")
			auto := createTestAutomation("auto-field-map", haName, "Field Mapping",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			auto.Spec.MaxExceeded = "error"
			auto.Spec.InitialState = ptr.To(true)
			auto.Spec.Mode = hav1alpha1.AutomationModeParallel
			max := int32(5)
			auto.Spec.Max = &max
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling")
			Expect(reconcileAutomationTwice("auto-field-map")).To(Succeed())

			By("Verifying snake_case field names in YAML")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				yamlContent := cm.Data["automations.yaml"]
				g.Expect(yamlContent).To(ContainSubstring("max_exceeded: error"))
				g.Expect(yamlContent).To(ContainSubstring("initial_state: true"))
				g.Expect(yamlContent).To(ContainSubstring("mode: parallel"))
				g.Expect(yamlContent).To(ContainSubstring("max: 5"))
			}, timeout, interval).Should(Succeed())
		})

		It("should omit optional fields when not set and include kubebuilder defaults", func() {
			By("Creating automation with minimal fields")
			auto := createTestAutomation("auto-minimal", haName, "Minimal Auto",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			// No description, initialState explicitly set
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling")
			Expect(reconcileAutomationTwice("auto-minimal")).To(Succeed())

			By("Verifying truly optional fields are absent and kubebuilder defaults are present")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				yamlContent := cm.Data["automations.yaml"]
				// Truly optional - no kubebuilder default
				g.Expect(yamlContent).NotTo(ContainSubstring("description:"))
				g.Expect(yamlContent).NotTo(ContainSubstring("initial_state:"))
				// Kubebuilder defaults are applied by API server
				g.Expect(yamlContent).To(ContainSubstring("mode: single"))
				g.Expect(yamlContent).To(ContainSubstring("max_exceeded: warning"))
				g.Expect(yamlContent).To(ContainSubstring("max: 10"))
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("When tracking hash and change detection", func() {
		It("should produce a stable hash for the same spec (idempotent)", func() {
			By("Creating an automation")
			auto := createTestAutomation("auto-hash-stable", haName, "Stable Hash",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling twice")
			Expect(reconcileAutomationTwice("auto-hash-stable")).To(Succeed())

			var firstHash string
			Eventually(func(g Gomega) {
				updated := &hav1alpha1.HomeAssistantAutomation{}
				g.Expect(k8sClient.Get(
					ctx,
					types.NamespacedName{Name: "auto-hash-stable", Namespace: namespace},
					updated,
				)).To(Succeed())
				firstHash = updated.Status.AutomationHash
				g.Expect(firstHash).NotTo(BeEmpty())
			}, timeout, interval).Should(Succeed())

			Expect(reconcileAutomationTwice("auto-hash-stable")).To(Succeed())

			By("Verifying hash did not change")
			Eventually(func(g Gomega) {
				updated := &hav1alpha1.HomeAssistantAutomation{}
				g.Expect(k8sClient.Get(
					ctx,
					types.NamespacedName{Name: "auto-hash-stable", Namespace: namespace},
					updated,
				)).To(Succeed())
				g.Expect(updated.Status.AutomationHash).To(Equal(firstHash))
			}, timeout, interval).Should(Succeed())
		})

		It("should change hash when triggers change", func() {
			By("Creating an automation")
			auto := createTestAutomation("auto-hash-change", haName, "Hash Change",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling (phase 1)")
			Expect(reconcileAutomationTwice("auto-hash-change")).To(Succeed())

			var initialHash string
			Eventually(func(g Gomega) {
				updated := &hav1alpha1.HomeAssistantAutomation{}
				g.Expect(k8sClient.Get(
					ctx,
					types.NamespacedName{Name: "auto-hash-change", Namespace: namespace},
					updated,
				)).To(Succeed())
				initialHash = updated.Status.AutomationHash
				g.Expect(initialHash).NotTo(BeEmpty())
			}, timeout, interval).Should(Succeed())

			By("Updating triggers")
			Eventually(func() error {
				updated := &hav1alpha1.HomeAssistantAutomation{}
				if err := k8sClient.Get(
					ctx,
					types.NamespacedName{Name: "auto-hash-change", Namespace: namespace},
					updated,
				); err != nil {
					return err
				}
				updated.Spec.Triggers = []hav1alpha1.AutomationTrigger{
					{RawExtension: rawExt(map[string]interface{}{"platform": "time", "at": "06:00:00"})},
				}
				return k8sClient.Update(ctx, updated)
			}, timeout, interval).Should(Succeed())

			By("Reconciling (phase 2)")
			Expect(reconcileAutomationTwice("auto-hash-change")).To(Succeed())

			By("Verifying hash changed")
			Eventually(func(g Gomega) {
				updated := &hav1alpha1.HomeAssistantAutomation{}
				g.Expect(k8sClient.Get(
					ctx,
					types.NamespacedName{Name: "auto-hash-change", Namespace: namespace},
					updated,
				)).To(Succeed())
				g.Expect(updated.Status.AutomationHash).NotTo(Equal(initialHash))
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("When handling hot-reload (autoReload)", func() {
		It("should skip reload when autoReload is disabled", func() {
			By("Creating an automation with autoReload=false")
			auto := createTestAutomation("auto-no-reload", haName, "No Reload",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			auto.Spec.AutoReload = ptr.To(false)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling")
			Expect(reconcileAutomationTwice("auto-no-reload")).To(Succeed())

			By("Verifying no lastReloadTime and no lastError")
			Eventually(func(g Gomega) {
				updated := &hav1alpha1.HomeAssistantAutomation{}
				g.Expect(k8sClient.Get(
					ctx,
					types.NamespacedName{Name: "auto-no-reload", Namespace: namespace},
					updated,
				)).To(Succeed())
				g.Expect(updated.Status.LastReloadTime).To(BeNil())
				g.Expect(updated.Status.LastError).To(BeEmpty())
				condition := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			}, timeout, interval).Should(Succeed())
		})

		It("should gracefully handle missing API token", func() {
			By("Creating an automation with autoReload=true (default)")
			auto := createTestAutomation("auto-no-token", haName, "No Token",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling (no API token secret exists)")
			Expect(reconcileAutomationTwice("auto-no-token")).To(Succeed())

			By("Verifying graceful skip with lastError set")
			Eventually(func(g Gomega) {
				updated := &hav1alpha1.HomeAssistantAutomation{}
				g.Expect(k8sClient.Get(
					ctx,
					types.NamespacedName{Name: "auto-no-token", Namespace: namespace},
					updated,
				)).To(Succeed())
				g.Expect(updated.Status.LastError).To(ContainSubstring("API token"))
				// Should still be Ready since reload failure is graceful
				condition := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).To(Equal(metav1.ConditionTrue))
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("When verifying idempotency", func() {
		It("should not update ConfigMap on repeated reconciles without changes", func() {
			By("Creating an automation")
			auto := createTestAutomation("auto-idempotent", haName, "Idempotent Auto",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling initially")
			Expect(reconcileAutomationTwice("auto-idempotent")).To(Succeed())

			By("Capturing initial ConfigMap resourceVersion")
			var initialVersion string
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				initialVersion = cm.ResourceVersion
				g.Expect(initialVersion).NotTo(BeEmpty())
			}, timeout, interval).Should(Succeed())

			By("Reconciling multiple times")
			for i := 0; i < 3; i++ {
				Expect(reconcileAutomationTwice("auto-idempotent")).To(Succeed())
			}

			By("Verifying ConfigMap resourceVersion did not change")
			Consistently(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				g.Expect(cm.ResourceVersion).To(Equal(initialVersion))
			}, time.Second*2, interval).Should(Succeed())
		})
	})

	Context("When deleting and toggling automations", func() {
		It("should remove deleted automation from ConfigMap", func() {
			By("Creating two automations")
			auto1 := createTestAutomation("auto-del-1", haName, "First Auto",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			auto2 := createTestAutomation("auto-del-2", haName, "Second Auto",
				[]runtime.RawExtension{rawExt(map[string]interface{}{"platform": "time", "at": "12:00:00"})},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto1)).To(Succeed())
			Expect(k8sClient.Create(ctx, auto2)).To(Succeed())

			By("Reconciling both")
			Expect(reconcileAutomationTwice("auto-del-1")).To(Succeed())
			Expect(reconcileAutomationTwice("auto-del-2")).To(Succeed())

			By("Verifying both are in ConfigMap")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				g.Expect(cm.Data["automations.yaml"]).To(ContainSubstring("First Auto"))
				g.Expect(cm.Data["automations.yaml"]).To(ContainSubstring("Second Auto"))
			}, timeout, interval).Should(Succeed())

			By("Deleting first automation")
			toDelete := &hav1alpha1.HomeAssistantAutomation{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "auto-del-1", Namespace: namespace}, toDelete)).To(Succeed())
			Expect(k8sClient.Delete(ctx, toDelete)).To(Succeed())

			By("Reconciling to handle finalizer cleanup")
			Expect(reconcileAutomationTwice("auto-del-1")).To(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "auto-del-1", Namespace: namespace}, toDelete)
				return err != nil
			}, timeout, interval).Should(BeTrue())

			By("Reconciling remaining automation")
			Expect(reconcileAutomationTwice("auto-del-2")).To(Succeed())

			By("Verifying ConfigMap contains only the remaining automation")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				g.Expect(cm.Data["automations.yaml"]).NotTo(ContainSubstring("First Auto"))
				g.Expect(cm.Data["automations.yaml"]).To(ContainSubstring("Second Auto"))
			}, timeout, interval).Should(Succeed())
		})

		It("should add automation back when toggled from disabled to enabled", func() {
			By("Creating a disabled automation")
			auto := createTestAutomation("auto-toggle", haName, "Toggle Auto",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			auto.Spec.Enabled = ptr.To(false)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling (disabled)")
			Expect(reconcileAutomationTwice("auto-toggle")).To(Succeed())

			By("Verifying automation is not in ConfigMap")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				g.Expect(cm.Data["automations.yaml"]).NotTo(ContainSubstring("Toggle Auto"))
			}, timeout, interval).Should(Succeed())

			By("Enabling the automation")
			Eventually(func() error {
				updated := &hav1alpha1.HomeAssistantAutomation{}
				if err := k8sClient.Get(ctx, types.NamespacedName{Name: "auto-toggle", Namespace: namespace}, updated); err != nil {
					return err
				}
				updated.Spec.Enabled = ptr.To(true)
				return k8sClient.Update(ctx, updated)
			}, timeout, interval).Should(Succeed())

			By("Reconciling (enabled)")
			Expect(reconcileAutomationTwice("auto-toggle")).To(Succeed())

			By("Verifying automation is now in ConfigMap")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				g.Expect(cm.Data["automations.yaml"]).To(ContainSubstring("Toggle Auto"))
			}, timeout, interval).Should(Succeed())
		})

		It("should remove automation from ConfigMap when toggled from enabled to disabled", func() {
			By("Creating an enabled automation")
			auto := createTestAutomation("auto-disable", haName, "Disable Me",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling (enabled)")
			Expect(reconcileAutomationTwice("auto-disable")).To(Succeed())

			By("Verifying automation is in ConfigMap")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				g.Expect(cm.Data["automations.yaml"]).To(ContainSubstring("Disable Me"))
			}, timeout, interval).Should(Succeed())

			By("Disabling the automation")
			Eventually(func() error {
				updated := &hav1alpha1.HomeAssistantAutomation{}
				if err := k8sClient.Get(
					ctx,
					types.NamespacedName{Name: "auto-disable", Namespace: namespace},
					updated,
				); err != nil {
					return err
				}
				updated.Spec.Enabled = ptr.To(false)
				return k8sClient.Update(ctx, updated)
			}, timeout, interval).Should(Succeed())

			By("Reconciling (disabled)")
			Expect(reconcileAutomationTwice("auto-disable")).To(Succeed())

			By("Verifying automation is no longer in ConfigMap")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				g.Expect(cm.Data["automations.yaml"]).NotTo(ContainSubstring("Disable Me"))
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("When updating status conditions", func() {
		It("should set ObservedGeneration matching metadata.generation", func() {
			By("Creating an automation")
			auto := createTestAutomation("auto-obsgen", haName, "ObsGen Test",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling")
			Expect(reconcileAutomationTwice("auto-obsgen")).To(Succeed())

			By("Verifying ObservedGeneration")
			Eventually(func(g Gomega) {
				updated := &hav1alpha1.HomeAssistantAutomation{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "auto-obsgen", Namespace: namespace}, updated)).To(Succeed())
				g.Expect(updated.Status.ObservedGeneration).To(Equal(updated.Generation))
			}, timeout, interval).Should(Succeed())
		})

		It("should set AutomationHash in status after reconcile", func() {
			By("Creating an automation")
			auto := createTestAutomation("auto-status-hash", haName, "Status Hash",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling")
			Expect(reconcileAutomationTwice("auto-status-hash")).To(Succeed())

			By("Verifying AutomationHash is set in status")
			Eventually(func(g Gomega) {
				updated := &hav1alpha1.HomeAssistantAutomation{}
				g.Expect(k8sClient.Get(
					ctx,
					types.NamespacedName{Name: "auto-status-hash", Namespace: namespace},
					updated,
				)).To(Succeed())
				g.Expect(updated.Status.AutomationHash).NotTo(BeEmpty())
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("When handling multiple HomeAssistant instances", func() {
		It("should isolate automations between different HA instances", func() {
			By("Creating a second HomeAssistant")
			ha2 := &hav1alpha1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-ha-auto-2",
					Namespace: namespace,
				},
				Spec: hav1alpha1.HomeAssistantSpec{
					Version: "stable",
				},
			}
			Expect(k8sClient.Create(ctx, ha2)).To(Succeed())

			By("Creating automation for HA 1")
			auto1 := createTestAutomation("auto-iso-1", haName, "HA1 Automation",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto1)).To(Succeed())

			By("Creating automation for HA 2")
			auto2 := createTestAutomation("auto-iso-2", "test-ha-auto-2", "HA2 Automation",
				[]runtime.RawExtension{rawExt(map[string]interface{}{"platform": "time", "at": "08:00:00"})},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto2)).To(Succeed())

			By("Reconciling both")
			Expect(reconcileAutomationTwice("auto-iso-1")).To(Succeed())
			Expect(reconcileAutomationTwice("auto-iso-2")).To(Succeed())

			By("Verifying HA1 ConfigMap contains only HA1 automation")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				g.Expect(cm.Data["automations.yaml"]).To(ContainSubstring("HA1 Automation"))
				g.Expect(cm.Data["automations.yaml"]).NotTo(ContainSubstring("HA2 Automation"))
			}, timeout, interval).Should(Succeed())

			By("Verifying HA2 ConfigMap contains only HA2 automation")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      "test-ha-auto-2-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				g.Expect(cm.Data["automations.yaml"]).To(ContainSubstring("HA2 Automation"))
				g.Expect(cm.Data["automations.yaml"]).NotTo(ContainSubstring("HA1 Automation"))
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("When description is provided", func() {
		It("should include description in YAML output", func() {
			By("Creating automation with description")
			auto := createTestAutomation("auto-desc", haName, "Described Auto",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			auto.Spec.Description = "This automation turns on lights at sunset"
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling")
			Expect(reconcileAutomationTwice("auto-desc")).To(Succeed())

			By("Verifying description is in ConfigMap")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				g.Expect(cm.Data["automations.yaml"]).To(ContainSubstring("description: This automation turns on lights at sunset"))
			}, timeout, interval).Should(Succeed())
		})
	})

	Context("When ConfigMap labels are set", func() {
		It("should set standard operator labels on ConfigMap", func() {
			By("Creating an automation")
			auto := createTestAutomation("auto-labels", haName, "Labels Test",
				[]runtime.RawExtension{defaultTrigger()},
				[]runtime.RawExtension{defaultAction()},
			)
			Expect(k8sClient.Create(ctx, auto)).To(Succeed())

			By("Reconciling")
			Expect(reconcileAutomationTwice("auto-labels")).To(Succeed())

			By("Verifying labels")
			Eventually(func(g Gomega) {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{
					Name:      haName + "-automations",
					Namespace: namespace,
				}, cm)).To(Succeed())
				g.Expect(cm.Labels).To(HaveKeyWithValue("app.kubernetes.io/name", "homeassistant"))
				g.Expect(cm.Labels).To(HaveKeyWithValue("app.kubernetes.io/instance", haName))
				g.Expect(cm.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "homeassistant-operator"))
				g.Expect(cm.Labels).To(HaveKeyWithValue("app.kubernetes.io/component", "automations"))
			}, timeout, interval).Should(Succeed())
		})
	})
})
