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
	scenesYamlKey          = "scenes.yaml"
	generatedScenesSuffix  = "-scenes"
	sceneHashAnnotationKey = "ha.homeassistant.io/scene-hash"
	sceneFinalizerName     = "ha.homeassistant.io/scene-finalizer"

	// Condition reasons for HomeAssistantScene
	reasonSceneGenerated = "SceneGenerated"
	reasonSceneNotFound  = "SceneNotFound"
	reasonInvalidScene   = "InvalidScene"
)

// HomeAssistantSceneReconciler reconciles a HomeAssistantScene object
type HomeAssistantSceneReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantscenes,verbs=get;list;watch
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantscenes,verbs=create;update;patch;delete
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantscenes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantscenes/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistants,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HomeAssistantSceneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the HomeAssistantScene instance
	scene := &hav1alpha1.HomeAssistantScene{}
	if err := r.Get(ctx, req.NamespacedName, scene); err != nil {
		if errors.IsNotFound(err) {
			log.Info("HomeAssistantScene resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get HomeAssistantScene")
		return ctrl.Result{}, err
	}

	// Handle finalizer for proper cleanup
	if !scene.DeletionTimestamp.IsZero() {
		// Resource is being deleted
		if controllerutil.ContainsFinalizer(scene, sceneFinalizerName) {
			log.Info("Handling deletion - regenerating ConfigMap without this scene")

			// Regenerate the aggregated ConfigMap without this scene
			if err := r.reconcileScenesConfigMap(ctx, scene); err != nil {
				log.Error(err, "Failed to update ConfigMap during deletion")
				return ctrl.Result{}, err
			}

			// Remove finalizer to allow deletion
			controllerutil.RemoveFinalizer(scene, sceneFinalizerName)
			if err := r.Update(ctx, scene); err != nil {
				log.Error(err, "Failed to remove finalizer")
				return ctrl.Result{}, err
			}
			log.Info("Finalizer removed, scene deleted")
		}
		return ctrl.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(scene, sceneFinalizerName) {
		log.Info("Adding finalizer to scene")
		controllerutil.AddFinalizer(scene, sceneFinalizerName)
		if err := r.Update(ctx, scene); err != nil {
			log.Error(err, "Failed to add finalizer")
			return ctrl.Result{}, err
		}
		// Continue with reconciliation after adding finalizer
		log.V(1).Info("Finalizer added, continuing reconciliation")
	}

	// Validate HomeAssistant reference exists
	haRef := types.NamespacedName{
		Name:      scene.Spec.HomeAssistantRef.Name,
		Namespace: scene.Namespace,
	}
	ha, err := r.validateHomeAssistantRef(ctx, haRef, scene)
	if err != nil {
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Calculate hash of the scene
	sceneHash, err := r.calculateSceneHash(scene)
	if err != nil {
		log.Error(err, "Failed to calculate scene hash")
		return ctrl.Result{}, err
	}

	// Reconcile the aggregated scenes ConfigMap
	if err := r.reconcileScenesConfigMap(ctx, scene); err != nil {
		log.Error(err, "Failed to reconcile scenes ConfigMap")
		meta.SetStatusCondition(&scene.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             "ReconciliationFailed",
			Message:            fmt.Sprintf("Failed to create/update scenes ConfigMap: %v", err),
			ObservedGeneration: scene.Generation,
		})
		if statusErr := r.Status().Update(ctx, scene); statusErr != nil {
			log.Error(statusErr, "Failed to update status")
		}
		return ctrl.Result{}, err
	}

	// Perform hot-reload if enabled and hash changed
	if scene.Status.SceneHash != sceneHash {
		if err := r.performSceneReload(ctx, scene, ha, sceneHash); err != nil {
			if statusErr := r.Status().Update(ctx, scene); statusErr != nil {
				log.Error(statusErr, "Failed to update status")
			}
			return ctrl.Result{}, err
		}
	}

	// Update status
	scene.Status.SceneHash = sceneHash
	scene.Status.ObservedGeneration = scene.Generation
	scene.Status.EntityCount = int32(len(scene.Spec.Entities))
	meta.SetStatusCondition(&scene.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             reasonSceneGenerated,
		Message:            "Scene successfully generated and loaded",
		ObservedGeneration: scene.Generation,
	})

	if err := r.Status().Update(ctx, scene); err != nil {
		log.Error(err, "Failed to update HomeAssistantScene status")
		return ctrl.Result{}, err
	}

	log.Info("Successfully reconciled HomeAssistantScene")
	return ctrl.Result{}, nil
}

