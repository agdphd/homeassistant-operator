package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/przemekhys/homeassistant-operator/internal/haclient"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

// ReloadConfig configures reload behavior
type ReloadConfig struct {
	MaxRetries    int
	RetryDelay    time.Duration
	ComponentName string // "script", "automation", "scene"
}

// ReloadResult contains result of reload operation
type ReloadResult struct {
	Success         bool
	Method          string // "hot-reload", "skipped", "failed"
	Error           error
	Attempts        int
	Duration        time.Duration
	ComponentLoaded bool
	ReloadID        string
}

// HAClientInterface defines methods needed for reload operations
type HAClientInterface interface {
	IsComponentLoaded(ctx context.Context, token string, component string) (bool, error)
}

// PerformReloadWithRetry executes hot-reload with smart detection and retry
func PerformReloadWithRetry(
	ctx context.Context,
	haClient HAClientInterface,
	token string,
	config ReloadConfig,
	reloadFunc func(ctx context.Context, token string) error,
) *ReloadResult {
	log := logf.FromContext(ctx)

	result := &ReloadResult{
		ReloadID: uuid.New().String()[:8],
	}
	startTime := time.Now()
	defer func() {
		result.Duration = time.Since(startTime)
	}()

	log.Info("Starting hot-reload with smart detection",
		"reloadID", result.ReloadID,
		"component", config.ComponentName,
		"maxRetries", config.MaxRetries)

	// Step 1: Check if component loaded
	loaded, err := haClient.IsComponentLoaded(ctx, token, config.ComponentName)
	result.ComponentLoaded = loaded

	if err != nil {
		log.Error(err, "Failed to check component status",
			"reloadID", result.ReloadID,
			"component", config.ComponentName)
		result.Success = false
		result.Error = fmt.Errorf("component check failed: %w", err)
		result.Method = "failed"
		return result
	}

	if !loaded {
		log.Info("Component not loaded yet, skipping hot-reload",
			"reloadID", result.ReloadID,
			"component", config.ComponentName,
			"reason", "will retry on next reconcile")
		result.Success = false
		result.Error = fmt.Errorf("%s component not loaded in Home Assistant", config.ComponentName)
		result.Method = "skipped"
		return result
	}

	// Step 2: Component loaded, attempt hot-reload with retry
	log.Info("Component loaded, attempting hot-reload with retry",
		"reloadID", result.ReloadID,
		"component", config.ComponentName,
		"maxRetries", config.MaxRetries)

	var lastErr error
	for attempt := 1; attempt <= config.MaxRetries; attempt++ {
		result.Attempts = attempt
		attemptStart := time.Now()

		log.V(1).Info("Hot-reload attempt",
			"reloadID", result.ReloadID,
			"attempt", attempt,
			"maxAttempts", config.MaxRetries)

		err := reloadFunc(ctx, token)
		attemptDuration := time.Since(attemptStart)

		if err == nil {
			// SUCCESS
			log.Info("Hot-reload successful",
				"reloadID", result.ReloadID,
				"component", config.ComponentName,
				"attempts", attempt,
				"attemptDuration", attemptDuration,
				"totalDuration", time.Since(startTime))

			result.Success = true
			result.Method = "hot-reload"
			return result
		}

		// FAILED
		statusCode := getStatusCode(err)
		responseBody := getResponseBody(err)
		lastErr = err

		if attempt < config.MaxRetries {
			log.Info("Hot-reload attempt failed, will retry",
				"reloadID", result.ReloadID,
				"attempt", attempt,
				"statusCode", statusCode,
				"responseBody", truncateString(responseBody, 200),
				"attemptDuration", attemptDuration,
				"nextRetryIn", config.RetryDelay)

			time.Sleep(config.RetryDelay)
		} else {
			log.Error(err, "Hot-reload failed after all retries",
				"reloadID", result.ReloadID,
				"attempts", attempt,
				"statusCode", statusCode,
				"responseBody", responseBody,
				"totalDuration", time.Since(startTime))
		}
	}

	// All retries exhausted
	result.Success = false
	result.Error = fmt.Errorf("hot-reload failed after %d attempts: %w", config.MaxRetries, lastErr)
	result.Method = "failed"
	return result
}

// getStatusCode extracts HTTP status code from haclient.Error
func getStatusCode(err error) int {
	if haErr, ok := err.(*haclient.Error); ok {
		return haErr.StatusCode
	}
	return 0
}

// getResponseBody extracts response body/message from haclient.Error
func getResponseBody(err error) string {
	if haErr, ok := err.(*haclient.Error); ok {
		return haErr.Message
	}
	return err.Error()
}

// truncateString truncates string for logging
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
