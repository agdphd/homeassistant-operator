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
	configurationYamlKey     = "configuration.yaml"
	generatedConfigmapSuffix = "-configuration"
	// configHashAnnotationKey moved to constants.go (shared with homeassistant_controller.go)

	// Condition reasons for HomeAssistantConfiguration
	reasonConfigurationGenerated = "ConfigurationGenerated"
	reasonConfigurationNotFound  = "ConfigurationNotFound"
	reasonInvalidConfig          = "InvalidConfiguration"

	// Reload method names for status tracking
	reloadMethodRestart   = "restart"
	reloadMethodHotReload = "hot-reload"
	reloadMethodNone      = "none"

	// Default port for Home Assistant
	defaultHomeAssistantPort = 8123
)

// Critical sections that always require pod restart
var criticalSections = map[string]bool{
	"homeassistant": true,
	"http":          true,
}

// Sections that can be hot-reloaded
var reloadableSections = map[string]bool{
	"automation":     true,
	"script":         true,
	"scene":          true,
	"group":          true,
	"input_boolean":  true,
	"input_number":   true,
	"input_select":   true,
	"input_text":     true,
	"input_datetime": true,
	"timer":          true,
	"counter":        true,
}

// Keys under homeassistant: that require restart
var criticalHomeAssistantKeys = map[string]bool{
	"name":         true,
	"latitude":     true,
	"longitude":    true,
	"elevation":    true,
	"time_zone":    true,
	"unit_system":  true,
	"currency":     true,
	"country":      true,
	"language":     true,
	"internal_url": true,
	"external_url": true,
}

// Keys under homeassistant: that can be hot-reloaded
var reloadableHomeAssistantKeys = map[string]bool{
	"customize":        true,
	"customize_domain": true,
	"customize_glob":   true,
}

// Keys under http: that require restart
var criticalHttpKeys = map[string]bool{
	"server_port":     true,
	"ssl_certificate": true,
	"ssl_key":         true,
	"ssl_profile":     true,
	"ip_ban_enabled":  true,
}

// Keys under http: that can be hot-reloaded
var reloadableHttpKeys = map[string]bool{
	"cors_allowed_origins": true,
	"use_x_forwarded_for":  true,
	"trusted_proxies":      true,
}

