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
	"k8s.io/client-go/tools/events"
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
	scriptsYamlKey         = "scripts.yaml"
	generatedScriptsSuffix = "-scripts"
	scriptFinalizerName    = "ha.homeassistant.io/script-finalizer"

	// Condition reasons for HomeAssistantScript
	reasonScriptGenerated = "ScriptGenerated"
	reasonInvalidScript   = "InvalidScript"
)

// HomeAssistantScriptReconciler reconciles a HomeAssistantScript object
type HomeAssistantScriptReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantscripts,verbs=get;list;watch
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantscripts,verbs=create;update;patch;delete
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantscripts/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantscripts/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistants,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HomeAssistantScriptReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the HomeAssistantScript instance
	script := &hav1alpha1.HomeAssistantScript{}
	if err := r.Get(ctx, req.NamespacedName, script); err != nil {
		if errors.IsNotFound(err) {
			log.Info("HomeAssistantScript resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get HomeAssistantScript")
		return ctrl.Result{}, err
	}

	// Handle finalizer for proper cleanup
	if !script.DeletionTimestamp.IsZero() {
		// Resource is being deleted
		if controllerutil.ContainsFinalizer(script, scriptFinalizerName) {
			log.Info("Handling deletion - regenerating ConfigMap without this script")

			// Regenerate the aggregated ConfigMap without this script
			if err := r.reconcileScriptsConfigMap(ctx, script); err != nil {
				log.Error(err, "Failed to update ConfigMap during deletion")
				return ctrl.Result{}, err
			}

			// Remove finalizer to allow deletion
			controllerutil.RemoveFinalizer(script, scriptFinalizerName)
			if err := r.Update(ctx, script); err != nil {
				log.Error(err, "Failed to remove finalizer")
				return ctrl.Result{}, err
			}
			log.Info("Finalizer removed, script deleted")
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(script, scriptFinalizerName) {
		log.Info("Adding finalizer to script")
		controllerutil.AddFinalizer(script, scriptFinalizerName)
		if err := r.Update(ctx, script); err != nil {
			log.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		// Continue with reconciliation after adding finalizer
		log.V(1).Info("Finalizer added, continuing reconciliation")
	}

	// Validate HomeAssistant reference exists
	haRef := types.NamespacedName{
		Name:      script.Spec.HomeAssistantRef.Name,
		Namespace: script.Namespace,
	}
	ha, err := r.validateHomeAssistantRef(ctx, haRef, script)
	if err != nil {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Calculate hash of the script
	scriptHash, err := r.calculateScriptHash(script)
	if err != nil {
		log.Error(err, "Failed to calculate script hash")
		return ctrl.Result{}, err
	}

	// Reconcile the aggregated scripts ConfigMap
	if err := r.reconcileScriptsConfigMap(ctx, script); err != nil {
		log.Error(err, "Failed to reconcile scripts ConfigMap")
		meta.SetStatusCondition(&script.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             "ReconciliationFailed",
			Message:            fmt.Sprintf("Failed to create/update scripts ConfigMap: %v", err),
			ObservedGeneration: script.Generation,
		})
		if statusErr := r.Status().Update(ctx, script); statusErr != nil {
			log.Error(statusErr, "Failed to update status")
		}
		return ctrl.Result{}, err
	}

	// Perform hot-reload if enabled and hash changed
	if script.Status.ScriptHash != scriptHash {
		if err := r.performScriptReload(ctx, script, ha, scriptHash); err != nil {
			if statusErr := r.Status().Update(ctx, script); statusErr != nil {
				log.Error(statusErr, "Failed to update status")
			}
			return ctrl.Result{}, err
		}
	}

	// Update status (only reached when reload succeeded or hash was unchanged)
	script.Status.ScriptHash = scriptHash
	script.Status.ObservedGeneration = script.Generation
	meta.SetStatusCondition(&script.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             reasonScriptGenerated,
		Message:            "Script successfully generated and loaded",
		ObservedGeneration: script.Generation,
	})

	if err := r.Status().Update(ctx, script); err != nil {
		log.Error(err, "Failed to update HomeAssistantScript status")
		return ctrl.Result{}, err
	}

	log.Info("Successfully reconciled HomeAssistantScript")
	return ctrl.Result{}, nil
}

// reconcileScriptsConfigMap creates or updates the aggregated
// scripts.yaml ConfigMap. This ConfigMap contains ALL scripts
// for a given HomeAssistant instance.
func (r *HomeAssistantScriptReconciler) reconcileScriptsConfigMap(
	ctx context.Context,
	script *hav1alpha1.HomeAssistantScript,
) error {
	log := logf.FromContext(ctx)

	configMapName := script.Spec.HomeAssistantRef.Name + generatedScriptsSuffix

	// Fetch all HomeAssistantScript resources for this HomeAssistant instance
	scriptList := &hav1alpha1.HomeAssistantScriptList{}
	if err := r.List(ctx, scriptList, client.InNamespace(script.Namespace)); err != nil {
		return fmt.Errorf("failed to list HomeAssistantScript resources: %w", err)
	}

	// Filter scripts for this HomeAssistant instance and convert to map
	// Home Assistant expects scripts as a map: { script_id: { ... } }
	scriptsMap := make(map[string]interface{})
	for _, sc := range scriptList.Items {
		if sc.Spec.HomeAssistantRef.Name != script.Spec.HomeAssistantRef.Name {
			continue
		}
		// Skip scripts being deleted
		if !sc.DeletionTimestamp.IsZero() {
			log.Info("Skipping script being deleted", "name", sc.Name)
			continue
		}

		// Convert script to YAML-compatible map
		scriptID := sc.Spec.ID
		if scriptID == "" {
			scriptID = sc.Name
		}

		scriptYaml, err := r.scriptToYaml(&sc)
		if err != nil {
			log.Error(err, "Failed to convert script to YAML", "name", sc.Name)
			r.Recorder.Eventf(&sc, nil, corev1.EventTypeWarning,
				"YamlConversionFailed", "YamlConversionFailed",
				"Failed to convert script to YAML: %v", err)
			continue
		}
		scriptsMap[scriptID] = scriptYaml
	}

	// Generate scripts.yaml content
	yamlData, err := yaml.Marshal(scriptsMap)
	if err != nil {
		return fmt.Errorf("failed to marshal scripts to YAML: %w", err)
	}

	// Create or update ConfigMap
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: script.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "homeassistant",
				"app.kubernetes.io/instance":   script.Spec.HomeAssistantRef.Name,
				"app.kubernetes.io/managed-by": "homeassistant-operator",
				"app.kubernetes.io/component":  "scripts",
			},
		},
		Data: map[string]string{
			scriptsYamlKey: string(yamlData),
		},
	}

	// Get HomeAssistant to set owner reference
	ha := &hav1alpha1.HomeAssistant{}
	haRef := types.NamespacedName{Name: script.Spec.HomeAssistantRef.Name, Namespace: script.Namespace}
	if err := r.Get(ctx, haRef, ha); err != nil {
		// If HA doesn't exist, we can't create/update ConfigMap with proper owner reference
		// This is expected during cleanup/deletion scenarios
		if errors.IsNotFound(err) {
			log.V(1).Info("HomeAssistant not found, skipping ConfigMap reconciliation", "ha", haRef.Name)
			return nil
		}
		return fmt.Errorf("failed to get HomeAssistant: %w", err)
	}

	// Set owner reference to HomeAssistant (not to individual script)
	if err := controllerutil.SetControllerReference(ha, configMap, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference: %w", err)
	}

	// Check if ConfigMap exists
	existingConfigMap := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: script.Namespace}, existingConfigMap)
	if err != nil && errors.IsNotFound(err) {
		// Create new ConfigMap
		log.Info("Creating scripts ConfigMap", "name", configMapName)
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
		log.Info("Updating scripts ConfigMap", "name", configMapName)
		if err := r.Update(ctx, existingConfigMap); err != nil {
			return fmt.Errorf("failed to update ConfigMap: %w", err)
		}
	} else {
		log.V(1).Info("ConfigMap unchanged, skipping update", "name", configMapName)
	}

	return nil
}

