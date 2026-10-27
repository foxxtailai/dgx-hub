package web

import "embed"

// Assets contains the production React build. Run the build script before compiling Go.
//
//go:embed all:dist
var Assets embed.FS
