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

// lockRedis dials Redis briefly for scheduler locks. A nil return is not
// an error: the scheduler falls back to the in-process locker (correct for
// one replica; run the redis queue connection for fleet-wide exclusion).
func lockRedis(cfg *config.Config) *redis.Client {
	return redisPkg.DialOrNil(cfg.Database.Redis, 2*time.Second)
}

// RunScheduler pushes due schedule entries onto the queue broker until
// SIGINT/SIGTERM. Only the selected queue driver is connected, like
// RunWorker; locks prefer Redis when reachable, else stay in-process.
func RunScheduler(cfg *config.Config) error {
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
	// Locks prefer Redis (fleet-wide); the broker client is reused when it
	// exists, otherwise a short dial is attempted before falling back to
	// the in-process locker (correct for a single replica).
	var locker scheduler.Locker = scheduler.NewMemoryLocker()
	if rdb != nil {
		locker = scheduler.NewRedisLocker(rdb)
	} else if lrdb := lockRedis(cfg); lrdb != nil {
		defer func() { _ = lrdb.Close() }()
		locker = scheduler.NewRedisLocker(lrdb)
	} else {
		appLog.Info("scheduler locks are in-process only: reach Redis for multi-replica exclusion")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	appLog.Info("scheduler listening", "connection", cfg.Queue.Connection)
	return scheduler.Run(ctx, q, locker, appLog.Errorf)
}
