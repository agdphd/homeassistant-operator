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
// Uses yaml.Node to preserve custom YAML tags (e.g. !secret, !include) through
// the unmarshal/marshal round-trip.
// Returns an error if the YAML cannot be parsed or marshalled.
func injectLocation(configYAML string, loc *hav1alpha1.LocationConfig) (string, error) {
	if loc == nil {
		return configYAML, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(configYAML), &doc); err != nil {
		return "", fmt.Errorf("failed to parse configuration YAML for location injection: %w", err)
	}

	// Handle empty document
	if doc.Kind == 0 {
		doc = yaml.Node{
			Kind:    yaml.DocumentNode,
			Content: []*yaml.Node{{Kind: yaml.MappingNode}},
		}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", fmt.Errorf("expected mapping at root of configuration YAML")
	}

	// Find or create "homeassistant" mapping section
	haSection := nodeMappingValue(root, "homeassistant")
	if haSection == nil {
		haSection = &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "homeassistant"},
			haSection,
		)
	} else if haSection.Kind == yaml.ScalarNode && haSection.Value == "" {
		// Empty value (e.g. "homeassistant:\n") — upgrade to mapping in-place
		haSection.Kind = yaml.MappingNode
		haSection.Tag = ""
		haSection.Value = ""
	}
	if haSection.Kind != yaml.MappingNode {
		// homeassistant is not a mapping (e.g. scalar) — cannot inject, return as-is
		out, err := yaml.Marshal(&doc)
		if err != nil {
			return "", fmt.Errorf("failed to marshal configuration YAML: %w", err)
		}
		return string(out), nil
	}

	// Inject location fields only if not already defined by user
	log := logf.Log.WithName("injectLocation")
	if loc.Latitude != "" {
		if _, err := strconv.ParseFloat(loc.Latitude, 64); err != nil {
			log.Error(err, "Invalid latitude value, skipping", "latitude", loc.Latitude)
		} else {
			setNodeField(haSection, "latitude", loc.Latitude, "!!float")
		}
	}
	if loc.Longitude != "" {
		if _, err := strconv.ParseFloat(loc.Longitude, 64); err != nil {
			log.Error(err, "Invalid longitude value, skipping", "longitude", loc.Longitude)
		} else {
			setNodeField(haSection, "longitude", loc.Longitude, "!!float")
		}
	}
	if loc.Elevation != nil {
		setNodeField(haSection, "elevation", strconv.Itoa(*loc.Elevation), "!!int")
	}
	if loc.UnitSystem != "" {
		setNodeField(haSection, "unit_system", loc.UnitSystem, "!!str")
	}
	if loc.TimeZone != "" {
		setNodeField(haSection, "time_zone", loc.TimeZone, "!!str")
	}
	if loc.Name != "" {
		setNodeField(haSection, "name", loc.Name, "!!str")
	}
	if loc.Currency != "" {
		setNodeField(haSection, "currency", loc.Currency, "!!str")
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal configuration YAML after location injection: %w", err)
	}
	return string(out), nil
}

// nodeMappingValue returns the value node for a given key in a mapping node, or nil.
func nodeMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// setNodeField adds a key-value pair to a mapping node if the key doesn't already exist.
func setNodeField(mapping *yaml.Node, key, value, tag string) {
	if nodeMappingValue(mapping, key) != nil {
		return
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value, Tag: tag},
	)
}

// overrideNodeField sets a key-value pair in a mapping node, replacing any existing value.
func overrideNodeField(mapping *yaml.Node, key, value, tag string) {
	for i := 0; i < len(mapping.Content)-1; i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1].Kind = yaml.ScalarNode
			mapping.Content[i+1].Tag = tag
			mapping.Content[i+1].Value = value
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value, Tag: tag},
	)
}

// injectRecorder merges spec.recorder fields into the recorder: section of
// configuration.yaml. dbURL is the already-resolved database URL (plain text,
// no !secret tag). Uses yaml.Node to preserve !include / !secret tags in other
// sections through the round-trip. Returns configYAML unchanged when recorder
// is nil or disabled.
func injectRecorder(configYAML string, recorder *hav1alpha1.RecorderConfig, dbURL string) (string, error) {
	if recorder == nil {
		return configYAML, nil
	}
	// If explicitly disabled, skip injection
	if recorder.Enabled != nil && !*recorder.Enabled {
		return configYAML, nil
	}
	// Nothing to inject
	if dbURL == "" && recorder.PurgeKeepDays == nil {
		return configYAML, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(configYAML), &doc); err != nil {
		return "", fmt.Errorf("failed to parse configuration YAML for recorder injection: %w", err)
	}
	if doc.Kind == 0 {
		doc = yaml.Node{
			Kind:    yaml.DocumentNode,
			Content: []*yaml.Node{{Kind: yaml.MappingNode}},
		}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return "", fmt.Errorf("expected mapping at root of configuration YAML")
	}

	recSection := nodeMappingValue(root, "recorder")
	if recSection == nil {
		recSection = &yaml.Node{Kind: yaml.MappingNode}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: "recorder"},
			recSection,
		)
	} else if recSection.Kind == yaml.ScalarNode &&
		recSection.Value == "" &&
		(recSection.Tag == "" || recSection.Tag == "!!null") {
		// bare "recorder:" with truly empty/null value — upgrade to mapping
		recSection.Kind = yaml.MappingNode
		recSection.Tag = ""
		recSection.Value = ""
	}
	if recSection.Kind != yaml.MappingNode {
		// Preserve tagged scalars like "recorder: !include recorder.yaml" unchanged
		return configYAML, nil
	}

	if dbURL != "" {
		overrideNodeField(recSection, "db_url", dbURL, "!!str")
	}
	if recorder.PurgeKeepDays != nil {
		overrideNodeField(recSection, "purge_keep_days", strconv.Itoa(int(*recorder.PurgeKeepDays)), "!!int")
	}

	out, err := yaml.Marshal(&doc)
	if err != nil {
		return "", fmt.Errorf("failed to marshal configuration YAML after recorder injection: %w", err)
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
