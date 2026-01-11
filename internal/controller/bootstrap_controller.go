package controller

import (
	"context"
	"fmt"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	hav1alpha1 "github.com/przemekhys/homeassistant-operator/api/v1alpha1"
	"github.com/przemekhys/homeassistant-operator/internal/haclient"
)

const (
	// Bootstrap constants
	defaultOwnerName          = "Admin"
	defaultLanguage           = "en"
	defaultUsernameKey        = "username"
	defaultPasswordKey        = "password"
	apiTokenSecretKeyName     = "token"
	defaultApiTokenSecretName = "homeassistant-api-token"

	// Reconciliation intervals
	bootstrapRetryInterval    = 30 * time.Second
	bootstrapHealthCheckRetry = 10 * time.Second

	// Condition reasons
	reasonBootstrapInProgress         = "BootstrapInProgress"
	reasonBootstrapCompleted          = "BootstrapCompleted"
	reasonBootstrapFailed             = "BootstrapFailed"
	reasonBootstrapNotReady           = "HomeAssistantNotReady"
	reasonBootstrapAlreadyDone        = "BootstrapAlreadyDone"
	reasonBootstrapMissingCredentials = "MissingCredentials"
)

// reconcileBootstrap handles the automatic onboarding and API token creation
// This is a complete rewrite from Job-based to native Go implementation
func (r *HomeAssistantReconciler) reconcileBootstrap(ctx context.Context, ha *hav1alpha1.HomeAssistant) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Check if bootstrap is enabled
	if ha.Spec.Bootstrap == nil || !ha.Spec.Bootstrap.Enabled {
		return ctrl.Result{}, nil
	}

	// Check if bootstrap already completed
	if ha.Status.Bootstrap != nil && ha.Status.Bootstrap.Completed {
		log.V(1).Info("Bootstrap already completed, skipping")
		return ctrl.Result{}, nil
	}

	// Initialize bootstrap status if needed
	if ha.Status.Bootstrap == nil {
		ha.Status.Bootstrap = &hav1alpha1.BootstrapStatus{}
	}

	// Validate bootstrap configuration
	if err := r.validateBootstrapConfig(ha); err != nil {
		return r.updateBootstrapStatus(ctx, ha, reasonBootstrapMissingCredentials, err.Error(), false, false)
	}

	// Get credentials from Secret
	username, password, err := r.getBootstrapCredentials(ctx, ha)
	if err != nil {
		log.Error(err, "Failed to get bootstrap credentials")
		return r.updateBootstrapStatus(ctx, ha, reasonBootstrapFailed, fmt.Sprintf("Failed to get credentials: %v", err), false, false)
	}

	// Build Home Assistant URL
	haURL := r.buildHomeAssistantURL(ha)

	// Create HA client
	client := haclient.NewClient(haURL).WithTimeout(30 * time.Second)

	// Get bootstrap configuration
	ownerName := getOrDefault(ha.Spec.Bootstrap.OwnerName, defaultOwnerName)
	language := getOrDefault(ha.Spec.Bootstrap.Language, defaultLanguage)

	// Prepare bootstrap options
	opts := &haclient.BootstrapOptions{
		CreateLongLivedToken: ha.Spec.Bootstrap.CreateApiToken,
		EnableAnalytics:      ha.Spec.Bootstrap.Analytics,
	}

	// Add location config if provided
	if ha.Spec.Bootstrap.Location != nil {
		opts.CoreConfig = r.buildCoreConfigRequest(ha)
	}

	// Perform bootstrap
	log.Info("Performing Home Assistant bootstrap", "url", haURL)
	token, err := client.PerformBootstrap(ctx, username, password, ownerName, language, opts)

	if err != nil {
		return r.handleBootstrapError(ctx, ha, err)
	}

	// Bootstrap completed successfully
	log.Info("Bootstrap completed successfully")

	// Create Secret with API token if requested
	tokenCreated := false
	if ha.Spec.Bootstrap.CreateApiToken && token != "" {
		if err := r.createAPITokenSecret(ctx, ha, token); err != nil {
			log.Error(err, "Failed to create API token Secret")
			return r.updateBootstrapStatus(ctx, ha, reasonBootstrapFailed, fmt.Sprintf("Failed to create token Secret: %v", err), false, false)
		}
		tokenCreated = true
	}

	// Mark bootstrap as completed
	return r.updateBootstrapStatus(ctx, ha, reasonBootstrapCompleted, "Bootstrap completed successfully", true, tokenCreated)
}

