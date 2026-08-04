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

// failurePolicy=ignore keeps a default-on webhook from blocking HomeAssistant
// create/update while it is briefly unavailable (operator restart / rollout);
// validation is still enforced whenever the webhook is serving.
// +kubebuilder:webhook:path=/validate-ha-homeassistant-io-v1-homeassistant,mutating=false,failurePolicy=ignore,sideEffects=None,groups=ha.homeassistant.io,resources=homeassistants,verbs=create;update,versions=v1,name=vhomeassistant-v1.kb.io,admissionReviewVersions=v1

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
	msgs = append(msgs, validateGatewayFilters(&ha.Spec)...)
	msgs = append(msgs, validateDevices(&ha.Spec)...)
	if len(msgs) > 0 {
		log.Info("rejecting HomeAssistant with invalid configuration", "name", ha.Name, "reasons", msgs)
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

// validateGatewayFilters validates spec.gateway.filters: each entry's type
// must be one of the four supported values, exactly the sub-object matching
// that type must be set (and populated with usable content), and a path
// modifier's fields must match its own declared type. Returns one message per
// problem found, empty when valid.
func validateGatewayFilters(spec *hav1.HomeAssistantSpec) []string {
	if spec.Gateway == nil {
		return nil
	}
	var errs []string
	for i, f := range spec.Gateway.Filters {
		errs = append(errs, validateGatewayFilter(i, f)...)
	}
	return errs
}

func validateGatewayFilter(index int, f hav1.HTTPRouteFilter) []string {
	path := fmt.Sprintf("spec.gateway.filters[%d]", index)

	type sub struct {
		typeName string
		field    string
		present  bool
	}
	subs := []sub{
		{"RequestHeaderModifier", "requestHeaderModifier", f.RequestHeaderModifier != nil},
		{"ResponseHeaderModifier", "responseHeaderModifier", f.ResponseHeaderModifier != nil},
		{"RequestRedirect", "requestRedirect", f.RequestRedirect != nil},
		{"URLRewrite", "urlRewrite", f.URLRewrite != nil},
	}

	var expected *sub
	for i := range subs {
		if subs[i].typeName == f.Type {
			expected = &subs[i]
		}
	}
	if expected == nil {
		return []string{fmt.Sprintf(
			"%s.type must be one of RequestHeaderModifier, ResponseHeaderModifier, RequestRedirect, URLRewrite, got %q",
			path, f.Type)}
	}

	var errs []string
	for _, s := range subs {
		if s.present && s.typeName != f.Type {
			errs = append(errs, fmt.Sprintf("%s.%s must not be set when type is %q", path, s.field, f.Type))
		}
	}
	if !expected.present {
		errs = append(errs, fmt.Sprintf("%s.%s is required when type is %q", path, expected.field, f.Type))
	}
	if len(errs) > 0 {
		return errs
	}

	switch f.Type {
	case "RequestHeaderModifier":
		errs = append(errs, validateHeaderFilter(path+".requestHeaderModifier", f.RequestHeaderModifier)...)
	case "ResponseHeaderModifier":
		errs = append(errs, validateHeaderFilter(path+".responseHeaderModifier", f.ResponseHeaderModifier)...)
	case "RequestRedirect":
		errs = append(errs, validateRequestRedirect(path+".requestRedirect", f.RequestRedirect)...)
	case "URLRewrite":
		errs = append(errs, validateURLRewrite(path+".urlRewrite", f.URLRewrite)...)
	}
	return errs
}

// validateDevices validates spec.alpha.devices: each entry's hostPath (and
// containerPath, when set) must be a non-empty absolute path under /dev with
// no ".." traversal segments, no two entries may declare the same hostPath,
// and no two entries may resolve to the same effective container mount path
// (an omitted containerPath defaults to hostPath — see buildStatefulSet).
// Returns one message per problem found, empty when valid.
func validateDevices(spec *hav1.HomeAssistantSpec) []string {
	if spec.Alpha == nil || len(spec.Alpha.Devices) == 0 {
		return nil
	}
	var errs []string
	seenHostPaths := make(map[string]bool, len(spec.Alpha.Devices))
	seenContainerPaths := make(map[string]bool, len(spec.Alpha.Devices))
	for i, d := range spec.Alpha.Devices {
		path := fmt.Sprintf("spec.alpha.devices[%d]", i)
		errs = append(errs, validateDevicePath(path+".hostPath", d.HostPath)...)
		if d.ContainerPath != "" {
			errs = append(errs, validateDevicePath(path+".containerPath", d.ContainerPath)...)
		}
		if d.HostPath != "" {
			if seenHostPaths[d.HostPath] {
				errs = append(errs, fmt.Sprintf("%s.hostPath %q is declared more than once", path, d.HostPath))
			}
			seenHostPaths[d.HostPath] = true

			effectiveContainerPath := d.ContainerPath
			if effectiveContainerPath == "" {
				effectiveContainerPath = d.HostPath
			}
			if seenContainerPaths[effectiveContainerPath] {
				errs = append(errs, fmt.Sprintf(
					"%s resolves to container mount path %q, already used by another device",
					path, effectiveContainerPath))
			}
			seenContainerPaths[effectiveContainerPath] = true
		}
	}
	return errs
}

// validateDevicePath rejects an empty path, a path not rooted under /dev, or
// a path containing a ".." traversal segment.
func validateDevicePath(fieldPath, value string) []string {
	if value == "" {
		return []string{fmt.Sprintf("%s must not be empty", fieldPath)}
	}
	if !strings.HasPrefix(value, "/dev/") {
		return []string{fmt.Sprintf("%s must be an absolute path under /dev, got %q", fieldPath, value)}
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return []string{fmt.Sprintf("%s must not contain \"..\" path segments, got %q", fieldPath, value)}
		}
	}
	return nil
}

