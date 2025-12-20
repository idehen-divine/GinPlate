package migration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMakeMigration is the single entry point for every make:migration
// test: file-base naming, version selection across clocks, and stub
// rendering.
func TestMakeMigration(t *testing.T) {
	t.Run("file-base", func(t *testing.T) {
		cases := map[string]string{
			"CreatePostsTable":   "create_posts_table",
			"create-posts-table": "create_posts_table",
			"create_posts_table": "create_posts_table",
		}
		for in, want := range cases {
			got, err := migrationFileBase(in)
			if err != nil {
				t.Fatalf("migrationFileBase(%q): %v", in, err)
			}
			if got != want {
				t.Errorf("migrationFileBase(%q) = %q, want %q", in, got, want)
			}
		}
		if _, err := migrationFileBase("bad name!"); err == nil {
			t.Error("expected error for invalid name")
		}
	})

	t.Run("next-version", func(t *testing.T) {
		root := t.TempDir()
		write := func(dialect, name string) {
			t.Helper()
			dir := filepath.Join(root, "app", dialect)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte("-- x"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		write("mysql", "00001_init_app.sql")
		write("mysql", "20261006190000_add_index.sql")
		write("pgsql", "20261006180000_init_app.sql")
		write("mysql", "README.md")

		now := time.Date(2026, 10, 6, 20, 0, 0, 0, time.UTC)
		v, err := nextMigrationVersion(root, "app", now)
		if err != nil {
			t.Fatal(err)
		}
		if v != 20261006200000 {
			t.Errorf("nextMigrationVersion = %d, want 20261006200000", v)
		}

		stale := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		v, err = nextMigrationVersion(root, "app", stale)
		if err != nil {
			t.Fatal(err)
		}
		if v != 20261006190001 {
			t.Errorf("stale clock: got %d, want 20261006190001", v)
		}

		v, err = nextMigrationVersion(root, "missing-domain", now)
		if err != nil {
			t.Fatal(err)
		}
		if v != 20261006200000 {
			t.Errorf("empty domain: got %d, want 20261006200000", v)
		}
	})

	t.Run("render-migration", func(t *testing.T) {
		withTable, err := renderMigration(mysqlTemplate, "posts")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"+goose Up", "+goose Down", "CREATE TABLE IF NOT EXISTS posts", "DROP TABLE IF EXISTS posts"} {
			if !strings.Contains(withTable, want) {
				t.Errorf("mysql stub missing %q", want)
			}
		}
		blank, err := renderMigration(pgsqlTemplate, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"+goose Up", "+goose Down", "Add your DDL here"} {
			if !strings.Contains(blank, want) {
				t.Errorf("pgsql blank missing %q", want)
			}
		}
		if strings.Contains(blank, "CREATE TABLE") {
			t.Error("blank stub should not contain CREATE TABLE")
		}
	})
}