// buildCoreConfigRequest builds CoreConfigRequest from HomeAssistant spec
func (r *HomeAssistantReconciler) buildCoreConfigRequest(ha *hav1alpha1.HomeAssistant) *haclient.CoreConfigRequest {
	if ha.Spec.Bootstrap == nil || ha.Spec.Bootstrap.Location == nil {
		return nil
	}

	loc := ha.Spec.Bootstrap.Location
	req := &haclient.CoreConfigRequest{
		LocationName: loc.Name,
		UnitSystem:   getOrDefault(loc.UnitSystem, "metric"),
	}

	if loc.Latitude != "" {
		if lat, err := strconv.ParseFloat(loc.Latitude, 64); err == nil {
			req.Latitude = lat
		}
	}
	if loc.Longitude != "" {
		if lon, err := strconv.ParseFloat(loc.Longitude, 64); err == nil {
			req.Longitude = lon
		}
	}
	if loc.Elevation != nil {
		req.Elevation = *loc.Elevation
	}
	if loc.Currency != "" {
		req.Currency = loc.Currency
	}

	// Use location timezone if specified, otherwise fall back to spec.timezone
	if loc.TimeZone != "" {
		req.TimeZone = loc.TimeZone
	} else if ha.Spec.Timezone != "" {
		req.TimeZone = ha.Spec.Timezone
	}

	return req
}

// validateBootstrapConfig validates bootstrap configuration
func (r *HomeAssistantReconciler) validateBootstrapConfig(ha *hav1alpha1.HomeAssistant) error {
	if ha.Spec.Bootstrap.Credentials == nil || ha.Spec.Bootstrap.Credentials.SecretRef == nil {
		return fmt.Errorf("bootstrap credentials secretRef is required when bootstrap is enabled")
	}
	if ha.Spec.Bootstrap.Credentials.SecretRef.Name == "" {
		return fmt.Errorf("bootstrap credentials secret name cannot be empty")
	}
	return nil
}

// getBootstrapCredentials retrieves username and password from the credentials Secret
func (r *HomeAssistantReconciler) getBootstrapCredentials(ctx context.Context, ha *hav1alpha1.HomeAssistant) (string, string, error) {
	secretRef := ha.Spec.Bootstrap.Credentials.SecretRef

	// Get Secret
	credentialsSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{
		Name:      secretRef.Name,
		Namespace: ha.Namespace,
	}, credentialsSecret); err != nil {
		if errors.IsNotFound(err) {
			return "", "", fmt.Errorf("credentials secret %q not found", secretRef.Name)
		}
		return "", "", err
	}

	// Get keys (with defaults)
	usernameKey := getOrDefault(secretRef.UsernameKey, defaultUsernameKey)
	passwordKey := getOrDefault(secretRef.PasswordKey, defaultPasswordKey)

	// Extract username
	usernameBytes, ok := credentialsSecret.Data[usernameKey]
	if !ok {
		return "", "", fmt.Errorf("credentials secret missing key %q", usernameKey)
	}

	// Extract password
	passwordBytes, ok := credentialsSecret.Data[passwordKey]
	if !ok {
		return "", "", fmt.Errorf("credentials secret missing key %q", passwordKey)
	}

	return string(usernameBytes), string(passwordBytes), nil
}

// buildHomeAssistantURL builds the internal service URL for Home Assistant
func (r *HomeAssistantReconciler) buildHomeAssistantURL(ha *hav1alpha1.HomeAssistant) string {
	// Use internal service name
	// Format: http://<name>.<namespace>.svc.cluster.local:<port>
	serviceName := ha.Name
	port := defaultPort
	if ha.Spec.Service != nil && ha.Spec.Service.Port > 0 {
		port = int(ha.Spec.Service.Port)
	}

	return fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", serviceName, ha.Namespace, port)
}

