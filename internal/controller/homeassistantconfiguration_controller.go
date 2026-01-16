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
	"time"

	"gopkg.in/yaml.v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	hav1alpha1 "github.com/przemekhys/homeassistant-operator/api/v1alpha1"
	"github.com/przemekhys/homeassistant-operator/internal/haclient"
)

const (
	configurationYamlKey     = "configuration.yaml"
	generatedConfigmapSuffix = "-configuration"
	configHashAnnotationKey  = "ha.homeassistant.io/config-hash"

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

	// Create or update the ConfigMap
	if err := r.reconcileGeneratedConfigMap(ctx, config, configHash); err != nil {
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
		if err := r.performConfigReload(ctx, config, ha, configHash); err != nil {
			log.Error(err, "Failed to reload configuration")
			// Don't fail reconciliation - ConfigMap is updated successfully
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
func (r *HomeAssistantConfigurationReconciler) reconcileGeneratedConfigMap(ctx context.Context, config *hav1alpha1.HomeAssistantConfiguration, hash string) error {
	log := logf.FromContext(ctx)

	configMapName := config.Spec.HomeAssistantRef.Name + generatedConfigmapSuffix
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName,
			Namespace: config.Namespace,
			Labels: map[string]string{
				labelAppName:      "homeassistant",
				labelAppInstance:  config.Spec.HomeAssistantRef.Name,
				labelAppManagedBy: "homeassistant-operator",
			},
			Annotations: map[string]string{
				configHashAnnotationKey: hash,
			},
		},
		Data: map[string]string{
			configurationYamlKey: config.Spec.Configuration,
		},
	}

	// Set HomeAssistantConfiguration as the owner
	if err := controllerutil.SetControllerReference(config, configMap, r.Scheme); err != nil {
		return err
	}

	// Check if ConfigMap already exists
	existingConfigMap := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: config.Namespace}, existingConfigMap)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new ConfigMap
			log.Info("Creating new generated ConfigMap", "name", configMapName)
			if err := r.Create(ctx, configMap); err != nil {
				return err
			}
			return nil
		}
		return err
	}

	// Update existing ConfigMap if hash changed
	if existingConfigMap.Annotations[configHashAnnotationKey] != hash {
		log.Info("Updating generated ConfigMap (hash changed)", "name", configMapName,
			"oldHash", existingConfigMap.Annotations[configHashAnnotationKey],
			"newHash", hash)
		existingConfigMap.Data = configMap.Data
		existingConfigMap.Annotations[configHashAnnotationKey] = hash
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
	if oldMap["logger"] != newMap["logger"] {
		return true, false // Logger is reloadable
	}

	// Check if automations changed (reloadable)
	if oldMap["automation"] != newMap["automation"] {
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
	serviceName := ha.Name + "-homeassistant"
	port := ha.Spec.Service.Port
	if port == 0 {
		port = defaultHomeAssistantPort
	}
	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", serviceName, ha.Namespace, port)
}

// performHotReload attempts to hot-reload the configuration via HA REST API
func (r *HomeAssistantConfigurationReconciler) performHotReload(ctx context.Context, haURL, token string) error {
	log := logf.FromContext(ctx)

	client := haclient.NewClient(haURL)

	// First, validate the configuration
	if err := client.CheckConfig(ctx, token); err != nil {
		log.Error(err, "Configuration validation failed, cannot hot-reload")
		return err
	}

	// Reload the core configuration
	if err := client.ReloadCoreConfig(ctx, token); err != nil {
		log.Error(err, "Failed to reload configuration")
		return err
	}

	log.Info("Configuration hot-reload successful")
	return nil
}

// updateStatefulSetConfigAnnotation updates the config hash annotation on the StatefulSet
// to trigger a rolling restart when config changes
func (r *HomeAssistantConfigurationReconciler) updateStatefulSetConfigAnnotation(ctx context.Context, ha *hav1alpha1.HomeAssistant, hash string) error {
	log := logf.FromContext(ctx)

	// StatefulSet has the same name as the HomeAssistant CR
	stsName := types.NamespacedName{
		Name:      ha.Name,
		Namespace: ha.Namespace,
	}

	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, stsName, sts); err != nil {
		if errors.IsNotFound(err) {
			log.Info("StatefulSet not found, skipping annotation update", "name", stsName.Name)
			return nil
		}
		return err
	}

	// Check if annotation needs update
	currentHash := ""
	if sts.Spec.Template.Annotations != nil {
		currentHash = sts.Spec.Template.Annotations[configHashAnnotationKey]
	}

	if currentHash == hash {
		log.V(1).Info("StatefulSet annotation already up to date", "hash", hash)
		return nil
	}

	// Update pod template annotation to trigger rolling restart
	if sts.Spec.Template.Annotations == nil {
		sts.Spec.Template.Annotations = make(map[string]string)
	}
	sts.Spec.Template.Annotations[configHashAnnotationKey] = hash

	log.Info("Updating StatefulSet annotation to trigger pod restart",
		"statefulset", stsName.Name,
		"oldHash", currentHash,
		"newHash", hash)

	if err := r.Update(ctx, sts); err != nil {
		return fmt.Errorf("failed to update StatefulSet: %w", err)
	}

	return nil
}

// performConfigReload executes reload based on strategy
func (r *HomeAssistantConfigurationReconciler) performConfigReload(ctx context.Context, config *hav1alpha1.HomeAssistantConfiguration, ha *hav1alpha1.HomeAssistant, newHash string) error {
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
		// Get old configuration from ConfigMap if it exists
		oldConfig := ""
		configMapName := config.Spec.HomeAssistantRef.Name + generatedConfigmapSuffix
		existingCM := &corev1.ConfigMap{}
		if err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: config.Namespace}, existingCM); err == nil {
			oldConfig = existingCM.Data[configurationYamlKey]
		}

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
		// Force pod restart via annotation
		if err := r.updateStatefulSetConfigAnnotation(ctx, ha, newHash); err != nil {
			log.Error(err, "Failed to update StatefulSet annotation")
			config.Status.LastError = fmt.Sprintf("Failed to trigger restart: %v", err)
			return err
		}
		config.Status.LastReloadMethod = reloadMethodRestart
		log.Info("Configuration reload: restart", "hash", newHash)
		return nil
	}

	// Try hot-reload (strategy is hot-reload or auto decided to try it)
	if tokenErr != nil {
		log.Error(tokenErr, "No API token available, falling back to restart")
		config.Status.LastError = fmt.Sprintf("No API token: %v", tokenErr)
		// Fall back to restart
		if err := r.updateStatefulSetConfigAnnotation(ctx, ha, newHash); err != nil {
			log.Error(err, "Failed to update StatefulSet annotation")
			config.Status.LastError = fmt.Sprintf("Fallback to restart failed: %v", err)
			return err
		}
		config.Status.LastReloadMethod = reloadMethodRestart
		return nil
	}

	// Attempt hot-reload
	if err := r.performHotReload(ctx, haURL, token); err != nil {
		log.Error(err, "Hot-reload failed, falling back to restart")
		config.Status.LastError = fmt.Sprintf("Hot-reload failed: %v", err)
		// Fall back to restart
		if err := r.updateStatefulSetConfigAnnotation(ctx, ha, newHash); err != nil {
			log.Error(err, "Fallback restart failed")
			return err
		}
		config.Status.LastReloadMethod = reloadMethodRestart
		return nil
	}

	config.Status.LastReloadMethod = reloadMethodHotReload
	log.Info("Configuration reload: hot-reload", "hash", newHash)
	return nil
}