// HomeAssistantConfigurationReconciler reconciles a HomeAssistantConfiguration object
type HomeAssistantConfigurationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantconfigurations,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantconfigurations/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistantconfigurations/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ha.homeassistant.io,resources=homeassistants,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
func (r *HomeAssistantConfigurationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Fetch the HomeAssistantConfiguration instance
	config := &hav1alpha1.HomeAssistantConfiguration{}
	if err := r.Get(ctx, req.NamespacedName, config); err != nil {
		if errors.IsNotFound(err) {
			log.Info("HomeAssistantConfiguration resource not found. Ignoring since object must be deleted")
			return ctrl.Result{}, nil
		}
		log.Error(err, "Failed to get HomeAssistantConfiguration")
		return ctrl.Result{}, err
	}

	// Validate HomeAssistant reference exists
	haRef := types.NamespacedName{
		Name:      config.Spec.HomeAssistantRef.Name,
		Namespace: config.Namespace,
	}
	ha := &hav1alpha1.HomeAssistant{}
	if err := r.Get(ctx, haRef, ha); err != nil {
		if errors.IsNotFound(err) {
			log.Error(err, "Referenced HomeAssistant not found", "name", haRef.Name)
			meta.SetStatusCondition(&config.Status.Conditions, metav1.Condition{
				Type:               conditionTypeReady,
				Status:             metav1.ConditionFalse,
				Reason:             reasonInvalidConfig,
				Message:            fmt.Sprintf("HomeAssistant %s not found", haRef.Name),
				ObservedGeneration: config.Generation,
			})
			if err := r.Status().Update(ctx, config); err != nil {
				log.Error(err, "Failed to update status")
			}
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		log.Error(err, "Failed to get HomeAssistant")
		return ctrl.Result{}, err
	}

	// Calculate hash of the configuration
	configHash := calculateConfigHash(config.Spec.Configuration)

	// Capture old configuration BEFORE updating ConfigMap
	// This is critical for needsRestart() to work correctly in auto mode
	var oldConfig string
	configMapName := config.Spec.HomeAssistantRef.Name + generatedConfigmapSuffix
	existingConfigMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: config.Namespace}, existingConfigMap); err == nil {
		oldConfig = existingConfigMap.Data[configurationYamlKey]
	}

	// Sync ConfigMap back to CRD state if it was modified externally (operator exclusivity)
	if err := r.syncConfigMapFromCRD(ctx, config); err != nil {
		log.Error(err, "Failed to sync ConfigMap from CRD")
		// Continue - we'll try to update it in reconcileGeneratedConfigMap
	}

	// Create or update the ConfigMap
	if err := r.reconcileGeneratedConfigMap(ctx, config); err != nil {
		log.Error(err, "Failed to reconcile generated ConfigMap")
		meta.SetStatusCondition(&config.Status.Conditions, metav1.Condition{
			Type:               conditionTypeReady,
			Status:             metav1.ConditionFalse,
			Reason:             "ReconciliationFailed",
			Message:            fmt.Sprintf("Failed to create/update ConfigMap: %v", err),
			ObservedGeneration: config.Generation,
		})
		if statusErr := r.Status().Update(ctx, config); statusErr != nil {
			log.Error(statusErr, "Failed to update status")
		}
		return ctrl.Result{}, err
	}

	// Perform configuration reload if hash changed
	if config.Status.ConfigHash != configHash {
		if err := r.performConfigReload(ctx, config, ha, configHash, oldConfig); err != nil {
			log.Error(err, "Failed to reload configuration")
			meta.SetStatusCondition(&config.Status.Conditions, metav1.Condition{
				Type:               conditionTypeReady,
				Status:             metav1.ConditionFalse,
				Reason:             "ReloadFailed",
				Message:            err.Error(),
				ObservedGeneration: config.Generation,
			})
			_ = r.Status().Update(ctx, config)
			return ctrl.Result{}, err
		}
	}

	// Update status
	config.Status.ConfigHash = configHash
	config.Status.ObservedGeneration = config.Generation
	meta.SetStatusCondition(&config.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             metav1.ConditionTrue,
		Reason:             reasonConfigurationGenerated,
		Message:            "Configuration successfully generated as ConfigMap",
		ObservedGeneration: config.Generation,
	})

	if err := r.Status().Update(ctx, config); err != nil {
		log.Error(err, "Failed to update HomeAssistantConfiguration status")
		return ctrl.Result{}, err
	}

	log.Info("Successfully reconciled HomeAssistantConfiguration")
	return ctrl.Result{}, nil
}

