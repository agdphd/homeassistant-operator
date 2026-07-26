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

// Package communityrepo validates and resolves HACS-compatible GitHub repositories
// (fetch, hacs.json/manifest parsing, install-target resolution). It has no
// Kubernetes dependency by design, so it can be unit tested without a cluster and
// so its logic mirrors — in spirit, not in code — the embedded Python script the
// community-repository sidecar runs.
package communityrepo

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
)

// CodeloadBaseURL is the GitHub tarball endpoint used instead of a git client or the
// REST API: no new dependency, and a separate, generally higher rate limit than the
// unauthenticated REST API's 60 requests/hour/IP.
// Exported (not const) so callers outside this package — controller envtest specs,
// E2E suites — can point it at a local fixture server instead of the real GitHub host.
var CodeloadBaseURL = "https://codeload.github.com"

// ExtractedRepo is an in-memory view of a fetched repository's contents, with the
// tarball's single top-level wrapper directory already stripped. Kept entirely in
// memory — never written to disk — because the operator container runs with
// readOnlyRootFilesystem and no writable /tmp (a production deployment default;
// os.MkdirTemp there fails outright, which is exactly the bug this design avoids).
// HACS repos are tiny plain-text source trees, so holding them in memory is cheap.
type ExtractedRepo struct {
	files map[string][]byte // cleaned relative path -> content, regular files only
	dirs  map[string]bool   // cleaned relative path -> true, includes intermediate dirs
}

// DirEntry is one entry returned by ExtractedRepo.ReadDir.
type DirEntry struct {
	Name  string
	IsDir bool
}

// ReadFile returns the content of the file at the given path (relative to the
// repository root), or an error satisfying errors.Is(err, ErrNotExist) if absent.
func (e *ExtractedRepo) ReadFile(p string) ([]byte, error) {
	clean := path.Clean(p)
	if data, ok := e.files[clean]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("%s: %w", p, ErrNotExist)
}

// ReadDir lists the entries directly inside the given directory (relative to the
// repository root; "" or "." means the repository root itself), or an error
// satisfying errors.Is(err, ErrNotExist) if the directory doesn't exist.
func (e *ExtractedRepo) ReadDir(p string) ([]DirEntry, error) {
	clean := path.Clean(p)
	if clean == "." {
		clean = ""
	}
	if clean != "" && !e.dirs[clean] {
		return nil, fmt.Errorf("%s: %w", p, ErrNotExist)
	}

	seen := map[string]bool{}
	var entries []DirEntry
	addOnce := func(name string, isDir bool) {
		if seen[name] {
			return
		}
		seen[name] = true
		entries = append(entries, DirEntry{Name: name, IsDir: isDir})
	}
	for filePath := range e.files {
		if name, ok := directChild(clean, filePath); ok {
			addOnce(name, false)
		}
	}
	for dirPath := range e.dirs {
		if name, ok := directChild(clean, dirPath); ok {
			addOnce(name, true)
		}
	}
	return entries, nil
}

// directChild reports whether childPath is a direct child of parent, returning
// its base name if so.
func directChild(parent, childPath string) (name string, ok bool) {
	if parent == "" {
		if strings.Contains(childPath, "/") {
			return "", false
		}
		return childPath, childPath != ""
	}
	prefix := parent + "/"
	if !strings.HasPrefix(childPath, prefix) {
		return "", false
	}
	rest := childPath[len(prefix):]
	if rest == "" || strings.Contains(rest, "/") {
		return "", false
	}
	return rest, true
}

// Exists reports whether the given path (file or directory) exists.
func (e *ExtractedRepo) Exists(p string) bool {
	clean := path.Clean(p)
	return e.files[clean] != nil || e.dirs[clean]
}

// ErrNotExist is returned by ExtractedRepo methods for a missing path.
var ErrNotExist = errors.New("path does not exist")

// FetchTarball downloads the tarball for repository ("owner/repo") at ref (tag,
// branch, or commit) and extracts it entirely in memory, stripping the tarball's
// single top-level wrapper directory (e.g. "owner-repo-<sha>/").
func FetchTarball(ctx context.Context, repository, ref string) (*ExtractedRepo, error) {
	url := fmt.Sprintf("%s/%s/tar.gz/%s", CodeloadBaseURL, repository, ref)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request for %s: %w", url, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: repository %q or ref %q not found", ErrRepositoryUnreachable, repository, ref)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: unexpected status %d fetching %s", ErrRepositoryUnreachable, resp.StatusCode, url)
	}

	extracted, err := extractTarGz(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to extract tarball for %s@%s: %w", repository, ref, err)
	}

	return stripTopLevelDir(extracted)
}

// extractTarGz reads a gzip-compressed tar stream fully into memory.
func extractTarGz(r io.Reader) (*ExtractedRepo, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to open gzip stream: %w", err)
	}
	defer func() { _ = gz.Close() }()

	extracted := &ExtractedRepo{files: map[string][]byte{}, dirs: map[string]bool{}}

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return extracted, nil
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar entry: %w", err)
		}

		// Guard against path traversal ("zip slip") from a malicious/unexpected tarball.
		name := path.Clean(hdr.Name)
		if name == ".." || strings.HasPrefix(name, "../") {
			return nil, fmt.Errorf("tar entry %q escapes the archive root", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			extracted.dirs[name] = true
			markParentDirs(extracted.dirs, name)
		case tar.TypeReg:
			data, err := io.ReadAll(tr)
			if err != nil {
				return nil, fmt.Errorf("failed to read %q: %w", hdr.Name, err)
			}
			extracted.files[name] = data
			markParentDirs(extracted.dirs, name)
		default:
			// Skip symlinks and other special entries — HACS repos are plain source trees.
			continue
		}
	}
}

// markParentDirs ensures every ancestor directory of p is recorded, so ReadDir
// sees intermediate directories even if the tarball never emitted an explicit
// TypeDir entry for them (some tarballs only list files).
func markParentDirs(dirs map[string]bool, p string) {
	dir := path.Dir(p)
	for dir != "." && dir != "/" && dir != "" {
		if dirs[dir] {
			return
		}
		dirs[dir] = true
		dir = path.Dir(dir)
	}
}

// stripTopLevelDir returns a new ExtractedRepo with the tarball's single top-level
// directory (which GitHub's codeload always wraps content in) removed from every path.
func stripTopLevelDir(extracted *ExtractedRepo) (*ExtractedRepo, error) {
	topDirs := map[string]bool{}
	for p := range extracted.files {
		if idx := strings.Index(p, "/"); idx >= 0 {
			topDirs[p[:idx]] = true
		}
	}
	for p := range extracted.dirs {
		if idx := strings.Index(p, "/"); idx >= 0 {
			topDirs[p[:idx]] = true
		} else if p != "" {
			topDirs[p] = true
		}
	}
	if len(topDirs) != 1 {
		return nil, fmt.Errorf("expected exactly one top-level directory in tarball, found %d", len(topDirs))
	}
	var prefix string
	for d := range topDirs {
		prefix = d + "/"
	}

	stripped := &ExtractedRepo{files: map[string][]byte{}, dirs: map[string]bool{}}
	for p, data := range extracted.files {
		if rel, ok := strings.CutPrefix(p, prefix); ok && rel != "" {
			stripped.files[rel] = data
		}
	}
	for p := range extracted.dirs {
		if rel, ok := strings.CutPrefix(p, prefix); ok && rel != "" {
			stripped.dirs[rel] = true
		}
	}
	return stripped, nil
}
