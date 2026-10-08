package database

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/idehen-divine/GinPlate/migrations"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	gomysql "gorm.io/driver/mysql"
	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	glogger "gorm.io/gorm/logger"
)

// Connect opens a GORM handle for driver ("mysql"|"pgsql") and DSN.
// MySQL DSNs are go-sql-driver shaped (user:pass@tcp(host:port)/db?params);
// Postgres DSNs are URL form (postgres://user:pass@host:port/db?sslmode=...).
// Pass an optional GORM logger (e.g. from pkg/logger); defaults to silent.
func Connect(driver, dsn string, gormLog ...glogger.Interface) (*gorm.DB, error) {
	gl := glogger.Default.LogMode(glogger.Silent)
	if len(gormLog) > 0 && gormLog[0] != nil {
		gl = gormLog[0]
	}
	var dialector gorm.Dialector
	switch driver {
	case "pgsql", "postgres", "postgresql", "pgx":
		dialector = gormpg.Open(dsn)
	default:
		dialector = gomysql.Open(dsn)
	}
	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: gl,
		// TranslateError normalizes driver errors (e.g. duplicate keys)
		// into matchable sentinels like gorm.ErrDuplicatedKey.
		TranslateError: true,
	})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetMaxOpenConns(20)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)
	// GORM opens lazily: ping so boot fails fast on a dead database
	// instead of succeeding until the first query.
	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// DB binds a normalized driver, DSN, and migration domain once, so callers
// stop threading driver strings through every migration call. Backs the
// `migrate` subcommands.
type DB struct {
	driver string
	dsn    string
	domain string
}

// For normalizes the engine once and binds DSN + migration domain
// (migrations/<domain>/{mysql,pgsql}, e.g. app/pgsql).
func For(driver, dsn, domain string) DB {
	return DB{driver: NormalizeDriver(driver), dsn: dsn, domain: domain}
}

// dir is the embedded migration subdirectory for this handle.
func (d DB) dir() string { return d.domain + "/" + d.driver }

// CreateDB creates the database if missing.
func (d DB) CreateDB() error {
	name := DBName(d.driver, d.dsn)
	if err := CreateDB(d.driver, ServerDSN(d.driver, d.dsn), name); err != nil {
		return err
	}
	return nil
}

// migrationTimeout bounds every migration operation so a blocked database
// cannot hang a deploy forever. Callers needing cancellation pass their own
// context to the *Ctx variants.
const migrationTimeout = 2 * time.Minute

// Up applies all pending migrations. Backs `migrate up`.
func (d DB) Up() error {
	return d.UpCtx(context.Background())
}

// UpCtx applies all pending migrations with cancellation.
func (d DB) UpCtx(ctx context.Context) error {
	return withDBCtx(ctx, d.driver, d.dsn, func(ctx context.Context, db *sql.DB) error {
		return goose.UpContext(ctx, db, d.dir())
	})
}

// Status reports applied/pending migrations. Backs `migrate status`.
func (d DB) Status() error {
	return d.StatusCtx(context.Background())
}

// StatusCtx reports applied/pending migrations with cancellation.
func (d DB) StatusCtx(ctx context.Context) error {
	return withDBCtx(ctx, d.driver, d.dsn, func(ctx context.Context, db *sql.DB) error {
		return goose.StatusContext(ctx, db, d.dir())
	})
}

// RollbackLast reverts the most recently applied migration. Backs
// `migrate rollback`.
func (d DB) RollbackLast() error {
	return d.RollbackLastCtx(context.Background())
}

// RollbackLastCtx reverts the most recent migration with cancellation.
func (d DB) RollbackLastCtx(ctx context.Context) error {
	return withDBCtx(ctx, d.driver, d.dsn, func(ctx context.Context, db *sql.DB) error {
		return goose.DownContext(ctx, db, d.dir())
	})
}

// RollbackAll reverts every applied migration, newest first. Backs
// `migrate reset`.
func (d DB) RollbackAll() error {
	return d.RollbackAllCtx(context.Background())
}

// RollbackAllCtx reverts every migration with cancellation.
func (d DB) RollbackAllCtx(ctx context.Context) error {
	return withDBCtx(ctx, d.driver, d.dsn, func(ctx context.Context, db *sql.DB) error {
		return goose.DownToContext(ctx, db, d.dir(), 0)
	})
}

// Refresh rolls everything back and re-applies it. Backs `migrate refresh`.
func (d DB) Refresh() error {
	if err := d.RollbackAll(); err != nil {
		return err
	}
	return d.Up()
}

// Fresh drops every table in the database, then runs all migrations from
// scratch. Backs `migrate fresh`. Destructive by design — never run it
// against data you want to keep.
func (d DB) Fresh() error {
	if err := dropAllTables(d.driver, d.dsn); err != nil {
		return fmt.Errorf("drop tables: %w", err)
	}
	return d.Up()
}

// NormalizeDriver maps GORM/generic names to canonical "mysql"|"pgsql".
func NormalizeDriver(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pgsql", "postgres", "postgresql", "pgx":
		return "pgsql"
	default:
		return "mysql"
	}
}

// gooseDialect maps canonical drivers to goose dialect names.
func gooseDialect(driver string) string {
	if NormalizeDriver(driver) == "pgsql" {
		return "postgres"
	}
	return "mysql"
}

