// Package app hosts the runnable entrypoints behind the single ginplate
// binary (cmd/ginplate). Keeping the wiring here lets every command share
// it without drift.
package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/idehen-divine/GinPlate/internal/modules/cadmin"
	configPkg "github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/database"
	"github.com/idehen-divine/GinPlate/pkg/logger"
	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/notify"
	"github.com/idehen-divine/GinPlate/pkg/queue"
	redisPkg "github.com/idehen-divine/GinPlate/pkg/redis"
	"github.com/idehen-divine/GinPlate/pkg/session"
	"github.com/idehen-divine/GinPlate/pkg/tenancy"
	"github.com/idehen-divine/GinPlate/pkg/web"
	"github.com/idehen-divine/GinPlate/pkg/web/httpmw"

	_ "github.com/idehen-divine/GinPlate/docs"
	"github.com/redis/go-redis/v9"
)

// storageRoutePath derives the mount path from the public base URL
// (e.g. https://app.com/storage -> /storage). Empty or unparseable URLs
// fall back to /storage.
func storageRoutePath(publicURL string) string {
	if u, err := url.Parse(strings.TrimSpace(publicURL)); err == nil && strings.HasPrefix(u.Path, "/") {
		return u.Path
	}
	return "/storage"
}

// QueueResources owns the notification dispatch queue plus any Redis client
// opened for it, so shutdown closes exactly what startup created.
type QueueResources struct {
	Queue queue.Queue
	Close func() error
}

func noopClose() error { return nil }

// openNotifyQueue builds the queue behind notification dispatch (sync runs
// inline, database/redis persist for the worker). An explicitly configured
// redis backend that is unreachable fails startup instead of silently
// dispatching inline: delivery semantics must not change on outage.
func openNotifyQueue(config *configPkg.Config, databaseConnection *gorm.DB, redisClient *redis.Client, deps notify.Deps) (QueueResources, error) {
	reg := queue.NewRegistry()
	notify.Register(reg, deps)
	// The same registry also serves direct mail queueing (appmail.Queue):
	// without this, sync delivery of mail.send jobs reports unknown job.
	if deps.Sender != nil {
		pkgmail.Register(reg, deps.Sender)
	}
	switch config.Queue.Connection {
	case "", "sync":
		return QueueResources{Queue: queue.NewSync(reg), Close: noopClose}, nil
	case "database":
		q, err := queue.Open(config.Queue, databaseConnection, nil)
		return QueueResources{Queue: q, Close: noopClose}, err
	case "redis":
		if redisClient == nil {
			owned := redisPkg.DialOrNil(config.Database.Redis, 2*time.Second)
			if owned == nil {
				return QueueResources{}, fmt.Errorf("notify queue: redis unreachable at %s (QUEUE_CONNECTION=redis)", config.Database.Redis.Addr())
			}
			q, err := queue.Open(config.Queue, databaseConnection, owned)
			if err != nil {
				_ = owned.Close()
				return QueueResources{}, err
			}
			return QueueResources{Queue: q, Close: owned.Close}, nil
		}
		q, err := queue.Open(config.Queue, databaseConnection, redisClient)
		return QueueResources{Queue: q, Close: noopClose}, err
	default:
		q, err := queue.Open(config.Queue, databaseConnection, redisClient)
		return QueueResources{Queue: q, Close: noopClose}, err
	}
}

// dbPool maps the shared pool bounds from config (replicas share one
// database: size for replica count, not one process).
func dbPool(config *configPkg.Config) database.Pool {
	return database.Pool{
		MaxOpen:     config.Database.MaxOpenConns,
		MaxIdle:     config.Database.MaxIdleConns,
		MaxLifetime: time.Duration(config.Database.ConnMaxLifetime) * time.Second,
		MaxIdleTime: time.Duration(config.Database.ConnMaxIdleTime) * time.Second,
	}
}

func GormLogLevel(mode string) gormlogger.LogLevel {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "info":
		return gormlogger.Info
	case "warn":
		return gormlogger.Warn
	case "error":
		return gormlogger.Error
	default:
		return gormlogger.Silent
	}
}

