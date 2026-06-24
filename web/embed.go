// Package web embeds the built dashboard (dist/) into the Go binary so end users never need Node.
package web

import "embed"

// Dist holds the Vite build output. A placeholder index lives in dist/ so the Go build succeeds
// before the frontend has been built; `pnpm build` replaces it.
//
//go:embed all:dist
var Dist embed.FS