// sqlDriver maps canonical drivers to database/sql driver names.
func sqlDriver(driver string) string {
	if NormalizeDriver(driver) == "pgsql" {
		return "pgx"
	}
	return "mysql"
}

// Open returns a *sql.DB for the canonical driver and GORM-format DSN.
// Postgres DSNs must be URL form (postgres://user:pass@host:port/db?sslmode=...).
func Open(driver, gormDSN string) (*sql.DB, error) {
	return sql.Open(sqlDriver(driver), gormDSN)
}

// withDB opens a database/sql handle with the goose dialect selected and the
// embedded migration FS mounted, then runs fn.
func withDB(driver, gormDSN string, fn func(db *sql.DB) error) error {
	return withDBCtx(context.Background(), driver, gormDSN, func(_ context.Context, db *sql.DB) error {
		return fn(db)
	})
}

// withDBCtx is withDB with cancellation: every migration runs under a
// timeout so a blocked connection cannot hang a deploy indefinitely.
func withDBCtx(ctx context.Context, driver, gormDSN string, fn func(ctx context.Context, db *sql.DB) error) error {
	driver = NormalizeDriver(driver)
	if err := goose.SetDialect(gooseDialect(driver)); err != nil {
		return err
	}
	goose.SetBaseFS(migrations.FS)
	db, err := Open(driver, gormDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, migrationTimeout)
	defer cancel()
	return fn(ctx, db)
}

// dropAllTables empties the database without dropping the database itself,
// so no re-CREATE or privilege juggling is needed afterwards.
func dropAllTables(driver, gormDSN string) error {
	db, err := Open(driver, gormDSN)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), migrationTimeout)
	defer cancel()
	if driver == "pgsql" {
		if _, err := db.ExecContext(ctx, `DROP SCHEMA public CASCADE`); err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, `CREATE SCHEMA public`)
		return err
	}
	dbName := DBName(driver, gormDSN)
	rows, err := db.QueryContext(ctx,
		`SELECT TABLE_NAME FROM INFORMATION_SCHEMA.TABLES WHERE TABLE_SCHEMA = ?`, dbName)
	if err != nil {
		return err
	}
	var tables []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return err
		}
		tables = append(tables, t)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `SET FOREIGN_KEY_CHECKS = 0`); err != nil {
		return err
	}
	defer func() {
		if _, err := db.ExecContext(context.Background(), `SET FOREIGN_KEY_CHECKS = 1`); err != nil {
			// Best-effort restore; the drop already succeeded.
			_ = err
		}
	}()
	for _, t := range tables {
		if !identRe.MatchString(t) {
			return fmt.Errorf("refusing to drop unexpected table %q", t)
		}
		if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS `"+t+"`"); err != nil {
			return err
		}
	}
	return nil
}

// ServerDSN drops the database name so we can connect before it exists.
// Handles MySQL (user:pass@tcp(h)/db?params) and Postgres URL DSNs.
func ServerDSN(driver, gormDSN string) string {
	if NormalizeDriver(driver) == "pgsql" {
		if i := strings.LastIndex(gormDSN, "/"); i >= 0 {
			q := ""
			if j := strings.Index(gormDSN[i:], "?"); j >= 0 {
				q = gormDSN[i+j:]
			}
			return gormDSN[:i] + "/postgres" + q
		}
		return gormDSN
	}
	slash := strings.LastIndex(gormDSN, "/")
	q := strings.Index(gormDSN, "?")
	params := ""
	if q >= 0 {
		params = gormDSN[q:]
	}
	return gormDSN[:slash+1] + params
}

// DBName extracts the database name from a GORM-format DSN (both shapes).
func DBName(driver, gormDSN string) string {
	if NormalizeDriver(driver) == "pgsql" {
		rest := gormDSN
		if i := strings.LastIndex(rest, "/"); i >= 0 {
			rest = rest[i+1:]
		}
		if i := strings.Index(rest, "?"); i >= 0 {
			rest = rest[:i]
		}
		return rest
	}
	slash := strings.LastIndex(gormDSN, "/")
	rest := gormDSN[slash+1:]
	if i := strings.Index(rest, "?"); i >= 0 {
		rest = rest[:i]
	}
	return rest
}

// CreateDB creates the named database if missing.
func CreateDB(driver, serverGormDSN, name string) error {
	driver = NormalizeDriver(driver)
	if !identRe.MatchString(name) {
		return fmt.Errorf("invalid database name %q", name)
	}
	db, err := Open(driver, ServerDSN(driver, serverGormDSN))
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), migrationTimeout)
	defer cancel()
	if driver == "pgsql" {
		var one int
		// datname cannot use a placeholder in all pg drivers for this
		// catalog probe; name is identRe-validated above.
		err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT 1 FROM pg_database WHERE datname = '%s'`, name)).Scan(&one)
		if err == nil {
			return nil // exists
		}
		_, err = db.ExecContext(ctx, fmt.Sprintf(`CREATE DATABASE "%s"`, name))
		return err
	}
	_, err = db.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS `"+name+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
	return err
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// quoteIdent validates a role/db identifier (injection guard for DDL that
// cannot use placeholders) and returns it double-quoted for Postgres.
func quoteIdent(s string) string {
	if !identRe.MatchString(s) {
		return ""
	}
	return s
}
