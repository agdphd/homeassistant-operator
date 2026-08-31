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

package v1

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

func TestValidateHomeAssistantTLS(t *testing.T) {
	tests := []struct {
		name         string
		spec         hav1.HomeAssistantSpec
		wantErrs     int
		wantWarnings int
	}{
		{
			name: "empty spec is valid",
			spec: hav1.HomeAssistantSpec{},
		},
		{
			name: "ingress TLS with issuer is valid",
			spec: hav1.HomeAssistantSpec{Ingress: &hav1.IngressSpec{
				Enabled: true,
				TLS:     &hav1.IngressTLSSpec{Enabled: true, IssuerRef: &hav1.IssuerReference{Name: "i"}},
			}},
		},
		{
			name: "ingress TLS with issuer AND secret warns",
			spec: hav1.HomeAssistantSpec{Ingress: &hav1.IngressSpec{
				Enabled: true,
				TLS: &hav1.IngressTLSSpec{
					Enabled: true, SecretName: "s", IssuerRef: &hav1.IssuerReference{Name: "i"},
				},
			}},
			wantWarnings: 1,
		},
		{
			name: "gateway without host is rejected",
			spec: hav1.HomeAssistantSpec{Gateway: &hav1.GatewaySpec{
				Enabled: true, ParentRef: &hav1.GatewayParentRef{Name: "gw"},
			}},
			wantErrs: 1,
		},
		{
			name: "gateway with host and parentRef is valid",
			spec: hav1.HomeAssistantSpec{Gateway: &hav1.GatewaySpec{
				Enabled: true, Host: "ha.example.com",
				ParentRef: &hav1.GatewayParentRef{Name: "gw"},
			}},
		},
		{
			name:     "gateway enabled without attach point is rejected",
			spec:     hav1.HomeAssistantSpec{Gateway: &hav1.GatewaySpec{Enabled: true, Host: "ha.example.com"}},
			wantErrs: 1,
		},
		{
			name: "ingress tls without secret or issuer is rejected",
			spec: hav1.HomeAssistantSpec{Ingress: &hav1.IngressSpec{
				Enabled: true, TLS: &hav1.IngressTLSSpec{Enabled: true},
			}},
			wantErrs: 1,
		},
		{
			name: "invalid issuer kind is rejected",
			spec: hav1.HomeAssistantSpec{Ingress: &hav1.IngressSpec{
				Enabled: true,
				TLS: &hav1.IngressTLSSpec{
					Enabled: true, IssuerRef: &hav1.IssuerReference{Name: "i", Kind: "Bogus"},
				},
			}},
			wantErrs: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			warnings, errs := validateHomeAssistantTLS(&tc.spec)
			if len(errs) != tc.wantErrs {
				t.Fatalf("errs = %d (%v), want %d", len(errs), errs, tc.wantErrs)
			}
			if len(warnings) != tc.wantWarnings {
				t.Fatalf("warnings = %d (%v), want %d", len(warnings), warnings, tc.wantWarnings)
			}
		})
	}
}

