// Package web embeds the dashboard's static assets into the binary.
// See CLAUDE.md Phase 7.
package web

import "embed"

//go:embed all:static
var StaticFS embed.FS
