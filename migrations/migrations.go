// Package migrations embeds the goose migration files so the API and
// migrate CLI all carry their schema inside the binary — no file://
// paths, no working-directory surprises. Two domains ship both dialects:
// app (shared/control database: tenant data plus tenants, domains,
// ledger, and control admins) and tenants (dedicated tenant databases
// and shared pools: tenant data only, no control-plane tables). The
// runner selects <domain>/<driver> (e.g. tenants/pgsql); see TENANCY.md
// for the layout contract.
package migrations

import "embed"

// FS holds every migration file.
//
//go:embed app/mysql/*.sql app/pgsql/*.sql tenants/mysql/*.sql tenants/pgsql/*.sql
var FS embed.FS
