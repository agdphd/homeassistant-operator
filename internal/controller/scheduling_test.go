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
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

var _ = Describe("HomeAssistant pod scheduling controls (spec.scheduling)", func() {
	var reconciler *HomeAssistantReconciler

	BeforeEach(func() {
		reconciler = &HomeAssistantReconciler{
			Client:    k8sClient,
			Scheme:    k8sClient.Scheme(),
			APIReader: k8sClient,
		}
	})

	Context("buildStatefulSet", func() {
		It("sets NodeSelector when declared", func() {
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "sched-nodeselector", Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Scheduling: &hav1.SchedulingSpec{
						NodeSelector: map[string]string{"ha-device-node": "zigbee"},
					},
				},
			}

			sts, err := reconciler.buildStatefulSet(ctx, ha)
			Expect(err).NotTo(HaveOccurred())
			Expect(sts.Spec.Template.Spec.NodeSelector).To(Equal(map[string]string{"ha-device-node": "zigbee"}))
		})

		It("leaves NodeSelector nil when spec.scheduling is unset or empty", func() {
			haWithout := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "sched-none", Namespace: devicePassthroughTestNamespace},
			}
			haWithEmptyScheduling := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "sched-none-empty", Namespace: devicePassthroughTestNamespace},
				Spec:       hav1.HomeAssistantSpec{Scheduling: &hav1.SchedulingSpec{}},
			}

			for _, ha := range []*hav1.HomeAssistant{haWithout, haWithEmptyScheduling} {
				sts, err := reconciler.buildStatefulSet(ctx, ha)
				Expect(err).NotTo(HaveOccurred())
				Expect(sts.Spec.Template.Spec.NodeSelector).To(BeEmpty())
				Expect(sts.Spec.Template.Spec.Affinity).To(BeNil())
				Expect(sts.Spec.Template.Spec.Tolerations).To(BeEmpty())
				Expect(sts.Spec.Template.Spec.PriorityClassName).To(BeEmpty())
			}
		})

		It("carries a required node-affinity rule onto the pod template unchanged", func() {
			requiredAffinity := &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{MatchExpressions: []corev1.NodeSelectorRequirement{
								{Key: "ha-device-node", Operator: corev1.NodeSelectorOpIn, Values: []string{"zigbee"}},
							}},
						},
					},
				},
			}
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "sched-affinity-required", Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Scheduling: &hav1.SchedulingSpec{Affinity: requiredAffinity},
				},
			}

			sts, err := reconciler.buildStatefulSet(ctx, ha)
			Expect(err).NotTo(HaveOccurred())
			Expect(sts.Spec.Template.Spec.Affinity).To(Equal(requiredAffinity))
		})

		It("carries a preferred node-affinity rule onto the pod template unchanged", func() {
			preferredAffinity := &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{
						{
							Weight: 1,
							Preference: corev1.NodeSelectorTerm{
								MatchExpressions: []corev1.NodeSelectorRequirement{
									{Key: "ha-storage", Operator: corev1.NodeSelectorOpIn, Values: []string{"nvme"}},
								},
							},
						},
					},
				},
			}
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "sched-affinity-preferred", Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Scheduling: &hav1.SchedulingSpec{Affinity: preferredAffinity},
				},
			}

			sts, err := reconciler.buildStatefulSet(ctx, ha)
			Expect(err).NotTo(HaveOccurred())
			Expect(sts.Spec.Template.Spec.Affinity).To(Equal(preferredAffinity))
		})

		It("carries a declared toleration onto the pod template unchanged", func() {
			tolerations := []corev1.Toleration{
				{Key: "ha-dedicated", Operator: corev1.TolerationOpEqual, Value: "true", Effect: corev1.TaintEffectNoSchedule},
			}
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "sched-tolerations", Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Scheduling: &hav1.SchedulingSpec{Tolerations: tolerations},
				},
			}

			sts, err := reconciler.buildStatefulSet(ctx, ha)
			Expect(err).NotTo(HaveOccurred())
			Expect(sts.Spec.Template.Spec.Tolerations).To(Equal(tolerations))
		})

		// Pod affinity/anti-affinity needs no new production code beyond node
		// affinity: the Affinity field already wired up above carries
		// PodAffinity/PodAntiAffinity too, since they're sub-fields of the
		// same corev1.Affinity struct. This only proves that content
		// round-trips correctly.
		It("carries a pod anti-affinity rule onto the pod template unchanged", func() {
			antiAffinity := &corev1.Affinity{
				PodAntiAffinity: &corev1.PodAntiAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{"app": "noisy-neighbor"},
							},
							TopologyKey: "kubernetes.io/hostname",
						},
					},
				},
			}
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "sched-pod-antiaffinity", Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Scheduling: &hav1.SchedulingSpec{Affinity: antiAffinity},
				},
			}

			sts, err := reconciler.buildStatefulSet(ctx, ha)
			Expect(err).NotTo(HaveOccurred())
			Expect(sts.Spec.Template.Spec.Affinity).To(Equal(antiAffinity))
		})

		It("carries a pod affinity rule onto the pod template unchanged", func() {
			podAffinity := &corev1.Affinity{
				PodAffinity: &corev1.PodAffinity{
					PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
						{
							Weight: 1,
							PodAffinityTerm: corev1.PodAffinityTerm{
								LabelSelector: &metav1.LabelSelector{
									MatchLabels: map[string]string{"app": "mqtt-broker"},
								},
								TopologyKey: "kubernetes.io/hostname",
							},
						},
					},
				},
			}
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "sched-pod-affinity", Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Scheduling: &hav1.SchedulingSpec{Affinity: podAffinity},
				},
			}

			sts, err := reconciler.buildStatefulSet(ctx, ha)
			Expect(err).NotTo(HaveOccurred())
			Expect(sts.Spec.Template.Spec.Affinity).To(Equal(podAffinity))
		})

		It("carries a declared priorityClassName onto the pod template unchanged", func() {
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "sched-priorityclass", Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Scheduling: &hav1.SchedulingSpec{PriorityClassName: "ha-critical"},
				},
			}

			sts, err := reconciler.buildStatefulSet(ctx, ha)
			Expect(err).NotTo(HaveOccurred())
			Expect(sts.Spec.Template.Spec.PriorityClassName).To(Equal("ha-critical"))
		})
	})

	Context("needsUpdate — Tolerations", func() {
		buildDesired := func(tolerations []corev1.Toleration) *appsv1.StatefulSet {
			return &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Tolerations: tolerations,
							Containers:  []corev1.Container{{Name: "home-assistant"}},
						},
					},
				},
			}
		}

		It("triggers an update when Tolerations content changes", func() {
			current := buildDesired([]corev1.Toleration{
				{Key: "ha-dedicated", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
			})
			desired := buildDesired([]corev1.Toleration{
				{Key: "ha-dedicated", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoExecute},
			})
			Expect(needsUpdate(current, desired)).To(BeTrue())
		})

		It("does not trigger an update when Tolerations is unchanged", func() {
			tolerations := []corev1.Toleration{
				{Key: "ha-dedicated", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule},
			}
			current := buildDesired(tolerations)
			desired := buildDesired(tolerations)
			Expect(needsUpdate(current, desired)).To(BeFalse())
		})
	})

	Context("needsUpdate — Affinity", func() {
		buildDesired := func(affinity *corev1.Affinity) *appsv1.StatefulSet {
			return &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Affinity:   affinity,
							Containers: []corev1.Container{{Name: "home-assistant"}},
						},
					},
				},
			}
		}

		It("triggers an update when Affinity content changes", func() {
			current := buildDesired(&corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{MatchExpressions: []corev1.NodeSelectorRequirement{
								{Key: "k", Operator: corev1.NodeSelectorOpIn, Values: []string{"a"}},
							}},
						},
					},
				},
			})
			desired := buildDesired(&corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{MatchExpressions: []corev1.NodeSelectorRequirement{
								{Key: "k", Operator: corev1.NodeSelectorOpIn, Values: []string{"b"}},
							}},
						},
					},
				},
			})
			Expect(needsUpdate(current, desired)).To(BeTrue())
		})

		It("does not trigger an update when Affinity is unchanged", func() {
			affinity := &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{
							{MatchExpressions: []corev1.NodeSelectorRequirement{
								{Key: "k", Operator: corev1.NodeSelectorOpIn, Values: []string{"a"}},
							}},
						},
					},
				},
			}
			current := buildDesired(affinity.DeepCopy())
			desired := buildDesired(affinity.DeepCopy())
			Expect(needsUpdate(current, desired)).To(BeFalse())
		})
	})

	Context("needsUpdate — PriorityClassName", func() {
		buildDesired := func(priorityClassName string) *appsv1.StatefulSet {
			return &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							PriorityClassName: priorityClassName,
							Containers:        []corev1.Container{{Name: "home-assistant"}},
						},
					},
				},
			}
		}

		It("triggers an update when PriorityClassName changes", func() {
			current := buildDesired("ha-standard")
			desired := buildDesired("ha-critical")
			Expect(needsUpdate(current, desired)).To(BeTrue())
		})

		It("does not trigger an update when PriorityClassName is unchanged", func() {
			current := buildDesired("ha-critical")
			desired := buildDesired("ha-critical")
			Expect(needsUpdate(current, desired)).To(BeFalse())
		})
	})

	Context("needsUpdate — NodeSelector", func() {
		buildDesired := func(nodeSelector map[string]string) *appsv1.StatefulSet {
			return &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							NodeSelector: nodeSelector,
							Containers:   []corev1.Container{{Name: "home-assistant"}},
						},
					},
				},
			}
		}

		It("triggers an update when NodeSelector changes", func() {
			current := buildDesired(map[string]string{"ha-device-node": "zigbee"})
			desired := buildDesired(map[string]string{"ha-device-node": "zwave"})
			Expect(needsUpdate(current, desired)).To(BeTrue())
		})

		It("does not trigger an update when NodeSelector is unchanged", func() {
			current := buildDesired(map[string]string{"ha-device-node": "zigbee"})
			desired := buildDesired(map[string]string{"ha-device-node": "zigbee"})
			Expect(needsUpdate(current, desired)).To(BeFalse())
		})
	})

	Context("buildSchedulingReadyCondition", func() {
		It("reports NoConstraintsDeclared/True when spec.scheduling is unset", func() {
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "schedcond-none", Namespace: devicePassthroughTestNamespace},
			}
			cond := reconciler.buildSchedulingReadyCondition(ha, false, nil)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(reasonNoConstraintsDeclared))
		})

		It("reports NoConstraintsDeclared/True when spec.scheduling is present but empty", func() {
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "schedcond-empty", Namespace: devicePassthroughTestNamespace},
				Spec:       hav1.HomeAssistantSpec{Scheduling: &hav1.SchedulingSpec{}},
			}
			cond := reconciler.buildSchedulingReadyCondition(ha, false, nil)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(reasonNoConstraintsDeclared))
		})

		It("reports Scheduled/True when the StatefulSet is ready", func() {
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "schedcond-ready", Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Scheduling: &hav1.SchedulingSpec{NodeSelector: map[string]string{"k": "v"}},
				},
			}
			cond := reconciler.buildSchedulingReadyCondition(ha, true, nil)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(reasonScheduled))
		})

		It("reports Pending/Unknown when no pod exists yet", func() {
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "schedcond-pending", Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Scheduling: &hav1.SchedulingSpec{NodeSelector: map[string]string{"k": "v"}},
				},
			}
			cond := reconciler.buildSchedulingReadyCondition(ha, false, nil)
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(reasonSchedulingPending))
		})

		It("reports Scheduled/True mirroring a real Pod's PodScheduled=True condition", func() {
			haName := "schedcond-pod-scheduled"
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: haName, Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Scheduling: &hav1.SchedulingSpec{NodeSelector: map[string]string{"k": "v"}},
				},
			}

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: haName + "-0", Namespace: devicePassthroughTestNamespace},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "home-assistant", Image: "busybox"}},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, pod)
			})

			pod.Status.Conditions = []corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionTrue},
			}
			Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

			cond := reconciler.buildSchedulingReadyCondition(ha, false, pod)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(reasonScheduled))
		})

		It("reports Unschedulable/False mirroring a real Pod's PodScheduled=False condition", func() {
			haName := "schedcond-unschedulable"
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: haName, Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Scheduling: &hav1.SchedulingSpec{NodeSelector: map[string]string{"ha-device-node": "does-not-exist"}},
				},
			}

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: haName + "-0", Namespace: devicePassthroughTestNamespace},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "home-assistant", Image: "busybox"}},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, pod)
			})

			unschedulableMsg := "0/3 nodes are available: 3 node(s) didn't match Pod's node affinity/selector."
			pod.Status.Conditions = []corev1.PodCondition{
				{
					Type:    corev1.PodScheduled,
					Status:  corev1.ConditionFalse,
					Reason:  "Unschedulable",
					Message: unschedulableMsg,
				},
			}
			Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

			cond := reconciler.buildSchedulingReadyCondition(ha, false, pod)
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(reasonUnschedulable))
			Expect(cond.Message).To(Equal(unschedulableMsg))
		})
	})

	Context("publishSchedulingReadyEarly (full Reconcile)", func() {
		It("publishes SchedulingReady=False even while bootstrap keeps requeuing on PodScheduled=False", func() {
			haName := "sched-early-bootstrap"

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: haName + "-creds", Namespace: devicePassthroughTestNamespace},
				Data: map[string][]byte{
					"username": []byte("admin"),
					"password": []byte("testpass123"),
				},
			}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, secret) })

			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: haName, Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Version:    "2024.1.0",
					Scheduling: &hav1.SchedulingSpec{NodeSelector: map[string]string{"ha-device-node": "does-not-exist"}},
					Bootstrap: &hav1.BootstrapSpec{
						Enabled: true,
						Credentials: &hav1.BootstrapCredentials{
							SecretRef: &hav1.CredentialsSecretRef{Name: secret.Name},
						},
					},
				},
			}
			Expect(k8sClient.Create(ctx, ha)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, ha) })

			haConfig := &hav1.HomeAssistantConfiguration{
				ObjectMeta: metav1.ObjectMeta{Name: haName + "-config", Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantConfigurationSpec{
					HomeAssistantRef: hav1.HomeAssistantReference{Name: haName},
					Configuration:    "homeassistant:\n  name: Home\n",
				},
			}
			Expect(k8sClient.Create(ctx, haConfig)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, haConfig) })

			haKey := types.NamespacedName{Name: haName, Namespace: devicePassthroughTestNamespace}

			By("reconciling once to create the StatefulSet")
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: haKey})
			Expect(err).NotTo(HaveOccurred())

			By("simulating an unschedulable pod, as a real scheduler would report it")
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: haName + "-0", Namespace: devicePassthroughTestNamespace},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "home-assistant", Image: "busybox"}},
				},
			}
			Expect(k8sClient.Create(ctx, pod)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, pod) })
			pod.Status.Conditions = []corev1.PodCondition{
				{
					Type:    corev1.PodScheduled,
					Status:  corev1.ConditionFalse,
					Reason:  "Unschedulable",
					Message: "0/1 nodes are available: 1 node(s) didn't match Pod's node affinity/selector.",
				},
			}
			Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

			By("reconciling again: bootstrap's health check fails (no real HA server) and requeues, " +
				"short-circuiting Reconcile before updateStatusFromStatefulSet would normally run")
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: haKey})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))

			By("verifying SchedulingReady=False was published anyway")
			Eventually(func(g Gomega) {
				fetched := &hav1.HomeAssistant{}
				g.Expect(k8sClient.Get(ctx, haKey, fetched)).To(Succeed())
				cond := meta.FindStatusCondition(fetched.Status.Conditions, conditionTypeSchedulingReady)
				g.Expect(cond).NotTo(BeNil())
				g.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(cond.Reason).To(Equal(reasonUnschedulable))
			}, time.Second*10, time.Millisecond*250).Should(Succeed())
		})
	})
})
