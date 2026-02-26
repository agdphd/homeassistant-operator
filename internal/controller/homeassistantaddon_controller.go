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
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hav1alpha1 "github.com/przemekhys/homeassistant-operator/api/v1alpha1"
)

const (
	addonFinalizerName = "ha.homeassistant.io/addon-finalizer"

	// Condition reasons for HomeAssistantAddon
	reasonAddonCreated         = "AddonCreated"
	reasonAddonFailed          = "AddonFailed"
	reasonAddonPending         = "AddonPending"
	reasonHAIntegrationUpdated = "HAIntegrationUpdated"
	reasonHAIntegrationFailed  = "HAIntegrationFailed"
	reasonHAIntegrationRemoved = "HAIntegrationRemoved"
	reasonWorkloadReady        = "WorkloadReady"
	reasonServiceReady         = "ServiceReady"
	reasonServiceFailed        = "ServiceFailed"
)

// HomeAssistantAddonReconciler reconciles a HomeAssistantAddon object
type HomeAssistantAddonReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantaddons,verbs=get;list;watch
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantaddons,verbs=create;update;patch;delete
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantaddons/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantaddons/finalizers,verbs=update
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistants,verbs=get;list;watch
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantconfigurations,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=ingresses,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HomeAssistantAddonReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, err error) {
	start := time.Now()
	profile := "custom"
	defer func() {
		result := "success"
		if err != nil {
			result = "failed"
		}
		addonReconcileTotal.WithLabelValues(result, profile).Inc()
		addonReconcileDuration.WithLabelValues(profile).Observe(time.Since(start).Seconds())
	}()

	log := logf.FromContext(ctx)

	// Fetch the HomeAssistantAddon instance
	addon := &hav1alpha1.HomeAssistantAddon{}
	if err = r.Get(ctx, req.NamespacedName, addon); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get HomeAssistantAddon")
		return ctrl.Result{}, err
	}

	// Set profile label for metrics now that we have the addon
	if addon.Spec.Profile != "" {
		profile = addon.Spec.Profile
	}

	// Handle deletion via finalizer
	if !addon.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(addon, addonFinalizerName) {
			log.Info("Handling deletion - removing HAIntegration if present")
			if err = r.cleanupHAIntegration(ctx, addon); err != nil {
				log.Error(err, "Failed to clean up HAIntegration during deletion")
				return ctrl.Result{}, err
			}
			r.Recorder.Eventf(addon, nil, corev1.EventTypeNormal, "AddonDeleted", "AddonDeleted",
				"Addon %s deleted", addon.Name)
			controllerutil.RemoveFinalizer(addon, addonFinalizerName)
			if err = r.Update(ctx, addon); err != nil {
				log.Error(err, "Failed to remove finalizer")
				return ctrl.Result{}, err
			}
			log.Info("Finalizer removed, addon deleted")
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(addon, addonFinalizerName) {
		log.Info("Adding finalizer to addon")
		controllerutil.AddFinalizer(addon, addonFinalizerName)
		if err = r.Update(ctx, addon); err != nil {
			log.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		log.V(1).Info("Finalizer added, continuing reconciliation")
	}

	// Validate HomeAssistant reference
	haRef := types.NamespacedName{
		Name:      addon.Spec.HomeAssistantRef.Name,
		Namespace: addon.Namespace,
	}
	_, haErr := getHomeAssistant(ctx, r.Client, haRef)
	if haErr != nil {
		if errors.IsNotFound(haErr) {
			log.Info("Referenced HomeAssistant not found, requeuing", "ha", haRef.Name)
			meta.SetStatusCondition(&addon.Status.Conditions, metav1.Condition{
				Type:               conditionTypeReady,
				Status:             metav1.ConditionFalse,
				Reason:             reasonAddonPending,
				Message:            fmt.Sprintf("HomeAssistant %q not found", haRef.Name),
				ObservedGeneration: addon.Generation,
			})
			_ = r.Status().Update(ctx, addon)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		return ctrl.Result{}, haErr
	}

	// Resolve spec (profile + overrides)
	resolved, specErr := resolveAddonSpec(&addon.Spec)
	if specErr != nil {
		log.Error(specErr, "Failed to resolve addon spec")
		meta.SetStatusCondition(&addon.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             reasonAddonFailed,
			Message:            fmt.Sprintf("Failed to resolve spec: %v", specErr),
			ObservedGeneration: addon.Generation,
		})
		addon.Status.Phase = hav1alpha1.AddonPhaseFailed
		_ = r.Status().Update(ctx, addon)
		return ctrl.Result{}, nil // spec error: don't requeue automatically, user must fix the spec
	}

	// Reconcile ConfigMap (addon config files)
	if err = r.reconcileConfigMap(ctx, addon, resolved); err != nil {
		log.Error(err, "Failed to reconcile ConfigMap")
		return ctrl.Result{}, err
	}

	// Reconcile workload (Deployment or StatefulSet)
	if err = r.reconcileWorkload(ctx, addon, resolved); err != nil {
		log.Error(err, "Failed to reconcile workload")
		meta.SetStatusCondition(&addon.Status.Conditions, metav1.Condition{
			Type:               "WorkloadReady",
			Status:             metav1.ConditionFalse,
			Reason:             reasonAddonFailed,
			Message:            fmt.Sprintf("Failed to reconcile workload: %v", err),
			ObservedGeneration: addon.Generation,
		})
		_ = r.Status().Update(ctx, addon)
		return ctrl.Result{}, err
	}

	// Reconcile Service
	if err = r.reconcileService(ctx, addon, resolved); err != nil {
		log.Error(err, "Failed to reconcile Service")
		meta.SetStatusCondition(&addon.Status.Conditions, metav1.Condition{
			Type:               "ServiceReady",
			Status:             metav1.ConditionFalse,
			Reason:             reasonServiceFailed,
			Message:            fmt.Sprintf("Failed to reconcile Service: %v", err),
			ObservedGeneration: addon.Generation,
		})
		_ = r.Status().Update(ctx, addon)
		return ctrl.Result{}, err
	}

	// Reconcile Ingress
	if err = r.reconcileIngress(ctx, addon, resolved); err != nil {
		log.Error(err, "Failed to reconcile Ingress")
		return ctrl.Result{}, err
	}

	// Reconcile HAIntegration (best-effort — failure does not block the addon)
	if haIntegrationErr := r.reconcileHAIntegration(ctx, addon, resolved); haIntegrationErr != nil {
		log.Error(haIntegrationErr, "Failed to reconcile HAIntegration")
	}

	// Calculate hash for change detection
	addonHash, hashErr := r.calculateAddonHash(resolved)
	if hashErr != nil {
		log.Error(hashErr, "Failed to calculate addon hash")
		return ctrl.Result{}, hashErr
	}

	// Update status — all sub-reconcilers succeeded
	serviceName := addonResourceName(addon)
	workloadType := "Deployment"
	if resolved.Storage != nil {
		workloadType = "StatefulSet"
	}

	addon.Status.Phase = hav1alpha1.AddonPhaseRunning
	addon.Status.ResolvedImage = resolved.Image
	addon.Status.AddonHash = addonHash
	addon.Status.ObservedGeneration = addon.Generation
	addon.Status.WorkloadType = workloadType
	addon.Status.ServiceName = serviceName

	meta.SetStatusCondition(&addon.Status.Conditions, metav1.Condition{
		Type:               "WorkloadReady",
		Status:             metav1.ConditionTrue,
		Reason:             reasonWorkloadReady,
		Message:            fmt.Sprintf("%s %s reconciled successfully", workloadType, addonResourceName(addon)),
		ObservedGeneration: addon.Generation,
	})
	meta.SetStatusCondition(&addon.Status.Conditions, metav1.Condition{
		Type:               "ServiceReady",
		Status:             metav1.ConditionTrue,
		Reason:             reasonServiceReady,
		Message:            fmt.Sprintf("Service %s reconciled successfully", addonResourceName(addon)),
		ObservedGeneration: addon.Generation,
	})
	meta.SetStatusCondition(&addon.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             reasonAddonCreated,
		Message:            fmt.Sprintf("Addon %s running as %s", resolved.Image, workloadType),
		ObservedGeneration: addon.Generation,
	})

	if err = r.Status().Update(ctx, addon); err != nil {
		log.Error(err, "Failed to update HomeAssistantAddon status")
		return ctrl.Result{}, err
	}

	log.Info("Successfully reconciled HomeAssistantAddon",
		"image", resolved.Image,
		"workload", workloadType,
		"hash", addonHash)

	return ctrl.Result{}, nil
}

