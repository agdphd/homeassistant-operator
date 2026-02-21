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
	"k8s.io/client-go/tools/record"
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

	// Note: reloadMethodRestart, reloadMethodHotReload, reloadMethodNone,
	// defaultHomeAssistantPort, apiTokenSecretSuffix, and errMsgTokenNotAvailable are defined in constants.go
)

// HomeAssistantAutomationReconciler reconciles a HomeAssistantAutomation object
type HomeAssistantAutomationReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantautomations,verbs=get;list;watch
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantautomations,verbs=create;update;patch;delete
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantautomations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantautomations/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistants,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

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
		// Return after adding finalizer to allow Kubernetes to trigger a new reconciliation
		log.V(1).Info("Finalizer added, waiting for next reconciliation")
		return ctrl.Result{}, nil
	}

	// Validate HomeAssistant reference exists
	haRef := types.NamespacedName{
		Name:      automation.Spec.HomeAssistantRef.Name,
		Namespace: automation.Namespace,
	}
	ha, err := r.validateHomeAssistantRef(ctx, haRef, automation)
	if err != nil {
		// Requeue quickly to retry validation
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
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
		log.Info("Automation hash changed, triggering hot-reload",
			"oldHash", automation.Status.AutomationHash,
			"newHash", automationHash)
		if err := r.performAutomationReload(ctx, automation, ha); err != nil {
			if statusErr := r.Status().Update(ctx, automation); statusErr != nil {
				log.Error(statusErr, "Failed to update status")
			}
			return ctrl.Result{}, err
		}
	} else {
		log.V(1).Info("Automation hash unchanged, skipping hot-reload", "hash", automationHash)
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
		// Note: Hot-reload is handled in Reconcile() after hash comparison
		// This avoids duplicate reload calls when multiple automations change
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

	// Triggers (Home Assistant expects singular "trigger" key)
	triggers := make([]interface{}, 0, len(automation.Spec.Triggers))
	for _, trigger := range automation.Spec.Triggers {
		var triggerMap interface{}
		if err := json.Unmarshal(trigger.Raw, &triggerMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal trigger: %w", err)
		}
		triggers = append(triggers, triggerMap)
	}
	result["trigger"] = triggers

	// Conditions (optional, Home Assistant expects singular "condition" key)
	if len(automation.Spec.Conditions) > 0 {
		conditions := make([]interface{}, 0, len(automation.Spec.Conditions))
		for _, condition := range automation.Spec.Conditions {
			var conditionMap interface{}
			if err := json.Unmarshal(condition.Raw, &conditionMap); err != nil {
				return nil, fmt.Errorf("failed to unmarshal condition: %w", err)
			}
			conditions = append(conditions, conditionMap)
		}
		result["condition"] = conditions
	}

	// Actions (Home Assistant expects singular "action" key)
	actions := make([]interface{}, 0, len(automation.Spec.Actions))
	for _, action := range automation.Spec.Actions {
		var actionMap interface{}
		if err := json.Unmarshal(action.Raw, &actionMap); err != nil {
			return nil, fmt.Errorf("failed to unmarshal action: %w", err)
		}
		actions = append(actions, actionMap)
	}
	result["action"] = actions

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

// performAutomationReload triggers hot-reload of automations via Home Assistant REST API
// Uses smart component detection and retry mechanism for reliability
func (r *HomeAssistantAutomationReconciler) performAutomationReload(
	ctx context.Context,
	automation *hav1alpha1.HomeAssistantAutomation,
	ha *hav1alpha1.HomeAssistant,
) error {
	log := logf.FromContext(ctx)

	// Check if autoReload is disabled
	if automation.Spec.AutoReload != nil && !*automation.Spec.AutoReload {
		log.Info("AutoReload disabled, skipping reload", "name", automation.Name)
		automation.Status.LastReloadMethod = reloadMethodNone
		automation.Status.LastError = ""
		return nil
	}

	// Get API token for hot-reload
	token, tokenErr := getApiToken(ctx, r.Client, ha)
	if tokenErr != nil {
		log.Info("API token not available, skipping hot-reload")
		automation.Status.LastError = errMsgTokenNotAvailable

		meta.SetStatusCondition(&automation.Status.Conditions, metav1.Condition{
			Type:               "ReloadReady",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: automation.Generation,
			Reason:             "TokenNotAvailable",
			Message:            "API token not available - bootstrap may not be configured",
		})

		return nil
	}

	// Build Home Assistant URL and client
	haURL := buildHomeAssistantURL(ha)
	haClient := haclient.NewClient(haURL)

	// Perform reload with retry and smart detection
	reloadConfig := ReloadConfig{
		MaxRetries:    3,
		RetryDelay:    5 * time.Second,
		ComponentName: "automation",
	}

	result := PerformReloadWithRetry(
		ctx,
		haClient,
		token,
		reloadConfig,
		haClient.ReloadAutomations,
	)

	// Update status based on result
	if result.Success {
		// SUCCESS
		now := metav1.Now()
		automation.Status.LastReloadTime = &now
		automation.Status.LastReloadMethod = result.Method
		automation.Status.LastError = ""

		meta.SetStatusCondition(&automation.Status.Conditions, metav1.Condition{
			Type:               "ReloadReady",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: automation.Generation,
			Reason:             "ReloadSuccessful",
			Message: fmt.Sprintf("Automation hot-reloaded successfully after %d attempts (%.1fs)",
				result.Attempts, result.Duration.Seconds()),
		})

		r.Recorder.Event(automation, corev1.EventTypeNormal, "ReloadSuccessful",
			fmt.Sprintf("Automation hot-reloaded successfully after %d attempts", result.Attempts))

		log.Info("Automation hot-reload completed successfully",
			"name", automation.Name,
			"attempts", result.Attempts,
			"duration", result.Duration)

		return nil
	}

	// FAILED or SKIPPED
	automation.Status.LastError = result.Error.Error()

	if !result.ComponentLoaded {
		// Component not loaded - will retry on next reconcile
		meta.SetStatusCondition(&automation.Status.Conditions, metav1.Condition{
			Type:               "ReloadReady",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: automation.Generation,
			Reason:             "ComponentNotLoaded",
			Message:            "Automation integration not loaded in Home Assistant yet (will retry automatically)",
		})

		// Requeue to retry when component loads
		log.Info("Automation component not loaded yet, will retry",
			"name", automation.Name,
			"reloadID", result.ReloadID)
		return result.Error
	}

	// Component loaded but reload failed after all retries
	meta.SetStatusCondition(&automation.Status.Conditions, metav1.Condition{
		Type:               "ReloadReady",
		Status:             metav1.ConditionFalse,
		ObservedGeneration: automation.Generation,
		Reason:             "ReloadFailed",
		Message: fmt.Sprintf("Hot-reload failed after %d attempts: %s",
			result.Attempts, truncateString(result.Error.Error(), 200)),
	})

	r.Recorder.Event(automation, corev1.EventTypeWarning, "ReloadFailed",
		fmt.Sprintf("Hot-reload failed after %d attempts: %s",
			result.Attempts, truncateString(result.Error.Error(), 100)))

	// Don't fail reconciliation - automation is in ConfigMap, will load on next HA restart
	log.Info("Hot-reload failed, automation will load on next HA restart",
		"name", automation.Name,
		"attempts", result.Attempts,
		"error", result.Error)

	return nil
}
