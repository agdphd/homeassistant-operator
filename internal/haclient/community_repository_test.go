package haclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/gorilla/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Specs registered here run as part of the single Ginkgo suite entry point
// (TestHAClient) declared in client_test.go — Ginkgo only supports one
// RunSpecs() call per test binary.
var _ = Describe("HAClient community repository activation methods", func() {
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

	Describe("ReloadThemes", func() {
		It("posts to frontend/reload_themes and succeeds on 200", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer GinkgoRecover()
				Expect(r.URL.Path).To(Equal("/api/services/frontend/reload_themes"))
				Expect(r.Method).To(Equal("POST"))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[]`))
			}))
			client = NewClient(server.URL)

			Expect(client.ReloadThemes(ctx, "test-token")).To(Succeed())
		})

		It("returns an error on non-200", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			}))
			client = NewClient(server.URL)

			Expect(client.ReloadThemes(ctx, "test-token")).To(HaveOccurred())
		})
	})

	Describe("ReloadPythonScripts", func() {
		It("posts to python_script/reload", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer GinkgoRecover()
				Expect(r.URL.Path).To(Equal("/api/services/python_script/reload"))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[]`))
			}))
			client = NewClient(server.URL)

			Expect(client.ReloadPythonScripts(ctx, "test-token")).To(Succeed())
		})
	})

	Describe("ReloadCustomTemplates", func() {
		It("posts to homeassistant/reload_custom_templates", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer GinkgoRecover()
				Expect(r.URL.Path).To(Equal("/api/services/homeassistant/reload_custom_templates"))
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`[]`))
			}))
			client = NewClient(server.URL)

			Expect(client.ReloadCustomTemplates(ctx, "test-token")).To(Succeed())
		})
	})

	Describe("ListLovelaceResources / RegisterLovelaceResource", func() {
		var (
			wsServer *httptest.Server
			upgrader = websocket.Upgrader{}
		)

		AfterEach(func() {
			if wsServer != nil {
				wsServer.Close()
			}
		})

		It("lists existing resources", func() {
			wsServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer GinkgoRecover()
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()

				_ = conn.WriteJSON(map[string]interface{}{"type": "auth_required"})
				var authMsg map[string]interface{}
				_ = conn.ReadJSON(&authMsg)
				_ = conn.WriteJSON(map[string]interface{}{"type": "auth_ok"})

				var cmd map[string]interface{}
				_ = conn.ReadJSON(&cmd)
				Expect(cmd["type"]).To(Equal("lovelace/resources"))

				_ = conn.WriteJSON(map[string]interface{}{
					"id":      cmd["id"],
					"type":    "result",
					"success": true,
					"result": []map[string]interface{}{
						{"id": "1", "type": "module", "url": "/community_plugin/example-card.js"},
					},
				})
			}))

			wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
			client = NewClient(wsURL)

			resources, err := client.ListLovelaceResources(ctx, "test-token")
			Expect(err).NotTo(HaveOccurred())
			Expect(resources).To(HaveLen(1))
			Expect(resources[0].URL).To(Equal("/community_plugin/example-card.js"))
		})

		It("returns ErrLovelaceYAMLMode when HA reports not_supported", func() {
			wsServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()

				_ = conn.WriteJSON(map[string]interface{}{"type": "auth_required"})
				var authMsg map[string]interface{}
				_ = conn.ReadJSON(&authMsg)
				_ = conn.WriteJSON(map[string]interface{}{"type": "auth_ok"})

				var cmd map[string]interface{}
				_ = conn.ReadJSON(&cmd)

				_ = conn.WriteJSON(map[string]interface{}{
					"id":      cmd["id"],
					"type":    "result",
					"success": false,
					"error": map[string]interface{}{
						"code":    "not_supported",
						"message": "Resources are not supported in YAML mode",
					},
				})
			}))

			wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
			client = NewClient(wsURL)

			_, err := client.ListLovelaceResources(ctx, "test-token")
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(ErrLovelaceYAMLMode))
		})

		It("is idempotent: does not re-register an already-present resource", func() {
			var createCalled bool
			wsServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()

				_ = conn.WriteJSON(map[string]interface{}{"type": "auth_required"})
				var authMsg map[string]interface{}
				_ = conn.ReadJSON(&authMsg)
				_ = conn.WriteJSON(map[string]interface{}{"type": "auth_ok"})

				var cmd map[string]interface{}
				_ = conn.ReadJSON(&cmd)
				if cmd["type"] == "lovelace/resources/create" {
					createCalled = true
				}

				_ = conn.WriteJSON(map[string]interface{}{
					"id":      cmd["id"],
					"type":    "result",
					"success": true,
					"result": []map[string]interface{}{
						{"id": "1", "type": "module", "url": "/community_plugin/example-card.js"},
					},
				})
			}))

			wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
			client = NewClient(wsURL)

			err := client.RegisterLovelaceResource(ctx, "test-token", "/community_plugin/example-card.js", "module")
			Expect(err).NotTo(HaveOccurred())
			Expect(createCalled).To(BeFalse())
		})

		It("registers a new resource when not already present", func() {
			var listCall, createCall map[string]interface{}
			wsServer = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()

				_ = conn.WriteJSON(map[string]interface{}{"type": "auth_required"})
				var authMsg map[string]interface{}
				_ = conn.ReadJSON(&authMsg)
				_ = conn.WriteJSON(map[string]interface{}{"type": "auth_ok"})

				var cmd map[string]interface{}
				_ = conn.ReadJSON(&cmd)

				switch cmd["type"] {
				case "lovelace/resources":
					listCall = cmd
					_ = conn.WriteJSON(map[string]interface{}{
						"id": cmd["id"], "type": "result", "success": true,
						"result": []map[string]interface{}{},
					})
				case "lovelace/resources/create":
					createCall = cmd
					_ = conn.WriteJSON(map[string]interface{}{
						"id": cmd["id"], "type": "result", "success": true,
						"result": map[string]interface{}{},
					})
				}
			}))

			wsURL := "ws" + strings.TrimPrefix(wsServer.URL, "http")
			client = NewClient(wsURL)

			err := client.RegisterLovelaceResource(ctx, "test-token", "/community_plugin/new-card.js", "module")
			Expect(err).NotTo(HaveOccurred())
			Expect(listCall).NotTo(BeNil())
			Expect(createCall).NotTo(BeNil())
			Expect(createCall["url"]).To(Equal("/community_plugin/new-card.js"))
			Expect(createCall["res_type"]).To(Equal("module"))
		})
	})
})
