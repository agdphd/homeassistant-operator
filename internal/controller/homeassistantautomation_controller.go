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
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hav1alpha1 "github.com/przemekhys/homeassistant-operator/api/v1alpha1"
	"github.com/przemekhys/homeassistant-operator/internal/haclient"
)

const (
	automationsYamlKey          = "automations.yaml"
	generatedAutomationsSuffix  = "-automations"
	automationHashAnnotationKey = "ha.homeassistant.io/automation-hash"
	automationFinalizerName     = "ha.homeassistant.io/automation-finalizer"

	// Condition reasons for HomeAssistantAutomation
	reasonAutomationGenerated = "AutomationGenerated"
	reasonAutomationNotFound  = "AutomationNotFound"
	reasonInvalidAutomation   = "InvalidAutomation"
	reasonReloadSucceeded     = "ReloadSucceeded"
	reasonReloadFailed        = "ReloadFailed"
)

// HomeAssistantAutomationReconciler reconciles a HomeAssistantAutomation object
type HomeAssistantAutomationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantautomations,verbs=get;list;watch
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantautomations,verbs=create;update;patch;delete
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantautomations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantautomations/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistants,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HomeAssistantAutomationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the HomeAssistantAutomation instance
	automation := &hav1alpha1.HomeAssistantAutomation{}
	if err := r.Get(ctx, req.NamespacedName, automation); err != nil {
		if errors.IsNotFound(err) {
			log.Info("HomeAssistantAutomation resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get HomeAssistantAutomation")
		return ctrl.Result{}, err
	}

	// Handle finalizer for proper cleanup
	if !automation.DeletionTimestamp.IsZero() {
		// Resource is being deleted
		if controllerutil.ContainsFinalizer(automation, automationFinalizerName) {
			log.Info("Handling deletion - regenerating ConfigMap without this automation")

			// Regenerate the aggregated ConfigMap without this automation
			if err := r.reconcileAutomationsConfigMap(ctx, automation); err != nil {
				log.Error(err, "Failed to update ConfigMap during deletion")
				return ctrl.Result{}, err
			}

			// Remove finalizer to allow deletion
			controllerutil.RemoveFinalizer(automation, automationFinalizerName)
			if err := r.Update(ctx, automation); err != nil {
				log.Error(err, "Failed to remove finalizer")
				return ctrl.Result{}, err
			}
			log.Info("Finalizer removed, automation deleted")
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(automation, automationFinalizerName) {
		log.Info("Adding finalizer to automation")
		controllerutil.AddFinalizer(automation, automationFinalizerName)
		if err := r.Update(ctx, automation); err != nil {
			log.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		// Continue with reconciliation after adding finalizer
		log.V(1).Info("Finalizer added, continuing reconciliation")
	}

	// Validate HomeAssistant reference exists
	haRef := types.NamespacedName{
		Name:      automation.Spec.HomeAssistantRef.Name,
		Namespace: automation.Namespace,
	}
	ha, err := r.validateHomeAssistantRef(ctx, haRef, automation)
	if err != nil {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Calculate hash of the automation
	automationHash, err := r.calculateAutomationHash(automation)
	if err != nil {
		log.Error(err, "Failed to calculate automation hash")
		return ctrl.Result{}, err
	}

	// Reconcile the aggregated automations ConfigMap
	if err := r.reconcileAutomationsConfigMap(ctx, automation); err != nil {
		log.Error(err, "Failed to reconcile automations ConfigMap")
		meta.SetStatusCondition(&automation.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             "ReconciliationFailed",
			Message:            fmt.Sprintf("Failed to create/update automations ConfigMap: %v", err),
			ObservedGeneration: automation.Generation,
		})
		if statusErr := r.Status().Update(ctx, automation); statusErr != nil {
			log.Error(statusErr, "Failed to update status")
		}
		return ctrl.Result{}, err
	}

	// Perform hot-reload if enabled and hash changed
	if automation.Status.AutomationHash != automationHash {
		if err := r.performAutomationReload(ctx, automation, ha, automationHash); err != nil {
			log.Error(err, "Failed to reload automation")
			meta.SetStatusCondition(&automation.Status.Conditions, metav1.Condition{
				Type:               conditionTypeReady,
				Status:             metav1.ConditionFalse,
				Reason:             reasonReloadFailed,
				Message:            err.Error(),
				ObservedGeneration: automation.Generation,
			})
			_ = r.Status().Update(ctx, automation)
			return ctrl.Result{}, err
		}
	}

	// Update status
	automation.Status.AutomationHash = automationHash
	automation.Status.ObservedGeneration = automation.Generation
	meta.SetStatusCondition(&automation.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             reasonAutomationGenerated,
		Message:            "Automation successfully generated and loaded",
		ObservedGeneration: automation.Generation,
	})

	if err := r.Status().Update(ctx, automation); err != nil {
		log.Error(err, "Failed to update HomeAssistantAutomation status")
		return ctrl.Result{}, err
	}

	log.Info("Successfully reconciled HomeAssistantAutomation")
	return ctrl.Result{}, nil
}

// reconcileAutomationsConfigMap creates or updates the aggregated
// automations.yaml ConfigMap. This ConfigMap contains ALL automations
// for a given HomeAssistant instance.
func (r *HomeAssistantAutomationReconciler) reconcileAutomationsConfigMap(
	ctx context.Context,
	automation *hav1alpha1.HomeAssistantAutomation,
) error {
	log := logf.FromContext(ctx)

	configMapName := automation.Spec.HomeAssistantRef.Name + generatedAutomationsSuffix

	// Fetch all HomeAssistantAutomation resources for this HomeAssistant instance
	automationList := &hav1alpha1.HomeAssistantAutomationList{}
	if err := r.List(ctx, automationList, client.InNamespace(automation.Namespace)); err != nil {
		return fmt.Errorf("failed to list HomeAssistantAutomation resources: %w", err)
	}

	// Filter automations for this HomeAssistant instance and that are enabled
	var automations []map[string]interface{}
	for _, auto := range automationList.Items {
		if auto.Spec.HomeAssistantRef.Name != automation.Spec.HomeAssistantRef.Name {
			continue
		}
		// Skip automations being deleted
		if !auto.DeletionTimestamp.IsZero() {
			log.Info("Skipping automation being deleted", "name", auto.Name)
			continue
		}
		// Skip disabled automations
		if auto.Spec.Enabled != nil && !*auto.Spec.Enabled {
			log.Info("Skipping disabled automation", "name", auto.Name)
			continue
		}

		// Convert automation to YAML-compatible map
		autoYaml, err := r.automationToYaml(&auto)
		if err != nil {
			log.Error(err, "Failed to convert automation to YAML", "name", auto.Name)
			continue
		}
		automations = append(automations, autoYaml)
	}

	// Generate automations.yaml content
	yamlData, err := yaml.Marshal(automations)
	if err != nil {
		return fmt.Errorf("failed to marshal automations to YAML: %w", err)
	}

	// Create or update ConfigMap
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: automation.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "homeassistant",
				"app.kubernetes.io/instance":   automation.Spec.HomeAssistantRef.Name,
				"app.kubernetes.io/managed-by": "homeassistant-operator",
				"app.kubernetes.io/component":  "automations",
			},
		},
		Data: map[string]string{
			automationsYamlKey: string(yamlData),
		},
	}

	// Get HomeAssistant to set owner reference
	ha := &hav1alpha1.HomeAssistant{}
	haRef := types.NamespacedName{Name: automation.Spec.HomeAssistantRef.Name, Namespace: automation.Namespace}
	if err := r.Get(ctx, haRef, ha); err != nil {
		// If HA doesn't exist, we can't create/update ConfigMap with proper owner reference
		// This is expected during cleanup/deletion scenarios
		if errors.IsNotFound(err) {
			log.V(1).Info("HomeAssistant not found, skipping ConfigMap reconciliation", "ha", haRef.Name)
			return nil
		}
		return fmt.Errorf("failed to get HomeAssistant: %w", err)
	}

	// Set owner reference to HomeAssistant (not to individual automation)
	if err := controllerutil.SetControllerReference(ha, configMap, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference: %w", err)
	}

	// Check if ConfigMap exists
	existingConfigMap := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: automation.Namespace}, existingConfigMap)
	if err != nil && errors.IsNotFound(err) {
		// Create new ConfigMap
		log.Info("Creating automations ConfigMap", "name", configMapName)
		if err := r.Create(ctx, configMap); err != nil {
			return fmt.Errorf("failed to create ConfigMap: %w", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to get existing ConfigMap: %w", err)
	}

	// Compare Data, Labels and OwnerReferences to determine if update is needed
	needsUpdate := false
	if !reflect.DeepEqual(existingConfigMap.Data, configMap.Data) {
		needsUpdate = true
		log.V(1).Info("ConfigMap Data changed")
	}
	if !reflect.DeepEqual(existingConfigMap.Labels, configMap.Labels) {
		needsUpdate = true
		log.V(1).Info("ConfigMap Labels changed")
	}
	if !reflect.DeepEqual(existingConfigMap.OwnerReferences, configMap.OwnerReferences) {
		needsUpdate = true
		log.V(1).Info("ConfigMap OwnerReferences changed")
	}

	// Only update if something actually changed
	if needsUpdate {
		existingConfigMap.Data = configMap.Data
		existingConfigMap.Labels = configMap.Labels
		existingConfigMap.OwnerReferences = configMap.OwnerReferences
		log.Info("Updating automations ConfigMap", "name", configMapName)
		if err := r.Update(ctx, existingConfigMap); err != nil {
			return fmt.Errorf("failed to update ConfigMap: %w", err)
		}
	} else {
		log.V(1).Info("ConfigMap unchanged, skipping update", "name", configMapName)
	}

	return nil
}

// automationToYaml converts HomeAssistantAutomation CR to YAML-compatible map
func (r *HomeAssistantAutomationReconciler) automationToYaml(
	automation *hav1alpha1.HomeAssistantAutomation,
) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	// ID
	id := automation.Spec.ID
	if id == "" {
		// Generate ID from CR name if not specified
		id = automation.Name
	}
	result["id"] = id

	// Alias
	result["alias"] = automation.Spec.Alias

	// Description
	if automation.Spec.Description != "" {
		result["description"] = automation.Spec.Description
	}

	// Triggers
	triggers := make([]interface{}, 0, len(automation.Spec.Triggers))
	for _, trigger := range automation.Spec.Triggers {
		var triggerMap interface{}
		if err := json.Unmarshal(trigger.Raw, &triggerMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal trigger: %w", err)
		}
		triggers = append(triggers, triggerMap)
	}
	result["triggers"] = triggers

	// Conditions (optional)
	if len(automation.Spec.Conditions) > 0 {
		conditions := make([]interface{}, 0, len(automation.Spec.Conditions))
		for _, condition := range automation.Spec.Conditions {
			var conditionMap interface{}
			if err := json.Unmarshal(condition.Raw, &conditionMap); err != nil {
				return nil, fmt.Errorf("failed to unmarshal condition: %w", err)
			}
			conditions = append(conditions, conditionMap)
		}
		result["conditions"] = conditions
	}

	// Actions
	actions := make([]interface{}, 0, len(automation.Spec.Actions))
	for _, action := range automation.Spec.Actions {
		var actionMap interface{}
		if err := json.Unmarshal(action.Raw, &actionMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal action: %w", err)
		}
		actions = append(actions, actionMap)
	}
	result["actions"] = actions

	// Mode
	if automation.Spec.Mode != "" {
		result["mode"] = string(automation.Spec.Mode)
	}

	// Max
	if automation.Spec.Max != nil {
		result["max"] = *automation.Spec.Max
	}

	// MaxExceeded
	if automation.Spec.MaxExceeded != "" {
		result["max_exceeded"] = automation.Spec.MaxExceeded
	}

	// InitialState
	if automation.Spec.InitialState != nil {
		result["initial_state"] = *automation.Spec.InitialState
	}

	// Enabled - always include in hash so toggling triggers reload (default: true)
	enabled := true
	if automation.Spec.Enabled != nil {
		enabled = *automation.Spec.Enabled
	}
	result["enabled"] = enabled

	return result, nil
}

