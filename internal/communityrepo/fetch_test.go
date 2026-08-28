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
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// buildTarball builds a gzip-compressed tarball containing files, wrapped in a single
// top-level directory named prefix (mirroring GitHub codeload's "owner-repo-sha/" layout).
func buildTarball(t *testing.T, prefix string, files map[string]string) []byte {
	t.Helper()
	return buildTarballWith(t, prefix, files, nil)
}

// buildTarballWith additionally writes entries given only by size, filling them with
// placeholder bytes streamed in chunks — so a multi-megabyte entry never has to be
// materialized in the test itself.
func buildTarballWith(t *testing.T, prefix string, files map[string]string, sized map[string]int64) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	dirName := prefix + "/"
	if err := tw.WriteHeader(&tar.Header{Name: dirName, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatalf("write dir header: %v", err)
	}
	for name, content := range files {
		full := prefix + "/" + name
		if err := tw.WriteHeader(&tar.Header{
			Name:     full,
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(content)),
		}); err != nil {
			t.Fatalf("write header for %s: %v", full, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write content for %s: %v", full, err)
		}
	}
	chunk := bytes.Repeat([]byte("a"), 1024*1024)
	for name, size := range sized {
		full := prefix + "/" + name
		if err := tw.WriteHeader(&tar.Header{
			Name:     full,
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     size,
		}); err != nil {
			t.Fatalf("write header for %s: %v", full, err)
		}
		for remaining := size; remaining > 0; {
			n := int64(len(chunk))
			if remaining < n {
				n = remaining
			}
			if _, err := tw.Write(chunk[:n]); err != nil {
				t.Fatalf("write content for %s: %v", full, err)
			}
			remaining -= n
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	return buf.Bytes()
}

func withMockCodeload(t *testing.T, handler http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	original := CodeloadBaseURL
	CodeloadBaseURL = srv.URL
	t.Cleanup(func() { CodeloadBaseURL = original })
}

func TestFetchTarball_Success(t *testing.T) {
	tarball := buildTarball(t, "owner-repo-abc123", map[string]string{
		"hacs.json":                         `{"name":"Test","category":"theme"}`,
		"themes/mytheme.yaml":               "mytheme: {}\n",
		"custom_components/foo/__init__.py": "",
	})

	withMockCodeload(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/owner/repo/tar.gz/main" {
			t.Errorf("unexpected request path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(tarball)
	})

	repo, err := FetchTarball(context.Background(), "owner/repo", "main")
	if err != nil {
		t.Fatalf("FetchTarball() error = %v", err)
	}

	if _, err := repo.ReadFile("hacs.json"); err != nil {
		t.Errorf("expected hacs.json to exist: %v", err)
	}
	if _, err := repo.ReadFile("themes/mytheme.yaml"); err != nil {
		t.Errorf("expected themes/mytheme.yaml to exist: %v", err)
	}
}

func TestFetchTarball_NotFound(t *testing.T) {
	withMockCodeload(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	_, err := FetchTarball(context.Background(), "owner/missing", "main")
	if !errors.Is(err, ErrRepositoryUnreachable) {
		t.Fatalf("expected ErrRepositoryUnreachable, got %v", err)
	}
}

func TestFetchTarball_ServerError(t *testing.T) {
	withMockCodeload(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	_, err := FetchTarball(context.Background(), "owner/repo", "main")
	if !errors.Is(err, ErrRepositoryUnreachable) {
		t.Fatalf("expected ErrRepositoryUnreachable, got %v", err)
	}
}

func TestFetchTarball_MultipleTopLevelDirs(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range []string{"dir-a/", "dir-b/"} {
		if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
			t.Fatalf("write header: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	withMockCodeload(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buf.Bytes())
	})

	_, err := FetchTarball(context.Background(), "owner/repo", "main")
	if err == nil {
		t.Fatal("expected an error for a tarball with more than one top-level directory")
	}
}

func TestExtractTarGz_RejectsPathTraversal(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	evil := "../../etc/passwd"
	if err := tw.WriteHeader(&tar.Header{
		Name:     evil,
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     0,
	}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	if _, err := extractTarGz(bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("expected extractTarGz to reject a path-traversal tar entry")
	}
}

func TestExtractTarGz_RejectsAbsolutePath(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "/etc/passwd",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     0,
	}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	if _, err := extractTarGz(bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("expected extractTarGz to reject an absolute-path tar entry")
	}
}

func TestExtractedRepo_ReadDir_Root(t *testing.T) {
	tarball := buildTarball(t, "owner-repo-abc123", map[string]string{
		"hacs.json":                         `{"name":"Test"}`,
		"themes/mytheme.yaml":               "mytheme: {}\n",
		"custom_components/foo/__init__.py": "",
	})
	withMockCodeload(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(tarball)
	})

	repo, err := FetchTarball(context.Background(), "owner/repo", "main")
	if err != nil {
		t.Fatalf("FetchTarball() error = %v", err)
	}

	entries, err := repo.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.) error = %v", err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		names[e.Name] = true
	}
	if !names["hacs.json"] || !names["themes"] || !names["custom_components"] {
		t.Errorf("unexpected root entries: %+v", entries)
	}
}

// TestFetchTarball_LargeEntryStaysVisible pins the behavior that makes a real HACS
// integration installable: a file too large to hold in memory is still part of the
// repository view (it is listed and it exists), only its content is not retained.
func TestFetchTarball_LargeEntryStaysVisible(t *testing.T) {
	tarball := buildTarballWith(t, "owner-repo-abc123",
		map[string]string{"hacs.json": `{"name":"Test"}`},
		map[string]int64{"assets/resources.py": maxRetainedEntryBytes + 1},
	)
	withMockCodeload(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(tarball)
	})

	repo, err := FetchTarball(context.Background(), "owner/repo", "main")
	if err != nil {
		t.Fatalf("FetchTarball() error = %v", err)
	}

	if !repo.Exists("assets/resources.py") {
		t.Error("expected the large entry to exist in the repository view")
	}
	entries, err := repo.ReadDir("assets")
	if err != nil || len(entries) != 1 || entries[0].Name != "resources.py" {
		t.Errorf("expected ReadDir to list the large entry, got %+v (err = %v)", entries, err)
	}
	if _, err := repo.ReadFile("assets/resources.py"); !errors.Is(err, ErrContentNotRetained) {
		t.Errorf("expected ErrContentNotRetained reading the large entry, got %v", err)
	}
	if data, err := repo.ReadFile("hacs.json"); err != nil || len(data) == 0 {
		t.Errorf("expected small files to keep their content, got %q (err = %v)", data, err)
	}
}