func TestValidateGatewayFilters(t *testing.T) {
	str := func(s string) *string { return &s }
	code := func(i int) *int { return &i }

	tests := []struct {
		name     string
		filters  []hav1.HTTPRouteFilter
		wantErrs int
	}{
		{name: "no filters is valid"},
		{
			name: "unknown type is rejected",
			filters: []hav1.HTTPRouteFilter{
				{Type: "RequestMirror"},
			},
			wantErrs: 1,
		},
		{
			name: "missing sub-object for declared type is rejected",
			filters: []hav1.HTTPRouteFilter{
				{Type: "RequestHeaderModifier"},
			},
			wantErrs: 1,
		},
		{
			name: "sub-object for a different type is rejected",
			filters: []hav1.HTTPRouteFilter{
				{
					Type:                   "RequestHeaderModifier",
					ResponseHeaderModifier: &hav1.HTTPHeaderFilter{Set: []hav1.HTTPHeader{{Name: "a", Value: "b"}}},
				},
			},
			wantErrs: 2, // wrong field set + declared type's own field missing
		},
		{
			name: "empty header filter is rejected",
			filters: []hav1.HTTPRouteFilter{
				{Type: "RequestHeaderModifier", RequestHeaderModifier: &hav1.HTTPHeaderFilter{}},
			},
			wantErrs: 1,
		},
		{
			name: "valid header filter is accepted",
			filters: []hav1.HTTPRouteFilter{
				{Type: "RequestHeaderModifier", RequestHeaderModifier: &hav1.HTTPHeaderFilter{Remove: []string{"X-Debug"}}},
			},
		},
		{
			name: "empty redirect filter is rejected",
			filters: []hav1.HTTPRouteFilter{
				{Type: "RequestRedirect", RequestRedirect: &hav1.HTTPRequestRedirectFilter{}},
			},
			wantErrs: 1,
		},
		{
			name: "valid redirect filter is accepted",
			filters: []hav1.HTTPRouteFilter{
				{
					Type: "RequestRedirect",
					RequestRedirect: &hav1.HTTPRequestRedirectFilter{
						Scheme: str("https"), StatusCode: code(301),
					},
				},
			},
		},
		{
			name: "empty url rewrite filter is rejected",
			filters: []hav1.HTTPRouteFilter{
				{Type: "URLRewrite", URLRewrite: &hav1.HTTPURLRewriteFilter{}},
			},
			wantErrs: 1,
		},
		{
			name: "valid url rewrite filter is accepted",
			filters: []hav1.HTTPRouteFilter{
				{Type: "URLRewrite", URLRewrite: &hav1.HTTPURLRewriteFilter{Hostname: str("new.example.com")}},
			},
		},
		{
			name: "path modifier missing its matching field is rejected",
			filters: []hav1.HTTPRouteFilter{
				{Type: "URLRewrite", URLRewrite: &hav1.HTTPURLRewriteFilter{
					Path: &hav1.HTTPPathModifier{Type: "ReplaceFullPath"},
				}},
			},
			wantErrs: 1,
		},
		{
			name: "valid path modifier is accepted",
			filters: []hav1.HTTPRouteFilter{
				{Type: "URLRewrite", URLRewrite: &hav1.HTTPURLRewriteFilter{
					Path: &hav1.HTTPPathModifier{Type: "ReplacePrefixMatch", ReplacePrefixMatch: str("/")},
				}},
			},
		},
		{
			name: "response header modifier valid",
			filters: []hav1.HTTPRouteFilter{
				{Type: "ResponseHeaderModifier", ResponseHeaderModifier: &hav1.HTTPHeaderFilter{
					Set: []hav1.HTTPHeader{{Name: "X-Frame-Options", Value: "SAMEORIGIN"}},
				}},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := &hav1.HomeAssistantSpec{Gateway: &hav1.GatewaySpec{Filters: tc.filters}}
			errs := validateGatewayFilters(spec)
			if len(errs) != tc.wantErrs {
				t.Fatalf("errs = %d (%v), want %d", len(errs), errs, tc.wantErrs)
			}
		})
	}
}

