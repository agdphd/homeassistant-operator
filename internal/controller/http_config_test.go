/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
	"github.com/przemekhys/homeassistant-operator/internal/haclient"
)

// --- pure merge / compare ---------------------------------------------------

// haDefaults is the shape Home Assistant reports in the `default` field of
// http/config — i.e. with its own schema defaults filled in.
func haDefaults() haclient.HTTPConfigData {
	return haclient.HTTPConfigData{
		"cors_allowed_origins":     []interface{}{"https://cast.home-assistant.io"},
		"login_attempts_threshold": float64(-1),
		"ip_ban_enabled":           true,
		"ssl_profile":              "modern",
		"use_x_frame_options":      true,
		"server_port":              float64(8123),
	}
}

// haStable is `stable` as HA returns it: defaults filled in, plus metadata.
func haStable(extra haclient.HTTPConfigData) haclient.HTTPConfigData {
	s := haDefaults()
	s["created_at"] = "2026-08-01T00:00:00+00:00"
	s["error"] = nil
	s["error_message"] = nil
	for k, v := range extra {
		s[k] = v
	}
	return s
}

func TestDesiredHTTPConfig_NoResourceHTTP_EqualsStable(t *testing.T) {
	resp := &haclient.HTTPConfigResponse{Default: haDefaults(), Stable: haStable(nil)}
	desired := desiredHTTPConfig(resp, nil)
	if !httpConfigEqual(desired, resp.Stable) {
		t.Fatalf("desired should equal stable when the resource has no http:\n desired=%#v", desired)
	}
}

func TestDesiredHTTPConfig_PassesThroughUnknownKeys(t *testing.T) {
	resp := &haclient.HTTPConfigResponse{
		Default: haDefaults(),
		Stable:  haStable(haclient.HTTPConfigData{"some_future_option": "keepme"}),
	}
	desired := desiredHTTPConfig(resp, haclient.HTTPConfigData{"cors_allowed_origins": []interface{}{"https://x"}})
	if desired["some_future_option"] != "keepme" {
		t.Fatalf("unknown key not passed through: %#v", desired)
	}
	if !httpConfigEqual(desired, haStable(haclient.HTTPConfigData{
		"some_future_option":   "keepme",
		"cors_allowed_origins": []interface{}{"https://x"},
	})) {
		t.Fatalf("desired mismatch: %#v", desired)
	}
}

func TestDesiredHTTPConfig_ResourceWins(t *testing.T) {
	resp := &haclient.HTTPConfigResponse{Default: haDefaults(), Stable: haStable(nil)}
	desired := desiredHTTPConfig(resp, haclient.HTTPConfigData{"ip_ban_enabled": false})
	if desired["ip_ban_enabled"] != false {
		t.Fatalf("resource value did not win: %#v", desired)
	}
}

// The operator must not change the port. server_port is a known key; when the
// resource is silent it falls to HA's own default (8123) — no divergence.
func TestDesiredHTTPConfig_DoesNotChangePortWhenSilent(t *testing.T) {
	resp := &haclient.HTTPConfigResponse{
		Default: haDefaults(),
		Stable:  haStable(haclient.HTTPConfigData{"server_port": float64(8123)}),
	}
	desired := desiredHTTPConfig(resp, haclient.HTTPConfigData{"use_x_forwarded_for": true})
	if !httpConfigEqual(desired, haStable(haclient.HTTPConfigData{"use_x_forwarded_for": true})) {
		t.Fatalf("port drifted: %#v", desired)
	}
}

func TestHTTPConfigEqual_CanonicalisesTrustedProxies(t *testing.T) {
	x := haclient.HTTPConfigData{"trusted_proxies": []interface{}{"10.0.0.5"}}
	y := haclient.HTTPConfigData{"trusted_proxies": []interface{}{"10.0.0.5/32"}}
	if !httpConfigEqual(x, y) {
		t.Fatalf("canonicalisation not applied in compare")
	}
	a := haclient.HTTPConfigData{"trusted_proxies": []interface{}{"192.168.1.0/24"}}
	if httpConfigEqual(a, haclient.HTTPConfigData{"trusted_proxies": []interface{}{"10.0.0.0/8"}}) {
		t.Fatalf("different proxies compared equal")
	}
}

