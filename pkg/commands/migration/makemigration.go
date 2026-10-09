package migration

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/spf13/cobra"
)

// NewMakeMigrationCmd builds the `make:migration` generator: writes versioned
// goose files for both dialects (UTC timestamps, so files order by creation).
// Pass --create for a CREATE TABLE stub, otherwise blank Up/Down.
func NewMakeMigrationCmd() *cobra.Command {
	var root, domain, create string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "make:migration <Name>",
		Short: "Generate a new versioned migration for both dialects",
		Long: `Generate goose migration files, e.g.:

	ginplate make:migration CreatePostsTable
	ginplate make:migration CreatePostsTable --create posts   # with CREATE TABLE stub

Files go to <dir>/<domain>/{mysql,pgsql}/YYYYMMDDHHMMSS_snake_name.sql using
a timestamp newer than every existing migration. Applies with ` + "`ginplate migrate up`" + `.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return generateMigration(cmd, root, domain, args[0], create, dryRun)
		},
	}
	cmd.Flags().StringVar(&root, "dir", "migrations", "Migrations root (run from repo root)")
	cmd.Flags().StringVar(&domain, "domain", "app", "Migration domain (subdirectory per dialect)")
	cmd.Flags().StringVar(&create, "create", "", "Table name to scaffold a CREATE TABLE stub for")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print the files instead of writing them")
	return cmd
}

var tableNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var migrationNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)

var mysqlTemplate = template.Must(template.New("mysql").Parse(`-- +goose Up

{{if .Table}}CREATE TABLE IF NOT EXISTS {{.Table}} (
  id CHAR(36) PRIMARY KEY,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
{{else}}-- Add your DDL here.
{{end}}
-- +goose Down

{{if .Table}}DROP TABLE IF EXISTS {{.Table}};
{{else}}-- Add the matching rollback here.
{{end}}`))

var pgsqlTemplate = template.Must(template.New("pgsql").Parse(`-- +goose Up

{{if .Table}}CREATE TABLE IF NOT EXISTS {{.Table}} (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
{{else}}-- Add your DDL here.
{{end}}
-- +goose Down

{{if .Table}}DROP TABLE IF EXISTS {{.Table}};
{{else}}-- Add the matching rollback here.
{{end}}`))

type migrationData struct {
	Table string
}

// migrationFileBase derives the snake_case base from a name in any style.
func migrationFileBase(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if !migrationNameRe.MatchString(raw) {
		return "", fmt.Errorf("invalid name %q: use letters, digits, '-' or '_' (start with a letter)", raw)
	}
	var words []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == '-' || r == '_' || r == ' ' }) {
		words = append(words, splitMigrationWords(part)...)
	}
	lower := make([]string, len(words))
	for i, w := range words {
		lower[i] = strings.ToLower(w)
	}
	return strings.Join(lower, "_"), nil
}

func splitMigrationWords(s string) []string {
	var words []string
	start := 0
	for i := 1; i < len(s); i++ {
		c, p := s[i], s[i-1]
		if c >= 'A' && c <= 'Z' && ((p >= 'a' && p <= 'z') || (p >= '0' && p <= '9')) {
			words = append(words, s[start:i])
			start = i
		}
	}
	return append(words, s[start:])
}

// nextMigrationVersion returns a UTC timestamp version greater than every
// migration on disk (falls back to max+1 on clock skew).
func nextMigrationVersion(root, domain string, now time.Time) (int64, error) {
	var max int64
	for _, dialect := range []string{"mysql", "pgsql"} {
		entries, err := os.ReadDir(filepath.Join(root, domain, dialect))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return 0, err
		}
		for _, e := range entries {
			name := e.Name()
			if !strings.HasSuffix(name, ".sql") {
				continue
			}
			idx := strings.IndexByte(name, '_')
			if idx <= 0 {
				continue
			}
			v, err := strconv.ParseInt(name[:idx], 10, 64)
			if err != nil || v < 1 {
				continue
			}
			if v > max {
				max = v
			}
		}
	}
	candidate, err := strconv.ParseInt(now.UTC().Format("20060102150405"), 10, 64)
	if err != nil {
		return 0, err
	}
	if candidate <= max {
		candidate = max + 1
	}
	return candidate, nil
}

func renderMigration(tmpl *template.Template, table string) (string, error) {
	var sb strings.Builder
	if err := tmpl.Execute(&sb, migrationData{Table: table}); err != nil {
		return "", err
	}
	return sb.String(), nil
}

// generateMigration validates the name and writes both dialect files.
func generateMigration(cmd *cobra.Command, root, domain, raw, create string, dryRun bool) error {
	base, err := migrationFileBase(raw)
	if err != nil {
		return err
	}
	if create != "" && !tableNameRe.MatchString(create) {
		return fmt.Errorf("invalid table name %q: use letters, digits, underscore (start with a letter or _)", create)
	}
	version, err := nextMigrationVersion(root, domain, time.Now())
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%d_%s.sql", version, base)
	files := map[string]*template.Template{
		filepath.Join(root, domain, "mysql", name): mysqlTemplate,
		filepath.Join(root, domain, "pgsql", name): pgsqlTemplate,
	}
	rendered := map[string]string{}
	for path, tmpl := range files {
		out, err := renderMigration(tmpl, create)
		if err != nil {
			return err
		}
		rendered[path] = out
	}
	paths := []string{
		filepath.Join(root, domain, "mysql", name),
		filepath.Join(root, domain, "pgsql", name),
	}
	if dryRun {
		for _, p := range paths {
			cmd.Printf("--- %s ---\n%s\n", p, rendered[p])
		}
		return nil
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("file %s already exists", p)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(rendered[p]), 0o644); err != nil {
			return err
		}
		cmd.Printf("created %s\n", p)
	}
	cmd.Printf("apply with: ginplate migrate up\n")
	return nil
}
