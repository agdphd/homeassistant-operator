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

// reconcileBootstrap handles the automatic onboarding and API token
// creation. This is a complete rewrite from Job-based to native Go
// implementation.
func (r *HomeAssistantReconciler) reconcileBootstrap(
	ctx context.Context,
	ha *hav1alpha1.HomeAssistant,
) (ctrl.Result, error) {
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
		return r.updateBootstrapStatus(
			ctx, ha,
			reasonBootstrapMissingCredentials,
			err.Error(), false, false,
		)
	}

	// Get credentials from Secret
	username, password, err := r.getBootstrapCredentials(ctx, ha)
	if err != nil {
		log.Error(err, "Failed to get bootstrap credentials")
		return r.updateBootstrapStatus(
			ctx, ha, reasonBootstrapFailed,
			fmt.Sprintf("Failed to get credentials: %v", err),
			false, false,
		)
	}

	// Build Home Assistant URL
	haURL := r.buildHomeAssistantURL(ha)

	// Create HA client
	client := haclient.NewClient(haURL).WithTimeout(30 * time.Second)

	// Health check - ensure HA is responding before attempting bootstrap
	log.Info("Performing health check before bootstrap", "url", haURL)
	if err := client.CheckHealth(ctx); err != nil {
		if haclient.IsNotReady(err) {
			log.Info("Home Assistant not ready for bootstrap",
				"error", err.Error())
			return r.updateBootstrapStatus(
				ctx, ha, reasonBootstrapNotReady,
				"Health check failed - not ready yet",
				false, false,
			)
		}
		log.Error(err, "Bootstrap health check failed")
		return r.updateBootstrapStatus(
			ctx, ha, reasonBootstrapFailed,
			fmt.Sprintf("Health check failed: %v", err),
			false, false,
		)
	}
	log.Info("Health check passed, proceeding with bootstrap")

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
			return r.updateBootstrapStatus(
				ctx, ha, reasonBootstrapFailed,
				fmt.Sprintf(
					"Failed to create token Secret: %v", err,
				), false, false,
			)
		}
		tokenCreated = true
	}

	// Mark bootstrap as completed
	return r.updateBootstrapStatus(
		ctx, ha, reasonBootstrapCompleted,
		"Bootstrap completed successfully", true, tokenCreated,
	)
}

// buildCoreConfigRequest builds CoreConfigRequest from HomeAssistant spec
func (r *HomeAssistantReconciler) buildCoreConfigRequest(
	ha *hav1alpha1.HomeAssistant,
) *haclient.CoreConfigRequest {
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
func (r *HomeAssistantReconciler) validateBootstrapConfig(
	ha *hav1alpha1.HomeAssistant,
) error {
	if ha.Spec.Bootstrap.Credentials == nil ||
		ha.Spec.Bootstrap.Credentials.SecretRef == nil {
		return fmt.Errorf(
			"bootstrap credentials secretRef required when enabled",
		)
	}
	if ha.Spec.Bootstrap.Credentials.SecretRef.Name == "" {
		return fmt.Errorf(
			"bootstrap credentials secret name cannot be empty",
		)
	}
	return nil
}

// getBootstrapCredentials retrieves username and password from
// the credentials Secret
func (r *HomeAssistantReconciler) getBootstrapCredentials(
	ctx context.Context,
	ha *hav1alpha1.HomeAssistant,
) (string, string, error) {
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

	return fmt.Sprintf(
		"http://%s.%s.svc.cluster.local:%d",
		serviceName, ha.Namespace, port,
	)
}

// handleBootstrapError handles errors from bootstrap process
func (r *HomeAssistantReconciler) handleBootstrapError(
	ctx context.Context,
	ha *hav1alpha1.HomeAssistant,
	err error,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Check error type
	if haclient.IsNotReady(err) {
		log.Info("Home Assistant not ready yet", "error",
			err.Error())
		return r.updateBootstrapStatus(
			ctx, ha, reasonBootstrapNotReady,
			"Home Assistant not ready yet", false, false,
		)
	}

	if haclient.IsOnboardingDone(err) {
		log.Info("Onboarding already completed, " +
			"attempting token creation")
		return r.handleOnboardingAlreadyDone(ctx, ha)
	}

	// Other errors
	log.Error(err, "Bootstrap failed")
	return r.updateBootstrapStatus(
		ctx, ha, reasonBootstrapFailed,
		fmt.Sprintf("Bootstrap failed: %v", err), false, false,
	)
}

// handleOnboardingAlreadyDone handles the case where onboarding was
// already completed (e.g., manually or by a previous run). It
// attempts to create an API token Secret if requested, even though
// onboarding is done.
func (r *HomeAssistantReconciler) handleOnboardingAlreadyDone(
	ctx context.Context,
	ha *hav1alpha1.HomeAssistant,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Check if API token creation was requested
	if !ha.Spec.Bootstrap.CreateApiToken {
		log.Info("Onboarding already completed, no API token requested")
		return r.updateBootstrapStatus(
			ctx, ha, reasonBootstrapAlreadyDone,
			"Onboarding already completed", true, false,
		)
	}

	// Check if Secret already exists
	secretName := r.getApiTokenSecretName(ha)
	existingSecret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{
		Name:      secretName,
		Namespace: ha.Namespace,
	}, existingSecret)
	if err == nil {
		// Secret already exists
		log.Info("API token Secret already exists",
			"Secret.Name", secretName)
		return r.updateBootstrapStatus(
			ctx, ha, reasonBootstrapAlreadyDone,
			"Onboarding completed, token exists",
			true, true,
		)
	}
	if !errors.IsNotFound(err) {
		// Error other than NotFound
		log.Error(err, "Failed to check for existing API Secret")
		return r.updateBootstrapStatus(
			ctx, ha, reasonBootstrapFailed,
			fmt.Sprintf(
				"Failed to check API Secret: %v", err,
			), false, false,
		)
	}

	// Secret doesn't exist - onboarding was completed manually before bootstrap ran
	// Since onboarding is done, we can't use CreateUser (it would fail).
	// Solution: Delete the HA pod ONCE to force fresh start and allow bootstrap to run properly
	log.Info("Onboarding already completed but no API token Secret exists. " +
		"This typically happens when HA was manually configured before bootstrap ran.")

	// Check if we already tried deleting the pod (prevent infinite loop)
	// We use pod creation time as a proxy: if pod is very new (< 5 min), we probably just deleted it
	podName := ha.Name + "-0"
	pod := &corev1.Pod{}
	podKey := types.NamespacedName{Name: podName, Namespace: ha.Namespace}
	if err := r.Get(ctx, podKey, pod); err != nil {
		if !errors.IsNotFound(err) {
			log.Error(err, "Failed to get pod for retry check")
			return r.updateBootstrapStatus(ctx, ha, reasonBootstrapFailed,
				fmt.Sprintf("Failed to check pod: %v", err),
				false, false)
		}
		// Pod doesn't exist - wait for StatefulSet to recreate it
		log.Info("Pod not found, waiting for recreation")
		return r.updateBootstrapStatus(ctx, ha, reasonBootstrapInProgress,
			"Waiting for pod recreation",
			false, false)
	}

	// Check if pod was created recently (within last 5 minutes) - if so, we already tried deletion
	podAge := time.Since(pod.CreationTimestamp.Time)
	if podAge < 5*time.Minute {
		// Pod is fresh, we probably already tried deletion - give up to prevent loop
		log.Info("Pod was recently created, not retrying deletion to prevent loop",
			"podAge", podAge.String())
		return r.updateBootstrapStatus(ctx, ha, reasonBootstrapFailed,
			"Onboarding completed manually, API token requested but cannot be created. "+
				"Options: (1) disable createApiToken, (2) create Secret manually, or (3) delete PVC to start fresh.",
			false, false)
	}

	// Pod is old enough - safe to delete for retry
	log.Info("Deleting pod to force fresh start and retry bootstrap", "pod", podName)
	if err := r.Delete(ctx, pod); err != nil {
		log.Error(err, "Failed to delete pod for fresh bootstrap")
		return r.updateBootstrapStatus(ctx, ha, reasonBootstrapFailed,
			fmt.Sprintf("Failed to delete pod: %v", err),
			false, false)
	}
	log.Info("Deleted pod successfully", "pod", podName)

	return r.updateBootstrapStatus(ctx, ha, reasonBootstrapInProgress,
		"Deleted pod to force fresh start - bootstrap will retry when pod recreates",
		false, false)
}

