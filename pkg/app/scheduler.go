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
	configPkg "github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/database"
	"github.com/idehen-divine/GinPlate/pkg/logger"
	"github.com/idehen-divine/GinPlate/pkg/queue"
	redisPkg "github.com/idehen-divine/GinPlate/pkg/redis"
	"github.com/idehen-divine/GinPlate/pkg/scheduler"
)

// lockRedis dials Redis briefly for scheduler locks. A nil return is not
// an error by itself: callers decide whether in-process locking is
// acceptable (single-instance dev) or must fail closed (hardened fleets).
func lockRedis(config *configPkg.Config) *redis.Client {
	return redisPkg.DialOrNil(config.Database.Redis, 2*time.Second)
}

// RunScheduler pushes due schedule entries onto the queue broker until
// SIGINT/SIGTERM. Only the selected queue driver is connected, like
// RunWorker; locks prefer Redis when reachable, else stay in-process.
func RunScheduler(config *configPkg.Config) error {
	if config == nil {
		return fmt.Errorf("nil config")
	}
	appLog := logger.New(config.App.Env, config.Logging.Level, config.Logging.Output)
	defer appLog.Sync()

	var databaseConnection *gorm.DB
	if config.Queue.Connection == "database" {
		var err error
		databaseConnection, err = database.ConnectPool(config.Database.Driver, config.Database.DSN(), dbPool(config), appLog.GormLogger(GormLogLevel(config.Database.LogMode), 0))
		if err != nil {
			return fmt.Errorf("database: %w", err)
		}
		if sqlDatabase, err := databaseConnection.DB(); err == nil {
			defer sqlDatabase.Close()
		}
	}
	var redisClient *redis.Client
	if config.Queue.Connection == "redis" {
		redisClient = redisPkg.DialOrNil(config.Database.Redis, 10*time.Second)
		if redisClient == nil {
			return fmt.Errorf("redis unreachable at %s", config.Database.Redis.Addr())
		}
		defer func() { _ = redisClient.Close() }()
	}
	q, err := queue.Open(config.Queue, databaseConnection, redisClient)
	if err != nil {
		return err
	}
	// Locks prefer Redis (fleet-wide); the broker client is reused when it
	// exists, otherwise a short dial is attempted. In-process locking is a
	// single-instance development fallback: hardened fleets refuse to boot
	// on it, since every replica would fire each schedule.
	var locker scheduler.Locker = scheduler.NewMemoryLocker()
	if redisClient != nil {
		locker = scheduler.NewRedisLocker(redisClient)
	} else if lockRedisClient := lockRedis(config); lockRedisClient != nil {
		defer func() { _ = lockRedisClient.Close() }()
		locker = scheduler.NewRedisLocker(lockRedisClient)
	} else if configPkg.IsHardenedEnv(config.App.Env) {
		return fmt.Errorf("scheduler locks need Redis with APP_ENV=%s (hardened): in-process locks duplicate every schedule across replicas", config.App.Env)
	} else {
		appLog.Info("scheduler locks are in-process only: reach Redis for multi-replica exclusion")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	appLog.Info("scheduler listening", "connection", config.Queue.Connection)
	return scheduler.Run(ctx, q, locker, appLog.Errorf)
}
