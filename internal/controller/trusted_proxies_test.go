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
	"strings"
	"testing"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

func TestInjectTrustedProxies(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		ha          *hav1.HomeAssistant
		wantOutcome trustedProxiesOutcome
		wantIn      []string
		wantNotIn   []string
		wantErr     bool
	}{
		{
			name:        "nil HomeAssistant — not exposed, input unchanged",
			input:       "homeassistant:\n  name: Test\n",
			ha:          nil,
			wantOutcome: trustedProxiesNotExposed,
			wantIn:      []string{"homeassistant:"},
			wantNotIn:   []string{"trusted_proxies"},
		},
		{
			name:        "neither Ingress nor Gateway enabled — not exposed, nothing injected",
			input:       "homeassistant:\n  name: Test\n",
			ha:          &hav1.HomeAssistant{},
			wantOutcome: trustedProxiesNotExposed,
			wantNotIn:   []string{"trusted_proxies", "use_x_forwarded_for"},
		},
		{
			name:  "Ingress enabled, no trusted_proxies set — defaults injected",
			input: "homeassistant:\n  name: Test\n",
			ha: &hav1.HomeAssistant{Spec: hav1.HomeAssistantSpec{
				Ingress: &hav1.IngressSpec{Enabled: true},
			}},
			wantOutcome: trustedProxiesApplied,
			wantIn: []string{
				"use_x_forwarded_for: true",
				"trusted_proxies:",
				"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
			},
		},
		{
			name:  "Gateway enabled, no trusted_proxies set — defaults injected",
			input: "homeassistant:\n  name: Test\n",
			ha: &hav1.HomeAssistant{Spec: hav1.HomeAssistantSpec{
				Gateway: &hav1.GatewaySpec{Enabled: true},
			}},
			wantOutcome: trustedProxiesApplied,
			wantIn: []string{
				"use_x_forwarded_for: true",
				"trusted_proxies:",
				"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
			},
		},
		{
			name:  "Ingress disabled explicitly — nothing injected",
			input: "homeassistant:\n  name: Test\n",
			ha: &hav1.HomeAssistant{Spec: hav1.HomeAssistantSpec{
				Ingress: &hav1.IngressSpec{Enabled: false},
			}},
			wantOutcome: trustedProxiesNotExposed,
			wantNotIn:   []string{"trusted_proxies", "use_x_forwarded_for"},
		},
		{
			name:  "opt-out set — exposed but nothing injected",
			input: "homeassistant:\n  name: Test\n",
			ha: &hav1.HomeAssistant{Spec: hav1.HomeAssistantSpec{
				Ingress:                      &hav1.IngressSpec{Enabled: true},
				DisableDefaultTrustedProxies: true,
			}},
			wantOutcome: trustedProxiesOptedOut,
			wantNotIn:   []string{"trusted_proxies", "use_x_forwarded_for"},
		},
		{
			name:  "user already set both keys — preserved, outcome userManaged",
			input: "http:\n  use_x_forwarded_for: false\n  trusted_proxies:\n    - 203.0.113.0/24\n",
			ha: &hav1.HomeAssistant{Spec: hav1.HomeAssistantSpec{
				Ingress: &hav1.IngressSpec{Enabled: true},
			}},
			wantOutcome: trustedProxiesUserManaged,
			wantIn:      []string{"use_x_forwarded_for: false", "203.0.113.0/24"},
			wantNotIn:   []string{"10.0.0.0/8"},
		},
		{
			name:  "user set trusted_proxies only — use_x_forwarded_for still filled in, outcome applied",
			input: "http:\n  trusted_proxies:\n    - 203.0.113.0/24\n",
			ha: &hav1.HomeAssistant{Spec: hav1.HomeAssistantSpec{
				Ingress: &hav1.IngressSpec{Enabled: true},
			}},
			wantOutcome: trustedProxiesApplied,
			wantIn:      []string{"use_x_forwarded_for: true", "203.0.113.0/24"},
			wantNotIn:   []string{"10.0.0.0/8"},
		},
		{
			name:  "tagged http scalar (!include) preserved untouched",
			input: "http: !include http.yaml\n",
			ha: &hav1.HomeAssistant{Spec: hav1.HomeAssistantSpec{
				Ingress: &hav1.IngressSpec{Enabled: true},
			}},
			wantOutcome: trustedProxiesUserManaged,
			wantIn:      []string{"http: !include http.yaml"},
		},
		{
			// Regression: "http:" with no value parses as a null scalar node, not
			// a mapping node — must be converted in place rather than mistaken
			// for a tagged scalar like "http: !include ...".
			name:  "null http value (bare 'http:') is converted into a mapping",
			input: "http:\n",
			ha: &hav1.HomeAssistant{Spec: hav1.HomeAssistantSpec{
				Ingress: &hav1.IngressSpec{Enabled: true},
			}},
			wantOutcome: trustedProxiesApplied,
			wantIn: []string{
				"use_x_forwarded_for: true",
				"trusted_proxies:",
				"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, outcome, err := injectTrustedProxies(tt.input, tt.ha)
			if (err != nil) != tt.wantErr {
				t.Fatalf("injectTrustedProxies() error = %v, wantErr %v", err, tt.wantErr)
			}
			if outcome != tt.wantOutcome {
				t.Errorf("injectTrustedProxies() outcome = %v, want %v", outcome, tt.wantOutcome)
			}
			for _, want := range tt.wantIn {
				if !strings.Contains(got, want) {
					t.Errorf("expected %q in output:\n%s", want, got)
				}
			}
			for _, notWant := range tt.wantNotIn {
				if strings.Contains(got, notWant) {
					t.Errorf("did not expect %q in output:\n%s", notWant, got)
				}
			}
		})
	}
}

