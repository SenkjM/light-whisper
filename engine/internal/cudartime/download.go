// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package cudartime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type redistribRoot map[string]json.RawMessage

type redistribPkg struct {
	Name    string                     `json:"name"`
	Version string                     `json:"version"`
	Win     *redistribArtifact         `json:"windows-x86_64"`
	Linux   *redistribArtifact         `json:"linux-x86_64"`
}

type redistribArtifact struct {
	RelativePath string `json:"relative_path"`
	SHA256       string `json:"sha256"`
	Size         string `json:"size"`
}

func fetchRedistrib(ctx context.Context, client *http.Client, label string) (map[string]redistribPkg, error) {
	url := fmt.Sprintf("%s/redistrib_%s.json", redistribBase, label)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("redistrib %s: HTTP %d", label, resp.StatusCode)
	}
	var raw redistribRoot
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := map[string]redistribPkg{}
	for _, id := range RedistribPackages {
		blob, ok := raw[id]
		if !ok {
			return nil, fmt.Errorf("redistrib missing package %s", id)
		}
		var pkg redistribPkg
		if err := json.Unmarshal(blob, &pkg); err != nil {
			return nil, fmt.Errorf("parse %s: %w", id, err)
		}
		out[id] = pkg
	}
	return out, nil
}

func artifactFor(pkg redistribPkg, goos string) (*redistribArtifact, error) {
	if goos == "windows" {
		if pkg.Win == nil {
			return nil, fmt.Errorf("%s: no windows-x86_64 artifact", pkg.Name)
		}
		return pkg.Win, nil
	}
	if pkg.Linux == nil {
		return nil, fmt.Errorf("%s: no linux-x86_64 artifact", pkg.Name)
	}
	return pkg.Linux, nil
}

// downloadFile resumes into dest partial file, verifies sha256, then renames to final.
func downloadFile(ctx context.Context, client *http.Client, url, shaExpected, finalPath string, progress func(done, total int64)) error {
	if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
		return err
	}
	partial := finalPath + ".partial"
	var start int64
	if st, err := os.Stat(partial); err == nil {
		start = st.Size()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if start > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", start))
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		start = 0 // server ignored range; rewrite
		_ = os.Remove(partial)
	case http.StatusPartialContent:
		// resume
	default:
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	total := start
	if cl := resp.ContentLength; cl > 0 {
		total = start + cl
	}
	flag := os.O_CREATE | os.O_WRONLY
	if start > 0 && resp.StatusCode == http.StatusPartialContent {
		flag |= os.O_APPEND
	} else {
		flag |= os.O_TRUNC
		start = 0
	}
	f, err := os.OpenFile(partial, flag, 0o644)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	if start > 0 {
		// Hash existing bytes.
		rf, err := os.Open(partial)
		if err != nil {
			f.Close()
			return err
		}
		if _, err := io.Copy(hasher, io.LimitReader(rf, start)); err != nil {
			rf.Close()
			f.Close()
			return err
		}
		rf.Close()
	}
	buf := make([]byte, 256<<10)
	done := start
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				f.Close()
				return err
			}
			_, _ = hasher.Write(buf[:n])
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return rerr
		}
		select {
		case <-ctx.Done():
			f.Close()
			return ctx.Err()
		default:
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	sum := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(sum, shaExpected) {
		_ = os.Remove(partial)
		return fmt.Errorf("sha256 mismatch for %s: got %s want %s", filepath.Base(finalPath), sum, shaExpected)
	}
	_ = os.Remove(finalPath)
	if err := os.Rename(partial, finalPath); err != nil {
		return err
	}
	if progress != nil {
		progress(done, done)
	}
	return nil
}

func extractNeededDLLs(zipPath, destDir string, want []string) error {
	return extractZipFiles(zipPath, destDir, want)
}

func writeVerifiedManifest(dir, version string, files map[string]string) error {
	type entry struct {
		Name   string `json:"name"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}
	man := struct {
		Version   string            `json:"version"`
		Backend   string            `json:"backend"`
		CUDARuntime string          `json:"cuda_runtime"`
		Files     map[string]entry  `json:"files"`
		FetchedAt time.Time         `json:"fetched_at"`
	}{
		Version: version, Backend: "cuda", CUDARuntime: "bundled_on_demand",
		Files: map[string]entry{}, FetchedAt: time.Now().UTC(),
	}
	for name, path := range files {
		sum, size, err := fileSHA256(path)
		if err != nil {
			return err
		}
		man.Files[name] = entry{Name: name, SHA256: sum, Size: size}
	}
	b, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "runtime-manifest.json"), append(b, '\n'), 0o644)
}

func fileSHA256(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
