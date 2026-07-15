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
	"fmt"
	"strings"

	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

// SetupHomeAssistantWebhookWithManager registers the validating webhook for the
// HomeAssistant kind. The webhook validates TLS/cert-manager configuration
// coherence; it deliberately does NOT check cert-manager availability (a runtime
// concern reported via status, not an admission-time rejection).
func SetupHomeAssistantWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &hav1.HomeAssistant{}).
		WithValidator(&HomeAssistantCustomValidator{}).
		Complete()
}

// +kubebuilder:webhook:path=/validate-ha-homeassistant-io-v1-homeassistant,mutating=false,failurePolicy=fail,sideEffects=None,groups=ha.homeassistant.io,resources=homeassistants,verbs=create;update,versions=v1,name=vhomeassistant-v1.kb.io,admissionReviewVersions=v1

// HomeAssistantCustomValidator validates HomeAssistant resources on admission.
type HomeAssistantCustomValidator struct{}

var _ admission.Validator[*hav1.HomeAssistant] = &HomeAssistantCustomValidator{}

func (v *HomeAssistantCustomValidator) ValidateCreate(
	_ context.Context, ha *hav1.HomeAssistant,
) (admission.Warnings, error) {
	return validateHomeAssistant(ha)
}

func (v *HomeAssistantCustomValidator) ValidateUpdate(
	_ context.Context, _, newObj *hav1.HomeAssistant,
) (admission.Warnings, error) {
	return validateHomeAssistant(newObj)
}

func (v *HomeAssistantCustomValidator) ValidateDelete(
	_ context.Context, _ *hav1.HomeAssistant,
) (admission.Warnings, error) {
	return nil, nil
}

func validateHomeAssistant(ha *hav1.HomeAssistant) (admission.Warnings, error) {
	log := logf.Log.WithName("homeassistant-webhook")
	warnings, msgs := validateHomeAssistantTLS(&ha.Spec)
	if len(msgs) > 0 {
		log.Info("rejecting HomeAssistant with invalid TLS configuration", "name", ha.Name, "reasons", msgs)
		return warnings, fmt.Errorf("invalid HomeAssistant %q: %s", ha.Name, strings.Join(msgs, "; "))
	}
	return warnings, nil
}

// validateHomeAssistantTLS applies the TLS/cert-manager coherence rules and returns
// admission warnings plus a list of validation failure messages (empty when valid).
// Kept as a pure function over the spec for straightforward unit testing.
func validateHomeAssistantTLS(spec *hav1.HomeAssistantSpec) (admission.Warnings, []string) {
	var warnings admission.Warnings
	var errs []string

	// Native TLS (alpha) requires an issuer or a bring-your-own Secret.
	if n := nativeTLS(spec); n != nil && n.Enabled {
		if n.IssuerRef == nil && n.SecretName == "" {
			errs = append(errs, "spec.alpha.tls.native requires issuerRef or secretName when enabled")
		}
		if n.IssuerRef != nil && n.SecretName != "" {
			warnings = append(warnings, "spec.alpha.tls.native: secretName (bring-your-own) overrides issuerRef")
		}
		errs = append(errs, validateIssuerKind("spec.alpha.tls.native.issuerRef", n.IssuerRef)...)
	}

	// Gateway exposure requires a host and an attach point.
	if g := spec.Gateway; g != nil && g.Enabled {
		if g.Host == "" {
			errs = append(errs, "spec.gateway requires host when enabled")
		}
		if g.ParentRef == nil && !g.ManageGateway {
			errs = append(errs, "spec.gateway requires parentRef or manageGateway when enabled")
		}
		if g.IssuerRef != nil && g.SecretName != "" {
			warnings = append(warnings, "spec.gateway: secretName (bring-your-own) overrides issuerRef")
		}
		errs = append(errs, validateIssuerKind("spec.gateway.issuerRef", g.IssuerRef)...)
	}

	// Ingress TLS requires a Secret or an issuer to obtain one.
	if i := spec.Ingress; i != nil && i.TLS != nil && i.TLS.Enabled {
		if i.TLS.SecretName == "" && i.TLS.IssuerRef == nil {
			errs = append(errs, "spec.ingress.tls requires secretName or issuerRef when enabled")
		}
		if i.TLS.SecretName != "" && i.TLS.IssuerRef != nil {
			warnings = append(warnings, "spec.ingress.tls: secretName (bring-your-own) overrides issuerRef")
		}
		errs = append(errs, validateIssuerKind("spec.ingress.tls.issuerRef", i.TLS.IssuerRef)...)
	}

	return warnings, errs
}

func validateIssuerKind(path string, ref *hav1.IssuerReference) []string {
	if ref == nil || ref.Kind == "" {
		return nil
	}
	if ref.Kind != "Issuer" && ref.Kind != "ClusterIssuer" {
		return []string{fmt.Sprintf("%s.kind must be Issuer or ClusterIssuer, got %q", path, ref.Kind)}
	}
	return nil
}

func nativeTLS(spec *hav1.HomeAssistantSpec) *hav1.NativeTLSAlphaSpec {
	if spec.Alpha != nil && spec.Alpha.TLS != nil {
		return spec.Alpha.TLS.Native
	}
	return nil
}
