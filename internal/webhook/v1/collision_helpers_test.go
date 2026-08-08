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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestEffectiveID(t *testing.T) {
	tests := []struct {
		name, id, resourceName, want string
	}{
		{name: "explicit id wins", id: "morning-lights", resourceName: "my-automation", want: "morning-lights"},
		{name: "empty id falls back to resource name", id: "", resourceName: "my-automation", want: "my-automation"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveID(tt.id, tt.resourceName); got != tt.want {
				t.Errorf("effectiveID(%q, %q) = %q, want %q", tt.id, tt.resourceName, got, tt.want)
			}
		})
	}
}

func TestFindIdentifierCollision(t *testing.T) {
	now := metav1.Now()
	tests := []struct {
		name                            string
		siblings                        []siblingDescriptor
		selfUID                         types.UID
		selfHomeAssistantRef, selfEffID string
		wantConflict                    string
	}{
		{
			name:                 "no siblings, no collision",
			siblings:             nil,
			selfUID:              "self",
			selfHomeAssistantRef: "home",
			selfEffID:            "foo",
			wantConflict:         "",
		},
		{
			name: "same id, same instance, different resource -> collision",
			siblings: []siblingDescriptor{
				{Name: "sibling", UID: "other", HomeAssistantRefName: "home", EffectiveID: "foo"},
			},
			selfUID:              "self",
			selfHomeAssistantRef: "home",
			selfEffID:            "foo",
			wantConflict:         "sibling",
		},
		{
			name: "same id, different instance -> no collision",
			siblings: []siblingDescriptor{
				{Name: "sibling", UID: "other", HomeAssistantRefName: "away", EffectiveID: "foo"},
			},
			selfUID:              "self",
			selfHomeAssistantRef: "home",
			selfEffID:            "foo",
			wantConflict:         "",
		},
		{
			name: "same id, same instance, but sibling is self (update) -> no collision",
			siblings: []siblingDescriptor{
				{Name: "self-resource", UID: "self", HomeAssistantRefName: "home", EffectiveID: "foo"},
			},
			selfUID:              "self",
			selfHomeAssistantRef: "home",
			selfEffID:            "foo",
			wantConflict:         "",
		},
		{
			name: "same id, same instance, sibling marked for deletion -> no collision",
			siblings: []siblingDescriptor{
				{Name: "sibling", UID: "other", HomeAssistantRefName: "home", EffectiveID: "foo", DeletionTimestamp: &now},
			},
			selfUID:              "self",
			selfHomeAssistantRef: "home",
			selfEffID:            "foo",
			wantConflict:         "",
		},
		{
			name: "different id, same instance -> no collision",
			siblings: []siblingDescriptor{
				{Name: "sibling", UID: "other", HomeAssistantRefName: "home", EffectiveID: "bar"},
			},
			selfUID:              "self",
			selfHomeAssistantRef: "home",
			selfEffID:            "foo",
			wantConflict:         "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findIdentifierCollision(tt.siblings, tt.selfUID, tt.selfHomeAssistantRef, tt.selfEffID)
			if got != tt.wantConflict {
				t.Errorf("findIdentifierCollision() = %q, want %q", got, tt.wantConflict)
			}
		})
	}
}
