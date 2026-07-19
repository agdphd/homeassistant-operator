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
			name: "native TLS with issuer is valid",
			spec: hav1.HomeAssistantSpec{Alpha: &hav1.AlphaSpec{TLS: &hav1.TLSAlphaSpec{
				Native: &hav1.NativeTLSAlphaSpec{Enabled: true, IssuerRef: &hav1.IssuerReference{Name: "i"}},
			}}},
		},
		{
			name: "native TLS without issuer or secret is rejected",
			spec: hav1.HomeAssistantSpec{Alpha: &hav1.AlphaSpec{TLS: &hav1.TLSAlphaSpec{
				Native: &hav1.NativeTLSAlphaSpec{Enabled: true},
			}}},
			wantErrs: 1,
		},
		{
			name: "native TLS with issuer AND secret warns",
			spec: hav1.HomeAssistantSpec{Alpha: &hav1.AlphaSpec{TLS: &hav1.TLSAlphaSpec{
				Native: &hav1.NativeTLSAlphaSpec{Enabled: true, IssuerRef: &hav1.IssuerReference{Name: "i"}, SecretName: "s"},
			}}},
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
			spec: hav1.HomeAssistantSpec{Alpha: &hav1.AlphaSpec{TLS: &hav1.TLSAlphaSpec{
				Native: &hav1.NativeTLSAlphaSpec{Enabled: true, IssuerRef: &hav1.IssuerReference{Name: "i", Kind: "Bogus"}},
			}}},
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

func TestValidatorRejectsAndAccepts(t *testing.T) {
	v := &HomeAssistantCustomValidator{}

	bad := &hav1.HomeAssistant{
		ObjectMeta: metav1.ObjectMeta{Name: "home"},
		Spec: hav1.HomeAssistantSpec{Alpha: &hav1.AlphaSpec{TLS: &hav1.TLSAlphaSpec{
			Native: &hav1.NativeTLSAlphaSpec{Enabled: true},
		}}},
	}
	if _, err := v.ValidateCreate(context.Background(), bad); err == nil {
		t.Fatal("expected ValidateCreate to reject native TLS without issuer/secret")
	}

	good := &hav1.HomeAssistant{
		ObjectMeta: metav1.ObjectMeta{Name: "home"},
		Spec: hav1.HomeAssistantSpec{Alpha: &hav1.AlphaSpec{TLS: &hav1.TLSAlphaSpec{
			Native: &hav1.NativeTLSAlphaSpec{Enabled: true, IssuerRef: &hav1.IssuerReference{Name: "i"}},
		}}},
	}
	if _, err := v.ValidateCreate(context.Background(), good); err != nil {
		t.Fatalf("expected valid HomeAssistant to be accepted, got %v", err)
	}
}