// reconcileGeneratedConfigMap creates or updates the ConfigMap containing configuration.yaml
func (r *HomeAssistantConfigurationReconciler) reconcileGeneratedConfigMap(ctx context.Context, config *hav1alpha1.HomeAssistantConfiguration) error {
	log := logf.FromContext(ctx)

	configMapName := config.Spec.HomeAssistantRef.Name + generatedConfigmapSuffix

	// Check if ConfigMap already exists
	existingConfigMap := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: config.Namespace}, existingConfigMap)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new ConfigMap WITHOUT hash annotation
			// The hash annotation is ONLY set by performConfigReload() when restart is needed
			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configMapName,
					Namespace: config.Namespace,
					Labels: map[string]string{
						labelAppName:      "homeassistant",
						labelAppInstance:  config.Spec.HomeAssistantRef.Name,
						labelAppManagedBy: "homeassistant-operator",
					},
					// NO hash annotation on initial creation
				},
				Data: map[string]string{
					configurationYamlKey: config.Spec.Configuration,
				},
			}

			// Set HomeAssistantConfiguration as the owner
			if err := controllerutil.SetControllerReference(config, configMap, r.Scheme); err != nil {
				return err
			}

			log.Info("Creating new generated ConfigMap (no hash annotation initially)", "name", configMapName)
			if err := r.Create(ctx, configMap); err != nil {
				return err
			}
			return nil
		}
		return err
	}

	// Update existing ConfigMap content if changed
	// IMPORTANT: We do NOT update the hash annotation here.
	// The hash annotation is ONLY updated by performConfigReload() when restart strategy is used.
	// For hot-reload, we update content but preserve the old annotation to avoid triggering pod restart.

	// Verify ownership before updating - check if owned by a DIFFERENT resource
	if len(existingConfigMap.OwnerReferences) > 0 {
		owner := existingConfigMap.OwnerReferences[0]
		// Check if owned by a different HomeAssistantConfiguration (by name, not UID)
		// This protects against accidentally modifying ConfigMaps from other resources
		if owner.Kind == "HomeAssistantConfiguration" && owner.Name != config.Name {
			log.Info("ConfigMap exists but is owned by different HomeAssistantConfiguration; skipping update",
				"name", configMapName,
				"owner", owner.Name)
			return nil
		}
		// If owner name matches but UID different (e.g., CR was deleted and recreated),
		// update the owner reference to point to current CR
		if owner.Name == config.Name && owner.UID != config.UID {
			log.Info("ConfigMap owned by old instance of same CR, updating owner reference",
				"name", configMapName)
			if err := controllerutil.SetControllerReference(config, existingConfigMap, r.Scheme); err != nil {
				return err
			}
			if err := r.Update(ctx, existingConfigMap); err != nil {
				return err
			}
		}
	} else {
		// ConfigMap exists but has no owner - adopt it by setting owner reference
		log.Info("Adopting existing ConfigMap (no owner reference)", "name", configMapName)
		if err := controllerutil.SetControllerReference(config, existingConfigMap, r.Scheme); err != nil {
			return err
		}
		if err := r.Update(ctx, existingConfigMap); err != nil {
			return err
		}
	}

	existingData := existingConfigMap.Data[configurationYamlKey]
	if existingData != config.Spec.Configuration {
		log.Info("Updating generated ConfigMap content (hash annotation preserved for hot-reload)", "name", configMapName)
		existingConfigMap.Data[configurationYamlKey] = config.Spec.Configuration
		if err := r.Update(ctx, existingConfigMap); err != nil {
			return err
		}
	}

	return nil
}

// calculateConfigHash computes SHA256 hash of the given configuration
func calculateConfigHash(content string) string {
	hash := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", hash)
}

// SetupWithManager sets up the controller with the Manager.
func (r *HomeAssistantConfigurationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&hav1alpha1.HomeAssistantConfiguration{}).
		Owns(&corev1.ConfigMap{}).
		Watches(
			&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(r.findHomeAssistantConfigurationForConfigMap),
		).
		Named("homeassistantconfiguration").
		Complete(r)
}

// getApiToken retrieves the API token from the bootstrap-created Secret
func (r *HomeAssistantConfigurationReconciler) getApiToken(ctx context.Context, ha *hav1alpha1.HomeAssistant) (string, error) {
	log := logf.FromContext(ctx)

	// Determine token secret name
	tokenSecretName := ha.Name + "-homeassistant-api-token"
	if ha.Status.Bootstrap != nil && ha.Status.Bootstrap.ApiTokenSecretName != "" {
		tokenSecretName = ha.Status.Bootstrap.ApiTokenSecretName
	}

	tokenSecret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      tokenSecretName,
		Namespace: ha.Namespace,
	}, tokenSecret)

	if err != nil {
		if errors.IsNotFound(err) {
			log.V(1).Info("API token secret not found, cannot perform hot-reload", "secret", tokenSecretName)
			return "", err
		}
		return "", err
	}

	tokenBytes, ok := tokenSecret.Data["token"]
	if !ok {
		log.Error(fmt.Errorf("missing token key"), "API token secret missing key", "secret", tokenSecretName)
		return "", fmt.Errorf("API token secret missing key 'token'")
	}

	return string(tokenBytes), nil
}

