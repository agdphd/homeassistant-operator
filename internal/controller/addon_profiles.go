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
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"

	hav1alpha1 "github.com/przemekhys/homeassistant-operator/api/v1alpha1"
)

// AddonProfile defines default values for a well-known addon.
// Users can override any field via HomeAssistantAddonSpec.
type AddonProfile struct {
	// Image is the base container image name (without tag)
	Image string
	// DefaultVersion is the default image tag
	DefaultVersion string
	// Ports to expose via a Kubernetes Service
	Ports []hav1alpha1.AddonPort
	// Storage configures persistent storage (triggers StatefulSet instead of Deployment)
	Storage *hav1alpha1.AddonStorage
	// Config defines configuration files to mount
	Config *hav1alpha1.AddonConfig
	// Resources defines CPU/memory requests and limits
	Resources *corev1.ResourceRequirements
	// LivenessProbe for the addon container
	LivenessProbe *corev1.Probe
	// ReadinessProbe for the addon container
	ReadinessProbe *corev1.Probe
	// HAIntegration configures automatic Home Assistant integration
	HAIntegration *hav1alpha1.HAIntegration
	// Ingress provides default Ingress configuration (usually disabled by default)
	Ingress *hav1alpha1.AddonIngress
}

// ResolvedAddonSpec is the result of merging profile defaults with user overrides.
// This is the canonical view used by the controller to create Kubernetes resources.
type ResolvedAddonSpec struct {
	// Image is the full container image reference (e.g., "eclipse-mosquitto:2")
	Image string
	// Ports to expose via a Kubernetes Service
	Ports []hav1alpha1.AddonPort
	// Storage configures persistent storage; nil means Deployment (no storage)
	Storage *hav1alpha1.AddonStorage
	// Config defines configuration files to mount via ConfigMap
	Config *hav1alpha1.AddonConfig
	// Resources defines CPU/memory requests and limits
	Resources *corev1.ResourceRequirements
	// Env defines environment variables for the addon container
	Env []corev1.EnvVar
	// Ingress configures an optional Ingress resource
	Ingress *hav1alpha1.AddonIngress
	// HostNetwork enables host networking mode
	HostNetwork bool
	// HAIntegration configures automatic HomeAssistantConfiguration modification
	HAIntegration *hav1alpha1.HAIntegration
	// LivenessProbe for the addon container
	LivenessProbe *corev1.Probe
	// ReadinessProbe for the addon container
	ReadinessProbe *corev1.Probe
	// SecurityContext for the addon container
	SecurityContext *corev1.SecurityContext
	// VolumeMounts defines additional volume mounts
	VolumeMounts []corev1.VolumeMount
	// Volumes defines additional volumes for the pod
	Volumes []corev1.Volume
}

