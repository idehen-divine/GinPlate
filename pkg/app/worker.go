package app

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	_ "github.com/idehen-divine/GinPlate/internal/jobs"
	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/database"
	"github.com/idehen-divine/GinPlate/pkg/logger"
	"github.com/idehen-divine/GinPlate/pkg/queue"
	redisPkg "github.com/idehen-divine/GinPlate/pkg/redis"
	"github.com/idehen-divine/GinPlate/pkg/scheduler"
)

// RunWorker connects the queue broker and runs jobs until SIGINT/SIGTERM.
// Only the selected driver is connected: database work needs no Redis
// running, and sync work needs neither. Handlers come from internal/jobs
// via blank import so cloners attach code without touching this file.
func RunWorker(cfg *config.Config) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	appLog := logger.New(cfg.App.Env, cfg.Logging.Level, cfg.Logging.Output)
	defer appLog.Sync()

	var db *gorm.DB
	if cfg.Queue.Connection == "database" {
		var err error
		db, err = database.Connect(cfg.Database.Driver, cfg.Database.DSN(), appLog.GormLogger(GormLogLevel(cfg.Database.LogMode), 0))
		if err != nil {
			return fmt.Errorf("database: %w", err)
		}
	}
	var rdb *redis.Client
	if cfg.Queue.Connection == "redis" {
		rdb = redisPkg.DialOrNil(cfg.Database.Redis, 10*time.Second)
		if rdb == nil {
			return fmt.Errorf("redis unreachable at %s", cfg.Database.Redis.Addr())
		}
		defer func() { _ = rdb.Close() }()
	}
	q, err := queue.Open(cfg.Queue, db, rdb)
	if err != nil {
		return err
	}
	if cfg.Queue.Connection == "sync" {
		appLog.Info("sync dispatches inline on push: no worker needed")
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	// Overlap locks release when jobs settle. The scheduler and worker are
	// separate processes, so release needs the shared (Redis) locker: dial
	// briefly, and skip the hook when Redis is unreachable (locks then
	// expire via TTL instead of releasing early).
	var hooks []queue.OnSettled
	if lrdb := lockRedis(cfg); lrdb != nil {
		defer func() { _ = lrdb.Close() }()
		hooks = append(hooks, scheduler.ReleaseHook(scheduler.NewRedisLocker(lrdb), appLog.Errorf))
	}
	// Buried jobs persist to failed_jobs when a database is reachable: the
	// broker's own handle when database-backed, else a best-effort dial
	// (log-only burial when unreachable, as before).
	var buried []queue.OnBuried
	failedDB := db
	if failedDB == nil {
		if fdb, err := database.Connect(cfg.Database.Driver, cfg.Database.DSN()); err == nil {
			defer func() {
				if sqlDB, err := fdb.DB(); err == nil {
					_ = sqlDB.Close()
				}
			}()
			failedDB = fdb
		}
	}
	if failedDB != nil {
		buried = append(buried, queue.RecordHook(queue.NewDatabaseFailedStore(failedDB), cfg.Queue.Connection, appLog.Errorf))
	} else {
		appLog.Info("buried jobs log only: no database for failed_jobs")
	}
	appLog.Info("worker listening", "connection", cfg.Queue.Connection)
	return queue.RunBuried(ctx, q, queue.Default(), appLog.Errorf, buried, hooks...)
}
