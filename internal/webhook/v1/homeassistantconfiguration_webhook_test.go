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
	"strings"
	"testing"

	hav1 "github.com/przemekhys/homeassistant-operator/api/v1"
)

func TestValidateRecorderPrecedence(t *testing.T) {
	tests := []struct {
		name         string
		spec         hav1.HomeAssistantConfigurationSpec
		wantWarnings int
	}{
		{
			name: "no recorder set",
			spec: hav1.HomeAssistantConfigurationSpec{},
		},
		{
			name: "only database set",
			spec: hav1.HomeAssistantConfigurationSpec{
				Recorder: &hav1.RecorderConfig{Database: "sqlite:////config/home-assistant_v2.db"},
			},
		},
		{
			name: "only databaseSecretRef set",
			spec: hav1.HomeAssistantConfigurationSpec{
				Recorder: &hav1.RecorderConfig{DatabaseSecretRef: &hav1.SecretKeySelector{Name: "db-secret"}},
			},
		},
		{
			name: "neither set",
			spec: hav1.HomeAssistantConfigurationSpec{
				Recorder: &hav1.RecorderConfig{},
			},
		},
		{
			name: "both database and databaseSecretRef set warns",
			spec: hav1.HomeAssistantConfigurationSpec{
				Recorder: &hav1.RecorderConfig{
					Database:          "sqlite:////config/home-assistant_v2.db",
					DatabaseSecretRef: &hav1.SecretKeySelector{Name: "db-secret"},
				},
			},
			wantWarnings: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			warnings := validateRecorderPrecedence(&tt.spec)
			if len(warnings) != tt.wantWarnings {
				t.Fatalf("got %d warnings, want %d: %v", len(warnings), tt.wantWarnings, warnings)
			}
			if tt.wantWarnings > 0 && !strings.Contains(warnings[0], "databaseSecretRef") {
				t.Errorf("warning %q does not name databaseSecretRef as authoritative", warnings[0])
			}
		})
	}
}