// addonResourceName returns the base name for all resources owned by this addon:
// "<ha-name>-<addon-name>"
func addonResourceName(addon *hav1alpha1.HomeAssistantAddon) string {
	return fmt.Sprintf("%s-%s", addon.Spec.HomeAssistantRef.Name, addon.Name)
}

// reconcileConfigMap creates or updates the ConfigMap with addon config files.
// No-op if resolved.Config is nil.
func (r *HomeAssistantAddonReconciler) reconcileConfigMap(
	ctx context.Context,
	addon *hav1alpha1.HomeAssistantAddon,
	resolved *ResolvedAddonSpec,
) error {
	if resolved.Config == nil {
		return nil
	}

	log := logf.FromContext(ctx)
	name := addonResourceName(addon) + addonConfigMapSuffix

	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: addon.Namespace,
			Labels:    addonLabels(addon),
		},
		Data: resolved.Config.Data,
	}

	if err := controllerutil.SetControllerReference(addon, desired, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference on ConfigMap: %w", err)
	}

	existing := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: addon.Namespace}, existing)
	if errors.IsNotFound(err) {
		log.Info("Creating addon ConfigMap", "name", name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return fmt.Errorf("failed to get ConfigMap: %w", err)
	}

	if !reflect.DeepEqual(existing.Data, desired.Data) || !reflect.DeepEqual(existing.Labels, desired.Labels) {
		existing.Data = desired.Data
		existing.Labels = desired.Labels
		log.Info("Updating addon ConfigMap", "name", name)
		return r.Update(ctx, existing)
	}
	return nil
}

