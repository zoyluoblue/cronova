// Package web embeds the cronova console static assets so the single binary
// serves its own UI.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var content embed.FS

// FS returns the embedded static web assets (rooted at static/).
func FS() fs.FS {
	sub, err := fs.Sub(content, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
