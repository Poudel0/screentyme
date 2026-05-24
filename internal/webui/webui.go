package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:static
var assets embed.FS

// FS returns the embedded static assets rooted at index.html so the server
// can serve them directly at "/".
func FS() fs.FS {
	sub, err := fs.Sub(assets, "static")
	if err != nil {
		panic(err)
	}
	return sub
}
