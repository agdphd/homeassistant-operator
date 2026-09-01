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
	"fmt"
	"net"

	"gopkg.in/yaml.v3"

	"github.com/przemekhys/homeassistant-operator/internal/haclient"
)

// httpKnownKeys are the http: options the operator recognises. Anything outside
// this set that Home Assistant reports (e.g. an option added in a newer HA) is
// passed through a merge untouched rather than dropped to its default.
var httpKnownKeys = map[string]struct{}{
	"server_host":              {},
	"server_port":              {},
	"ssl_certificate":          {},
	"ssl_peer_certificate":     {},
	"ssl_key":                  {},
	"cors_allowed_origins":     {},
	"use_x_forwarded_for":      {},
	"trusted_proxies":          {},
	"login_attempts_threshold": {},
	"ip_ban_enabled":           {},
	"ssl_profile":              {},
	"use_x_frame_options":      {},
}

// readHTTPSection extracts the http: section from generated configuration.yaml as
// a plain map. readable is false when the section exists but is not a plain
// mapping — e.g. `http: !include http.yaml` — which the operator cannot migrate
// to the API and must leave in place. An absent section is readable with nil data.
func readHTTPSection(configYAML string) (data haclient.HTTPConfigData, readable bool, err error) {
	doc, err := parseConfigYAML(configYAML)
	if err != nil {
		return nil, false, err
	}
	if len(doc.Content) == 0 {
		return nil, true, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, true, nil
	}
	node := nodeMappingValue(root, "http")
	if node == nil {
		return nil, true, nil
	}
	if node.Kind == yaml.ScalarNode && node.Value == "" && (node.Tag == "" || node.Tag == "!!null") {
		return nil, true, nil
	}
	if node.Kind != yaml.MappingNode {
		// Tagged scalar / external include — not something the operator can read.
		return nil, false, nil
	}
	var m map[string]interface{}
	if err := node.Decode(&m); err != nil {
		return nil, false, fmt.Errorf("failed to decode http: section: %w", err)
	}
	return haclient.HTTPConfigData(m), true, nil
}

// stripHTTPSection removes the http: key from generated configuration.yaml. Used
// on the API path so Home Assistant does not report a leftover-http-block repair
// issue. A missing or unreadable section is returned unchanged.
func stripHTTPSection(configYAML string) (string, error) {
	doc, err := parseConfigYAML(configYAML)
	if err != nil {
		return configYAML, err
	}
	if len(doc.Content) == 0 {
		return configYAML, nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return configYAML, nil
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "http" {
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			out, err := yaml.Marshal(doc)
			if err != nil {
				return configYAML, fmt.Errorf("failed to marshal configuration YAML after removing http: %w", err)
			}
			return string(out), nil
		}
	}
	return configYAML, nil
}

// canonicalizeTrustedProxies rewrites the trusted_proxies entries of data into
// the network/mask form Home Assistant stores them in (a bare address becomes
// address/32 or address/128). Comparing raw strings against HA's normalised
// values would otherwise report a permanent difference and loop writes forever.
// Entry order is preserved.
func canonicalizeTrustedProxies(data haclient.HTTPConfigData) {
	raw, ok := data["trusted_proxies"]
	if !ok {
		return
	}
	list, ok := toStringList(raw)
	if !ok {
		return
	}
	out := make([]interface{}, 0, len(list))
	for _, entry := range list {
		out = append(out, canonicalTrustedProxy(entry))
	}
	data["trusted_proxies"] = out
}

func canonicalTrustedProxy(entry string) string {
	if _, ipnet, err := net.ParseCIDR(entry); err == nil {
		return ipnet.String()
	}
	if ip := net.ParseIP(entry); ip != nil {
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		return (&net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}).String()
	}
	return entry
}

// toStringList coerces a YAML-decoded value (which may be []interface{} or
// []string) into []string.
func toStringList(v interface{}) ([]string, bool) {
	switch t := v.(type) {
	case []string:
		return t, true
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, e := range t {
			s, ok := e.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}
