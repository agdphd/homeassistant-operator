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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// AddonPort defines a port to be exposed by the addon Service
type AddonPort struct {
	// Name is a unique name for this port (e.g., "mqtt", "http")
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Port number to expose
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// Protocol for the port (TCP or UDP)
	// +kubebuilder:default="TCP"
	// +kubebuilder:validation:Enum=TCP;UDP
	// +optional
	Protocol string `json:"protocol,omitempty"`

	// NodePort exposes the Service on each Node's IP at this port.
	// If specified, the Service type becomes NodePort.
	// +optional
	// +kubebuilder:validation:Minimum=30000
	// +kubebuilder:validation:Maximum=32767
	NodePort int32 `json:"nodePort,omitempty"`
}

// AddonStorage configures persistent storage for the addon.
// When specified, the controller creates a StatefulSet with a VolumeClaimTemplate
// instead of a Deployment.
type AddonStorage struct {
	// Size of the persistent volume (e.g., "1Gi", "5Gi")
	// +kubebuilder:default="1Gi"
	// +optional
	Size resource.Quantity `json:"size,omitempty"`

	// StorageClassName for the PVC. If empty, uses cluster default.
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// MountPath is the path where the volume is mounted in the container
	// +kubebuilder:default="/data"
	// +optional
	MountPath string `json:"mountPath,omitempty"`
}

// AddonConfig defines configuration files to mount into the addon container
type AddonConfig struct {
	// MountPath is the directory where config files are mounted in the container
	// +kubebuilder:validation:Required
	MountPath string `json:"mountPath"`

	// Data contains the configuration file contents (key = filename, value = content)
	// +kubebuilder:validation:Required
	Data map[string]string `json:"data"`
}

// AddonIngress configures an Ingress resource for the addon web UI
type AddonIngress struct {
	// Enabled controls whether an Ingress resource is created
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Host is the hostname for the Ingress (e.g., "nodered.example.com")
	// +optional
	Host string `json:"host,omitempty"`

	// TargetPort is the port on the container to route traffic to.
	// If not specified, the first port from spec.ports is used.
	// +optional
	TargetPort int32 `json:"targetPort,omitempty"`

	// IngressClassName specifies the Ingress controller to use
	// +optional
	IngressClassName *string `json:"ingressClassName,omitempty"`

	// Annotations to add to the Ingress resource
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// TLS enables HTTPS for the Ingress
	// +optional
	TLS bool `json:"tls,omitempty"`
}

// HAIntegration defines automatic Home Assistant integration configuration.
// When specified, the addon controller adds the integration section to the
// HomeAssistantConfiguration CR, which triggers a hot-reload of Home Assistant.
type HAIntegration struct {
	// Section is the top-level key in configuration.yaml (e.g., "mqtt", "recorder")
	// +kubebuilder:validation:Required
	Section string `json:"section"`

	// Configuration contains the integration settings as arbitrary YAML/JSON.
	// The broker/host field is automatically filled with the addon Service DNS if not set.
	// Example: {"discovery": true, "port": 1883}
	// +optional
	// +kubebuilder:validation:Type=object
	// +kubebuilder:pruning:PreserveUnknownFields
	Configuration runtime.RawExtension `json:"configuration,omitempty"`
}

