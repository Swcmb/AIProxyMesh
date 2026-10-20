// Package webui embeds the static management interface.
package webui

import (
	"embed"
	"io/fs"
)

// embedded contains the compiled Vite output under internal/webui/dist.
//
//go:embed dist
var embedded embed.FS

// Assets is the embedded WebUI rooted at the dist directory.
var Assets fs.FS

func init() {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic("webui: embedded dist is invalid: " + err.Error())
	}
	Assets = sub
}
