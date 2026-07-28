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

import (
	"errors"
	"path"
	"testing"
)

// buildExtractedRepo constructs an in-memory ExtractedRepo directly from a
// relative-path -> content map, without going through FetchTarball/HTTP.
func buildExtractedRepo(files map[string]string) *ExtractedRepo {
	repo := &ExtractedRepo{files: map[string][]byte{}, dirs: map[string]bool{}}
	for rel, content := range files {
		clean := path.Clean(rel)
		repo.files[clean] = []byte(content)
		markParentDirs(repo.dirs, clean)
	}
	return repo
}

func TestValidateAndResolve_Integration(t *testing.T) {
	repo := buildExtractedRepo(map[string]string{
		"hacs.json": `{"name":"My Integration","category":"integration"}`,
		"custom_components/my_integration/manifest.json": `{"domain":"my_integration"}`,
		"custom_components/my_integration/__init__.py":   "",
	})

	got, err := ValidateAndResolve(repo, CategoryIntegration)
	if err != nil {
		t.Fatalf("ValidateAndResolve() error = %v", err)
	}
	if got.ResolvedTarget != "my_integration" {
		t.Errorf("ResolvedTarget = %q, want %q", got.ResolvedTarget, "my_integration")
	}
	if got.SourcePath != path.Join("custom_components", "my_integration") {
		t.Errorf("SourcePath = %q", got.SourcePath)
	}
}

func TestValidateAndResolve_Integration_MissingManifest(t *testing.T) {
	repo := buildExtractedRepo(map[string]string{
		"custom_components/my_integration/__init__.py": "",
	})

	_, err := ValidateAndResolve(repo, CategoryIntegration)
	if !errors.Is(err, ErrStructureInvalid) {
		t.Fatalf("expected ErrStructureInvalid, got %v", err)
	}
}

func TestValidateAndResolve_Integration_MultipleDirs(t *testing.T) {
	repo := buildExtractedRepo(map[string]string{
		"custom_components/a/manifest.json": `{"domain":"a"}`,
		"custom_components/b/manifest.json": `{"domain":"b"}`,
	})

	_, err := ValidateAndResolve(repo, CategoryIntegration)
	if !errors.Is(err, ErrStructureInvalid) {
		t.Fatalf("expected ErrStructureInvalid, got %v", err)
	}
}

func TestValidateAndResolve_Plugin_ByFilename(t *testing.T) {
	repo := buildExtractedRepo(map[string]string{
		"hacs.json":     `{"name":"My Card","category":"plugin","filename":"my-card.js"}`,
		"my-card.js":    "console.log('card');",
		"other-file.js": "not the plugin",
	})

	got, err := ValidateAndResolve(repo, CategoryPlugin)
	if err != nil {
		t.Fatalf("ValidateAndResolve() error = %v", err)
	}
	if got.ResolvedTarget != "my-card" {
		t.Errorf("ResolvedTarget = %q, want %q", got.ResolvedTarget, "my-card")
	}
}

func TestValidateAndResolve_Plugin_SingleFileFallback(t *testing.T) {
	repo := buildExtractedRepo(map[string]string{
		"my-card.js": "console.log('card');",
	})

	got, err := ValidateAndResolve(repo, CategoryPlugin)
	if err != nil {
		t.Fatalf("ValidateAndResolve() error = %v", err)
	}
	if got.ResolvedTarget != "my-card" {
		t.Errorf("ResolvedTarget = %q, want %q", got.ResolvedTarget, "my-card")
	}
}

func TestValidateAndResolve_Theme(t *testing.T) {
	repo := buildExtractedRepo(map[string]string{
		"themes/my_theme.yaml": "my_theme: {}\n",
	})

	got, err := ValidateAndResolve(repo, CategoryTheme)
	if err != nil {
		t.Fatalf("ValidateAndResolve() error = %v", err)
	}
	if got.ResolvedTarget != "my_theme" {
		t.Errorf("ResolvedTarget = %q, want %q", got.ResolvedTarget, "my_theme")
	}
	if got.SourcePath != path.Join("themes", "my_theme.yaml") {
		t.Errorf("SourcePath = %q", got.SourcePath)
	}
}