// reconcileWorkload creates/updates either a Deployment or a StatefulSet
// depending on whether storage is configured.
func (r *HomeAssistantAddonReconciler) reconcileWorkload(
	ctx context.Context,
	addon *hav1alpha1.HomeAssistantAddon,
	resolved *ResolvedAddonSpec,
) error {
	if resolved.Storage != nil {
		return r.reconcileStatefulSet(ctx, addon, resolved)
	}
	return r.reconcileDeployment(ctx, addon, resolved)
}

// reconcileDeployment creates or updates a Deployment for addons without storage.
func (r *HomeAssistantAddonReconciler) reconcileDeployment(
	ctx context.Context,
	addon *hav1alpha1.HomeAssistantAddon,
	resolved *ResolvedAddonSpec,
) error {
	log := logf.FromContext(ctx)
	name := addonResourceName(addon)

	// If a StatefulSet exists (e.g., storage was removed from spec), delete it first.
	staleSts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: addon.Namespace}, staleSts); err == nil {
		log.Info("Deleting stale StatefulSet (storage removed from spec)", "name", name)
		if err := r.Delete(ctx, staleSts); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete stale StatefulSet: %w", err)
		}
	}
	replicas := int32(1)
	desired := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: addon.Namespace,
			Labels:    addonLabels(addon),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: addonSelectorLabels(addon),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: addonSelectorLabels(addon),
				},
				Spec: r.buildPodSpec(addon, resolved, nil),
			},
		},
	}

	if err := controllerutil.SetControllerReference(addon, desired, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference on Deployment: %w", err)
	}

	existing := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: addon.Namespace}, existing)
	if errors.IsNotFound(err) {
		log.Info("Creating Deployment", "name", name)
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create Deployment: %w", err)
		}
		r.Recorder.Eventf(addon, nil, corev1.EventTypeNormal, "AddonCreated", "AddonCreated",
			"Created Deployment %s", name)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get Deployment: %w", err)
	}

	if deploymentNeedsUpdate(existing, desired) {
		existing.Labels = desired.Labels
		existing.Spec = desired.Spec
		log.Info("Updating Deployment", "name", name)
		if err := r.Update(ctx, existing); err != nil {
			return fmt.Errorf("failed to update Deployment: %w", err)
		}
		r.Recorder.Eventf(addon, nil, corev1.EventTypeNormal, "AddonUpdated", "AddonUpdated",
			"Updated Deployment %s", name)
	}
	return nil
}

