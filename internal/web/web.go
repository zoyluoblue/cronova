// Package web embeds the cronova console static assets so the single binary
// serves its own UI.
package web

import (
	"embed"
	"io/fs"
	"os"
)

//go:embed static
var content embed.FS

// FS returns the console's static web assets. By default these are the copies
// embedded into the binary at build time.
//
// For local UI development, set CRONOVA_WEB_DIR to a directory (e.g.
// internal/web/static) to serve the assets straight from disk instead — edits
// then show up on a browser reload, with no rebuild. Dev-only: production runs
// leave it unset and serve the embedded copies.
func FS() fs.FS {
	if dir := os.Getenv("CRONOVA_WEB_DIR"); dir != "" {
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return os.DirFS(dir)
		}
	}
	sub, err := fs.Sub(content, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
