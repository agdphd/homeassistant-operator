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
	"testing"
)

func TestEnsureAutoIncludes(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:  "empty config adds all three includes",
			input: "",
			expected: "automation: !include automations.yaml\n" +
				"scene: !include scenes.yaml\n" +
				"script: !include scripts.yaml\n",
		},
		{
			name:  "config with only homeassistant section adds all three",
			input: "homeassistant:\n  name: My Home\n",
			expected: "homeassistant:\n  name: My Home\n" +
				"automation: !include automations.yaml\n" +
				"scene: !include scenes.yaml\n" +
				"script: !include scripts.yaml\n",
		},
		{
			name:     "config with automation key does not duplicate",
			input:    "automation: !include automations.yaml\n",
			expected: "automation: !include automations.yaml\nscene: !include scenes.yaml\nscript: !include scripts.yaml\n",
		},
		{
			name:     "config with automation as empty list does not override",
			input:    "automation: []\n",
			expected: "automation: []\nscene: !include scenes.yaml\nscript: !include scripts.yaml\n",
		},
		{
			name:     "config with all three keys present returns unchanged",
			input:    "automation: !include automations.yaml\nscene: !include scenes.yaml\nscript: !include scripts.yaml\n",
			expected: "automation: !include automations.yaml\nscene: !include scenes.yaml\nscript: !include scripts.yaml\n",
		},
		{
			name:     "invalid YAML returns input unchanged",
			input:    "invalid: yaml: :\n  - [broken",
			expected: "invalid: yaml: :\n  - [broken",
		},
		{
			name: "idempotent - running twice gives same result",
			input: "homeassistant:\n  name: Test\n" +
				"automation: !include automations.yaml\n" +
				"scene: !include scenes.yaml\n" +
				"script: !include scripts.yaml\n",
			expected: "homeassistant:\n  name: Test\n" +
				"automation: !include automations.yaml\n" +
				"scene: !include scenes.yaml\n" +
				"script: !include scripts.yaml\n",
		},
		{
			name:  "config with inline automation definition does not add include",
			input: "automation:\n  - alias: Test\n    trigger: []\n",
			expected: "automation:\n  - alias: Test\n    trigger: []\n" +
				"scene: !include scenes.yaml\n" +
				"script: !include scripts.yaml\n",
		},
		{
			name:  "config without trailing newline gets newline added",
			input: "homeassistant:\n  name: Test",
			expected: "homeassistant:\n  name: Test\n" +
				"automation: !include automations.yaml\n" +
				"scene: !include scenes.yaml\n" +
				"script: !include scripts.yaml\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ensureAutoIncludes(tt.input)
			if result != tt.expected {
				t.Errorf("ensureAutoIncludes() =\n%q\nwant:\n%q", result, tt.expected)
			}
		})
	}
}

func TestEnsureAutoIncludes_Idempotent(t *testing.T) {
	input := "homeassistant:\n  name: Test\n"
	first := ensureAutoIncludes(input)
	second := ensureAutoIncludes(first)
	if first != second {
		t.Errorf("ensureAutoIncludes is not idempotent:\nfirst:  %q\nsecond: %q", first, second)
	}
}
