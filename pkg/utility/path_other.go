//go:build !windows

package utility

import (
	"path/filepath"
)

func DefaultDir(name string) string {
	return filepath.Join("/", "usr", "local", name)
}