func TestValidateAndResolve_PythonScript(t *testing.T) {
	repo := buildExtractedRepo(map[string]string{
		"python_scripts/my_script.py": "print('hi')\n",
	})

	got, err := ValidateAndResolve(repo, CategoryPythonScript)
	if err != nil {
		t.Fatalf("ValidateAndResolve() error = %v", err)
	}
	if got.ResolvedTarget != "my_script" {
		t.Errorf("ResolvedTarget = %q, want %q", got.ResolvedTarget, "my_script")
	}
}

func TestValidateAndResolve_Template(t *testing.T) {
	repo := buildExtractedRepo(map[string]string{
		"custom_templates/my_template.jinja": "{{ 1 + 1 }}",
	})

	got, err := ValidateAndResolve(repo, CategoryTemplate)
	if err != nil {
		t.Fatalf("ValidateAndResolve() error = %v", err)
	}
	if got.ResolvedTarget != "my_template" {
		t.Errorf("ResolvedTarget = %q, want %q", got.ResolvedTarget, "my_template")
	}
}

func TestValidateAndResolve_CategoryMismatch(t *testing.T) {
	repo := buildExtractedRepo(map[string]string{
		"hacs.json":            `{"name":"Test","category":"theme"}`,
		"themes/my_theme.yaml": "my_theme: {}\n",
	})

	_, err := ValidateAndResolve(repo, CategoryPythonScript)
	if !errors.Is(err, ErrCategoryMismatch) {
		t.Fatalf("expected ErrCategoryMismatch, got %v", err)
	}
}

func TestValidateAndResolve_NoManifest_StillResolves(t *testing.T) {
	repo := buildExtractedRepo(map[string]string{
		"themes/my_theme.yaml": "my_theme: {}\n",
	})

	got, err := ValidateAndResolve(repo, CategoryTheme)
	if err != nil {
		t.Fatalf("ValidateAndResolve() error = %v", err)
	}
	if got.ResolvedTarget != "my_theme" {
		t.Errorf("ResolvedTarget = %q, want %q", got.ResolvedTarget, "my_theme")
	}
}

func TestValidateAndResolve_MalformedManifest(t *testing.T) {
	repo := buildExtractedRepo(map[string]string{
		"hacs.json":            `{not valid json`,
		"themes/my_theme.yaml": "my_theme: {}\n",
	})

	_, err := ValidateAndResolve(repo, CategoryTheme)
	if !errors.Is(err, ErrStructureInvalid) {
		t.Fatalf("expected ErrStructureInvalid, got %v", err)
	}
}

func TestValidateAndResolve_NoMatchingFile(t *testing.T) {
	repo := buildExtractedRepo(map[string]string{
		"themes/readme.md": "not a theme",
	})

	_, err := ValidateAndResolve(repo, CategoryTheme)
	if !errors.Is(err, ErrStructureInvalid) {
		t.Fatalf("expected ErrStructureInvalid, got %v", err)
	}
}

func TestValidateAndResolve_MultipleMatchingFiles(t *testing.T) {
	repo := buildExtractedRepo(map[string]string{
		"themes/theme_one.yaml": "a: {}\n",
		"themes/theme_two.yaml": "b: {}\n",
	})

	_, err := ValidateAndResolve(repo, CategoryTheme)
	if !errors.Is(err, ErrStructureInvalid) {
		t.Fatalf("expected ErrStructureInvalid, got %v", err)
	}
}

func TestValidateAndResolve_MissingSubdir(t *testing.T) {
	repo := buildExtractedRepo(map[string]string{
		"README.md": "nothing relevant here",
	})

	_, err := ValidateAndResolve(repo, CategoryPythonScript)
	if !errors.Is(err, ErrStructureInvalid) {
		t.Fatalf("expected ErrStructureInvalid, got %v", err)
	}
}
