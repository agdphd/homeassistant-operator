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
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	hav1alpha1 "github.com/przemekhys/homeassistant-operator/api/v1alpha1"
)

const (
	// Default values
	defaultImage       = "ghcr.io/home-assistant/home-assistant"
	defaultVersion     = "stable"
	defaultPort        = 8123
	defaultStorageSize = "5Gi"
	defaultTimezone    = "UTC"

	// Labels
	labelAppName      = "app.kubernetes.io/name"
	labelAppInstance  = "app.kubernetes.io/instance"
	labelAppManagedBy = "app.kubernetes.io/managed-by"

	// Condition types
	conditionTypeReady = "Ready"
)

// HomeAssistantReconciler reconciles a HomeAssistant object
type HomeAssistantReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistants/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HomeAssistantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the HomeAssistant instance
	ha := &hav1alpha1.HomeAssistant{}
	if err := r.Get(ctx, req.NamespacedName, ha); err != nil {
		if errors.IsNotFound(err) {
			log.Info("HomeAssistant resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get HomeAssistant")
		return ctrl.Result{}, err
	}

	// Set initial status if not set
	if ha.Status.Phase == "" {
		ha.Status.Phase = hav1alpha1.PhasePending
		if err := r.Status().Update(ctx, ha); err != nil {
			log.Error(err, "Failed to update HomeAssistant status")
			return ctrl.Result{}, err
		}
	}

	// Reconcile PVC
	if err := r.reconcilePVC(ctx, ha); err != nil {
		log.Error(err, "Failed to reconcile PVC")
		return r.updateStatusFailed(ctx, ha, err)
	}

	// Reconcile StatefulSet
	if err := r.reconcileStatefulSet(ctx, ha); err != nil {
		log.Error(err, "Failed to reconcile StatefulSet")
		return r.updateStatusFailed(ctx, ha, err)
	}

	// Reconcile Service
	if err := r.reconcileService(ctx, ha); err != nil {
		log.Error(err, "Failed to reconcile Service")
		return r.updateStatusFailed(ctx, ha, err)
	}

	// Update status based on StatefulSet status
	return r.updateStatusFromStatefulSet(ctx, ha)
}

// reconcilePVC ensures the PVC exists for Home Assistant data
func (r *HomeAssistantReconciler) reconcilePVC(ctx context.Context, ha *hav1alpha1.HomeAssistant) error {
	log := logf.FromContext(ctx)
	pvcName := fmt.Sprintf("%s-config", ha.Name)

	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: ha.Namespace}, pvc)

	if err != nil && errors.IsNotFound(err) {
		// Create new PVC
		pvc = r.buildPVC(ha, pvcName)
		if err := controllerutil.SetControllerReference(ha, pvc, r.Scheme); err != nil {
			return err
		}
		log.Info("Creating PVC", "PVC.Name", pvc.Name)
		return r.Create(ctx, pvc)
	} else if err != nil {
		return err
	}

	// PVC exists, no update needed (PVCs are mostly immutable)
	log.V(1).Info("PVC already exists", "PVC.Name", pvc.Name)
	return nil
}

// buildPVC creates a PVC spec for Home Assistant
func (r *HomeAssistantReconciler) buildPVC(ha *hav1alpha1.HomeAssistant, name string) *corev1.PersistentVolumeClaim {
	labels := r.labelsForHomeAssistant(ha)

	storageSize := resource.MustParse(defaultStorageSize)
	if ha.Spec.Storage != nil && !ha.Spec.Storage.Size.IsZero() {
		storageSize = ha.Spec.Storage.Size
	}

	accessMode := corev1.ReadWriteOnce
	if ha.Spec.Storage != nil && ha.Spec.Storage.AccessMode != "" {
		accessMode = ha.Spec.Storage.AccessMode
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ha.Namespace,
			Labels:    labels,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{accessMode},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: storageSize,
				},
			},
		},
	}

	if ha.Spec.Storage != nil && ha.Spec.Storage.StorageClassName != nil {
		pvc.Spec.StorageClassName = ha.Spec.Storage.StorageClassName
	}

	return pvc
}

