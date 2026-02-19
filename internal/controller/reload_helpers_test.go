package controller

//nolint:goconst // Test fixtures use repeated strings for mock HTTP endpoints
import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/przemekhys/homeassistant-operator/internal/haclient"
)

var _ = Describe("PerformReloadWithRetry", func() {
	var (
		ctx    context.Context
		config ReloadConfig
		server *httptest.Server
		client *haclient.Client
	)

	BeforeEach(func() {
		ctx = context.Background()
		config = ReloadConfig{
			MaxRetries:    3,
			RetryDelay:    10 * time.Millisecond, // Short delay for tests
			ComponentName: "script",
		}
	})

	AfterEach(func() {
		if server != nil {
			server.Close()
		}
	})

	Describe("Success scenarios", func() {
		It("Should succeed on first attempt", func() {
			// Mock HA server that returns component loaded + reload success
			//nolint:goconst // Test fixtures use repeated endpoint strings
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/config":
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"components":["script","automation"],"version":"2024.2.0"}`))
				case "/api/services/script/reload":
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{}`))
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))

			client = haclient.NewClient(server.URL)

			reloadFunc := func(ctx context.Context, token string) error {
				return client.ReloadScripts(ctx, token)
			}

			result := PerformReloadWithRetry(ctx, client, "test-token", config, reloadFunc)

			Expect(result).NotTo(BeNil())
			Expect(result.Success).To(BeTrue())
			Expect(result.Method).To(Equal("hot-reload"))
			Expect(result.Attempts).To(Equal(1))
			Expect(result.ComponentLoaded).To(BeTrue())
			Expect(result.Error).ToNot(HaveOccurred())
			Expect(result.ReloadID).NotTo(BeEmpty())
			Expect(result.Duration).To(BeNumerically(">", 0))
		})

		It("Should succeed after retry", func() {
			attempts := 0
			// Mock server that fails twice then succeeds
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/config":
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"components":["script"],"version":"2024.2.0"}`))
				case "/api/services/script/reload":
					attempts++
					if attempts < 3 {
						w.WriteHeader(http.StatusServiceUnavailable)
						_, _ = w.Write([]byte(`Service Unavailable`))
					} else {
						w.WriteHeader(http.StatusOK)
						_, _ = w.Write([]byte(`{}`))
					}
				}
			}))

			client = haclient.NewClient(server.URL)

			reloadFunc := func(ctx context.Context, token string) error {
				return client.ReloadScripts(ctx, token)
			}

			result := PerformReloadWithRetry(ctx, client, "test-token", config, reloadFunc)

			Expect(result.Success).To(BeTrue())
			Expect(result.Method).To(Equal("hot-reload"))
			Expect(result.Attempts).To(Equal(3))
			Expect(attempts).To(Equal(3))
		})
	})

	Describe("Component not loaded scenarios", func() {
		It("Should skip when component not loaded", func() {
			// Mock server with config that doesn't include script component
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/config" {
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"components":["automation"],"version":"2024.2.0"}`))
				}
			}))

			client = haclient.NewClient(server.URL)

			reloadFunc := func(ctx context.Context, token string) error {
				Fail("Should not be called when component not loaded")
				return nil
			}

			result := PerformReloadWithRetry(ctx, client, "test-token", config, reloadFunc)

			Expect(result.Success).To(BeFalse())
			Expect(result.Method).To(Equal("skipped"))
			Expect(result.Attempts).To(Equal(0))
			Expect(result.ComponentLoaded).To(BeFalse())
			Expect(result.Error).To(HaveOccurred())
			Expect(result.Error.Error()).To(ContainSubstring("component not loaded"))
		})

		It("Should return error when component check fails", func() {
			// Mock server that returns 401 for config
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/config" {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`Unauthorized`))
				}
			}))

			client = haclient.NewClient(server.URL)

			reloadFunc := func(ctx context.Context, token string) error {
				Fail("Should not be called when component check fails")
				return nil
			}

			result := PerformReloadWithRetry(ctx, client, "test-token", config, reloadFunc)

			Expect(result.Success).To(BeFalse())
			Expect(result.Method).To(Equal("failed"))
			Expect(result.Attempts).To(Equal(0))
			Expect(result.Error).To(HaveOccurred())
			Expect(result.Error.Error()).To(ContainSubstring("component check failed"))
		})
	})

	Describe("Retry exhausted scenarios", func() {
		It("Should fail after all retries exhausted", func() {
			// Mock server that always returns 400
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/config":
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"components":["script"],"version":"2024.2.0"}`))
				case "/api/services/script/reload":
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`Bad Request`))
				}
			}))

			client = haclient.NewClient(server.URL)

			reloadFunc := func(ctx context.Context, token string) error {
				return client.ReloadScripts(ctx, token)
			}

			result := PerformReloadWithRetry(ctx, client, "test-token", config, reloadFunc)

			Expect(result.Success).To(BeFalse())
			Expect(result.Method).To(Equal("failed"))
			Expect(result.Attempts).To(Equal(3))
			Expect(result.Error).To(HaveOccurred())
			Expect(result.Error.Error()).To(ContainSubstring("hot-reload failed after 3 attempts"))
		})
	})

	Describe("Different components", func() {
		It("Should work with automation component", func() {
			config.ComponentName = "automation"

			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/config":
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"components":["automation"],"version":"2024.2.0"}`))
				case "/api/services/automation/reload":
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{}`))
				}
			}))

			client = haclient.NewClient(server.URL)

			reloadFunc := func(ctx context.Context, token string) error {
				return client.ReloadAutomations(ctx, token)
			}

			result := PerformReloadWithRetry(ctx, client, "test-token", config, reloadFunc)

			Expect(result.Success).To(BeTrue())
			Expect(result.ComponentLoaded).To(BeTrue())
		})
	})

	Describe("Helper functions", func() {
		Describe("getStatusCode", func() {
			It("Should extract status code from haclient.Error", func() {
				err := &haclient.Error{
					Type:       haclient.ErrorTypeHTTP,
					StatusCode: 404,
					Message:    "Not Found",
				}

				code := getStatusCode(err)
				Expect(code).To(Equal(404))
			})

			It("Should return 0 for non-haclient errors", func() {
				err := errors.New("generic error")

				code := getStatusCode(err)
				Expect(code).To(Equal(0))
			})

			It("Should return 0 for nil error", func() {
				code := getStatusCode(nil)
				Expect(code).To(Equal(0))
			})
		})

		Describe("getResponseBody", func() {
			It("Should extract message from haclient.Error", func() {
				err := &haclient.Error{
					Type:       haclient.ErrorTypeHTTP,
					StatusCode: 500,
					Message:    "Internal Server Error",
				}

				body := getResponseBody(err)
				Expect(body).To(Equal("Internal Server Error"))
			})

			It("Should return error message for non-haclient errors", func() {
				err := errors.New("generic error message")

				body := getResponseBody(err)
				Expect(body).To(Equal("generic error message"))
			})
		})

		Describe("truncateString", func() {
			It("Should not truncate short strings", func() {
				s := "short"
				result := truncateString(s, 100)
				Expect(result).To(Equal("short"))
			})

			It("Should truncate long strings", func() {
				s := "this is a very long string that should be truncated"
				result := truncateString(s, 10)
				Expect(result).To(Equal("this is a ..."))
			})

			It("Should handle exact length", func() {
				s := "exactly10!"
				result := truncateString(s, 10)
				Expect(result).To(Equal("exactly10!"))
			})
		})
	})
})
