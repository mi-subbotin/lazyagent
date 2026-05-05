// Package templates exposes the embedded prompt templates used by
// other lazyagent packages.
package templates

import (
	"embed"
	"io/fs"
)

//go:embed *.md
var fsys embed.FS

// FS returns the embedded template filesystem.
func FS() fs.FS { return fsys }

// Read returns the bytes of a named template (e.g. "doctor.md").
func Read(name string) ([]byte, error) {
	return fsys.ReadFile(name)
}