// reconcileScenesConfigMap creates or updates the aggregated
// scenes.yaml ConfigMap. This ConfigMap contains ALL scenes
// for a given HomeAssistant instance.
func (r *HomeAssistantSceneReconciler) reconcileScenesConfigMap(
	ctx context.Context,
	scene *hav1alpha1.HomeAssistantScene,
) error {
	log := logf.FromContext(ctx)

	configMapName := scene.Spec.HomeAssistantRef.Name + generatedScenesSuffix

	// Fetch all HomeAssistantScene resources for this HomeAssistant instance
	sceneList := &hav1alpha1.HomeAssistantSceneList{}
	if err := r.List(ctx, sceneList, client.InNamespace(scene.Namespace)); err != nil {
		return fmt.Errorf("failed to list HomeAssistantScene resources: %w", err)
	}

	// Filter scenes for this HomeAssistant instance
	var scenes []map[string]interface{}
	for _, sc := range sceneList.Items {
		if sc.Spec.HomeAssistantRef.Name != scene.Spec.HomeAssistantRef.Name {
			continue
		}
		// Skip scenes being deleted
		if !sc.DeletionTimestamp.IsZero() {
			log.Info("Skipping scene being deleted", "name", sc.Name)
			continue
		}

		// Convert scene to YAML-compatible map
		sceneYaml, err := r.sceneToYaml(&sc)
		if err != nil {
			log.Error(err, "Failed to convert scene to YAML", "name", sc.Name)
			continue
		}
		scenes = append(scenes, sceneYaml)
	}

	// Generate scenes.yaml content
	yamlData, err := yaml.Marshal(scenes)
	if err != nil {
		return fmt.Errorf("failed to marshal scenes to YAML: %w", err)
	}

	// Create or update ConfigMap
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: scene.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "homeassistant",
				"app.kubernetes.io/instance":   scene.Spec.HomeAssistantRef.Name,
				"app.kubernetes.io/managed-by": "homeassistant-operator",
				"app.kubernetes.io/component":  "scenes",
			},
		},
		Data: map[string]string{
			scenesYamlKey: string(yamlData),
		},
	}

	// Get HomeAssistant to set owner reference
	ha := &hav1alpha1.HomeAssistant{}
	haRef := types.NamespacedName{Name: scene.Spec.HomeAssistantRef.Name, Namespace: scene.Namespace}
	if err := r.Get(ctx, haRef, ha); err != nil {
		// If HA doesn't exist, we can't create/update ConfigMap with proper owner reference
		// This is expected during cleanup/deletion scenarios
		if errors.IsNotFound(err) {
			log.V(1).Info("HomeAssistant not found, skipping ConfigMap reconciliation", "ha", haRef.Name)
			return nil
		}
		return fmt.Errorf("failed to get HomeAssistant: %w", err)
	}

	// Set owner reference to HomeAssistant (not to individual scene)
	if err := controllerutil.SetControllerReference(ha, configMap, r.Scheme); err != nil {
		return fmt.Errorf("failed to set owner reference: %w", err)
	}

	// Check if ConfigMap exists
	existingConfigMap := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: scene.Namespace}, existingConfigMap)
	if err != nil && errors.IsNotFound(err) {
		// Create new ConfigMap
		log.Info("Creating scenes ConfigMap", "name", configMapName)
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
		log.Info("Updating scenes ConfigMap", "name", configMapName)
		if err := r.Update(ctx, existingConfigMap); err != nil {
			return fmt.Errorf("failed to update ConfigMap: %w", err)
		}
	} else {
		log.V(1).Info("ConfigMap unchanged, skipping update", "name", configMapName)
	}

	return nil
}