// needsRestart analyzes configuration changes and determines if restart is required
// Returns true if restart is needed, false if hot-reload is safe
func needsRestart(oldConfig, newConfig string) (bool, error) {
	// Parse both configs
	var oldYAML, newYAML map[string]interface{}

	if err := yaml.Unmarshal([]byte(oldConfig), &oldYAML); err != nil {
		return true, fmt.Errorf("failed to parse old config: %w", err) // Safe default: restart on parse error
	}

	if err := yaml.Unmarshal([]byte(newConfig), &newYAML); err != nil {
		return true, fmt.Errorf("failed to parse new config: %w", err) // Safe default: restart on parse error
	}

	// Initialize maps if nil
	if oldYAML == nil {
		oldYAML = make(map[string]interface{})
	}
	if newYAML == nil {
		newYAML = make(map[string]interface{})
	}

	// Check for new top-level sections (adding integrations)
	for key := range newYAML {
		if _, existed := oldYAML[key]; !existed {
			// New section added
			if reloadableSections[key] {
				continue // Can be hot-reloaded
			}
			// Unknown or critical section added - requires restart
			return true, nil
		}
	}

	// Check for removed sections
	for key := range oldYAML {
		if _, exists := newYAML[key]; !exists {
			// Section removed - always requires restart
			return true, nil
		}
	}

	// Check critical sections for changes
	for section := range criticalSections {
		if changed, critical := sectionChanged(oldYAML, newYAML, section); changed {
			if critical {
				return true, nil
			}
		}
	}

	// All changes are either in reloadable sections or non-critical changes
	return false, nil
}

// sectionChanged checks if a specific section changed and if it's critical
func sectionChanged(old, new map[string]interface{}, section string) (changed bool, critical bool) {
	oldSection, oldExists := old[section]
	newSection, newExists := new[section]

	if !oldExists && !newExists {
		return false, false
	}

	if !oldExists || !newExists {
		return true, true // Section added or removed
	}

	// Special handling for homeassistant section
	if section == "homeassistant" {
		return homeassistantSectionChanged(oldSection, newSection)
	}

	// Special handling for http section
	if section == "http" {
		return httpSectionChanged(oldSection, newSection)
	}

	// For other sections, just check if reloadable
	oldStr := fmt.Sprintf("%v", oldSection)
	newStr := fmt.Sprintf("%v", newSection)

	if oldStr != newStr {
		return true, criticalSections[section]
	}

	return false, false
}

// homeassistantSectionChanged checks homeassistant: section changes
func homeassistantSectionChanged(old, new interface{}) (changed bool, critical bool) {
	oldMap, oldOk := old.(map[string]interface{})
	newMap, newOk := new.(map[string]interface{})

	if !oldOk || !newOk {
		return true, true
	}

	// Check for critical key changes
	for key := range criticalHomeAssistantKeys {
		oldVal, oldHas := oldMap[key]
		newVal, newHas := newMap[key]

		if oldHas != newHas || fmt.Sprintf("%v", oldVal) != fmt.Sprintf("%v", newVal) {
			return true, true // Critical change
		}
	}

	// Check for reloadable key changes
	for key := range reloadableHomeAssistantKeys {
		oldVal, oldHas := oldMap[key]
		newVal, newHas := newMap[key]

		if oldHas != newHas || fmt.Sprintf("%v", oldVal) != fmt.Sprintf("%v", newVal) {
			return true, false // Reloadable change
		}
	}

	// Check if logger settings changed (reloadable)
	if !reflect.DeepEqual(oldMap["logger"], newMap["logger"]) {
		return true, false // Logger is reloadable
	}

	// Check if automations changed (reloadable)
	if !reflect.DeepEqual(oldMap["automation"], newMap["automation"]) {
		return true, false // Automations are reloadable
	}

	return false, false
}

// httpSectionChanged checks http: section changes
func httpSectionChanged(old, new interface{}) (changed bool, critical bool) {
	oldMap, oldOk := old.(map[string]interface{})
	newMap, newOk := new.(map[string]interface{})

	if !oldOk || !newOk {
		return true, true
	}

	// Check for critical HTTP key changes
	for key := range criticalHttpKeys {
		oldVal, oldHas := oldMap[key]
		newVal, newHas := newMap[key]

		if oldHas != newHas || fmt.Sprintf("%v", oldVal) != fmt.Sprintf("%v", newVal) {
			return true, true // Critical change
		}
	}

	// Check for reloadable HTTP key changes
	for key := range reloadableHttpKeys {
		oldVal, oldHas := oldMap[key]
		newVal, newHas := newMap[key]

		if oldHas != newHas || fmt.Sprintf("%v", oldVal) != fmt.Sprintf("%v", newVal) {
			return true, false // Reloadable change
		}
	}

	return false, false
}