// handleBootstrapError handles errors from bootstrap process
func (r *HomeAssistantReconciler) handleBootstrapError(ctx context.Context, ha *hav1alpha1.HomeAssistant, err error) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Check error type
	if haclient.IsNotReady(err) {
		log.Info("Home Assistant not ready yet, will retry", "error", err.Error())
		return r.updateBootstrapStatus(ctx, ha, reasonBootstrapNotReady, "Home Assistant not ready yet", false, false)
	}

	if haclient.IsOnboardingDone(err) {
		log.Info("Onboarding already completed")
		// completed=true but tokenCreated=false since we didn't create a token
		return r.updateBootstrapStatus(ctx, ha, reasonBootstrapAlreadyDone, "Onboarding already completed", true, false)
	}

	// Other errors
	log.Error(err, "Bootstrap failed")
	return r.updateBootstrapStatus(ctx, ha, reasonBootstrapFailed, fmt.Sprintf("Bootstrap failed: %v", err), false, false)
}

// updateBootstrapStatus updates the bootstrap status and returns appropriate Result
// tokenCreated indicates whether an API token was actually created (vs onboarding already done)
func (r *HomeAssistantReconciler) updateBootstrapStatus(ctx context.Context, ha *hav1alpha1.HomeAssistant, reason, message string, completed, tokenCreated bool) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	now := metav1.Now()
	ha.Status.Bootstrap.LastAttempt = &now
	ha.Status.Bootstrap.Message = message
	ha.Status.Bootstrap.Completed = completed

	if completed && tokenCreated {
		ha.Status.Bootstrap.ApiTokenReady = true
		ha.Status.Bootstrap.ApiTokenSecretName = r.getApiTokenSecretName(ha)
	}

	// Update main status condition
	conditionStatus := metav1.ConditionFalse
	if completed {
		conditionStatus = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&ha.Status.Conditions, metav1.Condition{
		Type:               "BootstrapReady",
		Status:             conditionStatus,
		ObservedGeneration: ha.Generation,
		Reason:             reason,
		Message:            message,
	})

	if err := r.Status().Update(ctx, ha); err != nil {
		log.Error(err, "Failed to update bootstrap status")
		return ctrl.Result{}, err
	}

	// Determine requeue behavior
	if completed {
		return ctrl.Result{}, nil
	}

	// Not completed - requeue based on reason
	switch reason {
	case reasonBootstrapNotReady:
		// HA not ready - retry quickly
		return ctrl.Result{RequeueAfter: bootstrapHealthCheckRetry}, nil
	case reasonBootstrapFailed, reasonBootstrapMissingCredentials:
		// Error - retry with backoff
		return ctrl.Result{RequeueAfter: bootstrapRetryInterval}, nil
	default:
		// Default retry
		return ctrl.Result{RequeueAfter: bootstrapRetryInterval}, nil
	}
}

// createAPITokenSecret creates or updates the Secret containing the API token
func (r *HomeAssistantReconciler) createAPITokenSecret(ctx context.Context, ha *hav1alpha1.HomeAssistant, token string) error {
	log := logf.FromContext(ctx)

	secretName := r.getApiTokenSecretName(ha)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: ha.Namespace,
			Labels:    r.labelsForHomeAssistant(ha),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			apiTokenSecretKeyName: []byte(token),
		},
	}

	// Set owner reference
	if err := controllerutil.SetControllerReference(ha, secret, r.Scheme); err != nil {
		return err
	}

	// Check if Secret already exists
	existingSecret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: ha.Namespace}, existingSecret)

	if err != nil && errors.IsNotFound(err) {
		// Create new Secret
		log.Info("Creating API token Secret", "Secret.Name", secretName)
		return r.Create(ctx, secret)
	} else if err != nil {
		return err
	}

	// Update existing Secret
	log.Info("Updating API token Secret", "Secret.Name", secretName)
	existingSecret.Data = secret.Data
	return r.Update(ctx, existingSecret)
}

// getApiTokenSecretName returns the name of the Secret for the API token
func (r *HomeAssistantReconciler) getApiTokenSecretName(ha *hav1alpha1.HomeAssistant) string {
	if ha.Spec.Bootstrap != nil && ha.Spec.Bootstrap.ApiTokenSecretName != "" {
		return ha.Spec.Bootstrap.ApiTokenSecretName
	}
	return ha.Name + "-" + defaultApiTokenSecretName
}

// getOrDefault returns value if non-empty, otherwise returns defaultValue
func getOrDefault(value, defaultValue string) string {
	if value != "" {
		return value
	}
	return defaultValue
}