// reconcileStatefulSet creates or updates a StatefulSet for addons with storage.
func (r *HomeAssistantAddonReconciler) reconcileStatefulSet(
	ctx context.Context,
	addon *hav1alpha1.HomeAssistantAddon,
	resolved *ResolvedAddonSpec,
) error {
	log := logf.FromContext(ctx)
	name := addonResourceName(addon)

	// If a Deployment exists (e.g., storage was added to spec), delete it first.
	staleDeployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: addon.Namespace}, staleDeployment); err == nil {
		log.Info("Deleting stale Deployment (storage added to spec)", "name", name)
		if err := r.Delete(ctx, staleDeployment); err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("failed to delete stale Deployment: %w", err)
		}
	}

	storageSize := resolved.Storage.Size
	if storageSize.IsZero() {
		storageSize = resource.MustParse("1Gi")
	}
	mountPath := resolved.Storage.MountPath
	if mountPath == "" {
		mountPath = "/data"
	}

	pvcName := name + "-data"
	replicas := int32(1)

	volumeClaimTemplates := []corev1.PersistentVolumeClaim{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: pvcName,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: storageSize,
					},
				},
				StorageClassName: resolved.Storage.StorageClassName,
			},
		},
	}

	pvcMount := &corev1.VolumeMount{
		Name:      pvcName,
		MountPath: mountPath,
	}

	desired := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: addon.Namespace,
			Labels:    addonLabels(addon),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: name,
			Selector: &metav1.LabelSelector{
				MatchLabels: addonSelectorLabels(addon),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: addonSelectorLabels(addon),
				},
				Spec: r.buildPodSpec(addon, resolved, pvcMount),
			},
			VolumeClaimTemplates: volumeClaimTemplates,
		},
	}

	if err := controllerutil.SetControllerReference(addon, desired, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference on StatefulSet: %w", err)
	}

	existing := &appsv1.StatefulSet{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: addon.Namespace}, existing)
	if errors.IsNotFound(err) {
		log.Info("Creating StatefulSet", "name", name)
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("failed to create StatefulSet: %w", err)
		}
		r.Recorder.Eventf(addon, nil, corev1.EventTypeNormal, "AddonCreated", "AddonCreated",
			"Created StatefulSet %s", name)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to get StatefulSet: %w", err)
	}

	if statefulSetNeedsUpdate(existing, desired) {
		existing.Labels = desired.Labels
		existing.Spec.Replicas = desired.Spec.Replicas
		existing.Spec.Template = desired.Spec.Template
		log.Info("Updating StatefulSet", "name", name)
		if err := r.Update(ctx, existing); err != nil {
			return fmt.Errorf("failed to update StatefulSet: %w", err)
		}
		r.Recorder.Eventf(addon, nil, corev1.EventTypeNormal, "AddonUpdated", "AddonUpdated",
			"Updated StatefulSet %s", name)
	}
	return nil
}

// reconcileService creates or updates the Kubernetes Service for the addon.
// No-op if resolved.Ports is empty.
func (r *HomeAssistantAddonReconciler) reconcileService(
	ctx context.Context,
	addon *hav1alpha1.HomeAssistantAddon,
	resolved *ResolvedAddonSpec,
) error {
	if len(resolved.Ports) == 0 {
		return nil
	}

	log := logf.FromContext(ctx)
	name := addonResourceName(addon)

	serviceType := corev1.ServiceTypeClusterIP
	var servicePorts []corev1.ServicePort
	for _, p := range resolved.Ports {
		protocol := corev1.Protocol(p.Protocol)
		if protocol == "" {
			protocol = corev1.ProtocolTCP
		}
		sp := corev1.ServicePort{
			Name:       p.Name,
			Port:       p.Port,
			TargetPort: intstr.FromInt(int(p.Port)),
			Protocol:   protocol,
		}
		if p.NodePort > 0 {
			sp.NodePort = p.NodePort
			serviceType = corev1.ServiceTypeNodePort
		}
		servicePorts = append(servicePorts, sp)
	}

	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: addon.Namespace,
			Labels:    addonLabels(addon),
		},
		Spec: corev1.ServiceSpec{
			Type:     serviceType,
			Selector: addonSelectorLabels(addon),
			Ports:    servicePorts,
		},
	}

	if err := controllerutil.SetControllerReference(addon, desired, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference on Service: %w", err)
	}

	existing := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: addon.Namespace}, existing)
	if errors.IsNotFound(err) {
		log.Info("Creating Service", "name", name)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return fmt.Errorf("failed to get Service: %w", err)
	}

	if serviceNeedsUpdate(existing, desired) {
		existing.Labels = desired.Labels
		existing.Spec.Ports = desired.Spec.Ports
		existing.Spec.Selector = desired.Spec.Selector
		existing.Spec.Type = desired.Spec.Type
		log.Info("Updating Service", "name", name)
		if err := r.Update(ctx, existing); err != nil {
			return fmt.Errorf("failed to update Service: %w", err)
		}
	}
	return nil
}