// buildHomeAssistantURL constructs the URL for Home Assistant service
func (r *HomeAssistantConfigurationReconciler) buildHomeAssistantURL(ha *hav1alpha1.HomeAssistant) string {
	// Service name matches the HomeAssistant CR name (not ha.Name + "-homeassistant")
	// See homeassistant_controller.go:578 for Service creation
	serviceName := ha.Name
	port := int32(defaultHomeAssistantPort)
	if ha.Spec.Service != nil && ha.Spec.Service.Port != 0 {
		port = ha.Spec.Service.Port
	}
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", serviceName, ha.Namespace, port)
}

// performHotReload attempts to hot-reload the configuration via HA REST API
// Retries with fixed interval to handle kubelet ConfigMap sync delay
// Kubelet typically syncs ConfigMap volumes every 60s (syncFrequency), so we need
// to wait for the file to be synced to the pod before hot-reload will work correctly
func (r *HomeAssistantConfigurationReconciler) performHotReload(ctx context.Context, haURL, token string) error {
	log := logf.FromContext(ctx)

	haClient := haclient.NewClient(haURL)

	log.Info("Waiting for kubelet to sync ConfigMap to pod")

	// Poll until CheckConfig succeeds (indicates file is synced)
	// Kubelet syncFrequency is typically 60s, so we need generous timeout
	const maxRetries = 20 // 20 * 5s = 100s max wait
	const retryInterval = 5 * time.Second

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			log.V(1).Info("Config not ready yet (waiting for kubelet sync)",
				"attempt", attempt+1,
				"waitTime", fmt.Sprintf("%ds", attempt*5),
				"nextRetryIn", retryInterval)
			time.Sleep(retryInterval)
		}

		// First, validate the configuration
		// CheckConfig reads /config/configuration.yaml from pod
		// It will fail if:
		// - Kubelet hasn't synced yet → old/invalid config
		// - Config has syntax errors
		if err := haClient.CheckConfig(ctx, token); err != nil {
			lastErr = err
			log.V(1).Info("Configuration validation failed, will retry", "attempt", attempt+1, "error", err.Error())
			continue
		}

		// Config is valid and readable - kubelet must have synced
		waitTime := time.Duration(attempt) * retryInterval
		log.Info("Config synced and validated, proceeding with hot-reload", "waitTime", waitTime)

		// Reload the core configuration
		if err := haClient.ReloadCoreConfig(ctx, token); err != nil {
			lastErr = err
			log.V(1).Info("Failed to reload configuration, will retry", "attempt", attempt+1, "error", err.Error())
			continue
		}

		log.Info("Configuration hot-reload successful", "attempts", attempt+1, "totalWaitTime", waitTime)
		return nil
	}

	log.Error(lastErr, "Configuration hot-reload failed after retries - timeout waiting for config sync", "maxRetries", maxRetries)
	return fmt.Errorf("timeout waiting for config sync: %w", lastErr)
}

// updateStatefulSetConfigAnnotation was removed in Faza 1 refactor.
// StatefulSet annotation updates are now handled by HomeAssistant Controller,
// which reads the hash from ConfigMap annotation and syncs to StatefulSet.
// See rozwiazanie-architektury.md for details.