// sceneToYaml converts HomeAssistantScene CR to YAML-compatible map
func (r *HomeAssistantSceneReconciler) sceneToYaml(
	scene *hav1alpha1.HomeAssistantScene,
) (map[string]interface{}, error) {
	result := make(map[string]interface{})

	// ID: auto-generate from CR name if not specified
	id := scene.Spec.ID
	if id == "" {
		id = scene.Name
	}
	result["id"] = id

	// Name (optional)
	if scene.Spec.Name != "" {
		result["name"] = scene.Spec.Name
	}

	// Icon (optional)
	if scene.Spec.Icon != "" {
		result["icon"] = scene.Spec.Icon
	}

	// Entities - convert to map[entity_id]attributes
	// Home Assistant expects: entities: { light.living_room: { state: on, brightness: 30 } }
	entities := make(map[string]interface{})
	for _, entity := range scene.Spec.Entities {
		entityData := make(map[string]interface{})

		// State is always required
		entityData["state"] = entity.State

		// Attributes (optional) - merge into entityData
		if len(entity.Attributes.Raw) > 0 {
			var attrs map[string]interface{}
			if err := json.Unmarshal(entity.Attributes.Raw, &attrs); err != nil {
				return nil, fmt.Errorf("failed to unmarshal attributes for %s: %w", entity.EntityID, err)
			}
			// Merge attributes into entityData
			for k, v := range attrs {
				entityData[k] = v
			}
		}

		entities[entity.EntityID] = entityData
	}
	result["entities"] = entities

	return result, nil
}

// performSceneReload triggers hot-reload of scenes via Home Assistant REST API
//
// nolint:gocyclo
//
//nolint:dupl // dupl: Similar to performAutomationReload by design
func (r *HomeAssistantSceneReconciler) performSceneReload(
	ctx context.Context,
	scene *hav1alpha1.HomeAssistantScene,
	ha *hav1alpha1.HomeAssistant,
	newHash string,
) error {
	log := logf.FromContext(ctx)

	// Check if autoReload is enabled
	autoReload := true
	if scene.Spec.AutoReload != nil {
		autoReload = *scene.Spec.AutoReload
	}

	if !autoReload {
		log.Info("AutoReload disabled, skipping reload", "name", scene.Name)
		scene.Status.LastError = ""
		return nil
	}

	// Get API token for hot-reload
	token, tokenErr := getApiToken(ctx, r.Client, ha)
	if tokenErr != nil {
		log.Info("API token not available, skipping hot-reload")
		scene.Status.LastError = "API token not found - bootstrap may not be configured"

		// Set condition: Token not available
		meta.SetStatusCondition(&scene.Status.Conditions, metav1.Condition{
			Type:               "ReloadReady",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: scene.Generation,
			Reason:             "TokenNotAvailable",
			Message:            "API token not available - bootstrap may not be configured",
		})
		return nil
	}

	// Build Home Assistant URL
	haURL := buildHomeAssistantURL(ha)
	haClient := haclient.NewClient(haURL)

	// Use PerformReloadWithRetry with smart detection
	config := ReloadConfig{
		MaxRetries:    3,
		RetryDelay:    5 * time.Second,
		ComponentName: "scene",
	}

	reloadFunc := func(ctx context.Context, token string) error {
		return haClient.ReloadScenes(ctx, token)
	}

	result := PerformReloadWithRetry(ctx, haClient, token, config, reloadFunc)

	// Update status based on result
	if result.Success {
		// SUCCESS
		now := metav1.Now()
		scene.Status.LastReloadTime = &now
		scene.Status.LastError = ""

		meta.SetStatusCondition(&scene.Status.Conditions, metav1.Condition{
			Type:               "ReloadReady",
			Status:             metav1.ConditionTrue,
			ObservedGeneration: scene.Generation,
			Reason:             "ReloadSuccessful",
			Message: fmt.Sprintf("Scene hot-reloaded successfully after %d attempts (%.1fs)",
				result.Attempts, result.Duration.Seconds()),
		})

		r.Recorder.Eventf(scene, corev1.EventTypeNormal, "ReloadSuccessful",
			"Scene hot-reload successful (attempts: %d, duration: %.1fs)",
			result.Attempts, result.Duration.Seconds())

		log.Info("Scene hot-reload successful", "name", scene.Name, "hash", newHash,
			"reloadID", result.ReloadID, "attempts", result.Attempts, "duration", result.Duration)
		return nil
	}

	// FAILURE
	if !result.ComponentLoaded {
		// Component not loaded - will retry on next reconcile
		scene.Status.LastError = result.Error.Error()

		meta.SetStatusCondition(&scene.Status.Conditions, metav1.Condition{
			Type:               "ReloadReady",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: scene.Generation,
			Reason:             "ComponentNotLoaded",
			Message:            "Scene integration not loaded in Home Assistant yet (will retry automatically)",
		})

		log.Info("Scene component not loaded, will retry",
			"reloadID", result.ReloadID, "error", result.Error)

		// Return error to trigger requeue
		return result.Error
	}

	// Hot-reload failed after retries
	scene.Status.LastError = result.Error.Error()

	meta.SetStatusCondition(&scene.Status.Conditions, metav1.Condition{
		Type:               "ReloadReady",
		Status:             metav1.ConditionFalse,
		ObservedGeneration: scene.Generation,
		Reason:             "ReloadFailed",
		Message: fmt.Sprintf("Hot-reload failed after %d attempts: %s",
			result.Attempts, truncateString(result.Error.Error(), 200)),
	})

	r.Recorder.Eventf(scene, corev1.EventTypeWarning, "ReloadFailed",
		"Hot-reload failed after %d attempts: %s",
		result.Attempts, truncateString(result.Error.Error(), 100))

	log.Error(result.Error, "Scene hot-reload failed after retries",
		"reloadID", result.ReloadID, "attempts", result.Attempts, "duration", result.Duration)

	// Don't fail reconciliation - scene is in ConfigMap, will be loaded on HA restart
	return nil
}

