// Package web embeds the built browser interface.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var files embed.FS

func FS() (fs.FS, error) {
	return fs.Sub(files, "dist")
}
