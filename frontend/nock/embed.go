// Package nockui embeds NOCK's built frontend (dist/, produced by `npm run build`) into the
// IDUNA binary, so serving it needs no separate static-file deploy step or Node process at
// runtime. go:embed can't reach outside the directory tree containing this file (no "../"
// patterns), which is why this tiny package lives here, co-located with dist/, rather than
// inside internal/http/handlers alongside the real page handler that actually serves it (see
// ../../internal/http/handlers/nock_page.go).
package nockui

import "embed"

//go:embed all:dist
var Dist embed.FS
