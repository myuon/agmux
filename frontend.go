package agmux

import (
	"embed"
	"io/fs"
)

//go:embed all:frontend/dist
var frontendDist embed.FS

func FrontendFS() (fs.FS, error) {
	return fs.Sub(frontendDist, "frontend/dist")
}