// reconcileIngress creates, updates or deletes an Ingress resource for the addon.
func (r *HomeAssistantAddonReconciler) reconcileIngress(
	ctx context.Context,
	addon *hav1alpha1.HomeAssistantAddon,
	resolved *ResolvedAddonSpec,
) error {
	log := logf.FromContext(ctx)
	name := addonResourceName(addon)

	if resolved.Ingress == nil || !resolved.Ingress.Enabled {
		// Delete Ingress if it exists
		existing := &networkingv1.Ingress{}
		err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: addon.Namespace}, existing)
		if err == nil {
			log.Info("Deleting Ingress (disabled)", "name", name)
			return r.Delete(ctx, existing)
		}
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get Ingress for deletion: %w", err)
	}

	// Determine target port
	targetPort := resolved.Ingress.TargetPort
	if targetPort == 0 && len(resolved.Ports) > 0 {
		targetPort = resolved.Ports[0].Port
	}
	if targetPort == 0 {
		return fmt.Errorf("cannot create Ingress: no target port specified and no ports defined")
	}

	pathType := networkingv1.PathTypePrefix
	desired := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   addon.Namespace,
			Labels:      addonLabels(addon),
			Annotations: resolved.Ingress.Annotations,
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: resolved.Ingress.IngressClassName,
			Rules: []networkingv1.IngressRule{
				{
					Host: resolved.Ingress.Host,
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathType,
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: name,
											Port: networkingv1.ServiceBackendPort{
												Number: targetPort,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	if resolved.Ingress.TLS && resolved.Ingress.Host != "" {
		desired.Spec.TLS = []networkingv1.IngressTLS{
			{
				Hosts:      []string{resolved.Ingress.Host},
				SecretName: name + "-tls",
			},
		}
	}

	if err := controllerutil.SetControllerReference(addon, desired, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference on Ingress: %w", err)
	}

	existing := &networkingv1.Ingress{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: addon.Namespace}, existing)
	if errors.IsNotFound(err) {
		log.Info("Creating Ingress", "name", name, "host", resolved.Ingress.Host)
		return r.Create(ctx, desired)
	}
	if err != nil {
		return fmt.Errorf("failed to get Ingress: %w", err)
	}

	if ingressNeedsUpdate(existing, desired) {
		existing.Labels = desired.Labels
		existing.Annotations = desired.Annotations
		existing.Spec = desired.Spec
		log.Info("Updating Ingress", "name", name)
		if err := r.Update(ctx, existing); err != nil {
			return fmt.Errorf("failed to update Ingress: %w", err)
		}
	}
	return nil
}

// reconcileHAIntegration adds or updates the integration section in HomeAssistantConfiguration CR.
// If the HAConfig CR doesn't exist, the function logs a warning and returns nil (best-effort).
func (r *HomeAssistantAddonReconciler) reconcileHAIntegration(
	ctx context.Context,
	addon *hav1alpha1.HomeAssistantAddon,
	resolved *ResolvedAddonSpec,
) error {
	if resolved.HAIntegration == nil {
		return nil
	}

	log := logf.FromContext(ctx)

	haConfig := &hav1alpha1.HomeAssistantConfiguration{}
	haConfigName := types.NamespacedName{
		Name:      addon.Spec.HomeAssistantRef.Name + "-config",
		Namespace: addon.Namespace,
	}
	if err := r.Get(ctx, haConfigName, haConfig); err != nil {
		if errors.IsNotFound(err) {
			log.Info("HomeAssistantConfiguration not found, skipping HAIntegration",
				"haConfig", haConfigName.Name)
			return nil
		}
		return fmt.Errorf("failed to get HomeAssistantConfiguration: %w", err)
	}

	// Parse existing configuration.yaml
	existingConfig := make(map[string]interface{})
	if haConfig.Spec.Configuration != "" {
		if err := yaml.Unmarshal([]byte(haConfig.Spec.Configuration), &existingConfig); err != nil {
			return fmt.Errorf("failed to parse HomeAssistantConfiguration YAML: %w", err)
		}
	}

	// Build integration config
	integrationConfig := make(map[string]interface{})

	// Parse user-provided configuration if any
	if len(resolved.HAIntegration.Configuration.Raw) > 0 {
		if err := json.Unmarshal(resolved.HAIntegration.Configuration.Raw, &integrationConfig); err != nil {
			return fmt.Errorf("failed to parse HAIntegration configuration: %w", err)
		}
	}

	// Inject Service DNS as broker/host if not explicitly set
	serviceDNS := fmt.Sprintf("%s.%s.svc.cluster.local",
		addonResourceName(addon), addon.Namespace)

	section := resolved.HAIntegration.Section
	if section == "mqtt" {
		if _, ok := integrationConfig["broker"]; !ok {
			integrationConfig["broker"] = serviceDNS
		}
	} else {
		// Generic: inject "host" if not set
		if _, ok := integrationConfig["host"]; !ok {
			integrationConfig["host"] = serviceDNS
		}
	}

	// Check if the section already matches (avoid unnecessary updates)
	if existing, ok := existingConfig[section]; ok {
		existingBytes, _ := yaml.Marshal(existing)
		desiredBytes, _ := yaml.Marshal(integrationConfig)
		if string(existingBytes) == string(desiredBytes) {
			log.V(1).Info("HAIntegration section unchanged, skipping update", "section", section)
			return nil
		}
	}

	existingConfig[section] = integrationConfig

	// Serialize back to YAML
	updatedYAML, err := yaml.Marshal(existingConfig)
	if err != nil {
		return fmt.Errorf("failed to serialize updated configuration: %w", err)
	}

	haConfig.Spec.Configuration = string(updatedYAML)
	if err := r.Update(ctx, haConfig); err != nil {
		meta.SetStatusCondition(&addon.Status.Conditions, metav1.Condition{
			Type:               "HAIntegrationReady",
			Status:             metav1.ConditionFalse,
			Reason:             reasonHAIntegrationFailed,
			Message:            fmt.Sprintf("Failed to update HomeAssistantConfiguration: %v", err),
			ObservedGeneration: addon.Generation,
		})
		return fmt.Errorf("failed to update HomeAssistantConfiguration: %w", err)
	}

	meta.SetStatusCondition(&addon.Status.Conditions, metav1.Condition{
		Type:               "HAIntegrationReady",
		Status:             metav1.ConditionTrue,
		Reason:             reasonHAIntegrationUpdated,
		Message:            fmt.Sprintf("Integration section %q updated in HomeAssistantConfiguration", section),
		ObservedGeneration: addon.Generation,
	})

	r.Recorder.Eventf(addon, nil, corev1.EventTypeNormal,
		"HAIntegrationUpdated", "HAIntegrationUpdated",
		"Updated section %q in HomeAssistantConfiguration %s", section, haConfigName.Name)

	log.Info("Updated HAIntegration section in HomeAssistantConfiguration",
		"section", section, "haConfig", haConfigName.Name)

	return nil
}

// cleanupHAIntegration removes the integration section from HomeAssistantConfiguration
// when the addon is deleted.
func (r *HomeAssistantAddonReconciler) cleanupHAIntegration(
	ctx context.Context,
	addon *hav1alpha1.HomeAssistantAddon,
) error {
	resolved, err := resolveAddonSpec(&addon.Spec)
	if err != nil || resolved.HAIntegration == nil {
		return nil
	}

	log := logf.FromContext(ctx)

	haConfig := &hav1alpha1.HomeAssistantConfiguration{}
	haConfigName := types.NamespacedName{
		Name:      addon.Spec.HomeAssistantRef.Name + "-config",
		Namespace: addon.Namespace,
	}
	if err := r.Get(ctx, haConfigName, haConfig); err != nil {
		if errors.IsNotFound(err) {
			return nil // Already gone
		}
		return fmt.Errorf("failed to get HomeAssistantConfiguration: %w", err)
	}

	existingConfig := make(map[string]interface{})
	if haConfig.Spec.Configuration != "" {
		if err := yaml.Unmarshal([]byte(haConfig.Spec.Configuration), &existingConfig); err != nil {
			return nil // Can't parse; skip cleanup
		}
	}

	section := resolved.HAIntegration.Section
	if _, ok := existingConfig[section]; !ok {
		return nil // Section doesn't exist; nothing to clean up
	}

	delete(existingConfig, section)

	updatedYAML, err := yaml.Marshal(existingConfig)
	if err != nil {
		return fmt.Errorf("failed to serialize configuration after cleanup: %w", err)
	}

	haConfig.Spec.Configuration = string(updatedYAML)
	if err := r.Update(ctx, haConfig); err != nil {
		return fmt.Errorf("failed to update HomeAssistantConfiguration after cleanup: %w", err)
	}

	r.Recorder.Eventf(addon, nil, corev1.EventTypeNormal,
		"HAIntegrationRemoved", "HAIntegrationRemoved",
		"Removed section %q from HomeAssistantConfiguration %s", section, haConfigName.Name)

	log.Info("Removed HAIntegration section from HomeAssistantConfiguration",
		"section", section, "haConfig", haConfigName.Name)

	return nil
}

// buildPodSpec constructs the PodSpec shared by both Deployment and StatefulSet.
// pvcMount is optional — only passed for StatefulSet (volume claim template mount).
func (r *HomeAssistantAddonReconciler) buildPodSpec(
	addon *hav1alpha1.HomeAssistantAddon,
	resolved *ResolvedAddonSpec,
	pvcMount *corev1.VolumeMount,
) corev1.PodSpec {
	name := addonResourceName(addon)

	container := corev1.Container{
		Name:            name,
		Image:           resolved.Image,
		Env:             resolved.Env,
		Resources:       derefResources(resolved.Resources),
		LivenessProbe:   resolved.LivenessProbe,
		ReadinessProbe:  resolved.ReadinessProbe,
		SecurityContext: resolved.SecurityContext,
	}

	// Ports
	for _, p := range resolved.Ports {
		container.Ports = append(container.Ports, corev1.ContainerPort{
			Name:          p.Name,
			ContainerPort: p.Port,
			Protocol:      corev1.Protocol(p.Protocol),
		})
	}

	// PVC mount (StatefulSet only)
	if pvcMount != nil {
		container.VolumeMounts = append(container.VolumeMounts, *pvcMount)
	}

	// ConfigMap mount
	var volumes []corev1.Volume
	if resolved.Config != nil {
		configMapName := name + addonConfigMapSuffix
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "config",
			MountPath: resolved.Config.MountPath,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
				},
			},
		})
	}

	// Additional user-provided volume mounts and volumes
	container.VolumeMounts = append(container.VolumeMounts, resolved.VolumeMounts...)
	volumes = append(volumes, resolved.Volumes...)

	return corev1.PodSpec{
		HostNetwork: resolved.HostNetwork,
		Containers:  []corev1.Container{container},
		Volumes:     volumes,
	}
}