func TestValidateDevices(t *testing.T) {
	tests := []struct {
		name     string
		devices  []hav1.DevicePassthroughEntry
		wantErrs int
	}{
		{name: "no devices is valid"},
		{
			name:     "empty hostPath is rejected",
			devices:  []hav1.DevicePassthroughEntry{{HostPath: ""}},
			wantErrs: 1,
		},
		{
			name:     "relative hostPath is rejected",
			devices:  []hav1.DevicePassthroughEntry{{HostPath: "relative/path"}},
			wantErrs: 1,
		},
		{
			name:     "hostPath outside /dev is rejected",
			devices:  []hav1.DevicePassthroughEntry{{HostPath: "/etc/passwd"}},
			wantErrs: 1,
		},
		{
			name:     "hostPath with .. traversal is rejected",
			devices:  []hav1.DevicePassthroughEntry{{HostPath: "/dev/../etc/passwd"}},
			wantErrs: 1,
		},
		{
			name:     "malformed containerPath is rejected",
			devices:  []hav1.DevicePassthroughEntry{{HostPath: "/dev/ttyACM0", ContainerPath: "relative"}},
			wantErrs: 1,
		},
		{
			name: "duplicate hostPath is rejected",
			devices: []hav1.DevicePassthroughEntry{
				{HostPath: "/dev/ttyACM0"},
				{HostPath: "/dev/ttyACM0"},
			},
			// Both hostPath and its defaulted effective containerPath collide.
			wantErrs: 2,
		},
		{
			name: "two distinct valid devices are accepted",
			devices: []hav1.DevicePassthroughEntry{
				{HostPath: "/dev/ttyACM0"},
				{HostPath: "/dev/ttyACM1", ContainerPath: "/dev/zigbee"},
			},
		},
		{
			name: "duplicate explicit containerPath is rejected",
			devices: []hav1.DevicePassthroughEntry{
				{HostPath: "/dev/ttyACM0", ContainerPath: "/dev/zigbee"},
				{HostPath: "/dev/ttyACM1", ContainerPath: "/dev/zigbee"},
			},
			wantErrs: 1,
		},
		{
			name: "omitted containerPath colliding with another device's explicit containerPath is rejected",
			devices: []hav1.DevicePassthroughEntry{
				{HostPath: "/dev/ttyACM0"},
				{HostPath: "/dev/ttyACM1", ContainerPath: "/dev/ttyACM0"},
			},
			wantErrs: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := &hav1.HomeAssistantSpec{Alpha: &hav1.AlphaSpec{Devices: tc.devices}}
			errs := validateDevices(spec)
			if len(errs) != tc.wantErrs {
				t.Fatalf("errs = %d (%v), want %d", len(errs), errs, tc.wantErrs)
			}
		})
	}
}

func TestValidateNodeSelector(t *testing.T) {
	tests := []struct {
		name         string
		nodeSelector map[string]string
		wantErrs     int
	}{
		{name: "no nodeSelector is valid"},
		{
			name:         "valid nodeSelector is accepted",
			nodeSelector: map[string]string{"ha-device-node": "zigbee"},
		},
		{
			name:         "valid prefixed key is accepted",
			nodeSelector: map[string]string{"example.com/ha-device-node": "zigbee"},
		},
		{
			name:         "key containing a space is rejected",
			nodeSelector: map[string]string{"ha device node": "zigbee"},
			wantErrs:     1,
		},
		{
			name:         "value starting with a hyphen is rejected",
			nodeSelector: map[string]string{"ha-device-node": "-zigbee"},
			wantErrs:     1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spec := &hav1.HomeAssistantSpec{}
			if tc.nodeSelector != nil {
				spec.Scheduling = &hav1.SchedulingSpec{NodeSelector: tc.nodeSelector}
			}
			errs := validateNodeSelector(spec)
			if len(errs) != tc.wantErrs {
				t.Fatalf("errs = %d (%v), want %d", len(errs), errs, tc.wantErrs)
			}
		})
	}
}

func TestValidatorRejectsAndAccepts(t *testing.T) {
	v := &HomeAssistantCustomValidator{}

	bad := &hav1.HomeAssistant{
		ObjectMeta: metav1.ObjectMeta{Name: "home"},
		Spec: hav1.HomeAssistantSpec{Ingress: &hav1.IngressSpec{
			Enabled: true, TLS: &hav1.IngressTLSSpec{Enabled: true},
		}},
	}
	if _, err := v.ValidateCreate(context.Background(), bad); err == nil {
		t.Fatal("expected ValidateCreate to reject ingress TLS without issuer/secret")
	}

	good := &hav1.HomeAssistant{
		ObjectMeta: metav1.ObjectMeta{Name: "home"},
		Spec: hav1.HomeAssistantSpec{Ingress: &hav1.IngressSpec{
			Enabled: true,
			TLS:     &hav1.IngressTLSSpec{Enabled: true, IssuerRef: &hav1.IssuerReference{Name: "i"}},
		}},
	}
	if _, err := v.ValidateCreate(context.Background(), good); err != nil {
		t.Fatalf("expected valid HomeAssistant to be accepted, got %v", err)
	}
}