// updateBootstrapStatus updates the bootstrap status and returns appropriate
// Result. tokenCreated indicates whether an API token was actually created
// (vs onboarding already done).
func (r *HomeAssistantReconciler) updateBootstrapStatus(
	ctx context.Context,
	ha *hav1alpha1.HomeAssistant,
	reason, message string,
	completed, tokenCreated bool,
) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// Retry with exponential backoff to handle optimistic locking conflicts
	// This prevents race conditions when multiple controllers try to update status simultaneously
	const maxRetries = 3
	backoff := time.Millisecond * 100

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// Refresh HomeAssistant from API server to get latest resourceVersion
		freshHA := &hav1alpha1.HomeAssistant{}
		if err := r.Get(ctx, types.NamespacedName{Name: ha.Name, Namespace: ha.Namespace}, freshHA); err != nil {
			return ctrl.Result{}, err
		}

		// Initialize bootstrap status if needed
		if freshHA.Status.Bootstrap == nil {
			freshHA.Status.Bootstrap = &hav1alpha1.BootstrapStatus{}
		}

		// Apply desired status updates to fresh object
		now := metav1.Now()
		freshHA.Status.Bootstrap.LastAttempt = &now
		freshHA.Status.Bootstrap.Message = message
		freshHA.Status.Bootstrap.Completed = completed

		if completed && tokenCreated {
			freshHA.Status.Bootstrap.ApiTokenReady = true
			freshHA.Status.Bootstrap.ApiTokenSecretName =
				r.getApiTokenSecretName(freshHA)
		}

		// Update main status condition
		conditionStatus := metav1.ConditionFalse
		if completed {
			conditionStatus = metav1.ConditionTrue
		}
		meta.SetStatusCondition(&freshHA.Status.Conditions, metav1.Condition{
			Type:               "BootstrapReady",
			Status:             conditionStatus,
			ObservedGeneration: freshHA.Generation,
			Reason:             reason,
			Message:            message,
		})

		// Attempt status update
		if err := r.Status().Update(ctx, freshHA); err != nil {
			if errors.IsConflict(err) && attempt < maxRetries {
				// Optimistic locking conflict - retry with backoff
				log.Info("Bootstrap status update conflict, retrying",
					"attempt", attempt,
					"backoff", backoff)
				time.Sleep(backoff)
				backoff *= 2 // Exponential backoff
				continue
			}
			// Non-conflict error or max retries exceeded
			log.Error(err, "Failed to update bootstrap status")
			return ctrl.Result{}, err
		}

		// Success
		log.Info("Bootstrap status updated successfully", "attempt", attempt)
		break
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

// createAPITokenSecret creates or updates the Secret containing the
// API token
func (r *HomeAssistantReconciler) createAPITokenSecret(
	ctx context.Context,
	ha *hav1alpha1.HomeAssistant,
	token string,
) error {
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