// calculateAddonHash computes a SHA256 hash of the resolved addon spec for change detection.
func (r *HomeAssistantAddonReconciler) calculateAddonHash(
	resolved *ResolvedAddonSpec,
) (string, error) {
	data, err := json.Marshal(map[string]interface{}{
		"image":           resolved.Image,
		"ports":           resolved.Ports,
		"storage":         resolved.Storage,
		"config":          resolved.Config,
		"env":             resolved.Env,
		"host":            resolved.HostNetwork,
		"res":             resolved.Resources,
		"ingress":         resolved.Ingress,
		"livenessProbe":   resolved.LivenessProbe,
		"readinessProbe":  resolved.ReadinessProbe,
		"securityContext": resolved.SecurityContext,
		"volumeMounts":    resolved.VolumeMounts,
		"volumes":         resolved.Volumes,
		"haIntegration":   resolved.HAIntegration,
	})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash), nil
}

// needsUpdate helpers

func deploymentNeedsUpdate(existing, desired *appsv1.Deployment) bool {
	if !reflect.DeepEqual(existing.Labels, desired.Labels) {
		return true
	}
	existingContainer := existing.Spec.Template.Spec.Containers
	desiredContainer := desired.Spec.Template.Spec.Containers
	if len(existingContainer) != len(desiredContainer) {
		return true
	}
	if len(existingContainer) > 0 && existingContainer[0].Image != desiredContainer[0].Image {
		return true
	}
	if !reflect.DeepEqual(existing.Spec.Template.Spec, desired.Spec.Template.Spec) {
		return true
	}
	return false
}