func RunAPI(config *configPkg.Config) error {
	if config == nil {
		return fmt.Errorf("nil config")
	}
	appLog := logger.New(config.App.Env, config.Logging.Level, config.Logging.Output)
	defer appLog.Sync()

	// Hardened-environment guardrails: fail at boot, not in an incident.
	if configPkg.IsHardenedEnv(config.App.Env) {
		if config.App.Debug {
			return fmt.Errorf("refusing to boot: APP_DEBUG=true with APP_ENV=%s (hardened)", config.App.Env)
		}
		if config.App.Swagger {
			return fmt.Errorf("refusing to boot: ENABLE_SWAGGER=true with APP_ENV=%s (hardened; disable or gate behind an internal boundary)", config.App.Env)
		}
		if config.Database.LogMode == "info" {
			return fmt.Errorf("refusing to boot: DB_LOG_MODE=info with APP_ENV=%s (hardened; use warn or error)", config.App.Env)
		}
		if config.Mail.Mailer == "log" {
			return fmt.Errorf("refusing to boot: MAIL_MAILER=log with APP_ENV=%s (hardened; emails would be discarded)", config.App.Env)
		}
		if len(config.App.HTTP.CORSOrigins()) == 0 {
			return fmt.Errorf("refusing to boot: CORS_ALLOWED_ORIGINS empty with APP_ENV=%s (hardened)", config.App.Env)
		}
		u, err := url.Parse(strings.TrimSpace(config.App.URL))
		if err != nil || !strings.EqualFold(u.Scheme, "https") {
			return fmt.Errorf("refusing to boot: APP_URL must be https with APP_ENV=%s (hardened)", config.App.Env)
		}
	}

	databaseConnection, err := database.ConnectPool(config.Database.Driver, config.Database.DSN(), dbPool(config), appLog.GormLogger(GormLogLevel(config.Database.LogMode), 0))
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	sqlDatabase, err := databaseConnection.DB()
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer sqlDatabase.Close()
	var redisClient *redis.Client
	if config.Session.Driver == "redis" {
		redisClient = redisPkg.DialOrNil(config.Database.Redis, 2*time.Second)
		if redisClient == nil {
			// Fail closed: without the session backend, logout and
			// revocation silently stop working.
			return fmt.Errorf("session backend unavailable: redis unreachable at %s", config.Database.Redis.Addr())
		}
		defer redisClient.Close()
	}
	store, err := session.Open(config.Session.Driver, databaseConnection, redisClient, "")
	if err != nil {
		return fmt.Errorf("session store: %w", err)
	}

	// Elastic tenancy: the control database owns tenant records; tenant
	// data resolves per request to shared pools or dedicated databases.
	// Empty CONTROL_DSN/SHARED_POOL_DSNS reuse the primary database, so
	// single-database development needs no extra configuration.
	mgr, err := tenancy.NewDBManager(tenancy.ManagerParams{
		Driver:      config.Database.Driver,
		ControlDSN:  config.Tenancy.ControlDSN,
		Pools:       tenancy.ParsePoolDSNs(config.Tenancy.PoolDSNs),
		DefaultPool: config.Tenancy.DefaultPool,
		DSNTemplate: config.Tenancy.DSNTemplate,
		AppUser:     config.Tenancy.AppUser,
		AppPass:     config.Tenancy.AppPass,
		Log:         appLog.GormLogger(GormLogLevel(config.Database.LogMode), 0),
	}, config.Database.DSN())
	if err != nil {
		return fmt.Errorf("tenancy: %w", err)
	}
	defer func() { _ = mgr.Close() }()

	controlKey, fallback, err := cadmin.ResolveSecret(config.Tenancy.ControlJWTSecret, config.Auth.JWT.Secret)
	if err != nil {
		return fmt.Errorf("control auth: %w", err)
	}
	if fallback {
		appLog.Warn("CONTROL_JWT_SECRET unset: control tokens share APP_KEY (set it explicitly in production)")
	}

	if !config.App.Debug {
		gin.SetMode(gin.ReleaseMode)
	}
	web.SetDebug(config.App.Debug)
	router := gin.New()
	router.Use(gin.LoggerWithWriter(appLog.GinWriter()), web.Recovery(appLog.Errorf))
	router.Use(web.Maintenance(config.App.Maintenance.Path))

	router.Use(httpmw.CORS(httpmw.CORSConfig{AllowedOrigins: config.App.HTTP.CORSOrigins(), AllowCredentials: true}))
	router.Use(httpmw.Security())
	if config.App.HTTP.EnableGzip {
		router.Use(gzip.Gzip(gzip.DefaultCompression))
	}
	if config.App.HTTP.RateLimitRPS > 0 {
		router.Use(httpmw.RateLimit(config.App.HTTP.RateLimitRPS))
	}
	if config.App.HTTP.MaxBodyBytes > 0 {
		router.Use(httpmw.MaxBodyBytes(config.App.HTTP.MaxBodyBytes))
	}

	router.Use(web.ProvideDB(databaseConnection))

	key, err := config.Auth.JWT.KeyBytes()
	if err != nil {
		return err
	}
	sender, err := pkgmail.OpenSender(config.Mail, config.Filesystem.S3)
	if err != nil {
		return fmt.Errorf("mail: %w", err)
	}
	notifier := notify.NewNotifier(databaseConnection, sender)
	queueRes, err := openNotifyQueue(config, databaseConnection, redisClient, notify.Deps{DB: databaseConnection, Sender: sender})
	if err != nil {
		return fmt.Errorf("notify queue: %w", err)
	}
	defer func() { _ = queueRes.Close() }()
	notifQueue := queueRes.Queue
	// Custom globals self-register via init()
	// (web.RegisterGlobalMiddleware) and run after all built-ins above,
	// so ProvideDB is already in place. Adding one = adding one file in
	// internal/middleware, no edit here.
	deps := &web.ModuleDeps{
		DB: databaseConnection, Key: key, Store: store, Sender: sender,
		Notifier: notifier, Queue: notifQueue,
		Debug: config.App.Debug, AppName: config.App.Name, AppURL: config.App.URL,
		AccessTTLMin: config.Auth.JWT.AccessTTLMin,
	}
	for _, m := range web.RegisteredGlobalMiddlewares() {
		m.Fn(router, deps)
	}
	// Modules self-register via init(); see internal/modules/register.go.
	// Tenancy resolution wraps the same module set on one /api/v1 group
	// (router.go), plus the control-plane admin surface.
	RegisterAPIRoutesWithTenancy(router, deps, &TenancyRouteDeps{
		Manager:     mgr,
		ControlKey:  controlKey,
		Store:       store,
		Sender:      sender,
		Queue:       notifQueue,
		Driver:      config.Database.Driver,
		DefaultPool: config.Tenancy.DefaultPool,
		ChunkSize:   config.Tenancy.ChunkSize,
		RetainDays:  config.Tenancy.RetainDays,
		AppUser:     config.Tenancy.AppUser,
		AppPass:     config.Tenancy.AppPass,
		AppName:     config.App.Name,
		AppURL:      config.App.URL,
		Logf:        appLog.Errorf,
	})
	RegisterInfraRoutes(router, databaseConnection, sqlDatabase, redisClient, config.Filesystem.PublicURL, config.Filesystem.PublicRoot, config.App.Swagger, config.App.Metrics)

	srv := &http.Server{
		Addr:         ":" + config.App.Port,
		Handler:      router,
		ReadTimeout:  time.Duration(config.App.HTTP.ReadTimeoutSec) * time.Second,
		WriteTimeout: time.Duration(config.App.HTTP.WriteTimeoutSec) * time.Second,
		IdleTimeout:  time.Duration(config.App.HTTP.IdleTimeoutSec) * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		appLog.Info("ginplate boot",
			"env", config.App.Env, "debug", config.App.Debug,
			"database", config.Database.Driver, "session", config.Session.Driver,
			"queue", config.Queue.Connection, "tries", config.Queue.Tries,
			"cache", config.Cache.Store, "mailer", config.Mail.Mailer,
			"swagger", config.App.Swagger)
		var err error
		if config.App.HTTP.TLSCertFile != "" && config.App.HTTP.TLSKeyFile != "" {
			appLog.Info("ginplate listening with TLS", "port", config.App.Port)
			err = srv.ListenAndServeTLS(config.App.HTTP.TLSCertFile, config.App.HTTP.TLSKeyFile)
		} else {
			appLog.Info("ginplate listening", "port", config.App.Port)
			if strings.EqualFold(config.App.Env, "production") || strings.EqualFold(config.App.Env, "prod") {
				appLog.Info("TLS terminates at the reverse proxy; APP_URL must be https")
			}
			err = srv.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			errCh <- err
		} else {
			errCh <- nil
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case <-quit:
		appLog.Info("shutting down gracefully...")
	}
	// Coordinated shutdown: stop accepting HTTP before closing dependencies.
	timeout := config.App.HTTP.ShutdownTimeoutSec
	if timeout <= 0 {
		timeout = 5
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("forced shutdown: %w", err)
	}
	appLog.Info("server stopped")
	return nil
}