// TestValidateAndResolve_IntegrationWithMultiMegabyteSource mirrors integrations that
// ship generated Python sources holding tens of megabytes of base64-encoded assets
// (map tiles, icons) in a single file: validation must resolve them like any other.
func TestValidateAndResolve_IntegrationWithMultiMegabyteSource(t *testing.T) {
	tarball := buildTarballWith(t, "owner-repo-abc123",
		map[string]string{
			"hacs.json": `{"name":"Test","category":"integration"}`,
			"custom_components/my_integration/manifest.json": `{"domain":"my_integration"}`,
			"custom_components/my_integration/__init__.py":   "",
		},
		map[string]int64{"custom_components/my_integration/resources.py": 30 * 1024 * 1024},
	)
	withMockCodeload(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(tarball)
	})

	repo, err := FetchTarball(context.Background(), "owner/repo", "main")
	if err != nil {
		t.Fatalf("FetchTarball() error = %v", err)
	}
	resolved, err := ValidateAndResolve(repo, CategoryIntegration)
	if err != nil {
		t.Fatalf("ValidateAndResolve() error = %v", err)
	}
	if resolved.ResolvedTarget != "my_integration" {
		t.Errorf("ResolvedTarget = %q, want %q", resolved.ResolvedTarget, "my_integration")
	}
}

// TestExtractTarGz_RejectsExceedingRetainedLimit covers the archive shape the
// cumulative guard cannot see: every entry is small enough for its content to be
// kept, so the archive stays under maxExtractedTotalBytes while the memory it needs
// does not. Rejecting it must be a diagnosable error, not an OOM kill.
func TestExtractTarGz_RejectsExceedingRetainedLimit(t *testing.T) {
	const entrySize = maxRetainedEntryBytes // retained, being at the threshold
	entryCount := int(maxRetainedTotalBytes/entrySize) + 2

	sized := map[string]int64{}
	for i := 0; i < entryCount; i++ {
		sized[fmt.Sprintf("file-%d", i)] = entrySize
	}
	tarball := buildTarballWith(t, "owner-repo-abc123", nil, sized)

	if int64(entryCount)*entrySize > maxExtractedTotalBytes {
		t.Fatalf("test archive (%d bytes) must stay under the cumulative limit to be meaningful",
			int64(entryCount)*entrySize)
	}
	_, err := extractTarGz(bytes.NewReader(tarball))
	if err == nil {
		t.Fatal("expected extractTarGz to reject an archive holding too much content in memory")
	}
	// Assert on the reason, not just on failure: a rejection coming from one of the
	// other guards would mean this archive shape is still unchecked.
	if !strings.Contains(err.Error(), "keep in memory") {
		t.Errorf("expected the retained-content guard to reject, got %v", err)
	}
}

func TestExtractTarGz_RejectsExceedingCumulativeLimit(t *testing.T) {
	// The cumulative guard counts every regular entry, whether or not its content
	// is retained in memory — it bounds the archive as a whole. Entries here are
	// far above maxRetainedEntryBytes, so nothing is retained and this exercises
	// the cumulative guard alone.
	const chunkSize = 10 * 1024 * 1024 // 10 MiB
	chunkCount := int(maxExtractedTotalBytes/chunkSize) + 2
	chunk := bytes.Repeat([]byte("a"), chunkSize)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for i := 0; i < chunkCount; i++ {
		if err := tw.WriteHeader(&tar.Header{
			Name:     fmt.Sprintf("owner-repo-abc123/file-%d", i),
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     int64(len(chunk)),
		}); err != nil {
			t.Fatalf("write header: %v", err)
		}
		if _, err := tw.Write(chunk); err != nil {
			t.Fatalf("write content: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	if _, err := extractTarGz(bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("expected extractTarGz to reject an archive exceeding the cumulative size limit")
	}
}

func TestExtractTarGz_RejectsTooManyEntries(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for i := 0; i < maxExtractedEntries+1; i++ {
		if err := tw.WriteHeader(&tar.Header{
			Name:     fmt.Sprintf("owner-repo-abc123/file-%d", i),
			Typeflag: tar.TypeReg,
			Mode:     0o644,
			Size:     0,
		}); err != nil {
			t.Fatalf("write header: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	if _, err := extractTarGz(bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("expected extractTarGz to reject an archive with too many entries")
	}
}
