// Package app hosts the runnable entrypoints behind the single ginplate
// binary (cmd/ginplate). Keeping the wiring here lets every command share
// it without drift.
package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/idehen-divine/GinPlate/internal/mail"
	"github.com/idehen-divine/GinPlate/internal/mail/welcome"
	"github.com/idehen-divine/GinPlate/internal/modules/auth"
	"github.com/idehen-divine/GinPlate/internal/modules/notifications"
	"github.com/idehen-divine/GinPlate/internal/modules/users"
	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/database"
	"github.com/idehen-divine/GinPlate/pkg/logger"
	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/notify"
	"github.com/idehen-divine/GinPlate/pkg/queue"
	redisPkg "github.com/idehen-divine/GinPlate/pkg/redis"
	"github.com/idehen-divine/GinPlate/pkg/session"
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

// openNotifyQueue builds the queue behind notification dispatch: sync runs
// the notification.send job inline, database/redis persist it for the
// worker. An unreachable redis degrades to inline with a warning (cache
// philosophy: notifications must not take the API down); unknown backends
// fail fast via queue.Open.
func openNotifyQueue(cfg *config.Config, db *gorm.DB, rdb *redis.Client, deps notify.Deps, warnf func(msg string, args ...any)) (queue.Queue, error) {
	reg := queue.NewRegistry()
	notify.Register(reg, deps)
	// The same registry also serves direct mail queueing (appmail.Queue):
	// without this, sync delivery of mail.send jobs reports unknown job.
	if deps.Sender != nil {
		pkgmail.Register(reg, deps.Sender)
	}
	switch cfg.Queue.Connection {
	case "", "sync":
		return queue.NewSync(reg), nil
	case "database":
		return queue.Open(cfg.Queue, db, nil)
	case "redis":
		if rdb == nil {
			rdb = redisPkg.DialOrNil(cfg.Database.Redis, 2*time.Second)
			if rdb == nil {
				warnf("redis unreachable, notifications dispatch inline",
					"addr", cfg.Database.Redis.Addr())
				return queue.NewSync(reg), nil
			}
		}
		return queue.Open(cfg.Queue, db, rdb)
	default:
		return queue.Open(cfg.Queue, db, rdb)
	}
}

// GormLogLevel maps DB_LOG_MODE to a GORM log level.
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