// addonProfiles is the built-in catalog of well-known addon configurations.
// Each profile provides sensible defaults that users can override in HomeAssistantAddonSpec.
var addonProfiles = map[string]AddonProfile{
	"mosquito": {
		Image:          "eclipse-mosquitto",
		DefaultVersion: "2",
		Ports: []hav1alpha1.AddonPort{
			{Name: "mqtt", Port: 1883, Protocol: "TCP"},
			{Name: "mqtt-ws", Port: 9001, Protocol: "TCP"},
		},
		Config: &hav1alpha1.AddonConfig{
			MountPath: "/mosquitto/config",
			Data: map[string]string{
				"mosquitto.conf": "listener 1883\n" +
					"allow_anonymous true\n" +
					"persistence true\n" +
					"persistence_location /mosquitto/data/\n",
			},
		},
		Storage: &hav1alpha1.AddonStorage{
			Size:      resource.MustParse("1Gi"),
			MountPath: "/mosquitto/data",
		},
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("32Mi"),
				corev1.ResourceCPU:    resource.MustParse("25m"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt(1883),
				},
			},
			InitialDelaySeconds: 10,
			PeriodSeconds:       30,
		},
		// HAIntegration.Configuration is intentionally empty here.
		// The broker address is computed dynamically from the Service DNS
		// in reconcileHAIntegration() and injected at reconcile time.
		HAIntegration: &hav1alpha1.HAIntegration{
			Section: "mqtt",
		},
	},

	"mariadb": {
		Image:          "mariadb",
		DefaultVersion: "11",
		Ports: []hav1alpha1.AddonPort{
			{Name: "mysql", Port: 3306, Protocol: "TCP"},
		},
		Storage: &hav1alpha1.AddonStorage{
			Size:      resource.MustParse("5Gi"),
			MountPath: "/var/lib/mysql",
		},
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("128Mi"),
				corev1.ResourceCPU:    resource.MustParse("100m"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt(3306),
				},
			},
			InitialDelaySeconds: 30,
			PeriodSeconds:       30,
		},
		// No HAIntegration: MariaDB requires manual recorder configuration
		// because the connection string includes credentials not known at profile level.
	},

	"node-red": {
		Image:          "nodered/node-red",
		DefaultVersion: "latest",
		Ports: []hav1alpha1.AddonPort{
			{Name: "http", Port: 1880, Protocol: "TCP"},
		},
		Storage: &hav1alpha1.AddonStorage{
			Size:      resource.MustParse("2Gi"),
			MountPath: "/data",
		},
		Resources: &corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("128Mi"),
				corev1.ResourceCPU:    resource.MustParse("100m"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("512Mi"),
			},
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/",
					Port: intstr.FromInt(1880),
				},
			},
			InitialDelaySeconds: 15,
			PeriodSeconds:       30,
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path: "/",
					Port: intstr.FromInt(1880),
				},
			},
			InitialDelaySeconds: 10,
			PeriodSeconds:       10,
			FailureThreshold:    6,
		},
		// Ingress disabled by default; users opt in by setting spec.ingress.enabled=true
		Ingress: &hav1alpha1.AddonIngress{
			Enabled: false,
		},
		// No HAIntegration: Node-RED connects to HA using its own configuration UI
	},
}

// resolveAddonSpec merges profile defaults with user-provided overrides and returns
// a ResolvedAddonSpec ready for use by the controller.
//
// Merge precedence (highest to lowest):
//  1. User-provided fields in HomeAssistantAddonSpec
//  2. Profile defaults (if spec.profile is set)
func resolveAddonSpec(spec *hav1alpha1.HomeAssistantAddonSpec) (*ResolvedAddonSpec, error) {
	resolved := &ResolvedAddonSpec{}

	var profile AddonProfile
	if spec.Profile != "" {
		var ok bool
		profile, ok = addonProfiles[spec.Profile]
		if !ok {
			return nil, fmt.Errorf("unknown profile %q: valid profiles are mosquito, mariadb, node-red", spec.Profile)
		}
		resolved.applyProfile(profile, spec.Version)
	}

	resolved.applyUserOverrides(spec)

	if resolved.Image == "" {
		return nil, fmt.Errorf("image is required: either set spec.profile or specify spec.image directly")
	}

	return resolved, nil
}

// applyProfile copies profile defaults into the resolved spec.
// userVersion overrides the profile default image tag when non-empty.
func (r *ResolvedAddonSpec) applyProfile(profile AddonProfile, userVersion string) {
	// Compute image: base image + effective version (user version overrides profile default)
	version := profile.DefaultVersion
	if userVersion != "" {
		version = userVersion
	}
	r.Image = profile.Image + ":" + version

	if len(profile.Ports) > 0 {
		r.Ports = make([]hav1alpha1.AddonPort, len(profile.Ports))
		copy(r.Ports, profile.Ports)
	}

	if profile.Storage != nil {
		storageCopy := *profile.Storage
		r.Storage = &storageCopy
	}

	if profile.Config != nil {
		configCopy := *profile.Config
		if configCopy.Data != nil {
			dataCopy := make(map[string]string, len(profile.Config.Data))
			for k, v := range profile.Config.Data {
				dataCopy[k] = v
			}
			configCopy.Data = dataCopy
		}
		r.Config = &configCopy
	}

	if profile.Resources != nil {
		resourcesCopy := profile.Resources.DeepCopy()
		r.Resources = resourcesCopy
	}

	if profile.LivenessProbe != nil {
		probeCopy := profile.LivenessProbe.DeepCopy()
		r.LivenessProbe = probeCopy
	}

	if profile.ReadinessProbe != nil {
		probeCopy := profile.ReadinessProbe.DeepCopy()
		r.ReadinessProbe = probeCopy
	}

	if profile.HAIntegration != nil {
		haCopy := *profile.HAIntegration
		r.HAIntegration = &haCopy
	}

	if profile.Ingress != nil {
		ingressCopy := *profile.Ingress
		if profile.Ingress.Annotations != nil {
			annotationsCopy := make(map[string]string, len(profile.Ingress.Annotations))
			for k, v := range profile.Ingress.Annotations {
				annotationsCopy[k] = v
			}
			ingressCopy.Annotations = annotationsCopy
		}
		r.Ingress = &ingressCopy
	}
}

