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
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

const devicePassthroughTestNamespace = "default"

// deviceVolume returns the volume named "device-<index>" from a built pod
// spec, or nil if absent.
func deviceVolume(volumes []corev1.Volume, index int) *corev1.Volume {
	name := "device-" + string(rune('0'+index))
	for i := range volumes {
		if volumes[i].Name == name {
			return &volumes[i]
		}
	}
	return nil
}

var _ = Describe("HomeAssistant device passthrough (spec.alpha.devices)", func() {
	var reconciler *HomeAssistantReconciler

	BeforeEach(func() {
		reconciler = &HomeAssistantReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
		}
	})

	Context("buildStatefulSet", func() {
		It("mounts a single declared device without privileged escalation", func() {
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "device-single", Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Alpha: &hav1.AlphaSpec{
						Devices: []hav1.DevicePassthroughEntry{
							{HostPath: "/dev/ttyACM0"},
						},
					},
				},
			}

			sts, err := reconciler.buildStatefulSet(ctx, ha)
			Expect(err).NotTo(HaveOccurred())

			vol := deviceVolume(sts.Spec.Template.Spec.Volumes, 0)
			Expect(vol).NotTo(BeNil())
			Expect(vol.HostPath).NotTo(BeNil())
			Expect(vol.HostPath.Path).To(Equal("/dev/ttyACM0"))
			Expect(*vol.HostPath.Type).To(Equal(corev1.HostPathCharDev))

			container := sts.Spec.Template.Spec.Containers[0]
			var mount *corev1.VolumeMount
			for i := range container.VolumeMounts {
				if container.VolumeMounts[i].Name == "device-0" {
					mount = &container.VolumeMounts[i]
				}
			}
			Expect(mount).NotTo(BeNil())
			Expect(mount.MountPath).To(Equal("/dev/ttyACM0"), "containerPath unset must default to hostPath")

			Expect(container.SecurityContext).NotTo(BeNil())
			Expect(container.SecurityContext.Privileged).NotTo(BeNil())
			Expect(*container.SecurityContext.Privileged).To(BeFalse())
		})

		It("honors a distinct containerPath when set", func() {
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "device-custom-path", Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Alpha: &hav1.AlphaSpec{
						Devices: []hav1.DevicePassthroughEntry{
							{HostPath: "/dev/ttyACM0", ContainerPath: "/dev/zigbee"},
						},
					},
				},
			}

			sts, err := reconciler.buildStatefulSet(ctx, ha)
			Expect(err).NotTo(HaveOccurred())

			container := sts.Spec.Template.Spec.Containers[0]
			var mount *corev1.VolumeMount
			for i := range container.VolumeMounts {
				if container.VolumeMounts[i].Name == "device-0" {
					mount = &container.VolumeMounts[i]
				}
			}
			Expect(mount).NotTo(BeNil())
			Expect(mount.MountPath).To(Equal("/dev/zigbee"))
		})

		It("mounts multiple declared devices independently", func() {
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "device-multi", Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Alpha: &hav1.AlphaSpec{
						Devices: []hav1.DevicePassthroughEntry{
							{HostPath: "/dev/ttyACM0"},
							{HostPath: "/dev/ttyACM1"},
						},
					},
				},
			}

			sts, err := reconciler.buildStatefulSet(ctx, ha)
			Expect(err).NotTo(HaveOccurred())

			vol0 := deviceVolume(sts.Spec.Template.Spec.Volumes, 0)
			vol1 := deviceVolume(sts.Spec.Template.Spec.Volumes, 1)
			Expect(vol0).NotTo(BeNil())
			Expect(vol1).NotTo(BeNil())
			Expect(vol0.HostPath.Path).To(Equal("/dev/ttyACM0"))
			Expect(vol1.HostPath.Path).To(Equal("/dev/ttyACM1"))
		})

		It("leaves the pod template untouched when no devices are declared", func() {
			haWithout := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "device-none", Namespace: devicePassthroughTestNamespace},
			}
			haWithEmptyAlpha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "device-none-empty-alpha", Namespace: devicePassthroughTestNamespace},
				Spec:       hav1.HomeAssistantSpec{Alpha: &hav1.AlphaSpec{}},
			}

			stsWithout, err := reconciler.buildStatefulSet(ctx, haWithout)
			Expect(err).NotTo(HaveOccurred())
			stsWithEmptyAlpha, err := reconciler.buildStatefulSet(ctx, haWithEmptyAlpha)
			Expect(err).NotTo(HaveOccurred())

			for _, sts := range []*appsv1.StatefulSet{stsWithout, stsWithEmptyAlpha} {
				Expect(deviceVolume(sts.Spec.Template.Spec.Volumes, 0)).To(BeNil())
				Expect(sts.Spec.Template.Spec.Containers[0].SecurityContext).To(BeNil())
			}
		})
	})

	Context("needsUpdate", func() {
		buildDesired := func(hostPath string) *appsv1.StatefulSet {
			charType := corev1.HostPathCharDev
			return &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Volumes: []corev1.Volume{
								{
									Name: "device-0",
									VolumeSource: corev1.VolumeSource{
										HostPath: &corev1.HostPathVolumeSource{Path: hostPath, Type: &charType},
									},
								},
							},
							Containers: []corev1.Container{
								{
									Name: "home-assistant",
									VolumeMounts: []corev1.VolumeMount{
										{Name: "device-0", MountPath: hostPath},
									},
									SecurityContext: &corev1.SecurityContext{Privileged: ptr.To(false)},
								},
							},
						},
					},
				},
			}
		}

		It("triggers an update when a device's hostPath changes in place (same count)", func() {
			current := buildDesired("/dev/ttyACM0")
			desired := buildDesired("/dev/ttyACM1")
			Expect(needsUpdate(current, desired)).To(BeTrue())
		})

		It("does not trigger an update when device count and paths are unchanged", func() {
			current := buildDesired("/dev/ttyACM0")
			desired := buildDesired("/dev/ttyACM0")
			Expect(needsUpdate(current, desired)).To(BeFalse())
		})

		It("triggers an update when SecurityContext transitions from unset to privileged:false", func() {
			current := &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{{Name: "home-assistant"}},
						},
					},
				},
			}
			desired := &appsv1.StatefulSet{
				Spec: appsv1.StatefulSetSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{Name: "home-assistant", SecurityContext: &corev1.SecurityContext{Privileged: ptr.To(false)}},
							},
						},
					},
				},
			}
			Expect(needsUpdate(current, desired)).To(BeTrue())
		})
	})

	Context("buildDevicesReadyCondition", func() {
		It("reports NoDevicesDeclared/True when no devices are declared", func() {
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "devcond-none", Namespace: devicePassthroughTestNamespace},
			}
			cond := reconciler.buildDevicesReadyCondition(ctx, ha, false)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(reasonNoDevicesDeclared))
		})

		It("reports DevicesMounted/True when the StatefulSet is ready", func() {
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "devcond-ready", Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Alpha: &hav1.AlphaSpec{Devices: []hav1.DevicePassthroughEntry{{HostPath: "/dev/ttyACM0"}}},
				},
			}
			cond := reconciler.buildDevicesReadyCondition(ctx, ha, true)
			Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			Expect(cond.Reason).To(Equal(reasonDevicesMounted))
		})

		It("reports DeviceUnavailable/False when a FailedMount event references a declared path", func() {
			haName := "devcond-unavailable"
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: haName, Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Alpha: &hav1.AlphaSpec{Devices: []hav1.DevicePassthroughEntry{{HostPath: "/dev/does-not-exist-0"}}},
				},
			}

			failedMountMsg := `MountVolume.SetUp failed for volume "device-0": ` +
				`hostPath type check failed: /dev/does-not-exist-0 is not a character device`
			event := &corev1.Event{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "devcond-unavailable-event-",
					Namespace:    devicePassthroughTestNamespace,
				},
				InvolvedObject: corev1.ObjectReference{
					Kind:      "Pod",
					Name:      haName + "-0",
					Namespace: devicePassthroughTestNamespace,
				},
				Reason:         failedMountEventReason,
				Message:        failedMountMsg,
				Type:           corev1.EventTypeWarning,
				FirstTimestamp: metav1.Now(),
				LastTimestamp:  metav1.Now(),
			}
			Expect(k8sClient.Create(ctx, event)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, event)
			})

			cond := reconciler.buildDevicesReadyCondition(ctx, ha, false)
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal(reasonDeviceUnavailable))
			Expect(cond.Message).To(ContainSubstring("/dev/does-not-exist-0"))
		})

		It("reports Pending/Unknown when no matching event exists yet", func() {
			ha := &hav1.HomeAssistant{
				ObjectMeta: metav1.ObjectMeta{Name: "devcond-pending", Namespace: devicePassthroughTestNamespace},
				Spec: hav1.HomeAssistantSpec{
					Alpha: &hav1.AlphaSpec{Devices: []hav1.DevicePassthroughEntry{{HostPath: "/dev/ttyACM0"}}},
				},
			}
			cond := reconciler.buildDevicesReadyCondition(ctx, ha, false)
			Expect(cond.Status).To(Equal(metav1.ConditionUnknown))
			Expect(cond.Reason).To(Equal(reasonDevicesPending))
		})
	})
})