// performAutomationReload triggers hot-reload of automations via Home Assistant REST API
func (r *HomeAssistantAutomationReconciler) performAutomationReload(
	ctx context.Context,
	automation *hav1alpha1.HomeAssistantAutomation,
	ha *hav1alpha1.HomeAssistant,
	newHash string,
) error {
	log := logf.FromContext(ctx)

	// Check if autoReload is enabled
	autoReload := true
	if automation.Spec.AutoReload != nil {
		autoReload = *automation.Spec.AutoReload
	}

	if !autoReload {
		log.Info("AutoReload disabled, skipping reload", "name", automation.Name)
		automation.Status.LastError = ""
		return nil
	}

	// Get API token from bootstrap secret
	// Secret name matches bootstrap controller's naming: ha.Name + "-homeassistant-api-token"
	tokenSecretName := ha.Name + "-homeassistant-api-token"
	tokenSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: tokenSecretName, Namespace: ha.Namespace}, tokenSecret); err != nil {
		if errors.IsNotFound(err) {
			log.Info("API token not available, skipping hot-reload")
			automation.Status.LastError = "API token not found - bootstrap may not be configured"
			return nil
		}
		return fmt.Errorf("failed to get API token secret: %w", err)
	}

	token := string(tokenSecret.Data["token"])
	if token == "" {
		log.Info("API token empty, skipping hot-reload")
		automation.Status.LastError = "API token is empty"
		return nil
	}

	// Check if Home Assistant Service is ready before attempting hot-reload
	if !r.isHomeAssistantServiceReady(ctx, ha) {
		log.Info("Home Assistant Service not ready yet, skipping hot-reload (will retry on next reconcile)")
		automation.Status.LastError = "Service not ready - waiting for pod readiness"
		// Return nil to avoid error and requeue - controller will retry automatically
		return nil
	}

	// Construct Home Assistant URL
	// Service name matches the HomeAssistant CR name (see homeassistant_controller.go buildService)
	haURL := fmt.Sprintf(
		"http://%s.%s.svc.cluster.local:%d",
		ha.Name, ha.Namespace, defaultHomeAssistantPort,
	)
	haClient := haclient.NewClient(haURL)

	// Call automation reload endpoint
	log.Info("Triggering automation hot-reload", "url", haURL)
	if err := haClient.ReloadAutomations(ctx, token); err != nil {
		automation.Status.LastError = fmt.Sprintf("Hot-reload failed: %v", err)
		return fmt.Errorf("failed to reload automations via API: %w", err)
	}

	// Update status on success
	now := metav1.Now()
	automation.Status.LastReloadTime = &now
	automation.Status.LastError = ""

	log.Info("Automation hot-reload successful", "name", automation.Name, "hash", newHash)
	return nil
}

