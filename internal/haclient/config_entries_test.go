package haclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestListConfigEntries(t *testing.T) {
	entries := []ConfigEntry{
		{EntryID: "abc123", Domain: "mqtt", Title: "MQTT", State: "loaded", Source: "user"},
		{EntryID: "def456", Domain: "zha", Title: "ZHA", State: "loaded", Source: "user"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/config/config_entries/entry" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing or wrong Authorization header")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(entries)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	result, err := client.ListConfigEntries(context.Background(), "test-token")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
	if result[0].Domain != "mqtt" {
		t.Errorf("expected domain mqtt, got %s", result[0].Domain)
	}
}

func TestIsIntegrationConfigured(t *testing.T) {
	entries := []ConfigEntry{
		{EntryID: "abc123", Domain: "mqtt", Title: "MQTT"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(entries)
	}))
	defer server.Close()

	client := NewClient(server.URL)

	found, err := client.IsIntegrationConfigured(context.Background(), "token", "mqtt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found {
		t.Error("expected mqtt to be configured")
	}

	found, err = client.IsIntegrationConfigured(context.Background(), "token", "zha")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected zha to not be configured")
	}
}

func TestStartConfigFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/config/config_entries/flow" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}

		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["handler"] != "mqtt" {
			t.Errorf("expected handler=mqtt, got %v", body["handler"])
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(FlowResponse{
			FlowID: "flow-123",
			Type:   "form",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	resp, err := client.StartConfigFlow(context.Background(), "token", "mqtt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.FlowID != "flow-123" {
		t.Errorf("expected flow_id=flow-123, got %s", resp.FlowID)
	}
}

func TestSubmitConfigFlow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/config/config_entries/flow/") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(FlowResponse{
			FlowID: "flow-123",
			Type:   "create_entry",
			Title:  "MQTT",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	resp, err := client.SubmitConfigFlow(context.Background(), "token", "flow-123", map[string]interface{}{
		"broker": "mosquitto.default.svc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Type != "create_entry" {
		t.Errorf("expected type=create_entry, got %s", resp.Type)
	}
}

func TestSubmitConfigFlowUntilDone_SingleStep(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(FlowResponse{
			FlowID: "flow-123",
			Type:   "create_entry",
			Title:  "MQTT",
		})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	resp, err := client.SubmitConfigFlowUntilDone(context.Background(), "token", "flow-123",
		[]map[string]interface{}{{"broker": "mosquitto.default.svc"}},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Type != "create_entry" {
		t.Errorf("expected type=create_entry, got %s", resp.Type)
	}
}

func TestSubmitConfigFlowUntilDone_MultiStep(t *testing.T) {
	step := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step++
		w.Header().Set("Content-Type", "application/json")
		flowType := "form"
		if step == 2 {
			flowType = "create_entry"
		}
		_ = json.NewEncoder(w).Encode(FlowResponse{FlowID: "flow-multi", Type: flowType})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	resp, err := client.SubmitConfigFlowUntilDone(context.Background(), "token", "flow-multi",
		[]map[string]interface{}{
			{"broker": "mosquitto.default.svc"},
			{"port": 1883},
		},
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Type != "create_entry" {
		t.Errorf("expected type=create_entry, got %s", resp.Type)
	}
	if step != 2 {
		t.Errorf("expected 2 HTTP calls, got %d", step)
	}
}

func TestSubmitConfigFlowUntilDone_Abort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(FlowResponse{FlowID: "flow-123", Type: "abort"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.SubmitConfigFlowUntilDone(context.Background(), "token", "flow-123",
		[]map[string]interface{}{{"broker": "bad-host"}},
	)
	if err == nil {
		t.Fatal("expected error for aborted flow, got nil")
	}
}

func TestSubmitConfigFlowUntilDone_StepsExhausted(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Always return "form" — never reaches create_entry
		_ = json.NewEncoder(w).Encode(FlowResponse{FlowID: "flow-123", Type: "form"})
	}))
	defer server.Close()

	client := NewClient(server.URL)
	_, err := client.SubmitConfigFlowUntilDone(context.Background(), "token", "flow-123",
		[]map[string]interface{}{{"step1": "data"}},
	)
	if err == nil {
		t.Fatal("expected error when steps exhausted, got nil")
	}
}

func TestRemoveConfigEntry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("expected DELETE, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	err := client.RemoveConfigEntry(context.Background(), "token", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRemoveConfigEntry_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewClient(server.URL)
	err := client.RemoveConfigEntry(context.Background(), "token", "nonexistent")
	if err != nil {
		t.Fatalf("expected nil for 404, got: %v", err)
	}
}
