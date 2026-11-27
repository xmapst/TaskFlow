package runner

import (
	"fmt"
	"os"
	"path/filepath"
)

// openWorkdir opens the task workdir as an os.Root. Every file operation done
// through the returned root is confined to the workdir: the kernel-level
// resolution rejects "..", absolute paths and symlinks that point outside, on
// Linux, macOS and Windows alike. The workdir is created when missing.
func openWorkdir(workspace string) (*os.Root, error) {
	if workspace == "" {
		return nil, fmt.Errorf("task workdir is empty")
	}
	if err := os.MkdirAll(workspace, os.ModePerm); err != nil {
		return nil, err
	}
	return os.OpenRoot(workspace)
}

// localPath cleans a user supplied path and requires it to be a relative path
// that stays inside the workdir. It is a fast, readable early check; the actual
// confinement (including symlinks) is enforced by os.Root.
func localPath(p string) (string, error) {
	p = filepath.Clean(p)
	if !filepath.IsLocal(p) {
		return "", fmt.Errorf("path %q must be a relative path inside the task workdir", p)
	}
	return p, nil
}
