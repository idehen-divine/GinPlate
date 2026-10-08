package database

import (
	"strings"
	"testing"
)

// TestDatabase covers driver normalization, DSN shapes, name helpers, and
// destructive-command guards without a live database.
func TestDatabase(t *testing.T) {
	t.Run("normalize", func(t *testing.T) {
		for in, want := range map[string]string{
			"mysql": "mysql", "MYSQL": "mysql", "": "mysql",
			"pgsql": "pgsql", "postgres": "pgsql", "postgresql": "pgsql", "pgx": "pgsql",
		} {
			if got := NormalizeDriver(in); got != want {
				t.Errorf("NormalizeDriver(%q) = %q, want %q", in, got, want)
			}
		}
	})

	t.Run("server-dsn-and-name", func(t *testing.T) {
		mysql := "u:p@tcp(db:3306)/app?charset=utf8mb4&parseTime=True&loc=Local&tls=false"
		if got := DBName("mysql", mysql); got != "app" {
			t.Errorf("mysql DBName = %q", got)
		}
		if got := ServerDSN("mysql", mysql); strings.Contains(got, "/app") {
			t.Errorf("mysql ServerDSN still names db: %q", got)
		}
		pg := "postgres://u:p@db:5432/app?sslmode=verify-full"
		if got := DBName("pgsql", pg); got != "app" {
			t.Errorf("pgsql DBName = %q", got)
		}
		if got := ServerDSN("pgsql", pg); !strings.Contains(got, "/postgres?") {
			t.Errorf("pgsql ServerDSN = %q", got)
		}
	})

	t.Run("create-db-rejects-bad-name", func(t *testing.T) {
		// No connection is opened: validation runs before Open.
		for _, bad := range []string{"", "a;b", "a`b", "a/b", "a b", `"q"`} {
			if err := CreateDB("mysql", "u:p@tcp(db:3306)/?charset=utf8mb4", bad); err == nil {
				t.Errorf("CreateDB accepted %q", bad)
			}
		}
	})
}
