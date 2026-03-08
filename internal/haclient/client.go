package haclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultTimeout = 30 * time.Second
	userAgent      = "homeassistant-operator/1.0"
)

// Client is a client for Home Assistant API
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new Home Assistant API client
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: defaultTimeout,
			Transport: &http.Transport{
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 20 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
	}
}

// WithTimeout sets a custom timeout for the HTTP client
func (c *Client) WithTimeout(timeout time.Duration) *Client {
	c.httpClient.Timeout = timeout
	return c
}

// CheckHealth checks if Home Assistant is responding
// Returns nil if healthy, ErrorTypeNotReady if not ready
// Note: 401 Unauthorized is considered healthy (HA is running but needs onboarding)
func (c *Client) CheckHealth(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/", nil)
	if err != nil {
		return &Error{Type: ErrorTypeHTTP, Message: "failed to create request", Err: err}
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &Error{Type: ErrorTypeNotReady, Message: "Home Assistant not responding", Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	// 200 OK = healthy with auth, 401 Unauthorized = healthy but needs onboarding
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized {
		return nil
	}

	return &Error{
		Type:       ErrorTypeNotReady,
		Message:    "Home Assistant not ready",
		StatusCode: resp.StatusCode,
	}
}

// CheckOnboardingStatus checks if onboarding is needed
// Returns nil if onboarding needed, ErrorTypeOnboardingDone if already done
func (c *Client) CheckOnboardingStatus(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/onboarding", nil)
	if err != nil {
		return &Error{Type: ErrorTypeHTTP, Message: "failed to create request", Err: err}
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return &Error{Type: ErrorTypeHTTP, Message: "failed to check onboarding", Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return &Error{
			Type:       ErrorTypeHTTP,
			Message:    "unexpected status code",
			StatusCode: resp.StatusCode,
		}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return &Error{Type: ErrorTypeHTTP, Message: "failed to read response", Err: err}
	}

	// Check if response is array (onboarding needed) or object (done)
	trimmed := strings.TrimSpace(string(body))
	if strings.HasPrefix(trimmed, "[") {
		// Onboarding needed
		return nil
	}

	// Check if user step is done
	var status map[string]interface{}
	if err := json.Unmarshal(body, &status); err != nil {
		return &Error{Type: ErrorTypeInvalidResponse, Message: "failed to parse response", Err: err}
	}

	// Check if user step exists and is done
	if userStep, ok := status["user"].(map[string]interface{}); ok {
		if done, ok := userStep["done"].(bool); ok && done {
			return &Error{Type: ErrorTypeOnboardingDone, Message: "onboarding already completed"}
		}
	}

	// Onboarding needed
	return nil
}

// CreateUser creates initial admin user during onboarding
func (c *Client) CreateUser(ctx context.Context, req *CreateUserRequest) (*CreateUserResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, &Error{Type: ErrorTypeHTTP, Message: "failed to marshal request", Err: err}
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/onboarding/users", bytes.NewReader(body))
	if err != nil {
		return nil, &Error{Type: ErrorTypeHTTP, Message: "failed to create request", Err: err}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// Connection errors (EOF, connection refused, etc.) indicate HA not ready yet
		// This allows the controller to retry with appropriate backoff
		return nil, &Error{Type: ErrorTypeNotReady, Message: "failed to create user: Home Assistant not ready", Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		bodyStr := string(bodyBytes)

		// Check if user already exists - treat as idempotent success
		if strings.Contains(bodyStr, "User step already done") {
			return nil, &Error{
				Type:       ErrorTypeOnboardingDone,
				Message:    "user already created",
				StatusCode: resp.StatusCode,
			}
		}

		return nil, &Error{
			Type:       ErrorTypeHTTP,
			Message:    fmt.Sprintf("failed to create user: %s", bodyStr),
			StatusCode: resp.StatusCode,
		}
	}

	var createResp CreateUserResponse
	if err := json.NewDecoder(resp.Body).Decode(&createResp); err != nil {
		return nil, &Error{Type: ErrorTypeInvalidResponse, Message: "failed to decode response", Err: err}
	}

	if createResp.AuthCode == "" {
		return nil, &Error{Type: ErrorTypeInvalidResponse, Message: "auth_code not found in response"}
	}

	return &createResp, nil
}

// ExchangeAuthCode exchanges authorization code for access token
func (c *Client) ExchangeAuthCode(ctx context.Context, authCode, clientID string) (*TokenResponse, error) {
	formData := url.Values{}
	formData.Set("grant_type", "authorization_code")
	formData.Set("code", authCode)
	formData.Set("client_id", clientID)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/auth/token", strings.NewReader(formData.Encode()))
	if err != nil {
		return nil, &Error{Type: ErrorTypeHTTP, Message: "failed to create request", Err: err}
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// Connection errors indicate HA not responding - use NotReady type for retry
		return nil, &Error{
			Type:    ErrorTypeNotReady,
			Message: "failed to exchange auth code: Home Assistant not responding",
			Err:     err,
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, &Error{
			Type:       ErrorTypeAuth,
			Message:    fmt.Sprintf("failed to exchange auth code: %s", string(bodyBytes)),
			StatusCode: resp.StatusCode,
		}
	}

	var tokenResp TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, &Error{Type: ErrorTypeInvalidResponse, Message: "failed to decode token response", Err: err}
	}

	if tokenResp.AccessToken == "" {
		return nil, &Error{Type: ErrorTypeInvalidResponse, Message: "access_token not found in response"}
	}

	return &tokenResp, nil
}

// CreateLongLivedToken creates a long-lived access token via WebSocket API
// Home Assistant requires WebSocket for creating long-lived tokens
func (c *Client) CreateLongLivedToken(
	ctx context.Context,
	accessToken string,
	req *LongLivedTokenRequest,
) (*LongLivedTokenResponse, error) {
	// Convert HTTP URL to WebSocket URL
	wsURL := strings.Replace(c.baseURL, "http://", "ws://", 1)
	wsURL = strings.Replace(wsURL, "https://", "wss://", 1)
	wsURL += "/api/websocket"

	// Connect to WebSocket
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, &Error{Type: ErrorTypeHTTP, Message: "failed to connect to websocket", Err: err}
	}
	defer func() { _ = conn.Close() }()

	// Message ID counter
	var msgID atomic.Int64

	// Read auth_required message
	var authRequired map[string]interface{}
	if err := conn.ReadJSON(&authRequired); err != nil {
		return nil, &Error{Type: ErrorTypeHTTP, Message: "failed to read auth_required", Err: err}
	}

	if authRequired["type"] != "auth_required" {
		return nil, &Error{Type: ErrorTypeHTTP, Message: fmt.Sprintf("unexpected message type: %v", authRequired["type"])}
	}

	// Send auth message with access token
	authMsg := map[string]interface{}{
		"type":         "auth",
		"access_token": accessToken,
	}
	if err := conn.WriteJSON(authMsg); err != nil {
		return nil, &Error{Type: ErrorTypeAuth, Message: "failed to send auth message", Err: err}
	}

	// Read auth result
	var authResult map[string]interface{}
	if err := conn.ReadJSON(&authResult); err != nil {
		return nil, &Error{Type: ErrorTypeAuth, Message: "failed to read auth result", Err: err}
	}

	if authResult["type"] != "auth_ok" {
		return nil, &Error{Type: ErrorTypeAuth, Message: fmt.Sprintf("auth failed: %v", authResult["message"])}
	}

	// Send long_lived_access_token request
	id := msgID.Add(1)
	tokenReq := map[string]interface{}{
		"id":          id,
		"type":        "auth/long_lived_access_token",
		"client_name": req.ClientName,
		"lifespan":    req.Lifespan,
	}
	if err := conn.WriteJSON(tokenReq); err != nil {
		return nil, &Error{Type: ErrorTypeHTTP, Message: "failed to send token request", Err: err}
	}

	// Read token response
	var tokenResult map[string]interface{}
	if err := conn.ReadJSON(&tokenResult); err != nil {
		return nil, &Error{Type: ErrorTypeHTTP, Message: "failed to read token response", Err: err}
	}

	if tokenResult["success"] != true {
		errMsg := "unknown error"
		if e, ok := tokenResult["error"].(map[string]interface{}); ok {
			if msg, ok := e["message"].(string); ok {
				errMsg = msg
			}
		}
		return nil, &Error{Type: ErrorTypeHTTP, Message: fmt.Sprintf("failed to create long-lived token: %s", errMsg)}
	}

	token, ok := tokenResult["result"].(string)
	if !ok || token == "" {
		return nil, &Error{Type: ErrorTypeInvalidResponse, Message: "empty token received"}
	}

	return &LongLivedTokenResponse{Token: token}, nil
}

// SetCoreConfig configures location and core settings during onboarding
func (c *Client) SetCoreConfig(ctx context.Context, accessToken string, req *CoreConfigRequest) error {
	body, err := json.Marshal(req)
	if err != nil {
		return &Error{Type: ErrorTypeHTTP, Message: "failed to marshal request", Err: err}
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/onboarding/core_config", bytes.NewReader(body))
	if err != nil {
		return &Error{Type: ErrorTypeHTTP, Message: "failed to create request", Err: err}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", userAgent)
	httpReq.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return &Error{Type: ErrorTypeHTTP, Message: "failed to set core config", Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return &Error{
			Type:       ErrorTypeHTTP,
			Message:    fmt.Sprintf("failed to set core config (HTTP %d): %s", resp.StatusCode, string(bodyBytes)),
			StatusCode: resp.StatusCode,
		}
	}

	return nil
}

// SetAnalytics configures analytics preferences during onboarding
func (c *Client) SetAnalytics(ctx context.Context, accessToken string, enabled bool) error {
	var req AnalyticsRequest
	if enabled {
		req.Preferences = &AnalyticsPreferences{
			Base:        true,
			Diagnostics: true,
			Usage:       true,
			Statistics:  true,
		}
	}
	// If disabled, send empty preferences (or nil)

	body, err := json.Marshal(req)
	if err != nil {
		return &Error{Type: ErrorTypeHTTP, Message: "failed to marshal request", Err: err}
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/api/onboarding/analytics", bytes.NewReader(body))
	if err != nil {
		return &Error{Type: ErrorTypeHTTP, Message: "failed to create request", Err: err}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", userAgent)
	httpReq.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return &Error{Type: ErrorTypeHTTP, Message: "failed to set analytics", Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return &Error{
			Type:       ErrorTypeHTTP,
			Message:    fmt.Sprintf("failed to set analytics (HTTP %d): %s", resp.StatusCode, string(bodyBytes)),
			StatusCode: resp.StatusCode,
		}
	}

	return nil
}

// BootstrapOptions contains optional configuration for bootstrap
type BootstrapOptions struct {
	CreateLongLivedToken bool
	CoreConfig           *CoreConfigRequest
	EnableAnalytics      bool
}

// PerformBootstrap performs complete bootstrap flow:
// 1. Check health
// 2. Check onboarding status
// 3. Create user
// 4. Set core config (location) if provided
// 5. Set analytics preferences
// 6. Exchange auth code for token
// 7. Create long-lived token (if requested)
// Returns the long-lived token or empty string if not created
func (c *Client) PerformBootstrap(
	ctx context.Context,
	username, password, ownerName, language string,
	opts *BootstrapOptions,
) (string, error) {
	if opts == nil {
		opts = &BootstrapOptions{}
	}

	// 1. Check health
	if err := c.CheckHealth(ctx); err != nil {
		return "", err
	}

	// 2. Check onboarding status
	if err := c.CheckOnboardingStatus(ctx); err != nil {
		if IsOnboardingDone(err) {
			// Already done - this is not an error
			return "", &Error{Type: ErrorTypeOnboardingDone, Message: "onboarding already completed"}
		}
		return "", err
	}

	// 3. Create user
	clientID := c.baseURL + "/"
	userResp, err := c.CreateUser(ctx, &CreateUserRequest{
		ClientID: clientID,
		Name:     ownerName,
		Username: username,
		Password: password,
		Language: language,
	})
	if err != nil {
		return "", err
	}

	// 4. Exchange auth code (needed for subsequent steps)
	tokenResp, err := c.ExchangeAuthCode(ctx, userResp.AuthCode, clientID)
	if err != nil {
		return "", err
	}

	// 5. Set core config (location) if provided - requires auth token
	if opts.CoreConfig != nil {
		// Ignore errors - core config is optional and failure doesn't block bootstrap
		_ = c.SetCoreConfig(ctx, tokenResp.AccessToken, opts.CoreConfig)
	}

	// 6. Set analytics preferences - requires auth token
	// Ignore errors - analytics config is optional and failure doesn't block bootstrap
	_ = c.SetAnalytics(ctx, tokenResp.AccessToken, opts.EnableAnalytics)

	// 7. Create long-lived token if requested
	if !opts.CreateLongLivedToken {
		return "", nil
	}

	longLivedResp, err := c.CreateLongLivedToken(ctx, tokenResp.AccessToken, &LongLivedTokenRequest{
		ClientName: "kubernetes-operator",
		Lifespan:   3650, // 10 years
	})
	if err != nil {
		return "", err
	}

	return longLivedResp.Token, nil
}

// CheckConfig validates the current Home Assistant configuration
// Returns nil if config is valid, error if invalid
// Requires authenticated API call with long-lived token
func (c *Client) CheckConfig(ctx context.Context, token string) error {
	httpReq, err := http.NewRequestWithContext(
		ctx,
		"POST",
		c.baseURL+"/api/services/homeassistant/check_config",
		bytes.NewReader([]byte("{}")),
	)
	if err != nil {
		return &Error{Type: ErrorTypeHTTP, Message: "failed to create request", Err: err}
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return &Error{Type: ErrorTypeHTTP, Message: "failed to check config", Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return &Error{
			Type:       ErrorTypeHTTP,
			Message:    fmt.Sprintf("config check failed: %s", string(bodyBytes)),
			StatusCode: resp.StatusCode,
		}
	}

	// Parse response - Home Assistant can return either array or object
	var rawResponse json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&rawResponse); err != nil {
		return &Error{Type: ErrorTypeInvalidResponse, Message: "failed to decode response", Err: err}
	}

	// Try to parse as object first (expected format for errors)
	var resultObj map[string]interface{}
	if err := json.Unmarshal(rawResponse, &resultObj); err == nil {
		// It's an object - check for various error formats

		// Check for "errors" field (array)
		if errors, ok := resultObj["errors"].([]interface{}); ok && len(errors) > 0 {
			return &Error{
				Type:    ErrorTypeHTTP,
				Message: fmt.Sprintf("config validation errors: %v", errors),
			}
		}

		// Check for "errors" field (string)
		if errorStr, ok := resultObj["errors"].(string); ok && errorStr != "" {
			return &Error{
				Type:    ErrorTypeHTTP,
				Message: fmt.Sprintf("config validation error: %s", errorStr),
			}
		}

		// Check for "error" field (string)
		if errorStr, ok := resultObj["error"].(string); ok && errorStr != "" {
			return &Error{
				Type:    ErrorTypeHTTP,
				Message: fmt.Sprintf("config validation error: %s", errorStr),
			}
		}

		// Check for "message" field (string)
		if message, ok := resultObj["message"].(string); ok && message != "" {
			return &Error{
				Type:    ErrorTypeHTTP,
				Message: fmt.Sprintf("config validation message: %s", message),
			}
		}

		return nil
	}

	// Try to parse as array (valid response from service call)
	var resultArray []interface{}
	if err := json.Unmarshal(rawResponse, &resultArray); err == nil {
		// It's an array - treat as success
		return nil
	}

	// If neither worked, return error
	return &Error{
		Type:    ErrorTypeInvalidResponse,
		Message: "unexpected response format (neither array nor object)",
	}
}

// postServiceWithToken sends a POST request to a Home Assistant service
// endpoint with token authentication
func (c *Client) postServiceWithToken(
	ctx context.Context,
	endpoint string,
	token string,
	errorMsg string,
) error {
	httpReq, err := http.NewRequestWithContext(
		ctx,
		"POST",
		c.baseURL+endpoint,
		bytes.NewReader([]byte("{}")),
	)
	if err != nil {
		return &Error{
			Type:    ErrorTypeHTTP,
			Message: "failed to create request",
			Err:     err,
		}
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return &Error{
			Type:    ErrorTypeHTTP,
			Message: errorMsg,
			Err:     err,
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return &Error{
			Type:       ErrorTypeHTTP,
			Message:    fmt.Sprintf("%s: %s", errorMsg, string(bodyBytes)),
			StatusCode: resp.StatusCode,
		}
	}

	return nil
}

// ReloadCoreConfig triggers a hot-reload of Home Assistant core
// configuration. Returns nil if reload successful, error if failed.
// Requires authenticated API call with long-lived token.
func (c *Client) ReloadCoreConfig(
	ctx context.Context,
	token string,
) error {
	return c.postServiceWithToken(
		ctx,
		"/api/services/homeassistant/reload_core_config",
		token,
		"failed to reload config",
	)
}

// ReloadAutomations triggers a hot-reload of Home Assistant automations.
// Returns nil if reload successful, error if failed.
// Requires authenticated API call with long-lived token.
func (c *Client) ReloadAutomations(
	ctx context.Context,
	token string,
) error {
	return c.postServiceWithToken(
		ctx,
		"/api/services/automation/reload",
		token,
		"failed to reload automations",
	)
}

// ReloadScenes triggers a hot-reload of Home Assistant scenes.
// Returns nil if reload successful, error if failed.
// Requires authenticated API call with long-lived token.
func (c *Client) ReloadScenes(
	ctx context.Context,
	token string,
) error {
	return c.postServiceWithToken(
		ctx,
		"/api/services/scene/reload",
		token,
		"failed to reload scenes",
	)
}

// ReloadScripts triggers a hot-reload of Home Assistant scripts.
// Returns nil if reload successful, error if failed.
// Requires authenticated API call with long-lived token.
func (c *Client) ReloadScripts(
	ctx context.Context,
	token string,
) error {
	return c.postServiceWithToken(
		ctx,
		"/api/services/script/reload",
		token,
		"failed to reload scripts",
	)
}

// GetConfig returns Home Assistant configuration including loaded components.
// This is useful for checking if specific integrations are loaded.
// Requires authenticated API call with long-lived token.
func (c *Client) GetConfig(ctx context.Context, token string) (*ConfigResponse, error) {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/api/config", nil)
	if err != nil {
		return nil, &Error{
			Type:    ErrorTypeHTTP,
			Message: "failed to create config request",
			Err:     err,
		}
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, &Error{
			Type:    ErrorTypeNotReady,
			Message: "failed to get config",
			Err:     err,
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, &Error{
			Type:       ErrorTypeHTTP,
			Message:    fmt.Sprintf("failed to get config: %s", string(bodyBytes)),
			StatusCode: resp.StatusCode,
		}
	}

	var config ConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, &Error{
			Type:    ErrorTypeInvalidResponse,
			Message: "failed to decode config response",
			Err:     err,
		}
	}

	return &config, nil
}

// putConfig sends a PUT request to a HA config endpoint with JSON body.
// Used for creating/updating automation, scene and script configs via REST API.
func (c *Client) putConfig(ctx context.Context, token, path string, data map[string]interface{}) error {
	body, err := json.Marshal(data)
	if err != nil {
		return &Error{Type: ErrorTypeHTTP, Message: "failed to marshal request", Err: err}
	}

	httpReq, err := http.NewRequestWithContext(ctx, "PUT", c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return &Error{Type: ErrorTypeHTTP, Message: "failed to create request", Err: err}
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return &Error{Type: ErrorTypeNotReady, Message: fmt.Sprintf("failed to PUT %s", path), Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return &Error{
			Type:       ErrorTypeHTTP,
			Message:    fmt.Sprintf("PUT %s failed: %s", path, string(bodyBytes)),
			StatusCode: resp.StatusCode,
		}
	}

	return nil
}

// deleteConfig sends a DELETE request to a HA config endpoint.
// 404 is treated as success (idempotent delete).
func (c *Client) deleteConfig(ctx context.Context, token, path string) error {
	httpReq, err := http.NewRequestWithContext(ctx, "DELETE", c.baseURL+path, nil)
	if err != nil {
		return &Error{Type: ErrorTypeHTTP, Message: "failed to create request", Err: err}
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("User-Agent", userAgent)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return &Error{Type: ErrorTypeNotReady, Message: fmt.Sprintf("failed to DELETE %s", path), Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	// 404 = already gone, treat as success (idempotent)
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return &Error{
			Type:       ErrorTypeHTTP,
			Message:    fmt.Sprintf("DELETE %s failed: %s", path, string(bodyBytes)),
			StatusCode: resp.StatusCode,
		}
	}

	return nil
}

// PutAutomation creates or updates an automation via HA REST API.
// HA writes the result to automations.yaml on the PVC (writable).
// Idempotent: safe to call on every reconcile.
func (c *Client) PutAutomation(ctx context.Context, token, id string, data map[string]interface{}) error {
	return c.putConfig(ctx, token, "/api/config/automation/config/"+id, data)
}

// DeleteAutomation removes an automation via HA REST API.
// Idempotent: returns nil if automation does not exist (404).
func (c *Client) DeleteAutomation(ctx context.Context, token, id string) error {
	return c.deleteConfig(ctx, token, "/api/config/automation/config/"+id)
}

// PutScene creates or updates a scene via HA REST API.
// Idempotent: safe to call on every reconcile.
func (c *Client) PutScene(ctx context.Context, token, id string, data map[string]interface{}) error {
	return c.putConfig(ctx, token, "/api/config/scene/config/"+id, data)
}

// DeleteScene removes a scene via HA REST API.
// Idempotent: returns nil if scene does not exist (404).
func (c *Client) DeleteScene(ctx context.Context, token, id string) error {
	return c.deleteConfig(ctx, token, "/api/config/scene/config/"+id)
}

// PutScript creates or updates a script via HA REST API.
// Idempotent: safe to call on every reconcile.
func (c *Client) PutScript(ctx context.Context, token, id string, data map[string]interface{}) error {
	return c.putConfig(ctx, token, "/api/config/script/config/"+id, data)
}

// DeleteScript removes a script via HA REST API.
// Idempotent: returns nil if script does not exist (404).
func (c *Client) DeleteScript(ctx context.Context, token, id string) error {
	return c.deleteConfig(ctx, token, "/api/config/script/config/"+id)
}

// IsComponentLoaded checks if a specific component/integration is loaded in Home Assistant.
// Common components: "automation", "script", "scene", "homeassistant", "http", "mqtt", etc.
// Returns true if component is loaded, false otherwise.
// Requires authenticated API call with long-lived token.
func (c *Client) IsComponentLoaded(ctx context.Context, token, component string) (bool, error) {
	config, err := c.GetConfig(ctx, token)
	if err != nil {
		return false, err
	}

	for _, comp := range config.Components {
		if comp == component {
			return true, nil
		}
	}
	return false, nil
}
