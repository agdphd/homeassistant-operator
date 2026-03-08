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
	Scheme      *runtime.Scheme
	Recorder    events.EventRecorder
	NewHAClient func(baseURL string) *haclient.Client // overridable for testing
}

// haClientFor returns a HA API client for the given HomeAssistant instance.
func (r *HomeAssistantSceneReconciler) haClientFor(ha *hav1alpha1.HomeAssistant) *haclient.Client {
	haURL := buildHomeAssistantURL(ha)
	if r.NewHAClient != nil {
		return r.NewHAClient(haURL)
	}
	return haclient.NewClient(haURL)
}

// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantscenes,verbs=get;list;watch
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantscenes,verbs=create;update;patch;delete
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantscenes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantscenes/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistants,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=events.k8s.io,resources=events,verbs=create;patch

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
			log.Info("Handling deletion - deleting scene via HA REST API")

			// Delete scene via HA REST API (best effort — proceed even if HA is unavailable)
			haRef := types.NamespacedName{
				Name:      scene.Spec.HomeAssistantRef.Name,
				Namespace: scene.Namespace,
			}
			if ha, haErr := r.validateHomeAssistantRef(ctx, haRef, scene); haErr == nil {
				if token, tokenErr := getApiToken(ctx, r.Client, ha); tokenErr == nil {
					haClient := r.haClientFor(ha)
					id := scene.Spec.ID
					if id == "" {
						id = scene.Name
					}
					if delErr := haClient.DeleteScene(ctx, token, id); delErr != nil {
						log.Info("Failed to delete scene via HA API (proceeding with finalizer removal)", "error", delErr)
					}
				} else {
					log.Info("API token not available during deletion, proceeding with finalizer removal")
				}
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

	// Get API token — requeue if not available yet (bootstrap may still be running)
	token, tokenErr := getApiToken(ctx, r.Client, ha)
	if tokenErr != nil {
		log.Info("API token not available, requeueing")
		meta.SetStatusCondition(&scene.Status.Conditions, metav1.Condition{
			Type:               "ReloadReady",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: scene.Generation,
			Reason:             reasonTokenNotAvailable,
			Message:            errMsgTokenNotAvailable,
		})
		if statusErr := r.Status().Update(ctx, scene); statusErr != nil {
			log.Error(statusErr, "Failed to update status")
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// POST scene via HA REST API
	if err := r.reconcileSceneViaAPI(ctx, scene, ha, token); err != nil {
		log.Error(err, "Failed to POST scene via HA REST API")
		meta.SetStatusCondition(&scene.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             "ReconciliationFailed",
			Message:            fmt.Sprintf("Failed to POST scene via HA API: %v", err),
			ObservedGeneration: scene.Generation,
		})
		// Clear stale TokenNotAvailable from ReloadReady — token was found, failure is in the API call
		meta.SetStatusCondition(&scene.Status.Conditions, metav1.Condition{
			Type:               "ReloadReady",
			Status:             metav1.ConditionFalse,
			Reason:             "ReconciliationFailed",
			Message:            fmt.Sprintf("Failed to POST scene via HA API: %v", err),
			ObservedGeneration: scene.Generation,
		})
		if statusErr := r.Status().Update(ctx, scene); statusErr != nil {
			log.Error(statusErr, "Failed to update status")
		}
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Perform hot-reload if hash changed OR reload is pending (token was previously unavailable)
	hashChanged := scene.Status.SceneHash != sceneHash
	reloadPending := false
	if reloadCond := meta.FindStatusCondition(scene.Status.Conditions, "ReloadReady"); reloadCond != nil {
		reloadPending = reloadCond.Status == metav1.ConditionFalse && reloadCond.Reason == reasonTokenNotAvailable
	}
	if hashChanged || reloadPending {
		if err := r.performSceneReload(ctx, scene, ha, sceneHash); err != nil {
			if statusErr := r.Status().Update(ctx, scene); statusErr != nil {
				log.Error(statusErr, "Failed to update status")
			}
			return ctrl.Result{}, err
		}
	}

	// Update status — always mark Ready after ConfigMap sync (reload failure is graceful)
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

	// Requeue if hot-reload is still pending (token not yet available) so we retry once it is
	if reloadCond := meta.FindStatusCondition(scene.Status.Conditions, "ReloadReady"); reloadCond != nil {
		if reloadCond.Status == metav1.ConditionFalse && reloadCond.Reason == reasonTokenNotAvailable {
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}
	return ctrl.Result{}, nil
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

// reconcileSceneViaAPI creates or updates this scene in Home Assistant
// via REST API (PUT /api/config/scene/config/{id}).
// HA writes the result to scenes.yaml on the PVC (writable).
func (r *HomeAssistantSceneReconciler) reconcileSceneViaAPI(
	ctx context.Context,
	scene *hav1alpha1.HomeAssistantScene,
	ha *hav1alpha1.HomeAssistant,
	token string,
) error {
	sceneData, err := r.sceneToYaml(scene)
	if err != nil {
		return fmt.Errorf("failed to convert scene to map: %w", err)
	}

	id := scene.Spec.ID
	if id == "" {
		id = scene.Name
	}

	haClient := r.haClientFor(ha)
	return haClient.PutScene(ctx, token, id, sceneData)
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
		scene.Status.LastError = errMsgTokenNotAvailable

		// Set condition: Token not available
		meta.SetStatusCondition(&scene.Status.Conditions, metav1.Condition{
			Type:               "ReloadReady",
			Status:             metav1.ConditionFalse,
			ObservedGeneration: scene.Generation,
			Reason:             reasonTokenNotAvailable,
			Message:            "API token not available - bootstrap may not be configured",
		})
		return nil
	}

	// Build Home Assistant client
	haClient := r.haClientFor(ha)

	// Use PerformReloadWithRetry with smart detection
	config := ReloadConfig{
		MaxRetries:         3,
		RetryDelay:         5 * time.Second,
		ComponentName:      "scene",
		SkipComponentCheck: true, // scene is a core HA integration, always loaded
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

		r.Recorder.Eventf(scene, nil, corev1.EventTypeNormal, "ReloadSuccessful", "ReloadSuccessful",
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

	r.Recorder.Eventf(scene, nil, corev1.EventTypeWarning, "ReloadFailed", "ReloadFailed",
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
