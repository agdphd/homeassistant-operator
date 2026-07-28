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
	"testing"
)

// buildTarball builds a gzip-compressed tarball containing files, wrapped in a single
// top-level directory named prefix (mirroring GitHub codeload's "owner-repo-sha/" layout).
func buildTarball(t *testing.T, prefix string, files map[string]string) []byte {
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

func TestExtractTarGz_RejectsOversizedEntry(t *testing.T) {
	oversized := bytes.Repeat([]byte("a"), maxExtractedEntryBytes+1)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "owner-repo-abc123/big-file",
		Typeflag: tar.TypeReg,
		Mode:     0o644,
		Size:     int64(len(oversized)),
	}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tw.Write(oversized); err != nil {
		t.Fatalf("write content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}

	if _, err := extractTarGz(bytes.NewReader(buf.Bytes())); err == nil {
		t.Fatal("expected extractTarGz to reject an oversized entry")
	}
}

func TestExtractTarGz_RejectsExceedingCumulativeLimit(t *testing.T) {
	// Each entry stays well under maxExtractedEntryBytes, but enough of them
	// together exceed maxExtractedTotalBytes — this must be rejected on its own,
	// independent of the per-entry guard.
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
