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
)

// HomeAssistantSpec defines the desired state of HomeAssistant.
type HomeAssistantSpec struct {
	// Version is the Home Assistant version/tag to deploy (e.g., "2024.1.0", "stable", "latest")
	// +kubebuilder:default="stable"
	// +optional
	Version string `json:"version,omitempty"`

	// Image allows overriding the default Home Assistant image
	// +kubebuilder:default="ghcr.io/home-assistant/home-assistant"
	// +optional
	Image string `json:"image,omitempty"`

	// Storage configuration for Home Assistant data
	// +optional
	Storage *StorageSpec `json:"storage,omitempty"`

	// Resources defines CPU and memory requests/limits
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Service configuration for exposing Home Assistant
	// +optional
	Service *ServiceSpec `json:"service,omitempty"`

	// Ingress configuration for external access
	// +optional
	Ingress *IngressSpec `json:"ingress,omitempty"`

	// Timezone for the Home Assistant instance (e.g., "Europe/Warsaw")
	// +kubebuilder:default="UTC"
	// +optional
	Timezone string `json:"timezone,omitempty"`

	// ConfigurationFrom references a ConfigMap containing configuration.yaml
	// The ConfigMap should have a key "configuration.yaml" with the HA configuration
	// +optional
	ConfigurationFrom *ConfigMapReference `json:"configurationFrom,omitempty"`

	// SecretsFrom references a Secret containing secrets.yaml
	// The Secret should have a key "secrets.yaml" with the HA secrets
	// +optional
	SecretsFrom *SecretReference `json:"secretsFrom,omitempty"`
}

// ConfigMapReference references a ConfigMap for configuration
type ConfigMapReference struct {
	// Name of the ConfigMap
	Name string `json:"name"`
}

// SecretReference references a Secret for sensitive data
type SecretReference struct {
	// Name of the Secret
	Name string `json:"name"`
}

// StorageSpec defines storage configuration for Home Assistant.
type StorageSpec struct {
	// Size of the persistent volume (e.g., "5Gi", "10Gi")
	// +kubebuilder:default="5Gi"
	// +optional
	Size resource.Quantity `json:"size,omitempty"`

	// StorageClassName for the PVC. If empty, uses cluster default.
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`

	// AccessMode for the PVC
	// +kubebuilder:default="ReadWriteOnce"
	// +optional
	AccessMode corev1.PersistentVolumeAccessMode `json:"accessMode,omitempty"`
}

// ServiceSpec defines how Home Assistant is exposed within the cluster.
type ServiceSpec struct {
	// Type of Kubernetes Service (ClusterIP, NodePort, LoadBalancer)
	// +kubebuilder:default="ClusterIP"
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer
	// +optional
	Type corev1.ServiceType `json:"type,omitempty"`

	// Port for the Home Assistant web UI
	// +kubebuilder:default=8123
	// +optional
	Port int32 `json:"port,omitempty"`

	// NodePort for NodePort service type (optional, auto-assigned if not set)
	// +optional
	NodePort int32 `json:"nodePort,omitempty"`
}

// IngressSpec defines external access configuration.
type IngressSpec struct {
	// Enabled controls whether an Ingress resource is created
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Host is the hostname for the Ingress (e.g., "ha.example.com")
	// +optional
	Host string `json:"host,omitempty"`

	// IngressClassName specifies the Ingress controller to use
	// +optional
	IngressClassName *string `json:"ingressClassName,omitempty"`

	// TLS configuration
	// +optional
	TLS *IngressTLSSpec `json:"tls,omitempty"`

	// Annotations to add to the Ingress resource
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// IngressTLSSpec defines TLS configuration for Ingress.
type IngressTLSSpec struct {
	// Enabled controls whether TLS is enabled
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// SecretName containing the TLS certificate. If empty, cert-manager annotation should be used.
	// +optional
	SecretName string `json:"secretName,omitempty"`
}

// HomeAssistantStatus defines the observed state of HomeAssistant.
type HomeAssistantStatus struct {
	// Phase represents the current lifecycle phase of the HomeAssistant instance
	// +optional
	Phase HomeAssistantPhase `json:"phase,omitempty"`

	// Conditions represent the latest available observations of the HomeAssistant state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Version is the currently deployed Home Assistant version
	// +optional
	Version string `json:"version,omitempty"`

	// URL is the access URL for Home Assistant (if Ingress is enabled)
	// +optional
	URL string `json:"url,omitempty"`

	// Ready indicates if the Home Assistant instance is ready to serve traffic
	// +optional
	Ready bool `json:"ready,omitempty"`
}

// HomeAssistantPhase represents the current phase of the HomeAssistant instance.
// +kubebuilder:validation:Enum=Pending;Running;Failed;Unknown
type HomeAssistantPhase string

const (
	PhasePending HomeAssistantPhase = "Pending"
	PhaseRunning HomeAssistantPhase = "Running"
	PhaseFailed  HomeAssistantPhase = "Failed"
	PhaseUnknown HomeAssistantPhase = "Unknown"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status

// HomeAssistant is the Schema for the homeassistants API.
type HomeAssistant struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   HomeAssistantSpec   `json:"spec,omitempty"`
	Status HomeAssistantStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// HomeAssistantList contains a list of HomeAssistant.
type HomeAssistantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []HomeAssistant `json:"items"`
}

func init() {
	SchemeBuilder.Register(&HomeAssistant{}, &HomeAssistantList{})
}