// reconcileStatefulSet ensures the StatefulSet exists and is up to date
func (r *HomeAssistantReconciler) reconcileStatefulSet(ctx context.Context, ha *hav1alpha1.HomeAssistant) error {
	log := logf.FromContext(ctx)

	sts := &appsv1.StatefulSet{}
	err := r.Get(ctx, types.NamespacedName{Name: ha.Name, Namespace: ha.Namespace}, sts)

	if err != nil && errors.IsNotFound(err) {
		// Create new StatefulSet
		sts = r.buildStatefulSet(ha)
		if err := controllerutil.SetControllerReference(ha, sts, r.Scheme); err != nil {
			return err
		}
		log.Info("Creating StatefulSet", "StatefulSet.Name", sts.Name)
		return r.Create(ctx, sts)
	} else if err != nil {
		return err
	}

	// Update StatefulSet if needed
	desired := r.buildStatefulSet(ha)
	if needsUpdate(sts, desired) {
		sts.Spec = desired.Spec
		log.Info("Updating StatefulSet", "StatefulSet.Name", sts.Name)
		return r.Update(ctx, sts)
	}

	return nil
}

// buildStatefulSet creates a StatefulSet spec for Home Assistant
func (r *HomeAssistantReconciler) buildStatefulSet(ha *hav1alpha1.HomeAssistant) *appsv1.StatefulSet {
	labels := r.labelsForHomeAssistant(ha)
	replicas := int32(1)

	image := defaultImage
	if ha.Spec.Image != "" {
		image = ha.Spec.Image
	}

	version := defaultVersion
	if ha.Spec.Version != "" {
		version = ha.Spec.Version
	}

	timezone := defaultTimezone
	if ha.Spec.Timezone != "" {
		timezone = ha.Spec.Timezone
	}

	pvcName := fmt.Sprintf("%s-config", ha.Name)

	// Build volume mounts
	volumeMounts := []corev1.VolumeMount{
		{
			Name:      "config",
			MountPath: "/config",
		},
	}

	// Build volumes
	volumes := []corev1.Volume{
		{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: pvcName,
				},
			},
		},
	}

	// Add ConfigMap volume for configuration.yaml if specified
	if ha.Spec.ConfigurationFrom != nil {
		volumes = append(volumes, corev1.Volume{
			Name: "ha-configuration",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: ha.Spec.ConfigurationFrom.Name,
					},
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "ha-configuration",
			MountPath: "/config/configuration.yaml",
			SubPath:   "configuration.yaml",
		})
	}

	// Add Secret volume for secrets.yaml if specified
	if ha.Spec.SecretsFrom != nil {
		volumes = append(volumes, corev1.Volume{
			Name: "ha-secrets",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: ha.Spec.SecretsFrom.Name,
				},
			},
		})
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "ha-secrets",
			MountPath: "/config/secrets.yaml",
			SubPath:   "secrets.yaml",
		})
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ha.Name,
			Namespace: ha.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			ServiceName: ha.Name,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "home-assistant",
							Image: fmt.Sprintf("%s:%s", image, version),
							Ports: []corev1.ContainerPort{
								{
									Name:          "http",
									ContainerPort: defaultPort,
									Protocol:      corev1.ProtocolTCP,
								},
							},
							Env: []corev1.EnvVar{
								{
									Name:  "TZ",
									Value: timezone,
								},
							},
							VolumeMounts: volumeMounts,
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/",
										Port: intstr.FromInt(defaultPort),
									},
								},
								InitialDelaySeconds: 30,
								PeriodSeconds:       10,
								TimeoutSeconds:      5,
								FailureThreshold:    3,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/",
										Port: intstr.FromInt(defaultPort),
									},
								},
								InitialDelaySeconds: 10,
								PeriodSeconds:       5,
								TimeoutSeconds:      3,
								FailureThreshold:    3,
							},
						},
					},
					Volumes: volumes,
				},
			},
		},
	}

	// Apply resource requirements if specified
	if ha.Spec.Resources.Limits != nil || ha.Spec.Resources.Requests != nil {
		sts.Spec.Template.Spec.Containers[0].Resources = ha.Spec.Resources
	}

	return sts
}