// scriptToYaml converts HomeAssistantScript CR to YAML-compatible map
func (r *HomeAssistantScriptReconciler) scriptToYaml(
	script *hav1alpha1.HomeAssistantScript,
) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	// Alias (required)
	result["alias"] = script.Spec.Alias

	// Description (optional)
	if script.Spec.Description != "" {
		result["description"] = script.Spec.Description
	}

	// Icon (optional)
	if script.Spec.Icon != "" {
		result["icon"] = script.Spec.Icon
	}

	// Mode (optional, defaults handled by kubebuilder)
	if script.Spec.Mode != "" {
		result["mode"] = string(script.Spec.Mode)
	}

	// Max and MaxExceeded (only relevant for queued/parallel modes)
	if script.Spec.Mode == hav1alpha1.ScriptModeQueued || script.Spec.Mode == hav1alpha1.ScriptModeParallel {
		if script.Spec.Max != nil {
			result["max"] = *script.Spec.Max
		}
		if script.Spec.MaxExceeded != "" {
			result["max_exceeded"] = script.Spec.MaxExceeded
		}
	}

	// Fields (optional) - input parameters
	if len(script.Spec.Fields) > 0 {
		fields := make(map[string]interface{})
		for fieldName, fieldSpec := range script.Spec.Fields {
			var fieldData map[string]interface{}
			if err := json.Unmarshal(fieldSpec.Raw, &fieldData); err != nil {
				return nil, fmt.Errorf("failed to unmarshal field %s: %w", fieldName, err)
			}
			fields[fieldName] = fieldData
		}
		result["fields"] = fields
	}

	// Sequence (required) - list of actions
	var sequence []interface{}
	for i, action := range script.Spec.Sequence {
		var actionData map[string]interface{}
		if err := json.Unmarshal(action.Raw, &actionData); err != nil {
			return nil, fmt.Errorf("failed to unmarshal action %d: %w", i, err)
		}
		sequence = append(sequence, actionData)
	}
	result["sequence"] = sequence

	return result, nil
}

