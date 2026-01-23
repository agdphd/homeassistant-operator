package haclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestHAClient(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "HAClient Suite")
}

const (
	testAPIPath         = "/api/"
	testOnboardingPath  = "/api/onboarding"
	testOnboardingUsers = "/api/onboarding/users"
	testAuthToken       = "/auth/token"
	testCoreConfig      = "/api/onboarding/core_config"
	testAnalytics       = "/api/onboarding/analytics"
	testWebsocket       = "/api/websocket"
)

var _ = Describe("HAClient", func() {
	var (
		server *httptest.Server
		client *Client
		ctx    context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	AfterEach(func() {
		if server != nil {
			server.Close()
		}
	})

	Describe("CheckHealth", func() {
		It("Should return nil for 200 OK response", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal(testAPIPath))
				Expect(r.Method).To(Equal("GET"))
				Expect(r.Header.Get("User-Agent")).To(Equal(userAgent))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"message": "API running."}`))
			}))

			client = NewClient(server.URL)
			err := client.CheckHealth(ctx)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should return nil for 401 Unauthorized (HA ready but needs onboarding)", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			}))

			client = NewClient(server.URL)
			err := client.CheckHealth(ctx)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should return NotReady error for other status codes", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			}))

			client = NewClient(server.URL)
			err := client.CheckHealth(ctx)
			Expect(err).To(HaveOccurred())
			Expect(IsNotReady(err)).To(BeTrue())
		})

		It("Should return NotReady error for network failure", func() {
			client = NewClient("http://localhost:1") // Invalid address
			client = client.WithTimeout(100 * time.Millisecond)
			err := client.CheckHealth(ctx)
			Expect(err).To(HaveOccurred())
			Expect(IsNotReady(err)).To(BeTrue())
		})
	})

	Describe("CheckOnboardingStatus", func() {
		It("Should return nil when onboarding is needed (array response)", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal(testOnboardingPath))
				Expect(r.Method).To(Equal("GET"))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`["user", "core_config", "analytics"]`))
			}))

			client = NewClient(server.URL)
			err := client.CheckOnboardingStatus(ctx)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should return OnboardingDone error when user step is done", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"user": {"step": "user", "done": true}}`))
			}))

			client = NewClient(server.URL)
			err := client.CheckOnboardingStatus(ctx)
			Expect(err).To(HaveOccurred())
			Expect(IsOnboardingDone(err)).To(BeTrue())
		})

		It("Should return nil when user step is not done", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"user": {"step": "user", "done": false}}`))
			}))

			client = NewClient(server.URL)
			err := client.CheckOnboardingStatus(ctx)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should return HTTP error for non-200 status", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))

			client = NewClient(server.URL)
			err := client.CheckOnboardingStatus(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.(*Error).Type).To(Equal(ErrorTypeHTTP))
		})
	})

	Describe("CreateUser", func() {
		It("Should create user successfully and return auth_code", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal(testOnboardingUsers))
				Expect(r.Method).To(Equal("POST"))
				Expect(r.Header.Get("Content-Type")).To(Equal("application/json"))

				var req CreateUserRequest
				err := json.NewDecoder(r.Body).Decode(&req)
				Expect(err).NotTo(HaveOccurred())
				Expect(req.Username).To(Equal("admin"))
				Expect(req.Password).To(Equal("password123"))
				Expect(req.Name).To(Equal("Admin"))
				Expect(req.Language).To(Equal("en"))

				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"auth_code": "test-auth-code-123"}`))
			}))

			client = NewClient(server.URL)
			resp, err := client.CreateUser(ctx, &CreateUserRequest{
				ClientID: "test-client",
				Name:     "Admin",
				Username: "admin",
				Password: "password123",
				Language: "en",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp).NotTo(BeNil())
			Expect(resp.AuthCode).To(Equal("test-auth-code-123"))
		})

		It("Should return error for invalid response (no auth_code)", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			}))

			client = NewClient(server.URL)
			resp, err := client.CreateUser(ctx, &CreateUserRequest{
				Username: "admin",
				Password: "password123",
			})
			Expect(err).To(HaveOccurred())
			Expect(resp).To(BeNil())
			Expect(err.(*Error).Type).To(Equal(ErrorTypeInvalidResponse))
		})

		It("Should return error for HTTP error status", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error": "invalid username"}`))
			}))

			client = NewClient(server.URL)
			resp, err := client.CreateUser(ctx, &CreateUserRequest{
				Username: "admin",
				Password: "password123",
			})
			Expect(err).To(HaveOccurred())
			Expect(resp).To(BeNil())
			Expect(err.(*Error).Type).To(Equal(ErrorTypeHTTP))
			Expect(err.(*Error).StatusCode).To(Equal(http.StatusBadRequest))
		})
	})

	Describe("ExchangeAuthCode", func() {
		It("Should exchange auth code for access token", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal(testAuthToken))
				Expect(r.Method).To(Equal("POST"))
				Expect(r.Header.Get("Content-Type")).To(Equal("application/x-www-form-urlencoded"))

				err := r.ParseForm()
				Expect(err).NotTo(HaveOccurred())
				Expect(r.Form.Get("grant_type")).To(Equal("authorization_code"))
				Expect(r.Form.Get("code")).To(Equal("test-auth-code"))
				Expect(r.Form.Get("client_id")).To(Equal("test-client-id"))

				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{
					"access_token": "test-access-token",
					"token_type": "Bearer",
					"expires_in": 1800
				}`))
			}))

			client = NewClient(server.URL)
			resp, err := client.ExchangeAuthCode(ctx, "test-auth-code", "test-client-id")
			Expect(err).NotTo(HaveOccurred())
			Expect(resp).NotTo(BeNil())
			Expect(resp.AccessToken).To(Equal("test-access-token"))
			Expect(resp.TokenType).To(Equal("Bearer"))
			Expect(resp.ExpiresIn).To(Equal(1800))
		})

		It("Should return error for invalid response (no access_token)", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			}))

			client = NewClient(server.URL)
			resp, err := client.ExchangeAuthCode(ctx, "test-auth-code", "test-client-id")
			Expect(err).To(HaveOccurred())
			Expect(resp).To(BeNil())
			Expect(err.(*Error).Type).To(Equal(ErrorTypeInvalidResponse))
		})

		It("Should return auth error for authentication failure", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error": "invalid_grant"}`))
			}))

			client = NewClient(server.URL)
			resp, err := client.ExchangeAuthCode(ctx, "invalid-code", "test-client-id")
			Expect(err).To(HaveOccurred())
			Expect(resp).To(BeNil())
			Expect(err.(*Error).Type).To(Equal(ErrorTypeAuth))
			Expect(err.(*Error).StatusCode).To(Equal(http.StatusUnauthorized))
		})
	})

	Describe("SetCoreConfig", func() {
		It("Should set core config successfully", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal(testCoreConfig))
				Expect(r.Method).To(Equal("POST"))
				Expect(r.Header.Get("Content-Type")).To(Equal("application/json"))
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))

				var req CoreConfigRequest
				err := json.NewDecoder(r.Body).Decode(&req)
				Expect(err).NotTo(HaveOccurred())
				Expect(req.LocationName).To(Equal("Home"))
				Expect(req.Latitude).To(Equal(52.2297))
				Expect(req.Longitude).To(Equal(21.0122))
				Expect(req.UnitSystem).To(Equal("metric"))

				w.WriteHeader(http.StatusOK)
			}))

			client = NewClient(server.URL)
			err := client.SetCoreConfig(ctx, "test-token", &CoreConfigRequest{
				LocationName: "Home",
				Latitude:     52.2297,
				Longitude:    21.0122,
				UnitSystem:   "metric",
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should return error for HTTP error status", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error": "invalid coordinates"}`))
			}))

			client = NewClient(server.URL)
			err := client.SetCoreConfig(ctx, "test-token", &CoreConfigRequest{
				LocationName: "Home",
			})
			Expect(err).To(HaveOccurred())
			Expect(err.(*Error).Type).To(Equal(ErrorTypeHTTP))
			Expect(err.(*Error).StatusCode).To(Equal(http.StatusBadRequest))
		})
	})

	Describe("SetAnalytics", func() {
		It("Should enable analytics successfully", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal(testAnalytics))
				Expect(r.Method).To(Equal("POST"))
				Expect(r.Header.Get("Content-Type")).To(Equal("application/json"))
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))

				var req AnalyticsRequest
				err := json.NewDecoder(r.Body).Decode(&req)
				Expect(err).NotTo(HaveOccurred())
				Expect(req.Preferences).NotTo(BeNil())
				Expect(req.Preferences.Base).To(BeTrue())
				Expect(req.Preferences.Diagnostics).To(BeTrue())

				w.WriteHeader(http.StatusOK)
			}))

			client = NewClient(server.URL)
			err := client.SetAnalytics(ctx, "test-token", true)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should disable analytics successfully", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal(testAnalytics))

				var req AnalyticsRequest
				err := json.NewDecoder(r.Body).Decode(&req)
				Expect(err).NotTo(HaveOccurred())
				Expect(req.Preferences).To(BeNil())

				w.WriteHeader(http.StatusOK)
			}))

			client = NewClient(server.URL)
			err := client.SetAnalytics(ctx, "test-token", false)
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should return error for HTTP error status", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			}))

			client = NewClient(server.URL)
			err := client.SetAnalytics(ctx, "test-token", true)
			Expect(err).To(HaveOccurred())
			Expect(err.(*Error).Type).To(Equal(ErrorTypeHTTP))
		})
	})

	Describe("CreateLongLivedToken", func() {
		var (
			wsServer *httptest.Server
			upgrader = websocket.Upgrader{}
		)

		It("Should create long-lived token via WebSocket", func() {
			// Create WebSocket server
			wsServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()

				// Send auth_required
				err = conn.WriteJSON(map[string]interface{}{
					"type": "auth_required",
				})
				Expect(err).NotTo(HaveOccurred())

				// Read auth message
				var authMsg map[string]interface{}
				err = conn.ReadJSON(&authMsg)
				Expect(err).NotTo(HaveOccurred())
				Expect(authMsg["type"]).To(Equal("auth"))
				Expect(authMsg["access_token"]).To(Equal("test-access-token"))

				// Send auth_ok
				err = conn.WriteJSON(map[string]interface{}{
					"type": "auth_ok",
				})
				Expect(err).NotTo(HaveOccurred())

				// Read token request
				var tokenReq map[string]interface{}
				err = conn.ReadJSON(&tokenReq)
				Expect(err).NotTo(HaveOccurred())
				Expect(tokenReq["type"]).To(Equal("auth/long_lived_access_token"))
				Expect(tokenReq["client_name"]).To(Equal("test-client"))
				Expect(tokenReq["lifespan"]).To(BeNumerically("==", 3650))

				// Send success response
				err = conn.WriteJSON(map[string]interface{}{
					"id":      tokenReq["id"],
					"type":    "result",
					"success": true,
					"result":  "test-long-lived-token-123",
				})
				Expect(err).NotTo(HaveOccurred())
			}))

			// Convert http URL to ws URL
			wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
			client = NewClient(wsURL)

			resp, err := client.CreateLongLivedToken(ctx, "test-access-token", &LongLivedTokenRequest{
				ClientName: "test-client",
				Lifespan:   3650,
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(resp).NotTo(BeNil())
			Expect(resp.Token).To(Equal("test-long-lived-token-123"))
		})

		It("Should return error for auth failure", func() {
			wsServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()

				// Send auth_required
				_ = conn.WriteJSON(map[string]interface{}{
					"type": "auth_required",
				})

				// Read auth message
				var authMsg map[string]interface{}
				_ = conn.ReadJSON(&authMsg)

				// Send auth_invalid
				_ = conn.WriteJSON(map[string]interface{}{
					"type":    "auth_invalid",
					"message": "Invalid access token",
				})
			}))

			wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
			client = NewClient(wsURL)

			resp, err := client.CreateLongLivedToken(ctx, "invalid-token", &LongLivedTokenRequest{
				ClientName: "test-client",
				Lifespan:   3650,
			})
			Expect(err).To(HaveOccurred())
			Expect(resp).To(BeNil())
			Expect(err.(*Error).Type).To(Equal(ErrorTypeAuth))
		})

		It("Should return error when token creation fails", func() {
			wsServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()

				// Auth flow
				_ = conn.WriteJSON(map[string]interface{}{"type": "auth_required"})
				var authMsg map[string]interface{}
				_ = conn.ReadJSON(&authMsg)
				_ = conn.WriteJSON(map[string]interface{}{"type": "auth_ok"})

				// Read token request
				var tokenReq map[string]interface{}
				_ = conn.ReadJSON(&tokenReq)

				// Send error response
				_ = conn.WriteJSON(map[string]interface{}{
					"id":      tokenReq["id"],
					"type":    "result",
					"success": false,
					"error": map[string]interface{}{
						"message": "Failed to create token",
					},
				})
			}))

			wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
			client = NewClient(wsURL)

			resp, err := client.CreateLongLivedToken(ctx, "test-token", &LongLivedTokenRequest{
				ClientName: "test-client",
				Lifespan:   3650,
			})
			Expect(err).To(HaveOccurred())
			Expect(resp).To(BeNil())
		})

		AfterEach(func() {
			if wsServer != nil {
				wsServer.Close()
			}
		})
	})

	Describe("PerformBootstrap", func() {
		var (
			wsServer        *httptest.Server
			upgrader        = websocket.Upgrader{}
			msgID           atomic.Int64
			healthCalled    bool
			onboardCalled   bool
			userCalled      bool
			configCalled    bool
			analyticsCalled bool
		)

		BeforeEach(func() {
			healthCalled = false
			onboardCalled = false
			userCalled = false
			configCalled = false
			analyticsCalled = false
			msgID.Store(0)
		})

		It("Should perform complete bootstrap flow successfully", func() {
			// Create combined HTTP + WebSocket server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case testAPIPath:
					healthCalled = true
					w.WriteHeader(http.StatusUnauthorized)
				case testOnboardingPath:
					onboardCalled = true
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`["user", "core_config", "analytics"]`))
				case testOnboardingUsers:
					userCalled = true
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"auth_code": "test-auth-code"}`))
				case testAuthToken:
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"access_token": "test-access-token", "token_type": "Bearer", "expires_in": 1800}`))
				case testCoreConfig:
					configCalled = true
					w.WriteHeader(http.StatusOK)
				case testAnalytics:
					analyticsCalled = true
					w.WriteHeader(http.StatusOK)
				case testWebsocket:
					// WebSocket upgrade
					conn, err := upgrader.Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer func() { _ = conn.Close() }()

					// WebSocket flow
					_ = conn.WriteJSON(map[string]interface{}{"type": "auth_required"})
					var authMsg map[string]interface{}
					_ = conn.ReadJSON(&authMsg)
					_ = conn.WriteJSON(map[string]interface{}{"type": "auth_ok"})

					var tokenReq map[string]interface{}
					_ = conn.ReadJSON(&tokenReq)
					_ = conn.WriteJSON(map[string]interface{}{
						"id":      tokenReq["id"],
						"type":    "result",
						"success": true,
						"result":  "test-long-lived-token",
					})
				}
			}))

			client = NewClient(server.URL)
			token, err := client.PerformBootstrap(ctx, "admin", "password123", "Admin", "en", &BootstrapOptions{
				CreateLongLivedToken: true,
				CoreConfig: &CoreConfigRequest{
					LocationName: "Home",
					Latitude:     52.2297,
					Longitude:    21.0122,
					UnitSystem:   "metric",
				},
				EnableAnalytics: true,
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(token).To(Equal("test-long-lived-token"))
			Expect(healthCalled).To(BeTrue())
			Expect(onboardCalled).To(BeTrue())
			Expect(userCalled).To(BeTrue())
			Expect(configCalled).To(BeTrue())
			Expect(analyticsCalled).To(BeTrue())
		})

		It("Should skip long-lived token creation when not requested", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case testAPIPath:
					w.WriteHeader(http.StatusOK)
				case testOnboardingPath:
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`["user"]`))
				case testOnboardingUsers:
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"auth_code": "test-auth-code"}`))
				case testAuthToken:
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"access_token": "test-access-token", "token_type": "Bearer", "expires_in": 1800}`))
				case testAnalytics:
					w.WriteHeader(http.StatusOK)
				}
			}))

			client = NewClient(server.URL)
			token, err := client.PerformBootstrap(ctx, "admin", "password123", "Admin", "en", &BootstrapOptions{
				CreateLongLivedToken: false,
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(token).To(Equal(""))
		})

		It("Should return OnboardingDone error when already completed", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case testAPIPath:
					w.WriteHeader(http.StatusOK)
				case testOnboardingPath:
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"user": {"step": "user", "done": true}}`))
				}
			}))

			client = NewClient(server.URL)
			token, err := client.PerformBootstrap(ctx, "admin", "password123", "Admin", "en", nil)

			Expect(err).To(HaveOccurred())
			Expect(IsOnboardingDone(err)).To(BeTrue())
			Expect(token).To(Equal(""))
		})

		It("Should return NotReady error when HA not responding", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusServiceUnavailable)
			}))

			client = NewClient(server.URL)
			token, err := client.PerformBootstrap(ctx, "admin", "password123", "Admin", "en", nil)

			Expect(err).To(HaveOccurred())
			Expect(IsNotReady(err)).To(BeTrue())
			Expect(token).To(Equal(""))
		})

		It("Should fail when user creation fails", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case testAPIPath:
					w.WriteHeader(http.StatusOK)
				case testOnboardingPath:
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`["user"]`))
				case testOnboardingUsers:
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error": "invalid username"}`))
				}
			}))

			client = NewClient(server.URL)
			token, err := client.PerformBootstrap(ctx, "admin", "pass", "Admin", "en", nil)

			Expect(err).To(HaveOccurred())
			Expect(err.(*Error).Type).To(Equal(ErrorTypeHTTP))
			Expect(token).To(Equal(""))
		})

		AfterEach(func() {
			if wsServer != nil {
				wsServer.Close()
			}
		})
	})

	Describe("Error Helpers", func() {
		It("IsNotReady should return true for NotReady error", func() {
			err := &Error{Type: ErrorTypeNotReady, Message: "not ready"}
			Expect(IsNotReady(err)).To(BeTrue())
		})

		It("IsNotReady should return false for other error types", func() {
			err := &Error{Type: ErrorTypeHTTP, Message: "http error"}
			Expect(IsNotReady(err)).To(BeFalse())
		})

		It("IsNotReady should return false for non-Error type", func() {
			err := fmt.Errorf("generic error")
			Expect(IsNotReady(err)).To(BeFalse())
		})

		It("IsOnboardingDone should return true for OnboardingDone error", func() {
			err := &Error{Type: ErrorTypeOnboardingDone, Message: "already done"}
			Expect(IsOnboardingDone(err)).To(BeTrue())
		})

		It("IsOnboardingDone should return false for other error types", func() {
			err := &Error{Type: ErrorTypeAuth, Message: "auth error"}
			Expect(IsOnboardingDone(err)).To(BeFalse())
		})

		It("IsOnboardingDone should return false for non-Error type", func() {
			err := fmt.Errorf("generic error")
			Expect(IsOnboardingDone(err)).To(BeFalse())
		})
	})

	Describe("Client Configuration", func() {
		It("Should trim trailing slash from base URL", func() {
			client := NewClient("http://example.com/")
			Expect(client.baseURL).To(Equal("http://example.com"))
		})

		It("Should set custom timeout", func() {
			client := NewClient("http://example.com")
			client = client.WithTimeout(5 * time.Second)
			Expect(client.httpClient.Timeout).To(Equal(5 * time.Second))
		})

		It("Should have default timeout", func() {
			client := NewClient("http://example.com")
			Expect(client.httpClient.Timeout).To(Equal(defaultTimeout))
		})
	})

	Describe("CheckConfig", func() {
		It("Should handle object response with errors", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal("/api/services/homeassistant/check_config"))
				Expect(r.Method).To(Equal("POST"))
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))
				Expect(r.Header.Get("Content-Type")).To(Equal("application/json"))
				Expect(r.Header.Get("User-Agent")).To(Equal(userAgent))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"errors": ["config error 1", "config error 2"]}`))
			}))

			client = NewClient(server.URL)
			err := client.CheckConfig(ctx, "test-token")
			Expect(err).To(HaveOccurred())
			Expect(err.(*Error).Type).To(Equal(ErrorTypeHTTP))
			Expect(err.(*Error).Message).To(ContainSubstring("config validation errors"))
		})

		It("Should handle object response without errors", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal("/api/services/homeassistant/check_config"))
				Expect(r.Method).To(Equal("POST"))
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"result": "valid"}`))
			}))

			client = NewClient(server.URL)
			err := client.CheckConfig(ctx, "test-token")
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should handle empty array response", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal("/api/services/homeassistant/check_config"))
				Expect(r.Method).To(Equal("POST"))
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[]`))
			}))

			client = NewClient(server.URL)
			err := client.CheckConfig(ctx, "test-token")
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should handle array response with elements", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				Expect(r.URL.Path).To(Equal("/api/services/homeassistant/check_config"))
				Expect(r.Method).To(Equal("POST"))
				Expect(r.Header.Get("Authorization")).To(Equal("Bearer test-token"))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[{"service": "homeassistant.check_config"}]`))
			}))

			client = NewClient(server.URL)
			err := client.CheckConfig(ctx, "test-token")
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should return HTTP error for non-200 status", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
			}))

			client = NewClient(server.URL)
			err := client.CheckConfig(ctx, "test-token")
			Expect(err).To(HaveOccurred())
			Expect(err.(*Error).Type).To(Equal(ErrorTypeHTTP))
			Expect(err.(*Error).StatusCode).To(Equal(http.StatusUnauthorized))
		})

		It("Should return error for invalid JSON response", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`invalid json`))
			}))

			client = NewClient(server.URL)
			err := client.CheckConfig(ctx, "test-token")
			Expect(err).To(HaveOccurred())
			Expect(err.(*Error).Type).To(Equal(ErrorTypeInvalidResponse))
		})

		It("Should return error for unexpected response format", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`"just a string"`))
			}))

			client = NewClient(server.URL)
			err := client.CheckConfig(ctx, "test-token")
			Expect(err).To(HaveOccurred())
			Expect(err.(*Error).Type).To(Equal(ErrorTypeInvalidResponse))
			Expect(err.(*Error).Message).To(ContainSubstring("unexpected response format"))
		})
	})
})
