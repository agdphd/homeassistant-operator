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
	hav1alpha1 "github.com/przemekhys/homeassistant-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
)

var _ = Describe("Addon Profile System", func() {

	Describe("resolveAddonSpec", func() {

		Context("mosquitto profile", func() {
			It("resolves correct image and version", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{
					Profile: "mosquitto",
				}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Image).To(Equal("eclipse-mosquitto:2"))
			})

			It("exposes mqtt and mqtt-ws ports", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "mosquitto"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Ports).To(HaveLen(2))
				Expect(resolved.Ports[0].Name).To(Equal("mqtt"))
				Expect(resolved.Ports[0].Port).To(Equal(int32(1883)))
				Expect(resolved.Ports[1].Name).To(Equal("mqtt-ws"))
				Expect(resolved.Ports[1].Port).To(Equal(int32(9001)))
			})

			It("includes storage configuration", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "mosquitto"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Storage).NotTo(BeNil())
				Expect(resolved.Storage.Size).To(Equal(resource.MustParse("1Gi")))
				Expect(resolved.Storage.MountPath).To(Equal("/mosquitto/data"))
			})

			It("includes mosquitto.conf in ConfigMap data", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "mosquitto"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Config).NotTo(BeNil())
				Expect(resolved.Config.MountPath).To(Equal("/mosquitto/config"))
				Expect(resolved.Config.Data).To(HaveKey("mosquitto.conf"))
			})

			It("includes HAIntegration with mqtt section", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "mosquitto"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.HAIntegration).NotTo(BeNil())
				Expect(resolved.HAIntegration.Section).To(Equal("mqtt"))
			})

			It("includes liveness probe on port 1883", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "mosquitto"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.LivenessProbe).NotTo(BeNil())
				Expect(resolved.LivenessProbe.TCPSocket).NotTo(BeNil())
				Expect(resolved.LivenessProbe.TCPSocket.Port.IntVal).To(Equal(int32(1883)))
			})

			It("includes resource requests and limits", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "mosquitto"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Resources).NotTo(BeNil())
				mem := resolved.Resources.Requests[corev1.ResourceMemory]
				Expect(mem).To(Equal(resource.MustParse("32Mi")))
			})
		})

		Context("mariadb profile", func() {
			It("resolves correct image and version", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "mariadb"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Image).To(Equal("mariadb:11"))
			})

			It("exposes mysql port 3306", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "mariadb"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Ports).To(HaveLen(1))
				Expect(resolved.Ports[0].Name).To(Equal("mysql"))
				Expect(resolved.Ports[0].Port).To(Equal(int32(3306)))
			})

			It("includes storage with 5Gi size", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "mariadb"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Storage).NotTo(BeNil())
				Expect(resolved.Storage.Size).To(Equal(resource.MustParse("5Gi")))
				Expect(resolved.Storage.MountPath).To(Equal("/var/lib/mysql"))
			})

			It("does not include HAIntegration (requires manual config)", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "mariadb"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.HAIntegration).To(BeNil())
			})

			It("includes liveness probe on port 3306", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "mariadb"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.LivenessProbe).NotTo(BeNil())
				Expect(resolved.LivenessProbe.TCPSocket).NotTo(BeNil())
				Expect(resolved.LivenessProbe.TCPSocket.Port.IntVal).To(Equal(int32(3306)))
			})
		})

		Context("node-red profile", func() {
			It("resolves correct image", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "node-red"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Image).To(Equal("nodered/node-red:latest"))
			})

			It("exposes http port 1880", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "node-red"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Ports).To(HaveLen(1))
				Expect(resolved.Ports[0].Name).To(Equal("http"))
				Expect(resolved.Ports[0].Port).To(Equal(int32(1880)))
			})

			It("includes storage with 2Gi size", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "node-red"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Storage).NotTo(BeNil())
				Expect(resolved.Storage.Size).To(Equal(resource.MustParse("2Gi")))
			})

			It("has ingress disabled by default", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "node-red"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Ingress).NotTo(BeNil())
				Expect(resolved.Ingress.Enabled).To(BeFalse())
			})

			It("includes liveness probe via HTTP GET on port 1880", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "node-red"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.LivenessProbe).NotTo(BeNil())
				Expect(resolved.LivenessProbe.HTTPGet).NotTo(BeNil())
				Expect(resolved.LivenessProbe.HTTPGet.Port.IntVal).To(Equal(int32(1880)))
			})

			It("includes readiness probe via HTTP GET on port 1880", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "node-red"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.ReadinessProbe).NotTo(BeNil())
				Expect(resolved.ReadinessProbe.HTTPGet).NotTo(BeNil())
				Expect(resolved.ReadinessProbe.HTTPGet.Port.IntVal).To(Equal(int32(1880)))
			})

			It("does not include HAIntegration", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "node-red"}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.HAIntegration).To(BeNil())
			})
		})

		Context("unknown profile", func() {
			It("returns an error", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "nonexistent"}
				_, err := resolveAddonSpec(spec)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unknown profile"))
				Expect(err.Error()).To(ContainSubstring("nonexistent"))
			})
		})

		Context("user overrides", func() {
			It("overrides profile image with custom image", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{
					Profile: "mosquitto",
					Image:   "my-registry/mosquitto:custom",
				}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Image).To(Equal("my-registry/mosquitto:custom"))
			})

			It("overrides profile version", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{
					Profile: "mosquitto",
					Version: "2.1",
				}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Image).To(Equal("eclipse-mosquitto:2.1"))
			})

			It("overrides profile resources", func() {
				customMemory := resource.MustParse("256Mi")
				spec := &hav1alpha1.HomeAssistantAddonSpec{
					Profile: "mosquitto",
					Resources: &corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceMemory: customMemory,
						},
					},
				}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Resources).NotTo(BeNil())
				mem := resolved.Resources.Requests[corev1.ResourceMemory]
				Expect(mem).To(Equal(customMemory))
			})

			It("overrides profile storage size", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{
					Profile: "mosquitto",
					Storage: &hav1alpha1.AddonStorage{
						Size:      resource.MustParse("10Gi"),
						MountPath: "/mosquitto/data",
					},
				}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Storage.Size).To(Equal(resource.MustParse("10Gi")))
			})

			It("overrides profile ports", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{
					Profile: "mosquitto",
					Ports: []hav1alpha1.AddonPort{
						{Name: "mqtt", Port: 1884, Protocol: "TCP"},
					},
				}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Ports).To(HaveLen(1))
				Expect(resolved.Ports[0].Port).To(Equal(int32(1884)))
			})

			It("overrides profile HAIntegration", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{
					Profile: "mosquitto",
					HAIntegration: &hav1alpha1.HAIntegration{
						Section: "custom_mqtt",
					},
				}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.HAIntegration.Section).To(Equal("custom_mqtt"))
			})

			It("sets env vars (no profile default)", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{
					Profile: "mariadb",
					Env: []corev1.EnvVar{
						{Name: "MARIADB_ROOT_PASSWORD", Value: "secret"},
					},
				}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Env).To(HaveLen(1))
				Expect(resolved.Env[0].Name).To(Equal("MARIADB_ROOT_PASSWORD"))
			})

			It("enables ingress for node-red", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{
					Profile: "node-red",
					Ingress: &hav1alpha1.AddonIngress{
						Enabled: true,
						Host:    "nodered.example.com",
					},
				}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Ingress).NotTo(BeNil())
				Expect(resolved.Ingress.Enabled).To(BeTrue())
				Expect(resolved.Ingress.Host).To(Equal("nodered.example.com"))
			})

			It("sets hostNetwork from user spec", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{
					Profile:     "mosquitto",
					HostNetwork: true,
				}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.HostNetwork).To(BeTrue())
			})
		})

		Context("custom image (no profile)", func() {
			It("resolves correctly with explicit image", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{
					Image: "my-custom-app:1.0",
					Ports: []hav1alpha1.AddonPort{
						{Name: "http", Port: 8080, Protocol: "TCP"},
					},
				}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.Image).To(Equal("my-custom-app:1.0"))
				Expect(resolved.Ports).To(HaveLen(1))
			})

			It("returns error when neither profile nor image is specified", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{}
				_, err := resolveAddonSpec(spec)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("image is required"))
			})
		})

		Context("additional volume/probe/security overrides", func() {
			It("applies readiness probe override", func() {
				probe := &corev1.Probe{
					ProbeHandler: corev1.ProbeHandler{
						TCPSocket: &corev1.TCPSocketAction{
							Port: intstr.FromInt(9999),
						},
					},
					PeriodSeconds: 60,
				}
				spec := &hav1alpha1.HomeAssistantAddonSpec{
					Profile:        "mosquitto",
					ReadinessProbe: probe,
				}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.ReadinessProbe).NotTo(BeNil())
				Expect(resolved.ReadinessProbe.TCPSocket.Port.IntVal).To(Equal(int32(9999)))
			})

			It("applies security context override", func() {
				runAsUser := int64(1000)
				spec := &hav1alpha1.HomeAssistantAddonSpec{
					Profile: "mosquitto",
					SecurityContext: &corev1.SecurityContext{
						RunAsUser: &runAsUser,
					},
				}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.SecurityContext).NotTo(BeNil())
				Expect(*resolved.SecurityContext.RunAsUser).To(Equal(int64(1000)))
			})

			It("applies additional volume mounts and volumes", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{
					Profile: "mosquitto",
					VolumeMounts: []corev1.VolumeMount{
						{Name: "usb-device", MountPath: "/dev/ttyUSB0"},
					},
					Volumes: []corev1.Volume{
						{Name: "usb-device", VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{Path: "/dev/ttyUSB0"},
						}},
					},
				}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.VolumeMounts).To(HaveLen(1))
				Expect(resolved.VolumeMounts[0].Name).To(Equal("usb-device"))
				Expect(resolved.Volumes).To(HaveLen(1))
				Expect(resolved.Volumes[0].Name).To(Equal("usb-device"))
			})

			It("applies liveness probe override via user spec", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{
					Profile: "mosquitto",
					LivenessProbe: &corev1.Probe{
						ProbeHandler: corev1.ProbeHandler{
							HTTPGet: &corev1.HTTPGetAction{
								Path: "/health",
								Port: intstr.FromInt(8080),
							},
						},
					},
				}
				resolved, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.LivenessProbe.HTTPGet).NotTo(BeNil())
				Expect(resolved.LivenessProbe.HTTPGet.Port.IntVal).To(Equal(int32(8080)))
			})
		})

		Context("idempotency", func() {
			It("produces identical results on repeated calls with same spec", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{Profile: "mosquitto"}
				resolved1, err1 := resolveAddonSpec(spec)
				resolved2, err2 := resolveAddonSpec(spec)
				Expect(err1).NotTo(HaveOccurred())
				Expect(err2).NotTo(HaveOccurred())
				Expect(resolved1.Image).To(Equal(resolved2.Image))
				Expect(resolved1.Ports).To(Equal(resolved2.Ports))
				Expect(resolved1.Storage).To(Equal(resolved2.Storage))
			})

			It("user overrides do not mutate the profile defaults", func() {
				spec := &hav1alpha1.HomeAssistantAddonSpec{
					Profile: "mosquitto",
					Ports: []hav1alpha1.AddonPort{
						{Name: "custom", Port: 9999},
					},
				}
				_, err := resolveAddonSpec(spec)
				Expect(err).NotTo(HaveOccurred())

				// Re-resolve without user ports — should still get profile defaults
				spec2 := &hav1alpha1.HomeAssistantAddonSpec{Profile: "mosquitto"}
				resolved2, err2 := resolveAddonSpec(spec2)
				Expect(err2).NotTo(HaveOccurred())
				Expect(resolved2.Ports).To(HaveLen(2))
				Expect(resolved2.Ports[0].Port).To(Equal(int32(1883)))
			})
		})
	})
})
