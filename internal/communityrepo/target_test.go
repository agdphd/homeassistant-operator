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

package communityrepo

import "testing"

func TestNewConflictKey(t *testing.T) {
	r := Resolved{SourcePath: "themes/my_theme.yaml", ResolvedTarget: "my_theme"}
	got := NewConflictKey("my-ha", CategoryTheme, r)

	want := ConflictKey{HomeAssistantName: "my-ha", Category: CategoryTheme, ResolvedTarget: "my_theme"}
	if got != want {
		t.Errorf("NewConflictKey() = %+v, want %+v", got, want)
	}
}

func TestConflictKey_String(t *testing.T) {
	k := ConflictKey{HomeAssistantName: "my-ha", Category: CategoryIntegration, ResolvedTarget: "my_integration"}
	want := "my-ha/integration/my_integration"
	if got := k.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestConflictKey_EqualityAcrossSameInputs(t *testing.T) {
	r1 := Resolved{ResolvedTarget: "same_target"}
	r2 := Resolved{ResolvedTarget: "same_target"}

	k1 := NewConflictKey("ha1", CategoryPlugin, r1)
	k2 := NewConflictKey("ha1", CategoryPlugin, r2)
	if k1 != k2 {
		t.Errorf("expected identical ConflictKeys to be equal: %+v != %+v", k1, k2)
	}
	if k1.String() != k2.String() {
		t.Errorf("expected identical ConflictKeys to stringify equally: %q != %q", k1.String(), k2.String())
	}
}

func TestConflictKey_DiffersByCategory(t *testing.T) {
	r := Resolved{ResolvedTarget: "same_target"}
	k1 := NewConflictKey("ha1", CategoryIntegration, r)
	k2 := NewConflictKey("ha1", CategoryPlugin, r)
	if k1 == k2 {
		t.Errorf("expected keys with different categories to differ: %+v == %+v", k1, k2)
	}
}

func TestConflictKey_DiffersByHomeAssistantName(t *testing.T) {
	r := Resolved{ResolvedTarget: "same_target"}
	k1 := NewConflictKey("ha1", CategoryTheme, r)
	k2 := NewConflictKey("ha2", CategoryTheme, r)
	if k1 == k2 {
		t.Errorf("expected keys with different HomeAssistant names to differ: %+v == %+v", k1, k2)
	}
}

func TestConflictKey_DiffersByResolvedTarget(t *testing.T) {
	k1 := NewConflictKey("ha1", CategoryTheme, Resolved{ResolvedTarget: "theme_a"})
	k2 := NewConflictKey("ha1", CategoryTheme, Resolved{ResolvedTarget: "theme_b"})
	if k1 == k2 {
		t.Errorf("expected keys with different resolved targets to differ: %+v == %+v", k1, k2)
	}
}

func TestConflictKey_UsableAsMapKey(t *testing.T) {
	index := make(map[ConflictKey]string)
	k := NewConflictKey("my-ha", CategoryTheme, Resolved{ResolvedTarget: "my_theme"})
	index[k] = "owning-cr-name"

	if got, ok := index[k]; !ok || got != "owning-cr-name" {
		t.Errorf("expected ConflictKey to be usable as a map key; got %q, ok=%v", got, ok)
	}
}
