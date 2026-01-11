package haclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const (
	testAPIPath                  = "/api/"
	testOnboardingPath           = "/api/onboarding"
	testOnboardingUsersPath      = "/api/onboarding/users"
	testOnboardingCoreConfigPath = "/api/onboarding/core_config"
	testOnboardingAnalyticsPath  = "/api/onboarding/analytics"
	testAuthTokenPath            = "/auth/token"
	testLongLivedTokenPath       = "/api/auth/long_lived_access_token"
	testWebsocketPath            = "/api/websocket"
	testMethodPOST               = "POST"
)

// websocketUpgrader is used for WebSocket tests
var websocketUpgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// handleWebsocketForToken handles WebSocket connection for long-lived token creation in tests
func handleWebsocketForToken(t *testing.T, w http.ResponseWriter, r *http.Request, token string) {
	conn, err := websocketUpgrader.Upgrade(w, r, nil)
	if err != nil {
		t.Errorf("failed to upgrade websocket: %v", err)
		return
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Logf("failed to close websocket connection: %v", err)
		}
	}()

	// Send auth_required
	if err := conn.WriteJSON(map[string]interface{}{"type": "auth_required", "ha_version": "2024.1.0"}); err != nil {
		t.Errorf("failed to write auth_required: %v", err)
		return
	}

	// Read auth message
	var authMsg map[string]interface{}
	if err := conn.ReadJSON(&authMsg); err != nil {
		t.Errorf("failed to read auth message: %v", err)
		return
	}

	// Send auth_ok
	if err := conn.WriteJSON(map[string]interface{}{"type": "auth_ok", "ha_version": "2024.1.0"}); err != nil {
		t.Errorf("failed to write auth_ok: %v", err)
		return
	}

	// Read token request
	var tokenReq map[string]interface{}
	if err := conn.ReadJSON(&tokenReq); err != nil {
		t.Errorf("failed to read token request: %v", err)
		return
	}

	// Send token response
	if err := conn.WriteJSON(map[string]interface{}{
		"id":      tokenReq["id"],
		"type":    "result",
		"success": true,
		"result":  token,
	}); err != nil {
		t.Errorf("failed to write token response: %v", err)
		return
	}
}

func TestCheckHealth(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    bool
		errType    ErrorType
	}{
		{
			name:       "healthy",
			statusCode: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "not ready - 503",
			statusCode: http.StatusServiceUnavailable,
			wantErr:    true,
			errType:    ErrorTypeNotReady,
		},
		{
			name:       "not ready - 404",
			statusCode: http.StatusNotFound,
			wantErr:    true,
			errType:    ErrorTypeNotReady,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != testAPIPath {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				w.WriteHeader(tt.statusCode)
			}))
			defer server.Close()

			client := NewClient(server.URL)
			ctx := context.Background()

			err := client.CheckHealth(ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckHealth() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr {
				if haErr, ok := err.(*Error); ok {
					if haErr.Type != tt.errType {
						t.Errorf("CheckHealth() errorType = %v, want %v", haErr.Type, tt.errType)
					}
				} else {
					t.Errorf("CheckHealth() error is not *haclient.Error")
				}
			}
		})
	}
}

func TestCheckOnboardingStatus(t *testing.T) {
	tests := []struct {
		name     string
		response string
		wantErr  bool
		errType  ErrorType
	}{
		{
			name:     "onboarding needed - array response",
			response: `[{"step":"user","done":false}]`,
			wantErr:  false,
		},
		{
			name:     "onboarding done",
			response: `{"user":{"step":"user","done":true}}`,
			wantErr:  true,
			errType:  ErrorTypeOnboardingDone,
		},
		{
			name:     "onboarding in progress",
			response: `{"user":{"step":"user","done":false}}`,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != testOnboardingPath {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tt.response))
			}))
			defer server.Close()

			client := NewClient(server.URL)
			ctx := context.Background()

			err := client.CheckOnboardingStatus(ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckOnboardingStatus() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errType != "" {
				if haErr, ok := err.(*Error); ok {
					if haErr.Type != tt.errType {
						t.Errorf("CheckOnboardingStatus() errorType = %v, want %v", haErr.Type, tt.errType)
					}
				}
			}
		})
	}
}

