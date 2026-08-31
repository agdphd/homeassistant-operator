package haclient

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// wsHTTPConfigServer starts a mock Home Assistant WebSocket endpoint that runs
// handle(cmd) for each command and returns its reply map. A nil reply from
// handle closes the connection without answering.
func wsHTTPConfigServer(
	t *testing.T, handle func(cmd map[string]interface{}) map[string]interface{},
) (*Client, func()) {
	t.Helper()
	up := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.WriteJSON(map[string]interface{}{"type": "auth_required"})
		var auth map[string]interface{}
		_ = conn.ReadJSON(&auth)
		_ = conn.WriteJSON(map[string]interface{}{"type": "auth_ok"})
		for {
			var cmd map[string]interface{}
			if err := conn.ReadJSON(&cmd); err != nil {
				return
			}
			reply := handle(cmd)
			if reply == nil {
				return
			}
			reply["id"] = cmd["id"]
			_ = conn.WriteJSON(reply)
		}
	}))
	client := NewClient("ws" + strings.TrimPrefix(srv.URL, "http"))
	return client, srv.Close
}

func okResult(result interface{}) map[string]interface{} {
	return map[string]interface{}{"type": "result", "success": true, "result": result}
}

func wsErr(code string) map[string]interface{} {
	return map[string]interface{}{
		"type": "result", "success": false,
		"error": map[string]interface{}{"code": code, "message": code},
	}
}

func TestGetHTTPConfig_FullResponse(t *testing.T) {
	client, done := wsHTTPConfigServer(t, func(cmd map[string]interface{}) map[string]interface{} {
		if cmd["type"] != "http/config" {
			t.Fatalf("unexpected command %v", cmd["type"])
		}
		return okResult(map[string]interface{}{
			"stable": map[string]interface{}{
				"cors_allowed_origins":     []string{"https://cast.home-assistant.io"},
				"login_attempts_threshold": -1,
				"ip_ban_enabled":           true,
				"ssl_profile":              "modern",
				"use_x_frame_options":      true,
				"server_port":              8123,
				"created_at":               "2026-08-01T00:00:00+00:00",
				"error":                    nil,
				"error_message":            nil,
			},
			"pending":            nil,
			"revert_at":          nil,
			"active_config_type": "stable",
			"default": map[string]interface{}{
				"cors_allowed_origins": []string{"https://cast.home-assistant.io"},
				"ip_ban_enabled":       true,
				"ssl_profile":          "modern",
				"use_x_frame_options":  true,
				"server_port":          8123,
			},
		})
	})
	defer done()

	resp, err := client.GetHTTPConfig(context.Background(), "tok")
	if err != nil {
		t.Fatalf("GetHTTPConfig: %v", err)
	}
	if resp.ActiveConfigType != "stable" {
		t.Fatalf("active_config_type = %q", resp.ActiveConfigType)
	}
	if resp.Default["ssl_profile"] != "modern" {
		t.Fatalf("default not parsed: %v", resp.Default)
	}
	if _, ok := resp.Stable["created_at"]; !ok {
		t.Fatalf("stable should carry raw metadata for the caller to strip")
	}
	if _, ok := resp.Stable.StrippedMetadata()["created_at"]; ok {
		t.Fatalf("StrippedMetadata must drop created_at")
	}
}

func TestGetHTTPConfig_Unsupported(t *testing.T) {
	client, done := wsHTTPConfigServer(t, func(map[string]interface{}) map[string]interface{} {
		return wsErr("unknown_command")
	})
	defer done()

	_, err := client.GetHTTPConfig(context.Background(), "tok")
	if !errors.Is(err, ErrHTTPConfigUnsupported) {
		t.Fatalf("expected ErrHTTPConfigUnsupported, got %v", err)
	}
}

func TestConfigureHTTPConfig_SendsConfigAndReadsRestart(t *testing.T) {
	client, done := wsHTTPConfigServer(t, func(cmd map[string]interface{}) map[string]interface{} {
		if cmd["type"] != "http/config/configure" {
			t.Fatalf("unexpected command %v", cmd["type"])
		}
		if _, present := cmd["config"]; !present {
			t.Fatalf("configure must always include the config key")
		}
		if cmd["config"] == nil {
			t.Fatalf("expected a non-nil config for this case")
		}
		return okResult(map[string]interface{}{"restart": true})
	})
	defer done()

	restart, err := client.ConfigureHTTPConfig(context.Background(), "tok", HTTPConfigData{"use_x_forwarded_for": true})
	if err != nil {
		t.Fatalf("ConfigureHTTPConfig: %v", err)
	}
	if !restart {
		t.Fatalf("restart flag not read from response")
	}
}

func TestConfigureHTTPConfig_NilClearsPending(t *testing.T) {
	sawNull := false
	client, done := wsHTTPConfigServer(t, func(cmd map[string]interface{}) map[string]interface{} {
		v, present := cmd["config"]
		if !present {
			t.Fatalf("configure must always include the config key")
		}
		if v == nil {
			sawNull = true
		}
		return okResult(map[string]interface{}{"restart": false})
	})
	defer done()

	if _, err := client.ConfigureHTTPConfig(context.Background(), "tok", nil); err != nil {
		t.Fatalf("ConfigureHTTPConfig(nil): %v", err)
	}
	if !sawNull {
		t.Fatalf("nil config must be sent as JSON null to clear pending")
	}
}

func TestPromoteHTTPConfig(t *testing.T) {
	client, done := wsHTTPConfigServer(t, func(cmd map[string]interface{}) map[string]interface{} {
		if cmd["type"] != "http/config/promote" {
			t.Fatalf("unexpected command %v", cmd["type"])
		}
		return okResult(nil)
	})
	defer done()

	if err := client.PromoteHTTPConfig(context.Background(), "tok"); err != nil {
		t.Fatalf("PromoteHTTPConfig: %v", err)
	}
}