func validateHeaderFilter(path string, h *hav1.HTTPHeaderFilter) []string {
	if len(h.Set) == 0 && len(h.Add) == 0 && len(h.Remove) == 0 {
		return []string{fmt.Sprintf("%s must set at least one of set, add, remove", path)}
	}
	return nil
}

func validateRequestRedirect(path string, r *hav1.HTTPRequestRedirectFilter) []string {
	if r.Scheme == nil && r.Hostname == nil && r.Path == nil && r.Port == nil && r.StatusCode == nil {
		return []string{fmt.Sprintf("%s must set at least one of scheme, hostname, path, port, statusCode", path)}
	}
	if r.Path != nil {
		return validatePathModifier(path+".path", r.Path)
	}
	return nil
}

func validateURLRewrite(path string, u *hav1.HTTPURLRewriteFilter) []string {
	if u.Hostname == nil && u.Path == nil {
		return []string{fmt.Sprintf("%s must set at least one of hostname, path", path)}
	}
	if u.Path != nil {
		return validatePathModifier(path+".path", u.Path)
	}
	return nil
}

func validatePathModifier(path string, p *hav1.HTTPPathModifier) []string {
	switch p.Type {
	case "ReplaceFullPath":
		if p.ReplaceFullPath == nil {
			return []string{fmt.Sprintf("%s.replaceFullPath is required when path.type is ReplaceFullPath", path)}
		}
		if p.ReplacePrefixMatch != nil {
			return []string{fmt.Sprintf("%s.replacePrefixMatch must not be set when path.type is ReplaceFullPath", path)}
		}
	case "ReplacePrefixMatch":
		if p.ReplacePrefixMatch == nil {
			return []string{fmt.Sprintf("%s.replacePrefixMatch is required when path.type is ReplacePrefixMatch", path)}
		}
		if p.ReplaceFullPath != nil {
			return []string{fmt.Sprintf("%s.replaceFullPath must not be set when path.type is ReplacePrefixMatch", path)}
		}
	default:
		return []string{fmt.Sprintf("%s.type must be ReplaceFullPath or ReplacePrefixMatch, got %q", path, p.Type)}
	}
	return nil
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