func TestCreateUser(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != testMethodPOST {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != testOnboardingUsersPath {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content-type: %s", r.Header.Get("Content-Type"))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"auth_code":"test-auth-code-123"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx := context.Background()

	resp, err := client.CreateUser(ctx, &CreateUserRequest{
		ClientID: server.URL + "/",
		Name:     "Admin",
		Username: "admin",
		Password: "secret",
		Language: "en",
	})

	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}

	if resp.AuthCode != "test-auth-code-123" {
		t.Errorf("CreateUser() authCode = %v, want %v", resp.AuthCode, "test-auth-code-123")
	}
}

func TestExchangeAuthCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != testMethodPOST {
			t.Errorf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != testAuthTokenPath {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("unexpected content-type: %s", r.Header.Get("Content-Type"))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"access_token":"test-access-token","token_type":"Bearer","expires_in":1800}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx := context.Background()

	resp, err := client.ExchangeAuthCode(ctx, "test-auth-code", server.URL+"/")
	if err != nil {
		t.Fatalf("ExchangeAuthCode() error = %v", err)
	}

	if resp.AccessToken != "test-access-token" {
		t.Errorf("ExchangeAuthCode() accessToken = %v, want %v", resp.AccessToken, "test-access-token")
	}
}

func TestCreateLongLivedToken(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/websocket" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("failed to upgrade websocket: %v", err)
			return
		}
		defer func() {
			if err := conn.Close(); err != nil {
				t.Logf("failed to close websocket connection: %v", err)
			}
		}()

		// Send auth_required
		if err := conn.WriteJSON(map[string]interface{}{"type": "auth_required", "ha_version": "2024.1.0"}); err != nil {
			t.Errorf("failed to write auth_required: %v", err)
			return
		}

		// Read auth message
		var authMsg map[string]interface{}
		if err := conn.ReadJSON(&authMsg); err != nil {
			t.Errorf("failed to read auth message: %v", err)
			return
		}
		if authMsg["type"] != "auth" {
			t.Errorf("unexpected auth message type: %v", authMsg["type"])
			return
		}
		if authMsg["access_token"] != "test-access-token" {
			t.Errorf("unexpected access token: %v", authMsg["access_token"])
			return
		}

		// Send auth_ok
		if err := conn.WriteJSON(map[string]interface{}{"type": "auth_ok", "ha_version": "2024.1.0"}); err != nil {
			t.Errorf("failed to write auth_ok: %v", err)
			return
		}

		// Read token request
		var tokenReq map[string]interface{}
		if err := conn.ReadJSON(&tokenReq); err != nil {
			t.Errorf("failed to read token request: %v", err)
			return
		}
		if tokenReq["type"] != "auth/long_lived_access_token" {
			t.Errorf("unexpected token request type: %v", tokenReq["type"])
			return
		}
		if tokenReq["client_name"] != "kubernetes-operator" {
			t.Errorf("unexpected client_name: %v", tokenReq["client_name"])
		}

		// Send token response
		if err := conn.WriteJSON(map[string]interface{}{
			"id":      tokenReq["id"],
			"type":    "result",
			"success": true,
			"result":  "eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9",
		}); err != nil {
			t.Errorf("failed to write token response: %v", err)
			return
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx := context.Background()

	resp, err := client.CreateLongLivedToken(ctx, "test-access-token", &LongLivedTokenRequest{
		ClientName: "kubernetes-operator",
		Lifespan:   3650,
	})

	if err != nil {
		t.Fatalf("CreateLongLivedToken() error = %v", err)
	}

	if resp.Token != "eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9" {
		t.Errorf("CreateLongLivedToken() token = %v, want %v", resp.Token, "eyJ0eXAiOiJKV1QiLCJhbGciOiJIUzI1NiJ9")
	}
}

func TestClientTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL).WithTimeout(100 * time.Millisecond)
	ctx := context.Background()

	err := client.CheckHealth(ctx)
	if err == nil {
		t.Errorf("CheckHealth() expected timeout error, got nil")
	}
}

