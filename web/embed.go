package web

import "embed"

// DistFS holds the compiled React SPA assets under web/dist.
//
//go:embed all:dist
var DistFS embed.FS
