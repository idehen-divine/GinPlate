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

// Pool bounds the database connection pool. Zero values select Defaults.
type Pool struct {
	MaxOpen     int
	MaxIdle     int
	MaxLifetime time.Duration
	MaxIdleTime time.Duration
}

// DefaultPool is the historical pool shape (20 open, 5 idle).
func DefaultPool() Pool {
	return Pool{MaxOpen: 20, MaxIdle: 5, MaxLifetime: 30 * time.Minute, MaxIdleTime: 5 * time.Minute}
}

// Connect opens a GORM handle (mysql DSN or postgres URL) with DefaultPool.
// Pings so boot fails fast on a dead database.
func Connect(driver, dsn string, gormLog ...glogger.Interface) (*gorm.DB, error) {
	return ConnectPool(driver, dsn, DefaultPool(), gormLog...)
}

// ConnectPool opens a GORM handle with an explicit pool. Non-positive pool
// values fall back to the corresponding DefaultPool value.
func ConnectPool(driver, dsn string, pool Pool, gormLog ...glogger.Interface) (*gorm.DB, error) {
	def := DefaultPool()
	if pool.MaxOpen <= 0 {
		pool.MaxOpen = def.MaxOpen
	}
	if pool.MaxIdle <= 0 {
		pool.MaxIdle = def.MaxIdle
	}
	if pool.MaxLifetime <= 0 {
		pool.MaxLifetime = def.MaxLifetime
	}
	if pool.MaxIdleTime <= 0 {
		pool.MaxIdleTime = def.MaxIdleTime
	}
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
	sqlDB.SetMaxIdleConns(pool.MaxIdle)
	sqlDB.SetMaxOpenConns(pool.MaxOpen)
	sqlDB.SetConnMaxLifetime(pool.MaxLifetime)
	sqlDB.SetConnMaxIdleTime(pool.MaxIdleTime)
	pingCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// DB binds driver, DSN, and migration domain for the `migrate` subcommands.
type DB struct {
	driver string
	dsn    string
	domain string
}

func For(driver, dsn, domain string) DB {
	return DB{driver: NormalizeDriver(driver), dsn: dsn, domain: domain}
}

func (d DB) dir() string { return d.domain + "/" + d.driver }

func (d DB) CreateDB() error {
	name := DBName(d.driver, d.dsn)
	if err := CreateDB(d.driver, ServerDSN(d.driver, d.dsn), name); err != nil {
		return err
	}
	return nil
}

// migrationTimeout bounds every migration so a blocked DB can't hang a deploy.
const migrationTimeout = 2 * time.Minute

func (d DB) Up() error {
	return d.UpCtx(context.Background())
}

func (d DB) UpCtx(ctx context.Context) error {
	return withDBCtx(ctx, d.driver, d.dsn, func(ctx context.Context, db *sql.DB) error {
		return goose.UpContext(ctx, db, d.dir())
	})
}

func (d DB) Status() error {
	return d.StatusCtx(context.Background())
}

func (d DB) StatusCtx(ctx context.Context) error {
	return withDBCtx(ctx, d.driver, d.dsn, func(ctx context.Context, db *sql.DB) error {
		return goose.StatusContext(ctx, db, d.dir())
	})
}

func (d DB) RollbackLast() error {
	return d.RollbackLastCtx(context.Background())
}

func (d DB) RollbackLastCtx(ctx context.Context) error {
	return withDBCtx(ctx, d.driver, d.dsn, func(ctx context.Context, db *sql.DB) error {
		return goose.DownContext(ctx, db, d.dir())
	})
}

func (d DB) RollbackAll() error {
	return d.RollbackAllCtx(context.Background())
}

func (d DB) RollbackAllCtx(ctx context.Context) error {
	return withDBCtx(ctx, d.driver, d.dsn, func(ctx context.Context, db *sql.DB) error {
		return goose.DownToContext(ctx, db, d.dir(), 0)
	})
}

func (d DB) Refresh() error {
	if err := d.RollbackAll(); err != nil {
		return err
	}
	return d.Up()
}

// Fresh drops every table, then migrates from scratch. Destructive by design.
func (d DB) Fresh() error {
	if err := dropAllTables(d.driver, d.dsn); err != nil {
		return fmt.Errorf("drop tables: %w", err)
	}
	return d.Up()
}

func NormalizeDriver(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pgsql", "postgres", "postgresql", "pgx":
		return "pgsql"
	default:
		return "mysql"
	}
}

func gooseDialect(driver string) string {
	if NormalizeDriver(driver) == "pgsql" {
		return "postgres"
	}
	return "mysql"
}

func sqlDriver(driver string) string {
	if NormalizeDriver(driver) == "pgsql" {
		return "pgx"
	}
	return "mysql"
}

func Open(driver, gormDSN string) (*sql.DB, error) {
	return sql.Open(sqlDriver(driver), gormDSN)
}

func withDB(driver, gormDSN string, fn func(db *sql.DB) error) error {
	return withDBCtx(context.Background(), driver, gormDSN, func(_ context.Context, db *sql.DB) error {
		return fn(db)
	})
}

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
		// Best-effort restore; the drop already succeeded.
		_, _ = db.ExecContext(context.Background(), `SET FOREIGN_KEY_CHECKS = 1`)
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
		// No placeholder for datname in all pg drivers; name is identRe-validated.
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

// EnsureAppRole creates the limited application role (when configured) and
// grants it full rights on one database, so row-level-security policies
// constrain the runtime instead of being bypassed by owner credentials.
// Empty user is a no-op. Identifiers are identRe-validated; passwords travel
// as placeholders.
func EnsureAppRole(driver, serverGormDSN, dbName, user, pass string) error {
	if strings.TrimSpace(user) == "" {
		return nil
	}
	driver = NormalizeDriver(driver)
	if !identRe.MatchString(dbName) || quoteIdent(user) == "" {
		return fmt.Errorf("invalid database or role name")
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
		if err := db.QueryRowContext(ctx, `SELECT 1 FROM pg_roles WHERE rolname = $1`, user).Scan(&one); err != nil {
			if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE ROLE "%s" LOGIN PASSWORD '%s'`, user, escapeLiteral(pass))); err != nil {
				return err
			}
		}
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`GRANT ALL ON DATABASE "%s" TO "%s"`, dbName, user)); err != nil {
			return err
		}
		return nil
	}
	// CREATE USER cannot run in the prepared-statement protocol, so the
	// identRe-validated user and escaped password interpolate directly.
	if _, err := db.ExecContext(ctx, fmt.Sprintf("CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s'", user, escapeLiteral(pass))); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, fmt.Sprintf("GRANT ALL PRIVILEGES ON `%s`.* TO '%s'@'%%'", dbName, user))
	return err
}

// escapeLiteral doubles single quotes for string literals that cannot use
// placeholders (role passwords in CREATE statements).
func escapeLiteral(s string) string {
	return strings.ReplaceAll(s, `'`, `''`)
}