// performConfigReload executes reload based on strategy
// oldConfig parameter contains configuration content captured BEFORE ConfigMap update
func (r *HomeAssistantConfigurationReconciler) performConfigReload(ctx context.Context, config *hav1alpha1.HomeAssistantConfiguration, ha *hav1alpha1.HomeAssistant, newHash string, oldConfig string) error {
	log := logf.FromContext(ctx)

	// Check if autoReload is enabled (default: true)
	if config.Spec.AutoReload != nil && !*config.Spec.AutoReload {
		log.V(1).Info("AutoReload disabled, skipping reload")
		return nil
	}

	// Check if hash actually changed
	if config.Status.ConfigHash == newHash {
		log.V(1).Info("Configuration hash unchanged, no reload needed")
		return nil
	}

	// Get API token for hot-reload attempts
	token, tokenErr := r.getApiToken(ctx, ha)

	// Build Home Assistant URL
	haURL := r.buildHomeAssistantURL(ha)

	// Determine effective strategy
	strategy := string(config.Spec.ReloadStrategy)
	if strategy == "" || strategy == string(hav1alpha1.ConfigurationReloadStrategyAuto) {
		// Auto: decide based on config changes
		// Use oldConfig passed from Reconcile (captured before ConfigMap update)
		// This ensures needsRestart compares actual old vs new, not new vs new

		needsRestart, parseErr := needsRestart(oldConfig, config.Spec.Configuration)
		if parseErr != nil {
			log.Error(parseErr, "Failed to analyze config changes, defaulting to restart")
			needsRestart = true
		}

		if needsRestart || tokenErr != nil {
			strategy = reloadMethodRestart
		} else {
			strategy = reloadMethodHotReload
		}
	}

	// Execute strategy
	now := metav1.Now()
	config.Status.LastReloadTime = &now

	if strategy == string(hav1alpha1.ConfigurationReloadStrategyRestart) || strategy == reloadMethodRestart {
		// Update ConfigMap annotation hash to trigger pod restart via HomeAssistant Controller
		if err := r.updateConfigMapHashAnnotation(ctx, config, newHash); err != nil {
			return fmt.Errorf("failed to update ConfigMap hash for restart: %w", err)
		}

		config.Status.LastReloadMethod = reloadMethodRestart
		config.Status.LastError = ""
		log.Info("Configuration reload: restart (updated ConfigMap hash to trigger StatefulSet rolling restart)", "hash", newHash)
		return nil
	}

	// Try hot-reload (strategy is hot-reload or auto decided to try it)
	if tokenErr != nil {
		// If user explicitly requested hot-reload strategy, fail instead of falling back
		if strategy == string(hav1alpha1.ConfigurationReloadStrategyHotReload) {
			config.Status.LastError = fmt.Sprintf("Hot-reload strategy requested but no API token available: %v", tokenErr)
			return fmt.Errorf("hot-reload strategy requires API token but none available: %w", tokenErr)
		}

		// Auto strategy can fallback to restart
		log.Error(tokenErr, "No API token available, falling back to restart")
		config.Status.LastError = fmt.Sprintf("No API token: %v", tokenErr)

		// Update ConfigMap annotation to trigger restart via HomeAssistant Controller
		if err := r.updateConfigMapHashAnnotation(ctx, config, newHash); err != nil {
			return fmt.Errorf("failed to update ConfigMap hash for restart fallback: %w", err)
		}

		config.Status.LastReloadMethod = reloadMethodRestart
		log.Info("Configuration reload: restart (fallback - no API token)", "hash", newHash)
		return nil
	}

	// Attempt hot-reload
	if err := r.performHotReload(ctx, haURL, token); err != nil {
		// If user explicitly requested hot-reload strategy, fail instead of falling back
		if strategy == string(hav1alpha1.ConfigurationReloadStrategyHotReload) {
			config.Status.LastError = fmt.Sprintf("Hot-reload failed: %v", err)
			return fmt.Errorf("hot-reload strategy failed: %w", err)
		}

		// Auto strategy can fallback to restart
		log.Error(err, "Hot-reload failed, falling back to restart")
		config.Status.LastError = fmt.Sprintf("Hot-reload failed: %v", err)

		// Update ConfigMap annotation to trigger restart via HomeAssistant Controller
		if updateErr := r.updateConfigMapHashAnnotation(ctx, config, newHash); updateErr != nil {
			return fmt.Errorf("failed to update ConfigMap hash for restart fallback: %w", updateErr)
		}

		config.Status.LastReloadMethod = reloadMethodRestart
		log.Info("Configuration reload: restart (fallback - hot-reload failed)", "hash", newHash)
		return nil
	}

	// Hot-reload succeeded - do NOT update ConfigMap hash annotation
	// This prevents unnecessary pod restart
	config.Status.LastReloadMethod = reloadMethodHotReload
	config.Status.LastError = ""
	log.Info("Configuration reload: hot-reload (no pod restart)", "hash", newHash)
	return nil
}