// calculateAutomationHash computes SHA256 hash of the automation spec
func (r *HomeAssistantAutomationReconciler) calculateAutomationHash(
	automation *hav1alpha1.HomeAssistantAutomation,
) (string, error) {
	// Convert spec to YAML for consistent hashing
	yamlData, err := r.automationToYaml(automation)
	if err != nil {
		return "", err
	}

	yamlBytes, err := yaml.Marshal(yamlData)
	if err != nil {
		return "", err
	}

	hash := sha256.Sum256(yamlBytes)
	return fmt.Sprintf("%x", hash), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HomeAssistantAutomationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hav1alpha1.HomeAssistantAutomation{}).
		Owns(&corev1.ConfigMap{}).
		Watches(
			&hav1alpha1.HomeAssistant{},
			handler.EnqueueRequestsFromMapFunc(r.findAutomationsForHomeAssistant),
		).
		Named("homeassistantautomation").
		Complete(r)
}

// findAutomationsForHomeAssistant returns reconcile requests for all HomeAssistantAutomation
// resources that reference the given HomeAssistant
func (r *HomeAssistantAutomationReconciler) findAutomationsForHomeAssistant(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	ha := obj.(*hav1alpha1.HomeAssistant)

	automationList := &hav1alpha1.HomeAssistantAutomationList{}
	if err := r.List(ctx, automationList, client.InNamespace(ha.Namespace)); err != nil {
		return []reconcile.Request{}
	}

	var requests []reconcile.Request
	for _, automation := range automationList.Items {
		if automation.Spec.HomeAssistantRef.Name == ha.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      automation.Name,
					Namespace: automation.Namespace,
				},
			})
		}
	}

	return requests
}

