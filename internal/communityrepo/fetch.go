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
	"time"
)

// CodeloadBaseURL is the GitHub tarball endpoint used instead of a git client or the
// REST API: no new dependency, and a separate, generally higher rate limit than the
// unauthenticated REST API's 60 requests/hour/IP.
// Exported (not const) so callers outside this package — controller envtest specs,
// E2E suites — can point it at a local fixture server instead of the real GitHub host.
var CodeloadBaseURL = "https://codeload.github.com"

// codeloadHTTPClient has an explicit, finite timeout — the operator's reconcile
// loop must never block indefinitely on a hung connection to codeload.github.com
// (or a test fixture server), unlike http.DefaultClient which has none.
var codeloadHTTPClient = &http.Client{Timeout: 60 * time.Second}

// Limits on extraction, bounding worst-case memory use against a malicious or
// misbehaving upstream repository/host.
//
// maxExtractedTotalBytes matches the cumulative limit the init-container/sidecar
// enforces while materializing the same archive onto the Home Assistant PVC, so a
// repository that passes validation here cannot fail later for being too big.
//
// maxRetainedEntryBytes is deliberately *not* a rejection threshold: this package
// only ever reads the content of small metadata files (hacs.json,
// custom_components/*/manifest.json) and merely checks the existence of everything
// else, so the content of a larger entry is skipped instead of being held in memory.
// Rejecting on a per-file size would break legitimate repositories: integrations
// that render device maps (dreame-vacuum, for one) ship generated Python sources
// with tens of megabytes of base64-encoded assets in a single file.
//
// maxRetainedTotalBytes caps what that skipping leaves behind. Because entries above
// maxRetainedEntryBytes cost no memory, maxExtractedTotalBytes measures the size of
// the archive and no longer the memory it needs — an archive of many small files can
// sit far below it and still be held in full. This second ceiling bounds that: an
// operator container running at the chart's default 128Mi limit must fail one
// repository with a diagnosable status rather than be OOM-killed, which would restart
// every reconciler in the cluster and leave no trace on the resource that caused it.
const (
	maxRetainedEntryBytes  = 1 * 1024 * 1024   // 1 MiB — above this, content is not kept
	maxRetainedTotalBytes  = 32 * 1024 * 1024  // 32 MiB of content actually held in memory
	maxExtractedTotalBytes = 100 * 1024 * 1024 // 100 MiB cumulative across the archive
	maxExtractedEntries    = 20000             // archive entry count
)

// ExtractedRepo is an in-memory view of a fetched repository's contents, with the
// tarball's single top-level wrapper directory already stripped. Kept entirely in
// memory — never written to disk — because the operator container runs with
// readOnlyRootFilesystem and no writable /tmp (a production deployment default;
// os.MkdirTemp there fails outright, which is exactly the bug this design avoids).
// Every regular file of the archive is listed, but only content below
// maxRetainedEntryBytes is held (see that constant), which keeps the memory cost
// close to the size of the repository's metadata rather than of its assets.
type ExtractedRepo struct {
	files map[string]fileEntry // cleaned relative path -> entry, regular files only
	dirs  map[string]bool      // cleaned relative path -> true, includes intermediate dirs
}

// fileEntry is one regular file of the archive. content is nil and retained is
// false for a file whose content was skipped as too large to hold in memory; size
// is the declared size either way, so ReadFile can explain itself.
type fileEntry struct {
	content  []byte
	retained bool
	size     int64
}

// DirEntry is one entry returned by ExtractedRepo.ReadDir.
type DirEntry struct {
	Name  string
	IsDir bool
}

