// Package migrations embeds the goose migration files so the API and
// migrate CLI all carry their schema inside the binary — no file://
// paths, no working-directory surprises. Each domain ships both dialects;
// the runner selects <domain>/<driver> (e.g. app/pgsql).
package migrations

import "embed"

// FS holds every migration file.
//
//go:embed app/mysql/*.sql app/pgsql/*.sql
var FS embed.FS