// RunAPI starts the HTTP server with graceful shutdown. It blocks until
// SIGINT/SIGTERM and returns nil on clean shutdown.
func RunAPI(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	appLog := logger.New(cfg.App.Env, cfg.Logging.Level, cfg.Logging.Output)
	defer appLog.Sync()

	// Production guardrails: misconfiguration must scream at boot, not in
	// an incident. Debug detail + live docs + chatty drivers are dev tools.
	if cfg.App.Env == "production" {
		if cfg.App.Debug {
			return fmt.Errorf("refusing to boot: APP_DEBUG=true with APP_ENV=production")
		}
		if cfg.App.Swagger {
			appLog.Warn("swagger UI exposed in production: disable ENABLE_SWAGGER unless intentional")
		}
		if cfg.Database.LogMode == "info" {
			appLog.Warn("gorm log mode is info in production: consider warn or error")
		}
		if cfg.Mail.Mailer == "log" {
			appLog.Warn("mail driver is log in production: emails are discarded, not sent")
		}
	}

	db, err := database.Connect(cfg.Database.Driver, cfg.Database.DSN(), appLog.GormLogger(GormLogLevel(cfg.Database.LogMode), 0))
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	var rdb *redis.Client
	if cfg.Session.Driver == "redis" {
		rdb = redisPkg.DialOrNil(cfg.Database.Redis, 2*time.Second)
		if rdb == nil {
			appLog.Warn("redis unreachable, running without session tracking",
				"addr", cfg.Database.Redis.Addr())
		}
	}
	store, err := session.Open(cfg.Session.Driver, db, rdb, "")
	if err != nil {
		return fmt.Errorf("session store: %w", err)
	}

	if !cfg.App.Debug {
		gin.SetMode(gin.ReleaseMode)
	}
	web.SetDebug(cfg.App.Debug)
	router := gin.New()
	router.Use(gin.LoggerWithWriter(appLog.GinWriter()), web.Recovery(appLog.Errorf))
	router.Use(web.Maintenance(cfg.App.Maintenance.Path))

	router.Use(httpmw.CORS(httpmw.CORSConfig{AllowedOrigins: cfg.App.HTTP.CORSOrigins(), AllowCredentials: true}))
	router.Use(httpmw.Security())
	if cfg.App.HTTP.EnableGzip {
		router.Use(gzip.Gzip(gzip.DefaultCompression))
	}
	if cfg.App.HTTP.RateLimitRPS > 0 {
		router.Use(httpmw.RateLimit(cfg.App.HTTP.RateLimitRPS))
	}

	router.Use(web.ProvideDB(db))

	v1 := router.Group("/api/v1")
	{
		key, err := cfg.Auth.JWT.KeyBytes()
		if err != nil {
			return err
		}
		sender, err := pkgmail.OpenSender(cfg.Mail, cfg.Filesystem.S3)
		if err != nil {
			return fmt.Errorf("mail: %w", err)
		}
		notifier := notify.NewNotifier(db, sender)
		notifQueue, err := openNotifyQueue(cfg, db, rdb, notify.Deps{DB: db, Sender: sender}, appLog.Warnf)
		if err != nil {
			return fmt.Errorf("notify queue: %w", err)
		}
		authSvc := auth.NewService(key, cfg.Auth.JWT.AccessTTLMin, store).WithMailer(sender)
		authSvc.WithNotifications(notifier, notifQueue, cfg.App.Name, cfg.App.URL)
		auth.RegisterRoutes(v1, auth.NewHandler(authSvc), key, store)
		users.RegisterRoutes(v1, users.NewHandler(users.NewService(nil)), key, store)
		notifications.RegisterRoutes(v1, notifications.NewHandler(notifications.NewService(nil)), key, store)
		// Dev-only mail preview: admin JWT required, 404s outside debug.
		appmail.RegisterPreviewRoutes(v1, sender, key, store, cfg.App.Debug,
			func(to string) pkgmail.Mailable {
				return welcome.Welcome{AppName: cfg.App.Name, Name: "Preview", Email: to, AppURL: cfg.App.URL}
			})
	}

	router.NoRoute(func(c *gin.Context) {
		web.Render(c, web.NotFound("Not found."))
	})
	router.GET("/health", func(c *gin.Context) {
		data := gin.H{}
		// Queue depth + burial count when the tables exist (fresh,
		// unmigrated databases report status only, never 500).
		if db.Migrator().HasTable("jobs") {
			var depth int64
			if err := db.Table("jobs").Where("available_at <= ?", time.Now()).Count(&depth).Error; err == nil {
				data["queue_depth"] = depth
			}
		}
		if db.Migrator().HasTable("failed_jobs") {
			var failed int64
			if err := db.Table("failed_jobs").Count(&failed).Error; err == nil {
				data["failed_jobs"] = failed
			}
		}
		web.Success(c, http.StatusOK, "ok", data)
	})
	// Public disk files, world-readable: what disk.URL() returns for the
	// public disk resolves here. Private local files and s3 objects never
	// touch this route (s3 uses presigned links).
	router.Static(storageRoutePath(cfg.Filesystem.PublicURL), cfg.Filesystem.PublicRoot)
	if cfg.App.Swagger {
		router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}

	srv := &http.Server{
		Addr:         ":" + cfg.App.Port,
		Handler:      router,
		ReadTimeout:  time.Duration(cfg.App.HTTP.ReadTimeoutSec) * time.Second,
		WriteTimeout: time.Duration(cfg.App.HTTP.WriteTimeoutSec) * time.Second,
		IdleTimeout:  time.Duration(cfg.App.HTTP.IdleTimeoutSec) * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("ginplate listening", "port", cfg.App.Port)
		appLog.Info("ginplate boot",
			"env", cfg.App.Env, "debug", cfg.App.Debug,
			"database", cfg.Database.Driver, "session", cfg.Session.Driver,
			"queue", cfg.Queue.Connection, "tries", cfg.Queue.Tries,
			"cache", cfg.Cache.Store, "mailer", cfg.Mail.Mailer,
			"swagger", cfg.App.Swagger)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
		slog.Info("shutting down gracefully...")
	}

	timeout := cfg.App.HTTP.ShutdownTimeoutSec
	if timeout <= 0 {
		timeout = 5
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("forced shutdown: %w", err)
	}
	slog.Info("server stopped")
	return nil
}
