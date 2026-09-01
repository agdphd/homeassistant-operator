package haclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrHTTPConfigUnsupported indicates the running Home Assistant does not expose
// the http config WebSocket API (added in HA 2026.8). Callers should fall back to
// managing the http: section through configuration.yaml. This is an expected
// state on older Home Assistant, not a failure.
var ErrHTTPConfigUnsupported = errors.New(
	"home assistant does not support the http config API (needs 2026.8+)")

// httpConfigMetadataKeys are fields Home Assistant attaches to stored config that
// describe the write, not the desired state. They must be dropped before any
// comparison — created_at in particular changes on every write, so comparing it
// would loop forever. base_url is silently removed by HA and never sent.
var httpConfigMetadataKeys = []string{"created_at", "error", "error_message", "base_url"}

// HTTPConfigData is Home Assistant's http configuration as a free-form map. Using
// a map (rather than a struct) is deliberate: a merge can pass through keys the
// operator does not recognise — e.g. options added in a newer Home Assistant —
// untouched, instead of dropping them back to their default (a configure write
// is a full replacement, not a patch).
type HTTPConfigData map[string]interface{}

// HTTPConfigResponse is the result of the http/config command.
type HTTPConfigResponse struct {
	// Stable is the applied (promoted) configuration.
	Stable HTTPConfigData `json:"stable"`
	// Pending is a configuration awaiting confirmation, or nil. It auto-reverts
	// after AUTO_REVERT_DELAY (5 minutes) unless promoted.
	Pending HTTPConfigData `json:"pending"`
	// RevertAt is the ISO-8601 deadline at which Pending auto-reverts, or nil.
	RevertAt *string `json:"revert_at"`
	// ActiveConfigType is the slot the running server started from: "stable",
	// "pending", "default" or "default_legacy_port". A "default*" value means
	// Home Assistant fell back — the operator's config is not in effect.
	ActiveConfigType string `json:"active_config_type"`
	// Default is Home Assistant's own built-in default configuration. The
	// operator uses it as the base layer of the desired state instead of
	// hard-coding HA's schema defaults.
	Default HTTPConfigData `json:"default"`
}

// PendingError returns the error message Home Assistant recorded on a rejected
// pending configuration, or "" when there is none.
func (r *HTTPConfigResponse) PendingError() string {
	if r == nil || r.Pending == nil {
		return ""
	}
	if msg, ok := r.Pending["error_message"].(string); ok && msg != "" {
		return msg
	}
	if code, ok := r.Pending["error"].(string); ok && code != "" {
		return code
	}
	return ""
}

// StrippedMetadata returns a copy of the data with the metadata keys removed, so
// two configurations can be compared on their meaningful fields only.
func (d HTTPConfigData) StrippedMetadata() HTTPConfigData {
	if d == nil {
		return nil
	}
	out := make(HTTPConfigData, len(d))
	for k, v := range d {
		out[k] = v
	}
	for _, k := range httpConfigMetadataKeys {
		delete(out, k)
	}
	return out
}

// GetHTTPConfig reads the current http configuration from Home Assistant.
// Returns ErrHTTPConfigUnsupported (wrapped) when the API is absent.
func (c *Client) GetHTTPConfig(ctx context.Context, token string) (*HTTPConfigResponse, error) {
	result, code, err := c.sendWebSocketCommandWithCode(ctx, token, "http/config", nil)
	if err != nil {
		if code == "unknown_command" || code == "not_supported" {
			return nil, fmt.Errorf("%w: %v", ErrHTTPConfigUnsupported, err)
		}
		return nil, err
	}
	var resp HTTPConfigResponse
	if err := json.Unmarshal(result, &resp); err != nil {
		return nil, &Error{Type: ErrorTypeInvalidResponse, Message: "failed to parse http/config response", Err: err}
	}
	return &resp, nil
}

// ConfigureHTTPConfig writes a new http configuration as pending. Passing a nil
// config sends an explicit empty value, which clears a stuck pending
// configuration (the documented recovery path). Returns whether Home Assistant
// will restart its own process to apply the change.
func (c *Client) ConfigureHTTPConfig(ctx context.Context, token string, config HTTPConfigData) (bool, error) {
	// The command schema requires the "config" key to be present; a nil map
	// marshals to JSON null, which is exactly the clear-pending signal.
	var payload interface{}
	if config != nil {
		payload = map[string]interface{}(config)
	}
	result, code, err := c.sendWebSocketCommandWithCode(ctx, token, "http/config/configure", map[string]interface{}{
		"config": payload,
	})
	if err != nil {
		if code == "unknown_command" || code == "not_supported" {
			return false, fmt.Errorf("%w: %v", ErrHTTPConfigUnsupported, err)
		}
		return false, err
	}
	var out struct {
		Restart bool `json:"restart"`
	}
	if len(result) > 0 {
		_ = json.Unmarshal(result, &out)
	}
	return out.Restart, nil
}

// PromoteHTTPConfig confirms the pending configuration, making it stable and
// stopping the auto-revert timer.
func (c *Client) PromoteHTTPConfig(ctx context.Context, token string) error {
	_, code, err := c.sendWebSocketCommandWithCode(ctx, token, "http/config/promote", nil)
	if err != nil {
		if code == "unknown_command" || code == "not_supported" {
			return fmt.Errorf("%w: %v", ErrHTTPConfigUnsupported, err)
		}
		return err
	}
	return nil
}