// performScriptReload triggers hot-reload of scripts via Home Assistant REST API
// Uses smart component detection and retry mechanism for reliability
//
//nolint:dupl // dupl: Similar to performAutomationReload/performSceneReload by design
func (r *HomeAssistantScriptReconciler) performScriptReload(
	ctx context.Context,
	script *hav1alpha1.HomeAssistantScript,
	ha *hav1alpha1.HomeAssistant,
	newHash string,
) error {
	log := logf.FromContext(ctx)

	// Check if autoReload is enabled
	autoReload := true
	if script.Spec.AutoReload != nil {
		autoReload = *script.Spec.AutoReload
	}

	if !autoReload {
		log.Info("AutoReload disabled, skipping reload", "name", script.Name)
		script.Status.LastReloadMethod = reloadMethodNone
		script.Status.LastError = ""
		return nil
	}

	// Get API token for hot-reload
	token, tokenErr := getApiToken(ctx, r.Client, ha)
	if tokenErr != nil {
		log.Info("API token not available, skipping hot-reload")
		script.Status.LastError = errMsgTokenNotAvailable

		meta.SetStatusCondition(&script.Status.Conditions, metav1.Condition{
			Type:               "ReloadReady",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: script.Generation,
			Reason:             reasonTokenNotAvailable,
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
		ComponentName: "script",
	}

	result := PerformReloadWithRetry(
		ctx,
		haClient,
		token,
		reloadConfig,
		haClient.ReloadScripts,
	)

	// Update status based on result
	if result.Success {
		// SUCCESS
		now := metav1.Now()
		script.Status.LastReloadTime = &now
		script.Status.LastReloadMethod = result.Method
		script.Status.LastError = ""

		meta.SetStatusCondition(&script.Status.Conditions, metav1.Condition{
			Type:               "ReloadReady",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: script.Generation,
			Reason:             "ReloadSuccessful",
			Message: fmt.Sprintf("Script hot-reloaded successfully after %d attempts (%.1fs)",
				result.Attempts, result.Duration.Seconds()),
		})

		r.Recorder.Eventf(script, nil, corev1.EventTypeNormal, "ReloadSuccessful", "ReloadSuccessful",
			"Script hot-reloaded successfully after %d attempts", result.Attempts)

		log.Info("Script hot-reload completed successfully",
			"name", script.Name,
			"hash", newHash,
			"attempts", result.Attempts,
			"duration", result.Duration)

		return nil
	}

	// FAILED or SKIPPED
	script.Status.LastError = result.Error.Error()

	if !result.ComponentLoaded {
		// Component not loaded - will retry on next reconcile
		meta.SetStatusCondition(&script.Status.Conditions, metav1.Condition{
			Type:               "ReloadReady",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: script.Generation,
			Reason:             "ComponentNotLoaded",
			Message:            "Script integration not loaded in Home Assistant yet (will retry automatically)",
		})

		// Requeue to retry when component loads
		log.Info("Script component not loaded yet, will retry",
			"name", script.Name,
			"reloadID", result.ReloadID)
		return result.Error
	}

	// Component loaded but reload failed after all retries
	script.Status.LastReloadMethod = result.Method
	meta.SetStatusCondition(&script.Status.Conditions, metav1.Condition{
		Type:               "ReloadReady",
		Status:             metav1.ConditionFalse,
		ObservedGeneration: script.Generation,
		Reason:             "ReloadFailed",
		Message: fmt.Sprintf("Hot-reload failed after %d attempts: %s",
			result.Attempts, truncateString(result.Error.Error(), 200)),
	})

	r.Recorder.Eventf(script, nil, corev1.EventTypeWarning, "ReloadFailed", "ReloadFailed",
		"Hot-reload failed after %d attempts: %s",
		result.Attempts, truncateString(result.Error.Error(), 100))

	// Don't fail reconciliation - script is in ConfigMap, will load on next HA restart
	log.Info("Hot-reload failed, script will load on next HA restart",
		"name", script.Name,
		"attempts", result.Attempts,
		"error", result.Error)

	return nil
}

// calculateScriptHash computes SHA256 hash of the script spec.
// Includes the effective script ID (spec.id or CR name) so that ID-only
// changes also update the hash and trigger a reload.
func (r *HomeAssistantScriptReconciler) calculateScriptHash(
	script *hav1alpha1.HomeAssistantScript,
) (string, error) {
	// Effective ID matches the key used in the scripts ConfigMap
	scriptID := script.Spec.ID
	if scriptID == "" {
		scriptID = script.Name
	}

	// Convert spec to YAML for consistent hashing
	yamlData, err := r.scriptToYaml(script)
	if err != nil {
		return "", err
	}

	yamlBytes, err := yaml.Marshal(yamlData)
	if err != nil {
		return "", err
	}

	// Prefix with script ID so ID-only changes are captured
	data := append([]byte(scriptID+"\n"), yamlBytes...)
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *HomeAssistantScriptReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hav1alpha1.HomeAssistantScript{}).
		Watches(
			&hav1alpha1.HomeAssistant{},
			handler.EnqueueRequestsFromMapFunc(r.findScriptsForHomeAssistant),
		).
		Named("homeassistantscript").
		Complete(r)
}

// findScriptsForHomeAssistant returns reconcile requests for all HomeAssistantScript
// resources that reference the given HomeAssistant
func (r *HomeAssistantScriptReconciler) findScriptsForHomeAssistant(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	ha := obj.(*hav1alpha1.HomeAssistant)

	scriptList := &hav1alpha1.HomeAssistantScriptList{}
	if err := r.List(ctx, scriptList, client.InNamespace(ha.Namespace)); err != nil {
		return []reconcile.Request{}
	}

	var requests []reconcile.Request
	for _, script := range scriptList.Items {
		if script.Spec.HomeAssistantRef.Name == ha.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      script.Name,
					Namespace: script.Namespace,
				},
			})
		}
	}

	return requests
}

// validateHomeAssistantRef validates that referenced HomeAssistant exists
// and sets appropriate status condition if not found
func (r *HomeAssistantScriptReconciler) validateHomeAssistantRef(
	ctx context.Context,
	haRef types.NamespacedName,
	script *hav1alpha1.HomeAssistantScript,
) (*hav1alpha1.HomeAssistant, error) {
	log := logf.FromContext(ctx)

	ha, err := getHomeAssistant(ctx, r.Client, haRef)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Error(err, "Referenced HomeAssistant not found",
				"name", haRef.Name)
			meta.SetStatusCondition(&script.Status.Conditions,
				metav1.Condition{
					Type:   conditionTypeReady,
					Status: metav1.ConditionFalse,
					Reason: reasonInvalidScript,
					Message: fmt.Sprintf(
						"HomeAssistant %s not found",
						haRef.Name,
					),
					ObservedGeneration: script.Generation,
				})
			if err := r.Status().Update(ctx, script); err != nil {
				log.Error(err, "Failed to update status")
			}
			return nil, err
		}
		log.Error(err, "Failed to get HomeAssistant")
		return nil, err
	}
	return ha, nil
}
