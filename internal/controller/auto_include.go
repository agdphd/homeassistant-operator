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
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

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
// unit_system) from spec.bootstrap.location into the homeassistant: section of
// configuration.yaml, but only for fields not already defined by the user.
// If the YAML cannot be parsed (e.g. it contains HA-specific !include tags), the
// original string is returned unchanged as a safe fallback.
func injectLocation(configYAML string, loc *hav1alpha1.LocationConfig) string {
	if loc == nil {
		return configYAML
	}
	if loc.Latitude == "" && loc.Longitude == "" {
		return configYAML
	}

	var parsed map[string]interface{}
	if err := yaml.Unmarshal([]byte(configYAML), &parsed); err != nil {
		// Safe fallback: !include or other HA-specific tags cause parse errors
		return configYAML
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

	parsed["homeassistant"] = haSection

	out, err := yaml.Marshal(parsed)
	if err != nil {
		return configYAML
	}
	return string(out)
}

// buildEffectiveConfig applies ensureAutoIncludes and injectLocation transformations
// to produce the final configuration.yaml content written to the ConfigMap.
// ha may be nil (no location injection) if the HomeAssistant CR is unavailable.
func buildEffectiveConfig(rawConfig string, ha *hav1alpha1.HomeAssistant) string {
	var loc *hav1alpha1.LocationConfig
	if ha != nil && ha.Spec.Bootstrap != nil {
		loc = ha.Spec.Bootstrap.Location
	}
	return ensureAutoIncludes(injectLocation(rawConfig, loc))
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

	var additions []string
	for _, entry := range autoIncludeEntries {
		if _, exists := parsed[entry.key]; !exists {
			additions = append(additions, entry.key+": !include "+entry.fileName)
		}
	}

	if len(additions) == 0 {
		return configYAML
	}

	result := configYAML
	// Ensure trailing newline before appending
	if result != "" && !strings.HasSuffix(result, "\n") {
		result += "\n"
	}
	result += strings.Join(additions, "\n") + "\n"
	return result
}
