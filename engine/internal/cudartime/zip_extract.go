// SPDX-FileCopyrightText: 2026 SenkjM
// SPDX-License-Identifier: AGPL-3.0-only

package cudartime

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func extractZipFiles(zipPath, destDir string, wantNames []string) error {
	want := map[string]bool{}
	for _, n := range wantNames {
		want[strings.ToLower(n)] = true
	}
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	found := map[string]bool{}
	for _, f := range r.File {
		base := filepath.Base(f.Name)
		key := strings.ToLower(base)
		if !want[key] && !strings.EqualFold(base, "LICENSE") && !strings.HasPrefix(strings.ToUpper(base), "CUDA") {
			// Also allow license files.
			if !strings.Contains(strings.ToUpper(f.Name), "LICENSE") {
				continue
			}
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if want[key] || strings.Contains(strings.ToUpper(f.Name), "LICENSE") {
			outName := base
			if strings.Contains(strings.ToUpper(f.Name), "LICENSE") && !want[key] {
				outName = "CUDA-LICENSE-" + filepath.Base(filepath.Dir(f.Name)) + ".txt"
				if outName == "CUDA-LICENSE-.txt" {
					outName = "CUDA-LICENSE.txt"
				}
			}
			if err := writeZipFile(f, filepath.Join(destDir, outName)); err != nil {
				return err
			}
			if want[key] {
				found[key] = true
			}
		}
	}
	for _, n := range wantNames {
		if !found[strings.ToLower(n)] {
			// cufft optional at extract of cudart/cublas zips
			if strings.HasPrefix(strings.ToLower(n), "cufft") {
				continue
			}
			return fmt.Errorf("zip %s missing %s", filepath.Base(zipPath), n)
		}
	}
	return nil
}

func writeZipFile(f *zip.File, dest string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	tmp := dest + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, rc)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	_ = os.Remove(dest)
	return os.Rename(tmp, dest)
}