func TestInjectTrustedProxies_Idempotent(t *testing.T) {
	ha := &hav1.HomeAssistant{Spec: hav1.HomeAssistantSpec{
		Ingress: &hav1.IngressSpec{Enabled: true},
	}}
	input := "homeassistant:\n  name: Test\n"

	first, firstOutcome, err := injectTrustedProxies(input, ha)
	if err != nil {
		t.Fatalf("injectTrustedProxies() first pass error = %v", err)
	}
	if firstOutcome != trustedProxiesApplied {
		t.Fatalf("first pass outcome = %v, want %v", firstOutcome, trustedProxiesApplied)
	}

	second, secondOutcome, err := injectTrustedProxies(first, ha)
	if err != nil {
		t.Fatalf("injectTrustedProxies() second pass error = %v", err)
	}
	if secondOutcome != trustedProxiesUserManaged {
		t.Errorf("second pass outcome = %v, want %v (values already present)", secondOutcome, trustedProxiesUserManaged)
	}
	if first != second {
		t.Errorf("injectTrustedProxies is not idempotent:\nfirst:  %q\nsecond: %q", first, second)
	}
}

func TestTrustedProxiesStatusMessage(t *testing.T) {
	applied := true
	notApplied := false

	tests := []struct {
		name string
		ha   *hav1.HomeAssistant
		cfg  *hav1.HomeAssistantConfiguration
		want string
	}{
		{
			name: "opt-out wins regardless of config status",
			ha:   &hav1.HomeAssistant{Spec: hav1.HomeAssistantSpec{DisableDefaultTrustedProxies: true}},
			cfg: &hav1.HomeAssistantConfiguration{
				Status: hav1.HomeAssistantConfigurationStatus{TrustedProxiesDefaulted: &applied},
			},
			want: "default trusted proxies disabled (opt-out)",
		},
		{
			name: "nil HomeAssistantConfiguration — pending, not user-configured",
			ha:   &hav1.HomeAssistant{},
			cfg:  nil,
			want: "trusted proxies status pending (HomeAssistantConfiguration not yet reconciled)",
		},
		{
			name: "HomeAssistantConfiguration exists but never reconciled — pending",
			ha:   &hav1.HomeAssistant{},
			cfg:  &hav1.HomeAssistantConfiguration{},
			want: "trusted proxies status pending (HomeAssistantConfiguration not yet reconciled)",
		},
		{
			name: "reconciled and applied",
			ha:   &hav1.HomeAssistant{},
			cfg: &hav1.HomeAssistantConfiguration{
				Status: hav1.HomeAssistantConfigurationStatus{TrustedProxiesDefaulted: &applied},
			},
			want: "default trusted proxies applied",
		},
		{
			name: "reconciled and user-managed",
			ha:   &hav1.HomeAssistant{},
			cfg: &hav1.HomeAssistantConfiguration{
				Status: hav1.HomeAssistantConfigurationStatus{TrustedProxiesDefaulted: &notApplied},
			},
			want: "using user-configured trusted proxies",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trustedProxiesStatusMessage(tt.ha, tt.cfg)
			if got != tt.want {
				t.Errorf("trustedProxiesStatusMessage() = %q, want %q", got, tt.want)
			}
		})
	}
}