// applyUserOverrides applies user-provided spec fields on top of profile defaults.
// User fields always win over profile defaults.
func (r *ResolvedAddonSpec) applyUserOverrides(spec *hav1alpha1.HomeAssistantAddonSpec) {
	// Image: explicit spec.image overrides profile image+version entirely
	if spec.Image != "" {
		r.Image = spec.Image
	}
	// Note: spec.version without spec.image was already handled in applyProfile

	// Ports: user ports completely replace profile ports
	if len(spec.Ports) > 0 {
		r.Ports = make([]hav1alpha1.AddonPort, len(spec.Ports))
		copy(r.Ports, spec.Ports)
	}

	// Storage: user storage completely replaces profile storage
	if spec.Storage != nil {
		storageCopy := *spec.Storage
		r.Storage = &storageCopy
	}

	// Resources: user resources completely replace profile resources
	if spec.Resources != nil {
		resourcesCopy := spec.Resources.DeepCopy()
		r.Resources = resourcesCopy
	}

	// Config: user config completely replaces profile config
	if spec.Config != nil {
		configCopy := *spec.Config
		if spec.Config.Data != nil {
			dataCopy := make(map[string]string, len(spec.Config.Data))
			for k, v := range spec.Config.Data {
				dataCopy[k] = v
			}
			configCopy.Data = dataCopy
		}
		r.Config = &configCopy
	}

	// Env: user env vars (no merging with profile - profiles do not define env vars)
	if len(spec.Env) > 0 {
		r.Env = make([]corev1.EnvVar, len(spec.Env))
		copy(r.Env, spec.Env)
	}

	// Ingress: user ingress completely replaces profile ingress
	if spec.Ingress != nil {
		ingressCopy := *spec.Ingress
		if spec.Ingress.Annotations != nil {
			annotationsCopy := make(map[string]string, len(spec.Ingress.Annotations))
			for k, v := range spec.Ingress.Annotations {
				annotationsCopy[k] = v
			}
			ingressCopy.Annotations = annotationsCopy
		}
		r.Ingress = &ingressCopy
	}

	// HostNetwork: always take user value (defaults to false)
	r.HostNetwork = spec.HostNetwork

	// HAIntegration: user haIntegration completely replaces profile haIntegration
	if spec.HAIntegration != nil {
		haCopy := *spec.HAIntegration
		r.HAIntegration = &haCopy
	}

	// Probes: user probes completely replace profile probes
	if spec.LivenessProbe != nil {
		probeCopy := spec.LivenessProbe.DeepCopy()
		r.LivenessProbe = probeCopy
	}
	if spec.ReadinessProbe != nil {
		probeCopy := spec.ReadinessProbe.DeepCopy()
		r.ReadinessProbe = probeCopy
	}

	// SecurityContext: user value (no profile default)
	if spec.SecurityContext != nil {
		scCopy := spec.SecurityContext.DeepCopy()
		r.SecurityContext = scCopy
	}

	// Additional volumes: user values (no profile defaults)
	if len(spec.VolumeMounts) > 0 {
		r.VolumeMounts = make([]corev1.VolumeMount, len(spec.VolumeMounts))
		copy(r.VolumeMounts, spec.VolumeMounts)
	}
	if len(spec.Volumes) > 0 {
		r.Volumes = make([]corev1.Volume, len(spec.Volumes))
		copy(r.Volumes, spec.Volumes)
	}
}