func statefulSetNeedsUpdate(existing, desired *appsv1.StatefulSet) bool {
	if !reflect.DeepEqual(existing.Labels, desired.Labels) {
		return true
	}
	existingContainer := existing.Spec.Template.Spec.Containers
	desiredContainer := desired.Spec.Template.Spec.Containers
	if len(existingContainer) != len(desiredContainer) {
		return true
	}
	if len(existingContainer) > 0 && existingContainer[0].Image != desiredContainer[0].Image {
		return true
	}
	if !reflect.DeepEqual(existing.Spec.Template.Spec, desired.Spec.Template.Spec) {
		return true
	}
	return false
}

func serviceNeedsUpdate(existing, desired *corev1.Service) bool {
	if !reflect.DeepEqual(existing.Labels, desired.Labels) {
		return true
	}
	if !reflect.DeepEqual(existing.Spec.Selector, desired.Spec.Selector) {
		return true
	}
	if existing.Spec.Type != desired.Spec.Type {
		return true
	}
	// Compare ports (ignoring ClusterIP assigned by K8s)
	if len(existing.Spec.Ports) != len(desired.Spec.Ports) {
		return true
	}
	for i := range desired.Spec.Ports {
		e := existing.Spec.Ports[i]
		d := desired.Spec.Ports[i]
		if e.Name != d.Name || e.Port != d.Port || e.Protocol != d.Protocol {
			return true
		}
	}
	return false
}

