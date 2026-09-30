// Package web embeds the built frontend so the API server can serve the
// single-binary production build. The dist directory is populated by the
// frontend build step (see the repository Makefile).
package web

import "embed"

//go:embed all:dist
var Dist embed.FS