func TestPerformBootstrap(t *testing.T) {
	// Test successful flow
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case testAPIPath:
			w.WriteHeader(http.StatusOK)
		case testOnboardingPath:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"step":"user","done":false}]`))
		case testOnboardingUsersPath:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"auth_code":"test-auth-code"}`))
		case testAuthTokenPath:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"access_token":"test-token","token_type":"Bearer","expires_in":1800}`))
		case testOnboardingCoreConfigPath:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case testOnboardingAnalyticsPath:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case testWebsocketPath:
			handleWebsocketForToken(t, w, r, "test-long-lived-token")
			return
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx := context.Background()

	opts := &BootstrapOptions{
		CreateLongLivedToken: true,
		EnableAnalytics:      false,
	}

	token, err := client.PerformBootstrap(ctx, "admin", "password", "Admin", "en", opts)
	if err != nil {
		t.Fatalf("PerformBootstrap() error = %v", err)
	}

	if token != "test-long-lived-token" {
		t.Errorf("PerformBootstrap() token = %v, want %v", token, "test-long-lived-token")
	}
}

func TestPerformBootstrap_OnboardingDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case testAPIPath:
			w.WriteHeader(http.StatusOK)
		case testOnboardingPath:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"user":{"step":"user","done":true}}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx := context.Background()

	opts := &BootstrapOptions{
		CreateLongLivedToken: true,
	}

	_, err := client.PerformBootstrap(ctx, "admin", "password", "Admin", "en", opts)
	if err == nil {
		t.Fatalf("PerformBootstrap() expected error, got nil")
	}

	if !IsOnboardingDone(err) {
		t.Errorf("PerformBootstrap() expected OnboardingDone error, got %v", err)
	}
}

func TestSetCoreConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != testOnboardingCoreConfigPath {
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		// Verify request body
		var req CoreConfigRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed to decode request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Verify fields
		if req.LocationName != "Home" {
			t.Errorf("expected location_name 'Home', got '%s'", req.LocationName)
		}
		if req.UnitSystem != "metric" {
			t.Errorf("expected unit_system 'metric', got '%s'", req.UnitSystem)
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx := context.Background()

	req := &CoreConfigRequest{
		LocationName: "Home",
		Latitude:     52.2297,
		Longitude:    21.0122,
		Elevation:    100,
		UnitSystem:   "metric",
		Currency:     "PLN",
		TimeZone:     "Europe/Warsaw",
	}

	err := client.SetCoreConfig(ctx, "test-access-token", req)
	if err != nil {
		t.Fatalf("SetCoreConfig() error = %v", err)
	}
}

func TestSetAnalytics(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
	}{
		{"analytics enabled", true},
		{"analytics disabled", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != testOnboardingAnalyticsPath {
					t.Errorf("unexpected path: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}

				if r.Method != http.MethodPost {
					t.Errorf("expected POST, got %s", r.Method)
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}

				// Verify request body
				var req AnalyticsRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Errorf("failed to decode request: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}

				// Verify preferences based on enabled flag
				if tt.enabled {
					if req.Preferences == nil {
						t.Error("expected preferences to be set when analytics enabled")
					} else {
						if !req.Preferences.Base || !req.Preferences.Diagnostics || !req.Preferences.Usage || !req.Preferences.Statistics {
							t.Error("expected all preferences to be true when analytics enabled")
						}
					}
				}

				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()

			client := NewClient(server.URL)
			ctx := context.Background()

			err := client.SetAnalytics(ctx, "test-access-token", tt.enabled)
			if err != nil {
				t.Fatalf("SetAnalytics() error = %v", err)
			}
		})
	}
}

func TestPerformBootstrap_WithLocationAndAnalytics(t *testing.T) {
	var receivedCoreConfig *CoreConfigRequest
	var receivedAnalytics *AnalyticsRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		switch r.URL.Path {
		case testAPIPath:
			w.WriteHeader(http.StatusOK)
		case testOnboardingPath:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"step":"user","done":false}]`))
		case testOnboardingUsersPath:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"auth_code":"test-auth-code"}`))
		case testOnboardingCoreConfigPath:
			// Capture the request
			if err := json.NewDecoder(r.Body).Decode(&receivedCoreConfig); err != nil {
				t.Errorf("failed to decode core_config: %v", err)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case testOnboardingAnalyticsPath:
			// Capture the request
			if err := json.NewDecoder(r.Body).Decode(&receivedAnalytics); err != nil {
				t.Errorf("failed to decode analytics: %v", err)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case testAuthTokenPath:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"access_token":"test-token","token_type":"Bearer","expires_in":1800}`))
		case testWebsocketPath:
			handleWebsocketForToken(t, w, r, "test-long-lived-token")
			return
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL)
	ctx := context.Background()

	opts := &BootstrapOptions{
		CreateLongLivedToken: true,
		EnableAnalytics:      true,
		CoreConfig: &CoreConfigRequest{
			LocationName: "Home",
			Latitude:     52.2297,
			Longitude:    21.0122,
			Elevation:    100,
			UnitSystem:   "metric",
			Currency:     "PLN",
			TimeZone:     "Europe/Warsaw",
		},
	}

	token, err := client.PerformBootstrap(ctx, "admin", "password", "Admin", "en", opts)
	if err != nil {
		t.Fatalf("PerformBootstrap() error = %v", err)
	}

	if token != "test-long-lived-token" {
		t.Errorf("PerformBootstrap() token = %v, want %v", token, "test-long-lived-token")
	}

	// Verify core config was sent
	if receivedCoreConfig == nil {
		t.Error("core_config request was not received")
	} else {
		if receivedCoreConfig.LocationName != "Home" {
			t.Errorf("core_config location_name = %v, want 'Home'", receivedCoreConfig.LocationName)
		}
		if receivedCoreConfig.UnitSystem != "metric" {
			t.Errorf("core_config unit_system = %v, want 'metric'", receivedCoreConfig.UnitSystem)
		}
	}

	// Verify analytics was sent
	if receivedAnalytics == nil {
		t.Error("analytics request was not received")
	} else if receivedAnalytics.Preferences == nil {
		t.Error("analytics preferences should be set when enabled")
	} else {
		if !receivedAnalytics.Preferences.Base {
			t.Error("analytics base preference should be true")
		}
	}
}