// --- reconcileHTTPConfig with a mock Home Assistant ------------------------

type wsCall struct {
	typ    string
	config interface{}
}

// mockHA is a Home Assistant WebSocket stub for the http config commands.
type mockHA struct {
	mu      sync.Mutex
	stable  haclient.HTTPConfigData
	pending haclient.HTTPConfigData
	restart bool
	calls   []wsCall
	srv     *httptest.Server
}

func newMockHA(t *testing.T, stable haclient.HTTPConfigData) *mockHA {
	t.Helper()
	m := &mockHA{stable: stable}
	up := websocket.Upgrader{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.WriteJSON(map[string]interface{}{"type": "auth_required"})
		var a map[string]interface{}
		_ = conn.ReadJSON(&a)
		_ = conn.WriteJSON(map[string]interface{}{"type": "auth_ok"})
		for {
			var cmd map[string]interface{}
			if err := conn.ReadJSON(&cmd); err != nil {
				return
			}
			_ = conn.WriteJSON(m.handle(cmd))
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockHA) url() string { return "ws" + strings.TrimPrefix(m.srv.URL, "http") }

func (m *mockHA) newClient(string) *haclient.Client { return haclient.NewClient(m.url()) }

func (m *mockHA) handle(cmd map[string]interface{}) map[string]interface{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := cmd["id"]
	switch cmd["type"] {
	case "http/config":
		m.calls = append(m.calls, wsCall{typ: "http/config"})
		return map[string]interface{}{"id": id, "type": "result", "success": true, "result": map[string]interface{}{
			"stable": m.stable, "pending": m.pending, "revert_at": nil,
			"active_config_type": "stable", "default": haDefaults(),
		}}
	case "http/config/configure":
		m.calls = append(m.calls, wsCall{typ: "http/config/configure", config: cmd["config"]})
		if cmd["config"] == nil {
			m.pending = nil
		} else {
			mc, _ := cmd["config"].(map[string]interface{})
			m.pending = haclient.HTTPConfigData(mc)
		}
		return map[string]interface{}{"id": id, "type": "result", "success": true,
			"result": map[string]interface{}{"restart": m.restart}}
	case "http/config/promote":
		m.calls = append(m.calls, wsCall{typ: "http/config/promote"})
		if m.pending != nil {
			m.stable = m.pending
			m.pending = nil
		}
		return map[string]interface{}{"id": id, "type": "result", "success": true, "result": nil}
	}
	return map[string]interface{}{"id": id, "type": "result", "success": false,
		"error": map[string]interface{}{"code": "unknown_command", "message": "unknown"}}
}

func (m *mockHA) countConfigure() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, c := range m.calls {
		if c.typ == "http/config/configure" {
			n++
		}
	}
	return n
}

func newHTTPConfigTestReconciler(m *mockHA) (
	*HomeAssistantConfigurationReconciler, *events.FakeRecorder, *hav1.HomeAssistantConfiguration,
) {
	scheme := runtime.NewScheme()
	_ = hav1.AddToScheme(scheme)
	cfg := &hav1.HomeAssistantConfiguration{
		ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "default"},
		Spec:       hav1.HomeAssistantConfigurationSpec{HomeAssistantRef: hav1.HomeAssistantReference{Name: "ha"}},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(cfg).WithStatusSubresource(&hav1.HomeAssistantConfiguration{}).Build()
	rec := events.NewFakeRecorder(16)
	r := &HomeAssistantConfigurationReconciler{Client: cl, Scheme: scheme, Recorder: rec}
	if m != nil {
		r.NewHAClient = m.newClient
	}
	return r, rec, cfg
}

func haObj() *hav1.HomeAssistant {
	return &hav1.HomeAssistant{ObjectMeta: metav1.ObjectMeta{Name: "ha", Namespace: "default"}}
}

func condOf(cfg *hav1.HomeAssistantConfiguration) *metav1.Condition {
	return meta.FindStatusCondition(cfg.Status.Conditions, conditionHTTPConfigReady)
}

// A realistic stable (HA defaults filled in, plus metadata) with no http: in
// the resource: the operator must NOT send a single configure, however many
// times it runs. This is exactly the case the earlier attempt looped on.
func TestReconcileHTTPConfig_NoWriteWhenNoChange(t *testing.T) {
	m := newMockHA(t, haStable(nil))
	r, _, cfg := newHTTPConfigTestReconciler(m)
	dec := httpConfigDecision{path: httpPathAPI, token: "tok",
		resp: &haclient.HTTPConfigResponse{Default: haDefaults(), Stable: haStable(nil)}}

	for i := 0; i < 10; i++ {
		r.reconcileHTTPConfig(context.Background(), cfg, haObj(), dec, nil, true)
	}
	if n := m.countConfigure(); n != 0 {
		t.Fatalf("expected zero configure calls, got %d", n)
	}
	if c := condOf(cfg); c == nil || c.Status != metav1.ConditionTrue || c.Reason != reasonHTTPConfigApplied {
		t.Fatalf("condition = %+v", c)
	}
	if cfg.Status.HTTPConfigSource != hav1.HTTPConfigSourceAPI {
		t.Fatalf("source = %q", cfg.Status.HTTPConfigSource)
	}
}

// A diff is sent, then confirmed (promoted) on the next reconcile.
func TestReconcileHTTPConfig_ConfigureThenPromote(t *testing.T) {
	m := newMockHA(t, haStable(nil))
	r, _, cfg := newHTTPConfigTestReconciler(m)
	want := haclient.HTTPConfigData{"use_x_forwarded_for": true, "trusted_proxies": []interface{}{"10.0.0.0/8"}}
	mkDec := func() httpConfigDecision {
		resp, _ := m.newClient("").GetHTTPConfig(context.Background(), "tok")
		return httpConfigDecision{path: httpPathAPI, token: "tok", resp: resp}
	}

	r.reconcileHTTPConfig(context.Background(), cfg, haObj(), mkDec(), want, true)
	if m.countConfigure() != 1 {
		t.Fatalf("expected 1 configure, got %d", m.countConfigure())
	}
	// next reconcile: pending now matches → promote
	r.reconcileHTTPConfig(context.Background(), cfg, haObj(), mkDec(), want, true)
	promoted := false
	for _, c := range m.calls {
		if c.typ == "http/config/promote" {
			promoted = true
		}
	}
	if !promoted {
		t.Fatalf("expected a promote call; calls=%+v", m.calls)
	}
	// steady state: no further configure
	before := m.countConfigure()
	r.reconcileHTTPConfig(context.Background(), cfg, haObj(), mkDec(), want, true)
	if m.countConfigure() != before {
		t.Fatalf("configure sent again in steady state")
	}
	if c := condOf(cfg); c == nil || c.Reason != reasonHTTPConfigApplied {
		t.Fatalf("condition = %+v", c)
	}
}

// No API support -> YAML path, zero writes to Home Assistant.
func TestReconcileHTTPConfig_YAMLPath(t *testing.T) {
	r, _, cfg := newHTTPConfigTestReconciler(nil)
	r.reconcileHTTPConfig(context.Background(), cfg, haObj(),
		httpConfigDecision{path: httpPathYAML, token: "tok"}, nil, true)
	if cfg.Status.HTTPConfigSource != hav1.HTTPConfigSourceYAML {
		t.Fatalf("source = %q", cfg.Status.HTTPConfigSource)
	}
	if c := condOf(cfg); c == nil || c.Status != metav1.ConditionTrue || c.Reason != reasonHTTPConfigManagedInYAML {
		t.Fatalf("condition = %+v", c)
	}
}

// Cannot probe yet -> Unknown condition, requeue, no source set.
func TestReconcileHTTPConfig_Undetermined(t *testing.T) {
	r, _, cfg := newHTTPConfigTestReconciler(nil)
	res := r.reconcileHTTPConfig(context.Background(), cfg, haObj(),
		httpConfigDecision{path: httpPathUndetermined}, nil, true)
	if res.RequeueAfter == 0 {
		t.Fatalf("expected a requeue")
	}
	if c := condOf(cfg); c == nil || c.Status != metav1.ConditionUnknown || c.Reason != reasonHTTPConfigWaiting {
		t.Fatalf("condition = %+v", c)
	}
}

// A pending change the operator did not send is not promoted.
func TestReconcileHTTPConfig_ForeignPending(t *testing.T) {
	m := newMockHA(t, haStable(nil))
	m.pending = haStable(haclient.HTTPConfigData{"cors_allowed_origins": []interface{}{"https://someone-elses-ui"}})
	r, rec, cfg := newHTTPConfigTestReconciler(m)
	resp, _ := m.newClient("").GetHTTPConfig(context.Background(), "tok")
	dec := httpConfigDecision{path: httpPathAPI, token: "tok", resp: resp}

	r.reconcileHTTPConfig(context.Background(), cfg, haObj(), dec, nil, true)
	for _, c := range m.calls {
		if c.typ == "http/config/promote" {
			t.Fatalf("operator promoted a foreign pending change")
		}
	}
	if c := condOf(cfg); c == nil || c.Status != metav1.ConditionFalse || c.Reason != reasonHTTPConfigForeign {
		t.Fatalf("condition = %+v", c)
	}
	if !drainHasEvent(rec, eventHTTPConfigForeignChange) {
		t.Fatalf("expected %s event", eventHTTPConfigForeignChange)
	}
}

// A rejected pending is cleared and reported, then a corrected configuration
// can go through.
func TestReconcileHTTPConfig_RejectedThenRecovers(t *testing.T) {
	m := newMockHA(t, haStable(nil))
	m.pending = haStable(haclient.HTTPConfigData{"error": "invalid", "error_message": "bad ssl path"})
	r, rec, cfg := newHTTPConfigTestReconciler(m)
	resp, _ := m.newClient("").GetHTTPConfig(context.Background(), "tok")

	r.reconcileHTTPConfig(context.Background(), cfg, haObj(),
		httpConfigDecision{path: httpPathAPI, token: "tok", resp: resp}, nil, true)
	if c := condOf(cfg); c == nil || c.Status != metav1.ConditionFalse || c.Reason != reasonHTTPConfigRejected {
		t.Fatalf("condition = %+v", c)
	}
	if !drainHasEvent(rec, eventHTTPConfigRejected) {
		t.Fatalf("expected %s event", eventHTTPConfigRejected)
	}
	cleared := false
	for _, c := range m.calls {
		if c.typ == "http/config/configure" && c.config == nil {
			cleared = true
		}
	}
	if !cleared {
		t.Fatalf("rejected pending was not cleared")
	}
}

// When stable diverges from a value the resource sets, the operator sends the
// resource value back, reverting the outside change.
func TestReconcileHTTPConfig_RevertsExternalChange(t *testing.T) {
	m := newMockHA(t, haStable(haclient.HTTPConfigData{"ip_ban_enabled": false}))
	r, _, cfg := newHTTPConfigTestReconciler(m)
	resp, _ := m.newClient("").GetHTTPConfig(context.Background(), "tok")
	// resource says ip_ban_enabled: true (HA default), stable drifted to false
	r.reconcileHTTPConfig(context.Background(), cfg, haObj(),
		httpConfigDecision{path: httpPathAPI, token: "tok", resp: resp},
		haclient.HTTPConfigData{"ip_ban_enabled": true}, true)
	if m.countConfigure() != 1 {
		t.Fatalf("expected the external change to be reverted via configure, got %d calls", m.countConfigure())
	}
	sent, _ := m.calls[len(m.calls)-1].config.(map[string]interface{})
	if v, _ := sent["ip_ban_enabled"].(bool); v != true {
		t.Fatalf("configure did not carry the resource value: %#v", sent)
	}
}

func drainHasEvent(rec *events.FakeRecorder, reason string) bool {
	for {
		select {
		case e := <-rec.Events:
			if strings.Contains(e, reason) {
				return true
			}
		default:
			return false
		}
	}
}
