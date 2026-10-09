package migrations

import (
	"strings"
	"testing"
)

// TestTenantMirror enforces the tenants/ layout contract: the dedicated-DB
// + shared-pool domain mirrors the app/ tenant-data files byte-for-byte
// (modulo comments and blank lines) under the same per-dialect versions,
// minus the control-plane file that lives only in app/. Any schema change
// to tenant data must land in both domains; this test blocks merges that
// forget one side. Control-plane tables (tenants, tenant_domains,
// tenant_migrations, control_admins) are app/-only by the same rule in
// reverse. The CLI provisions the tenants/ domain via `migrate pool up`,
// `migrate provision <slug>`, and `migrate tenant <slug>`.
func TestTenantMirror(t *testing.T) {
	mirrored := map[string][]string{
		"mysql": {"20251107220300_users.sql", "20251107220301_init_app.sql"},
		"pgsql": {"20251108104100_users.sql", "20251108104101_init_app.sql"},
	}
	for dialect, names := range mirrored {
		for _, name := range names {
			appRaw, err := FS.ReadFile("app/" + dialect + "/" + name)
			if err != nil {
				t.Fatalf("app/%s/%s: %v", dialect, name, err)
			}
			tenantRaw, err := FS.ReadFile("tenants/" + dialect + "/" + name)
			if err != nil {
				t.Fatalf("tenants/%s/%s: %v", dialect, name, err)
			}
			appNorm, tenantNorm := normalizeSQL(string(appRaw)), normalizeSQL(string(tenantRaw))
			if appNorm != tenantNorm {
				t.Errorf("tenants/%s/%s diverged from app/ counterpart", dialect, name)
			}
		}
	}

	// Control-plane tables must never leak into dedicated databases.
	for _, dialect := range []string{"mysql", "pgsql"} {
		entries, err := FS.ReadDir("tenants/" + dialect)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			raw, err := FS.ReadFile("tenants/" + dialect + "/" + e.Name())
			if err != nil {
				t.Fatal(err)
			}
			upper := strings.ToUpper(normalizeSQL(string(raw)))
			for _, table := range []string{
				"CREATE TABLE IF NOT EXISTS TENANTS (",
				"CREATE TABLE IF NOT EXISTS TENANT_DOMAINS (",
				"CREATE TABLE IF NOT EXISTS TENANT_MIGRATIONS (",
				"CREATE TABLE IF NOT EXISTS CONTROL_ADMINS (",
			} {
				if strings.Contains(upper, table) {
					t.Errorf("tenants/%s/%s defines control table %q", dialect, e.Name(), table)
				}
			}
		}
	}
}

// TestAppTenancySchema guards the tenancy delta baked straight into the
// app/ base files: tenant_id columns on users, sessions, notifications,
// and password_reset_tokens; the composite (tenant_id, email) unique
// replacing the global email unique; the password_reset (email, kind) PK
// widening; the four control-plane tables; and the pgsql tenant_isolation
// RLS policies on tenant data only.
func TestAppTenancySchema(t *testing.T) {
	for _, dialect := range []string{"mysql", "pgsql"} {
		entries, err := FS.ReadDir("app/" + dialect)
		if err != nil {
			t.Fatal(err)
		}
		var combined strings.Builder
		for _, e := range entries {
			raw, err := FS.ReadFile("app/" + dialect + "/" + e.Name())
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(raw), "-- +goose Up") || !strings.Contains(string(raw), "-- +goose Down") {
				t.Errorf("app/%s/%s missing goose Up/Down markers", dialect, e.Name())
			}
			combined.WriteString(normalizeSQL(string(raw)))
			combined.WriteString("\n")
		}
		schema := strings.ToUpper(combined.String())

		for _, table := range []string{"USERS (", "SESSIONS (", "NOTIFICATIONS (", "PASSWORD_RESET_TOKENS ("} {
			if !strings.Contains(schema, "CREATE TABLE IF NOT EXISTS "+table) {
				t.Errorf("app/%s: missing table %s", dialect, table)
			}
		}
		if count := strings.Count(schema, "TENANT_ID"); count < 4 {
			t.Errorf("app/%s: expected tenant_id in 4+ tables, found %d mentions", dialect, count)
		}
		if !strings.Contains(schema, "TENANT_ID, EMAIL") && !strings.Contains(schema, "TENANT_ID , EMAIL") {
			t.Errorf("app/%s: missing composite (tenant_id, email) uniqueness", dialect)
		}
		// The global email unique must be gone from the users base file —
		// replaced by the composite. (Pre-tenancy databases predate this
		// branch and rebuild via `migrate fresh`; there is no ALTER path.)
		usersFile := map[string]string{
			"mysql": "20251107220300_users.sql",
			"pgsql": "20251108104100_users.sql",
		}[dialect]
		usersRaw, err := FS.ReadFile("app/" + dialect + "/" + usersFile)
		if err != nil {
			t.Fatalf("app/%s/%s: %v", dialect, usersFile, err)
		}
		usersNorm := strings.ToUpper(normalizeSQL(string(usersRaw)))
		if strings.Contains(usersNorm, "UQ_USERS_EMAIL (EMAIL)") || strings.Contains(usersNorm, "UNIQUE (EMAIL)") {
			t.Errorf("app/%s/%s: global email unique survived alongside the composite", dialect, usersFile)
		}
		if !strings.Contains(schema, "KIND") || !strings.Contains(schema, "PRIMARY KEY (EMAIL, KIND)") {
			t.Errorf("app/%s: missing password_reset kind/PK widening", dialect)
		}
		for _, table := range []string{
			"CREATE TABLE IF NOT EXISTS TENANTS (",
			"CREATE TABLE IF NOT EXISTS TENANT_DOMAINS (",
			"CREATE TABLE IF NOT EXISTS TENANT_MIGRATIONS (",
			"CREATE TABLE IF NOT EXISTS CONTROL_ADMINS (",
		} {
			if !strings.Contains(schema, table) {
				t.Errorf("app/%s: missing control table %q", dialect, table)
			}
		}
	}

	pgsqlEntries, err := FS.ReadDir("app/pgsql")
	if err != nil {
		t.Fatal(err)
	}
	var pgsql strings.Builder
	for _, e := range pgsqlEntries {
		raw, err := FS.ReadFile("app/pgsql/" + e.Name())
		if err != nil {
			t.Fatal(err)
		}
		pgsql.WriteString(normalizeSQL(string(raw)))
		pgsql.WriteString("\n")
	}
	pgsqlSchema := strings.ToUpper(pgsql.String())
	for _, table := range []string{"USERS", "NOTIFICATIONS", "SESSIONS", "PASSWORD_RESET_TOKENS"} {
		if !strings.Contains(pgsqlSchema, "TENANT_ISOLATION ON "+table) {
			t.Errorf("app/pgsql: missing tenant_isolation policy on %s", table)
		}
	}
	for _, table := range []string{"TENANTS", "TENANT_DOMAINS", "TENANT_MIGRATIONS", "CONTROL_ADMINS"} {
		if strings.Contains(pgsqlSchema, "TENANT_ISOLATION ON "+table) {
			t.Errorf("app/pgsql: control table %s must not carry a tenant_isolation policy", table)
		}
	}
}

// normalizeSQL strips line comments and collapses whitespace so formatting
// drift never fails the mirror (only real definition drift does).
func normalizeSQL(s string) string {
	var lines []string
	for _, line := range strings.Split(s, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