// ReadFile returns the content of the file at the given path (relative to the
// repository root), or an error satisfying errors.Is(err, ErrNotExist) if absent.
// A file larger than maxRetainedEntryBytes exists but has no content held in
// memory; reading it returns an error satisfying errors.Is(err, ErrContentNotRetained).
func (e *ExtractedRepo) ReadFile(p string) ([]byte, error) {
	clean := path.Clean(p)
	entry, ok := e.files[clean]
	if !ok {
		return nil, fmt.Errorf("%s: %w", p, ErrNotExist)
	}
	if !entry.retained {
		return nil, fmt.Errorf("%s (%d bytes, retained up to %d): %w",
			p, entry.size, maxRetainedEntryBytes, ErrContentNotRetained)
	}
	return entry.content, nil
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

// Exists reports whether the given path (file or directory) exists, regardless of
// whether the file's content was retained.
func (e *ExtractedRepo) Exists(p string) bool {
	clean := path.Clean(p)
	if _, ok := e.files[clean]; ok {
		return true
	}
	return e.dirs[clean]
}

// ErrNotExist is returned by ExtractedRepo methods for a missing path.
var ErrNotExist = errors.New("path does not exist")

// ErrContentNotRetained is returned by ReadFile for a file that exists in the
// archive but whose content was too large to hold in memory (see
// maxRetainedEntryBytes).
var ErrContentNotRetained = errors.New("file content was not retained in memory")

// FetchTarball downloads the tarball for repository ("owner/repo") at ref (tag,
// branch, or commit) and extracts it entirely in memory, stripping the tarball's
// single top-level wrapper directory (e.g. "owner-repo-<sha>/").
func FetchTarball(ctx context.Context, repository, ref string) (*ExtractedRepo, error) {
	url := fmt.Sprintf("%s/%s/tar.gz/%s", CodeloadBaseURL, repository, ref)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to build request for %s: %w", url, err)
	}

	resp, err := codeloadHTTPClient.Do(req)
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

// extractTarGz reads a gzip-compressed tar stream into memory, enforcing bounded
// extraction (maxExtractedTotalBytes/maxExtractedEntries) against a decompression
// bomb or a runaway/malicious archive — decompressed size is what matters here
// (not the compressed transfer size), since gzip can expand many times over.
// Entries above maxRetainedEntryBytes are indexed by path but their content is
// streamed past rather than held.
func extractTarGz(r io.Reader) (*ExtractedRepo, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("failed to open gzip stream: %w", err)
	}
	defer func() { _ = gz.Close() }()

	extracted := &ExtractedRepo{files: map[string]fileEntry{}, dirs: map[string]bool{}}

	var totalBytes, retainedBytes int64
	var entryCount int
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return extracted, nil
		}
		if err != nil {
			return nil, fmt.Errorf("failed to read tar entry: %w", err)
		}

		entryCount++
		if entryCount > maxExtractedEntries {
			return nil, fmt.Errorf("archive has too many entries (limit %d)", maxExtractedEntries)
		}

		// Guard against path traversal ("zip slip") from a malicious/unexpected tarball.
		name := path.Clean(hdr.Name)
		if name == ".." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			return nil, fmt.Errorf("tar entry %q escapes the archive root", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			extracted.dirs[name] = true
			markParentDirs(extracted.dirs, name)
		case tar.TypeReg:
			if hdr.Size < 0 {
				return nil, fmt.Errorf("tar entry %q declares a negative size (%d)", hdr.Name, hdr.Size)
			}
			totalBytes += hdr.Size
			if totalBytes > maxExtractedTotalBytes {
				return nil, fmt.Errorf("archive exceeds the cumulative extraction limit (%d bytes)", maxExtractedTotalBytes)
			}
			entry := fileEntry{size: hdr.Size}
			if hdr.Size <= maxRetainedEntryBytes {
				retainedBytes += hdr.Size
				if retainedBytes > maxRetainedTotalBytes {
					return nil, fmt.Errorf(
						"archive holds more than %d bytes of small files, too much to keep in memory",
						maxRetainedTotalBytes)
				}
				// Allocate exactly the declared size and read into it: io.ReadAll
				// would grow its buffer by doubling, transiently costing several
				// times the file size in an operator container that runs with a
				// modest memory limit. tar.Reader never yields more than hdr.Size
				// bytes for an entry, so no separate cap on the read is needed.
				entry.content = make([]byte, hdr.Size)
				if _, err := io.ReadFull(tr, entry.content); err != nil {
					return nil, fmt.Errorf("failed to read %q: %w", hdr.Name, err)
				}
				entry.retained = true
			}
			extracted.files[name] = entry
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

	stripped := &ExtractedRepo{files: map[string]fileEntry{}, dirs: map[string]bool{}}
	for p, entry := range extracted.files {
		if rel, ok := strings.CutPrefix(p, prefix); ok && rel != "" {
			stripped.files[rel] = entry
		}
	}
	for p := range extracted.dirs {
		if rel, ok := strings.CutPrefix(p, prefix); ok && rel != "" {
			stripped.dirs[rel] = true
		}
	}
	return stripped, nil
}