// reconcileService ensures the Service exists and is up to date
func (r *HomeAssistantReconciler) reconcileService(ctx context.Context, ha *hav1alpha1.HomeAssistant) error {
	log := logf.FromContext(ctx)

	svc := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: ha.Name, Namespace: ha.Namespace}, svc)

	if err != nil && errors.IsNotFound(err) {
		// Create new Service
		svc = r.buildService(ha)
		if err := controllerutil.SetControllerReference(ha, svc, r.Scheme); err != nil {
			return err
		}
		log.Info("Creating Service", "Service.Name", svc.Name)
		return r.Create(ctx, svc)
	} else if err != nil {
		return err
	}

	// Update Service if needed
	desired := r.buildService(ha)
	if svc.Spec.Type != desired.Spec.Type || len(svc.Spec.Ports) == 0 || len(desired.Spec.Ports) == 0 || svc.Spec.Ports[0].Port != desired.Spec.Ports[0].Port {
		svc.Spec.Type = desired.Spec.Type
		svc.Spec.Ports = desired.Spec.Ports
		log.Info("Updating Service", "Service.Name", svc.Name)
		return r.Update(ctx, svc)
	}

	return nil
}

// buildService creates a Service spec for Home Assistant
func (r *HomeAssistantReconciler) buildService(ha *hav1alpha1.HomeAssistant) *corev1.Service {
	labels := r.labelsForHomeAssistant(ha)

	serviceType := corev1.ServiceTypeClusterIP
	if ha.Spec.Service != nil && ha.Spec.Service.Type != "" {
		serviceType = ha.Spec.Service.Type
	}

	port := int32(defaultPort)
	if ha.Spec.Service != nil && ha.Spec.Service.Port != 0 {
		port = ha.Spec.Service.Port
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ha.Name,
			Namespace: ha.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:     serviceType,
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Name:       "http",
					Port:       port,
					TargetPort: intstr.FromInt(defaultPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}

	// Set NodePort if specified
	if serviceType == corev1.ServiceTypeNodePort && ha.Spec.Service != nil && ha.Spec.Service.NodePort != 0 {
		svc.Spec.Ports[0].NodePort = ha.Spec.Service.NodePort
	}

	return svc
}

// labelsForHomeAssistant returns the labels for selecting resources belonging to the given HomeAssistant CR
func (r *HomeAssistantReconciler) labelsForHomeAssistant(ha *hav1alpha1.HomeAssistant) map[string]string {
	return map[string]string{
		labelAppName:      "homeassistant",
		labelAppInstance:  ha.Name,
		labelAppManagedBy: "homeassistant-operator",
	}
}

// updateStatusFailed updates the status when reconciliation fails
func (r *HomeAssistantReconciler) updateStatusFailed(ctx context.Context, ha *hav1alpha1.HomeAssistant, reconcileErr error) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	ha.Status.Phase = hav1alpha1.PhaseFailed
	ha.Status.Ready = false

	meta.SetStatusCondition(&ha.Status.Conditions, metav1.Condition{
		Type:    conditionTypeReady,
		Status:  metav1.ConditionFalse,
		Reason:  "ReconciliationFailed",
		Message: reconcileErr.Error(),
	})

	if err := r.Status().Update(ctx, ha); err != nil {
		log.Error(err, "Failed to update HomeAssistant status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, reconcileErr
}

// updateStatusFromStatefulSet updates the status based on StatefulSet state
func (r *HomeAssistantReconciler) updateStatusFromStatefulSet(ctx context.Context, ha *hav1alpha1.HomeAssistant) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: ha.Name, Namespace: ha.Namespace}, sts); err != nil {
		return ctrl.Result{}, err
	}

	// Determine version from image
	version := defaultVersion
	if ha.Spec.Version != "" {
		version = ha.Spec.Version
	}
	ha.Status.Version = version

	// Check if StatefulSet is ready
	if sts.Status.ReadyReplicas > 0 && sts.Status.ReadyReplicas == sts.Status.Replicas {
		ha.Status.Phase = hav1alpha1.PhaseRunning
		ha.Status.Ready = true

		meta.SetStatusCondition(&ha.Status.Conditions, metav1.Condition{
			Type:    conditionTypeReady,
			Status:  metav1.ConditionTrue,
			Reason:  "StatefulSetReady",
			Message: "Home Assistant is running",
		})
	} else {
		ha.Status.Phase = hav1alpha1.PhasePending
		ha.Status.Ready = false

		meta.SetStatusCondition(&ha.Status.Conditions, metav1.Condition{
			Type:    conditionTypeReady,
			Status:  metav1.ConditionFalse,
			Reason:  "StatefulSetNotReady",
			Message: fmt.Sprintf("Waiting for StatefulSet to be ready (%d/%d)", sts.Status.ReadyReplicas, sts.Status.Replicas),
		})
	}

	if err := r.Status().Update(ctx, ha); err != nil {
		log.Error(err, "Failed to update HomeAssistant status")
		return ctrl.Result{}, err
	}

	// Requeue if not ready yet
	if !ha.Status.Ready {
		return ctrl.Result{RequeueAfter: 10 * 1000000000}, nil // 10 seconds
	}

	return ctrl.Result{}, nil
}

