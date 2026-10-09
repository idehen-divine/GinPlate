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
func RunWorker(config *config.Config) error {
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
	if config.Queue.Connection == "sync" {
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
	if lockRedisClient := lockRedis(config); lockRedisClient != nil {
		defer func() { _ = lockRedisClient.Close() }()
		hooks = append(hooks, scheduler.ReleaseHook(scheduler.NewRedisLocker(lockRedisClient), appLog.Errorf))
	}
	// Buried jobs persist to failed_jobs when a database is reachable: the
	// broker's own handle when database-backed, else a best-effort dial
	// (log-only burial when unreachable, as before).
	var buried []queue.OnBuried
	failedDatabase := databaseConnection
	if failedDatabase == nil {
		if dialedDatabase, err := database.Connect(config.Database.Driver, config.Database.DSN()); err == nil {
			defer func() {
				if sqlDatabase, err := dialedDatabase.DB(); err == nil {
					_ = sqlDatabase.Close()
				}
			}()
			failedDatabase = dialedDatabase
		}
	}
	if failedDatabase != nil {
		buried = append(buried, queue.RecordHook(queue.NewDatabaseFailedStore(failedDatabase), config.Queue.Connection, appLog.Errorf))
	} else {
		appLog.Info("buried jobs log only: no database for failed_jobs")
	}
	appLog.Info("worker listening", "connection", config.Queue.Connection)
	return queue.RunBuried(ctx, q, queue.Default(), appLog.Errorf, buried, hooks...)
}
