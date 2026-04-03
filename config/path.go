package config

import (
	"path/filepath"
)

func NormalizePath(p string) string {
	if p == "" {
		return ""
	}
	return filepath.Clean(p)
}