// needsUpdate checks if the StatefulSet needs to be updated
func needsUpdate(current, desired *appsv1.StatefulSet) bool {
	// Ensure containers exist
	if len(current.Spec.Template.Spec.Containers) == 0 || len(desired.Spec.Template.Spec.Containers) == 0 {
		return len(current.Spec.Template.Spec.Containers) != len(desired.Spec.Template.Spec.Containers)
	}

	currentContainer := current.Spec.Template.Spec.Containers[0]
	desiredContainer := desired.Spec.Template.Spec.Containers[0]

	// Check image
	if currentContainer.Image != desiredContainer.Image {
		return true
	}

	// Check volumes count (ConfigMap/Secret added or removed)
	if len(current.Spec.Template.Spec.Volumes) != len(desired.Spec.Template.Spec.Volumes) {
		return true
	}

	// Check volume mounts count
	if len(currentContainer.VolumeMounts) != len(desiredContainer.VolumeMounts) {
		return true
	}

	// Check environment variables
	if len(currentContainer.Env) != len(desiredContainer.Env) {
		return true
	}
	for i, env := range currentContainer.Env {
		if i >= len(desiredContainer.Env) {
			return true
		}
		if env.Name != desiredContainer.Env[i].Name || env.Value != desiredContainer.Env[i].Value {
			return true
		}
	}

	// Check resource limits and requests
	if !resourcesEqual(currentContainer.Resources, desiredContainer.Resources) {
		return true
	}

	// Check liveness probe
	if !probesEqual(currentContainer.LivenessProbe, desiredContainer.LivenessProbe) {
		return true
	}

	// Check readiness probe
	if !probesEqual(currentContainer.ReadinessProbe, desiredContainer.ReadinessProbe) {
		return true
	}

	return false
}

// resourcesEqual compares two ResourceRequirements
func resourcesEqual(current, desired corev1.ResourceRequirements) bool {
	return limitsEqual(current.Limits, desired.Limits) && limitsEqual(current.Requests, desired.Requests)
}

// limitsEqual compares two ResourceLists
func limitsEqual(current, desired corev1.ResourceList) bool {
	if len(current) != len(desired) {
		return false
	}
	for key, val := range current {
		if desiredVal, ok := desired[key]; !ok || val.Cmp(desiredVal) != 0 {
			return false
		}
	}
	return true
}

// probesEqual compares two Probe pointers
func probesEqual(current, desired *corev1.Probe) bool {
	if (current == nil) != (desired == nil) {
		return false
	}
	if current == nil {
		return true
	}

	// Compare probe settings
	if current.InitialDelaySeconds != desired.InitialDelaySeconds ||
		current.TimeoutSeconds != desired.TimeoutSeconds ||
		current.PeriodSeconds != desired.PeriodSeconds ||
		current.SuccessThreshold != desired.SuccessThreshold ||
		current.FailureThreshold != desired.FailureThreshold {
		return false
	}

	// Compare HTTPGet handler
	if (current.HTTPGet == nil) != (desired.HTTPGet == nil) {
		return false
	}
	if current.HTTPGet != nil && desired.HTTPGet != nil {
		if current.HTTPGet.Path != desired.HTTPGet.Path ||
			current.HTTPGet.Port != desired.HTTPGet.Port ||
			current.HTTPGet.Scheme != desired.HTTPGet.Scheme {
			return false
		}
	}

	return true
}

// SetupWithManager sets up the controller with the Manager.
func (r *HomeAssistantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hav1alpha1.HomeAssistant{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Named("homeassistant").
		Complete(r)
}