func ingressNeedsUpdate(existing, desired *networkingv1.Ingress) bool {
	if !reflect.DeepEqual(existing.Labels, desired.Labels) {
		return true
	}
	if !reflect.DeepEqual(existing.Annotations, desired.Annotations) {
		return true
	}
	if !reflect.DeepEqual(existing.Spec, desired.Spec) {
		return true
	}
	return false
}

// addonLabels returns standard labels for all resources managed by this addon.
func addonLabels(addon *hav1alpha1.HomeAssistantAddon) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/name":       "homeassistant-addon",
		"app.kubernetes.io/instance":   addon.Name,
		"app.kubernetes.io/managed-by": "homeassistant-operator",
		"ha.homeassistant.io/addon":    addon.Name,
		"ha.homeassistant.io/ha":       addon.Spec.HomeAssistantRef.Name,
	}
	if addon.Spec.Profile != "" {
		labels["ha.homeassistant.io/profile"] = addon.Spec.Profile
	}
	return labels
}

// addonSelectorLabels returns labels used by pod selectors.
func addonSelectorLabels(addon *hav1alpha1.HomeAssistantAddon) map[string]string {
	return map[string]string{
		"ha.homeassistant.io/addon": addon.Name,
		"ha.homeassistant.io/ha":    addon.Spec.HomeAssistantRef.Name,
	}
}

// derefResources safely dereferences a *corev1.ResourceRequirements.
func derefResources(r *corev1.ResourceRequirements) corev1.ResourceRequirements {
	if r == nil {
		return corev1.ResourceRequirements{}
	}
	return *r
}

// SetupWithManager sets up the controller with the Manager.
func (r *HomeAssistantAddonReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hav1alpha1.HomeAssistantAddon{}).
		Owns(&appsv1.Deployment{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&networkingv1.Ingress{}).
		Watches(
			&hav1alpha1.HomeAssistant{},
			handler.EnqueueRequestsFromMapFunc(r.findAddonsForHomeAssistant),
		).
		Named("homeassistantaddon").
		Complete(r)
}

// findAddonsForHomeAssistant returns reconcile requests for all HomeAssistantAddon
// resources that reference the given HomeAssistant.
func (r *HomeAssistantAddonReconciler) findAddonsForHomeAssistant(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	ha := obj.(*hav1alpha1.HomeAssistant)

	addonList := &hav1alpha1.HomeAssistantAddonList{}
	if err := r.List(ctx, addonList, client.InNamespace(ha.Namespace)); err != nil {
		return []reconcile.Request{}
	}

	var requests []reconcile.Request
	for _, addon := range addonList.Items {
		if addon.Spec.HomeAssistantRef.Name == ha.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      addon.Name,
					Namespace: addon.Namespace,
				},
			})
		}
	}
	return requests
}