// validateHomeAssistantRef validates that referenced HomeAssistant exists
// and sets appropriate status condition if not found
func (r *HomeAssistantAutomationReconciler) validateHomeAssistantRef(
	ctx context.Context,
	haRef types.NamespacedName,
	automation *hav1alpha1.HomeAssistantAutomation,
) (*hav1alpha1.HomeAssistant, error) {
	log := logf.FromContext(ctx)

	ha, err := getHomeAssistant(ctx, r.Client, haRef)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Error(err, "Referenced HomeAssistant not found",
				"name", haRef.Name)
			meta.SetStatusCondition(&automation.Status.Conditions,
				metav1.Condition{
					Type:   conditionTypeReady,
					Status: metav1.ConditionFalse,
					Reason: reasonInvalidAutomation,
					Message: fmt.Sprintf(
						"HomeAssistant %s not found",
						haRef.Name,
					),
					ObservedGeneration: automation.Generation,
				})
			if err := r.Status().Update(ctx, automation); err != nil {
				log.Error(err, "Failed to update status")
			}
			return nil, err
		}
		log.Error(err, "Failed to get HomeAssistant")
		return nil, err
	}
	return ha, nil
}

// isHomeAssistantServiceReady checks if the HomeAssistant Service has ready endpoints
// Returns true if service endpoints are available, false otherwise
func (r *HomeAssistantAutomationReconciler) isHomeAssistantServiceReady(
	ctx context.Context,
	ha *hav1alpha1.HomeAssistant,
) bool {
	log := logf.FromContext(ctx)

	// Check Service Endpoints to see if pod is ready
	endpoints := &corev1.Endpoints{}
	if err := r.Get(ctx, types.NamespacedName{Name: ha.Name, Namespace: ha.Namespace}, endpoints); err != nil {
		log.V(1).Info("Failed to get Service endpoints", "error", err)
		return false
	}

	// Check if there are any ready addresses
	if len(endpoints.Subsets) == 0 {
		log.V(1).Info("Service endpoints have no subsets")
		return false
	}

	for _, subset := range endpoints.Subsets {
		if len(subset.Addresses) > 0 {
			log.V(1).Info("Service has ready endpoints", "count", len(subset.Addresses))
			return true
		}
	}

	log.V(1).Info("Service endpoints have no ready addresses")
	return false
}