// calculateSceneHash computes SHA256 hash of the scene spec
func (r *HomeAssistantSceneReconciler) calculateSceneHash(
	scene *hav1alpha1.HomeAssistantScene,
) (string, error) {
	// Convert spec to YAML for consistent hashing
	yamlData, err := r.sceneToYaml(scene)
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
func (r *HomeAssistantSceneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hav1alpha1.HomeAssistantScene{}).
		Owns(&corev1.ConfigMap{}).
		Watches(
			&hav1alpha1.HomeAssistant{},
			handler.EnqueueRequestsFromMapFunc(r.findScenesForHomeAssistant),
		).
		Named("homeassistantscene").
		Complete(r)
}

// findScenesForHomeAssistant returns reconcile requests for all HomeAssistantScene
// resources that reference the given HomeAssistant
func (r *HomeAssistantSceneReconciler) findScenesForHomeAssistant(
	ctx context.Context,
	obj client.Object,
) []reconcile.Request {
	ha := obj.(*hav1alpha1.HomeAssistant)

	sceneList := &hav1alpha1.HomeAssistantSceneList{}
	if err := r.List(ctx, sceneList, client.InNamespace(ha.Namespace)); err != nil {
		return []reconcile.Request{}
	}

	var requests []reconcile.Request
	for _, scene := range sceneList.Items {
		if scene.Spec.HomeAssistantRef.Name == ha.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      scene.Name,
					Namespace: scene.Namespace,
				},
			})
		}
	}

	return requests
}

// validateHomeAssistantRef validates that referenced HomeAssistant exists
// and sets appropriate status condition if not found
func (r *HomeAssistantSceneReconciler) validateHomeAssistantRef(
	ctx context.Context,
	haRef types.NamespacedName,
	scene *hav1alpha1.HomeAssistantScene,
) (*hav1alpha1.HomeAssistant, error) {
	log := logf.FromContext(ctx)

	ha, err := getHomeAssistant(ctx, r.Client, haRef)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Error(err, "Referenced HomeAssistant not found",
				"name", haRef.Name)
			meta.SetStatusCondition(&scene.Status.Conditions,
				metav1.Condition{
					Type:   conditionTypeReady,
					Status: metav1.ConditionFalse,
					Reason: reasonInvalidScene,
					Message: fmt.Sprintf(
						"HomeAssistant %s not found",
						haRef.Name,
					),
					ObservedGeneration: scene.Generation,
				})
			if err := r.Status().Update(ctx, scene); err != nil {
				log.Error(err, "Failed to update status")
			}
			return nil, err
		}
		log.Error(err, "Failed to get HomeAssistant")
		return nil, err
	}
	return ha, nil
}
