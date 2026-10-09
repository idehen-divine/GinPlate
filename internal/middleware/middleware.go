// Package middleware hosts app-specific guards. Generic ones (CORS, security,
// rate limiting) live in pkg/web/httpmw. Global guards self-register via
// init(); per-route guards are plain constructors used in routes.go.
// Never import internal/modules/* (would cycle).
package middleware
