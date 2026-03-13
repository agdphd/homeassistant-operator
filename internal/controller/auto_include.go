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

	"gopkg.in/yaml.v3"
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
