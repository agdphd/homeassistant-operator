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
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	hav1alpha1 "github.com/przemekhys/homeassistant-operator/api/v1alpha1"
)

// autoIncludeEntries defines the keys and their corresponding !include directives.
// HA expects these files to be explicitly included in configuration.yaml.
var autoIncludeEntries = []struct {
	key      string
	fileName string
}{
	{"automation", "automations.yaml"},
	{"scene", "scenes.yaml"},
	{"script", "scripts.yaml"},
}

// injectLocation injects location fields (latitude, longitude, elevation, time_zone,
// unit_system, name, currency) from spec.bootstrap.location into the homeassistant:
// section of configuration.yaml, but only for fields not already defined by the user.
// Returns an error if the YAML cannot be parsed or marshalled.
func injectLocation(configYAML string, loc *hav1alpha1.LocationConfig) (string, error) {
	if loc == nil {
		return configYAML, nil
	}
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(configYAML), &parsed); err != nil {
		return "", fmt.Errorf("failed to parse configuration YAML for location injection: %w", err)
	}
	if parsed == nil {
		parsed = make(map[string]interface{})
	}

	haSection, _ := parsed["homeassistant"].(map[string]interface{})
	if haSection == nil {
		haSection = make(map[string]interface{})
	}

	if _, ok := haSection["latitude"]; !ok && loc.Latitude != "" {
		if v, err := strconv.ParseFloat(loc.Latitude, 64); err == nil {
			haSection["latitude"] = v
		}
	}
	if _, ok := haSection["longitude"]; !ok && loc.Longitude != "" {
		if v, err := strconv.ParseFloat(loc.Longitude, 64); err == nil {
			haSection["longitude"] = v
		}
	}
	if _, ok := haSection["elevation"]; !ok && loc.Elevation != nil {
		haSection["elevation"] = *loc.Elevation
	}
	if _, ok := haSection["unit_system"]; !ok && loc.UnitSystem != "" {
		haSection["unit_system"] = loc.UnitSystem
	}
	if _, ok := haSection["time_zone"]; !ok && loc.TimeZone != "" {
		haSection["time_zone"] = loc.TimeZone
	}
	if _, ok := haSection["name"]; !ok && loc.Name != "" {
		haSection["name"] = loc.Name
	}
	if _, ok := haSection["currency"]; !ok && loc.Currency != "" {
		haSection["currency"] = loc.Currency
	}

	parsed["homeassistant"] = haSection

	out, err := yaml.Marshal(parsed)
	if err != nil {
		return "", fmt.Errorf("failed to marshal configuration YAML after location injection: %w", err)
	}
	return string(out), nil
}

// buildEffectiveConfig applies injectLocation and ensureAutoIncludes transformations
// to produce the final configuration.yaml content written to the ConfigMap.
// ha may be nil (no location injection) if the HomeAssistant CR is unavailable.
// If location injection fails (unexpected YAML parse error), the error is logged and
// the function falls back to rawConfig before appending auto-include directives.
func buildEffectiveConfig(rawConfig string, ha *hav1alpha1.HomeAssistant) string {
	var loc *hav1alpha1.LocationConfig
	if ha != nil && ha.Spec.Bootstrap != nil {
		loc = ha.Spec.Bootstrap.Location
	}
	injected, err := injectLocation(rawConfig, loc)
	if err != nil {
		logf.Log.WithName("buildEffectiveConfig").Error(err, "Location injection failed, proceeding without location")
		injected = rawConfig
	}
	return ensureAutoIncludes(injected)
}

// ensureAutoIncludes adds `!include` directives for automation, scene, and script
// if they are not already present in the configuration YAML.
// Uses YAML parsing only for reading top-level keys; appends raw text to preserve
// HA's custom `!include` tag (which is not standard YAML).
func ensureAutoIncludes(configYAML string) string {
	// Parse YAML to get top-level keys
	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(configYAML), &parsed); err != nil {
		// Safe fallback: return input unchanged on parse error
		return configYAML
	}

	if parsed == nil {
		parsed = make(map[string]interface{})
	}

	result := configYAML
	var additions []string
	for _, entry := range autoIncludeEntries {
		val, exists := parsed[entry.key]
		if !exists {
			additions = append(additions, entry.key+": !include "+entry.fileName)
		} else if strVal, ok := val.(string); ok && strVal == entry.fileName {
			// Bare filename without !include tag — lost during YAML round-trip
			// (e.g. injectLocation's yaml.Marshal strips !include tags).
			// Fix in-place by restoring the !include directive.
			result = strings.Replace(
				result,
				entry.key+": "+entry.fileName,
				entry.key+": !include "+entry.fileName,
				1,
			)
		}
	}

	if len(additions) == 0 && result == configYAML {
		return configYAML
	}

	if len(additions) > 0 {
		// Ensure trailing newline before appending
		if result != "" && !strings.HasSuffix(result, "\n") {
			result += "\n"
		}
		result += strings.Join(additions, "\n") + "\n"
	}
	return result
}