// HomeAssistantAddonSpec defines the desired state of HomeAssistantAddon
type HomeAssistantAddonSpec struct {
	// HomeAssistantRef references the HomeAssistant CR that this addon belongs to
	// +kubebuilder:validation:Required
	HomeAssistantRef HomeAssistantReference `json:"homeAssistantRef"`

	// Profile selects a predefined addon configuration (e.g., "mosquito", "mariadb", "node-red").
	// Profile values can be overridden by other fields in this spec.
	// Either Profile or Image must be specified.
	// +optional
	// +kubebuilder:validation:Enum=mosquito;mariadb;node-red
	Profile string `json:"profile,omitempty"`

	// Version overrides the default image tag from the profile (e.g., "2.0", "11", "latest").
	// Has no effect if Image is specified directly.
	// +optional
	Version string `json:"version,omitempty"`

	// Image is the container image for the addon (e.g., "eclipse-mosquitto:2").
	// Required if Profile is not specified. Overrides the profile image if both are set.
	// +optional
	Image string `json:"image,omitempty"`

	// Ports defines the ports to expose via a Kubernetes Service.
	// If not specified and a Profile is used, profile defaults apply.
	// +optional
	Ports []AddonPort `json:"ports,omitempty"`

	// Storage configures persistent storage. When specified, the controller creates
	// a StatefulSet with a VolumeClaimTemplate instead of a Deployment.
	// Set to an empty object {} to use profile storage defaults without overrides.
	// +optional
	Storage *AddonStorage `json:"storage,omitempty"`

	// Resources defines CPU and memory requests/limits for the addon container
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// Config defines configuration files to mount into the addon container via a ConfigMap
	// +optional
	Config *AddonConfig `json:"config,omitempty"`

	// Env defines environment variables for the addon container
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Ingress configures an Ingress resource for web UI access
	// +optional
	Ingress *AddonIngress `json:"ingress,omitempty"`

	// HostNetwork enables host networking mode for the addon pod.
	// Use when the addon requires access to host network interfaces (e.g., mDNS, network discovery).
	// +kubebuilder:default=false
	// +optional
	HostNetwork bool `json:"hostNetwork,omitempty"`

	// HAIntegration configures automatic Home Assistant integration.
	// When set, the addon controller adds the specified section to the HomeAssistantConfiguration CR,
	// triggering a hot-reload of Home Assistant.
	// If not specified, the profile HAIntegration defaults apply (if the profile defines one).
	// +optional
	HAIntegration *HAIntegration `json:"haIntegration,omitempty"`

	// LivenessProbe overrides the liveness probe for the addon container
	// +optional
	LivenessProbe *corev1.Probe `json:"livenessProbe,omitempty"`

	// ReadinessProbe overrides the readiness probe for the addon container
	// +optional
	ReadinessProbe *corev1.Probe `json:"readinessProbe,omitempty"`

	// SecurityContext defines security options for the addon container
	// +optional
	SecurityContext *corev1.SecurityContext `json:"securityContext,omitempty"`

	// VolumeMounts defines additional volume mounts for the addon container.
	// Useful for mounting host paths (e.g., USB devices, hardware interfaces).
	// +optional
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`

	// Volumes defines additional volumes for the addon pod.
	// Used together with VolumeMounts to mount host paths or other sources.
	// +optional
	Volumes []corev1.Volume `json:"volumes,omitempty"`
}

// AddonPhase represents the current lifecycle phase of the addon
// +kubebuilder:validation:Enum=Pending;Running;Failed
type AddonPhase string

const (
	AddonPhasePending AddonPhase = "Pending"
	AddonPhaseRunning AddonPhase = "Running"
	AddonPhaseFailed  AddonPhase = "Failed"
)

// HomeAssistantAddonStatus defines the observed state of HomeAssistantAddon
type HomeAssistantAddonStatus struct {
	// Conditions represent the latest available observations of the addon state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Phase is the current lifecycle phase of the addon (Pending, Running, Failed)
	// +optional
	Phase AddonPhase `json:"phase,omitempty"`

	// ResolvedImage is the full container image reference after profile resolution
	// (e.g., "eclipse-mosquitto:2")
	// +optional
	ResolvedImage string `json:"resolvedImage,omitempty"`

	// AddonHash is the SHA256 hash of the current addon configuration.
	// Used to detect changes and trigger workload updates.
	// +optional
	AddonHash string `json:"addonHash,omitempty"`

	// ObservedGeneration reflects the generation of the most recently observed spec
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// WorkloadType indicates whether the addon runs as a Deployment or StatefulSet
	// +optional
	WorkloadType string `json:"workloadType,omitempty"`

	// ServiceName is the name of the Kubernetes Service created for this addon
	// +optional
	ServiceName string `json:"serviceName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=haaddon;haad,categories=homeassistant
// +kubebuilder:printcolumn:name="HomeAssistant",type=string,JSONPath=`.spec.homeAssistantRef.name`
// +kubebuilder:printcolumn:name="Profile",type=string,JSONPath=`.spec.profile`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.status.resolvedImage`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// HomeAssistantAddon is the Schema for the homeassistantaddons API.
// It deploys and manages a standalone Kubernetes workload (Deployment or StatefulSet)
// that acts as an addon service for a Home Assistant instance.
type HomeAssistantAddon struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HomeAssistantAddonSpec   `json:"spec,omitempty"`
	Status HomeAssistantAddonStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HomeAssistantAddonList contains a list of HomeAssistantAddon
type HomeAssistantAddonList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HomeAssistantAddon `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HomeAssistantAddon{}, &HomeAssistantAddonList{})
}