// updateConfigMapHashAnnotation updates the hash annotation on ConfigMap to trigger pod restart
// This should ONLY be called when restart strategy is used, not during hot-reload
func (r *HomeAssistantConfigurationReconciler) updateConfigMapHashAnnotation(ctx context.Context, config *hav1alpha1.HomeAssistantConfiguration, newHash string) error {
	log := logf.FromContext(ctx)

	configMapName := config.Spec.HomeAssistantRef.Name + generatedConfigmapSuffix
	configMap := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: config.Namespace}, configMap); err != nil {
		return fmt.Errorf("failed to get ConfigMap for hash update: %w", err)
	}

	if configMap.Annotations == nil {
		configMap.Annotations = make(map[string]string)
	}

	oldHash := configMap.Annotations[configHashAnnotationKey]
	if oldHash == newHash {
		// Hash already matches, no update needed
		return nil
	}

	configMap.Annotations[configHashAnnotationKey] = newHash
	if err := r.Update(ctx, configMap); err != nil {
		return fmt.Errorf("failed to update ConfigMap hash annotation: %w", err)
	}

	log.Info("Updated ConfigMap hash annotation to trigger pod restart",
		"configMapName", configMapName,
		"oldHash", oldHash,
		"newHash", newHash)
	return nil
}

// syncConfigMapFromCRD ensures ConfigMap matches CRD state (operator exclusivity)
// This prevents external modifications to ConfigMap by restoring it to CRD state
func (r *HomeAssistantConfigurationReconciler) syncConfigMapFromCRD(ctx context.Context, config *hav1alpha1.HomeAssistantConfiguration) error {
	log := logf.FromContext(ctx)

	configMapName := config.Spec.HomeAssistantRef.Name + generatedConfigmapSuffix
	existingConfigMap := &corev1.ConfigMap{}

	err := r.Get(ctx, types.NamespacedName{
		Name:      configMapName,
		Namespace: config.Namespace,
	}, existingConfigMap)

	if err != nil {
		if errors.IsNotFound(err) {
			// ConfigMap doesn't exist yet - will be created by reconcileGeneratedConfigMap
			return nil
		}
		return err
	}

	// Check if ConfigMap is owned by this HomeAssistantConfiguration
	isOwned := false
	for _, ownerRef := range existingConfigMap.OwnerReferences {
		if ownerRef.UID == config.UID {
			isOwned = true
			break
		}
	}

	if !isOwned {
		// ConfigMap exists but is not owned by this CRD - don't touch it
		log.Info("ConfigMap exists but is not owned by this HomeAssistantConfiguration, skipping sync",
			"configMapName", configMapName)
		return nil
	}

	// Check if ConfigMap was modified externally (content mismatch)
	// NOTE: We only check content, NOT hash annotation.
	// Hash annotation is managed by performConfigReload() and should not be synced here.
	currentContent := existingConfigMap.Data[configurationYamlKey]
	expectedContent := config.Spec.Configuration

	if currentContent == expectedContent {
		// ConfigMap content is in sync with CRD
		return nil
	}

	// ConfigMap was modified externally - restore from CRD
	// NOTE: We only restore the content (Data), NOT the hash annotation.
	// The hash annotation is ONLY updated during restart strategy in performConfigReload()
	// to explicitly trigger pod restart.
	log.Info("ConfigMap was modified externally, restoring from CRD state",
		"configMapName", configMapName,
		"currentContent", currentContent[:min(50, len(currentContent))],
		"expectedContent", expectedContent[:min(50, len(expectedContent))])

	existingConfigMap.Data[configurationYamlKey] = expectedContent
	// DO NOT update annotation hash here - it's managed by performConfigReload() only

	if err := r.Update(ctx, existingConfigMap); err != nil {
		log.Error(err, "Failed to restore ConfigMap from CRD state")
		return err
	}

	log.Info("Successfully restored ConfigMap to CRD state", "configMapName", configMapName)
	return nil
}

// findHomeAssistantConfigurationForConfigMap finds the HomeAssistantConfiguration that owns a given ConfigMap
func (r *HomeAssistantConfigurationReconciler) findHomeAssistantConfigurationForConfigMap(ctx context.Context, obj client.Object) []reconcile.Request {
	configMap := obj.(*corev1.ConfigMap)

	// Check if this ConfigMap is owned by a HomeAssistantConfiguration
	for _, ownerRef := range configMap.OwnerReferences {
		if ownerRef.Kind == "HomeAssistantConfiguration" {
			// Reconcile the owning HomeAssistantConfiguration
			return []reconcile.Request{
				{
					NamespacedName: types.NamespacedName{
						Name:      ownerRef.Name,
						Namespace: configMap.Namespace,
					},
				},
			}
		}
	}

	return []reconcile.Request{}
}
