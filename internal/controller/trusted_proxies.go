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

	"gopkg.in/yaml.v3"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

// defaultTrustedProxyRanges are the RFC1918 private address ranges injected by
// default. These cannot reliably substitute for the real cluster pod/service
// CIDR (which is not reliably readable from the Kubernetes API), only a
// conservative, commonly-correct guess — see spec.disableDefaultTrustedProxies
// for the opt-out when a cluster's network doesn't match.
var defaultTrustedProxyRanges = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}

// trustedProxiesOutcome reports what injectTrustedProxies did (or didn't do)
// on a given reconcile, so callers can both persist it to status and phrase
// the HomeAssistant's ExposureReady condition message accordingly.
type trustedProxiesOutcome int

const (
	// trustedProxiesNotExposed: neither Ingress nor Gateway API exposure is
	// enabled, so trusted-proxy defaults are not relevant.
	trustedProxiesNotExposed trustedProxiesOutcome = iota
	// trustedProxiesOptedOut: exposed, but spec.disableDefaultTrustedProxies
	// is true — the operator never manages these keys for this instance.
	trustedProxiesOptedOut
	// trustedProxiesUserManaged: exposed, opt-out not set, but the user had
	// already set both use_x_forwarded_for and trusted_proxies themselves (or
	// http: is a tagged scalar/external include the operator cannot safely
	// touch) — nothing was added.
	trustedProxiesUserManaged
	// trustedProxiesApplied: exposed, opt-out not set, and the operator added
	// at least one of its own defaults (the other key may already have been
	// user-set — per-key granularity, not all-or-nothing).
	trustedProxiesApplied
)

// injectTrustedProxies ensures http.use_x_forwarded_for and http.trusted_proxies
// are set to sensible RFC1918 defaults whenever the HomeAssistant instance is
// exposed via Ingress or Gateway API, unless the user opted out
// (spec.disableDefaultTrustedProxies) or already set either key themselves.
// Like injectRecorder, it recomputes from the pristine
// configYAML on every call — recomputing from spec.disableDefaultTrustedProxies/
// spec.ingress/spec.gateway is what makes removal automatic: a reconcile
// after exposure is disabled or the opt-out is set simply doesn't add the
// keys, with no dedicated cleanup path needed.
func injectTrustedProxies(configYAML string, ha *hav1.HomeAssistant) (string, trustedProxiesOutcome, error) {
	exposed := ha != nil &&
		((ha.Spec.Ingress != nil && ha.Spec.Ingress.Enabled) ||
			(ha.Spec.Gateway != nil && ha.Spec.Gateway.Enabled))
	if !exposed {
		return configYAML, trustedProxiesNotExposed, nil
	}
	if ha.Spec.DisableDefaultTrustedProxies {
		return configYAML, trustedProxiesOptedOut, nil
	}

	doc, err := parseConfigYAML(configYAML)
	if err != nil {
		return "", trustedProxiesNotExposed, err
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return configYAML, trustedProxiesUserManaged, nil
	}

	httpSection := nodeMappingValue(root, "http")
	switch {
	case httpSection == nil:
		httpSection = &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "http"},
			httpSection,
		)
	case httpSection.Kind == yaml.ScalarNode && httpSection.Value == "" &&
		(httpSection.Tag == "" || httpSection.Tag == "!!null"):
		httpSection.Kind = yaml.MappingNode
		httpSection.Tag = ""
	case httpSection.Kind != yaml.MappingNode:
		// Tagged scalar like "http: !include http.yaml" — the user manages this
		// section elsewhere; preserve unchanged rather than risk breaking it.
		return configYAML, trustedProxiesUserManaged, nil
	}

	hadXFF := nodeMappingValue(httpSection, "use_x_forwarded_for") != nil
	hadTrustedProxies := nodeMappingValue(httpSection, "trusted_proxies") != nil

	setNodeField(httpSection, "use_x_forwarded_for", "true", "!!bool")
	setSequenceFieldIfAbsent(httpSection, "trusted_proxies", defaultTrustedProxyRanges)

	out, err := yaml.Marshal(doc)
	if err != nil {
		return "", trustedProxiesNotExposed, fmt.Errorf(
			"failed to marshal configuration YAML after trusted-proxies injection: %w", err)
	}

	if hadXFF && hadTrustedProxies {
		return string(out), trustedProxiesUserManaged, nil
	}
	return string(out), trustedProxiesApplied, nil
}
