package queuefailed

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"gorm.io/gorm"

	_ "github.com/idehen-divine/GinPlate/internal/jobs"
	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/database"
	"github.com/idehen-divine/GinPlate/pkg/queue"
	redisPkg "github.com/idehen-divine/GinPlate/pkg/redis"
)

// store opens the failed-jobs table (missing = migration never ran).
func store(config *config.Config) (queue.FailedStore, *gorm.DB, error) {
	databaseConnection, err := database.Connect(config.Database.Driver, config.Database.DSN())
	if err != nil {
		return nil, nil, fmt.Errorf("database: %w", err)
	}
	if !databaseConnection.Migrator().HasTable("failed_jobs") {
		return nil, nil, fmt.Errorf("no failed_jobs table: run `ginplate migrate up` first")
	}
	return queue.NewDatabaseFailedStore(databaseConnection), databaseConnection, nil
}

func closeDB(databaseConnection *gorm.DB) {
	if sqlDatabase, err := databaseConnection.DB(); err == nil {
		_ = sqlDatabase.Close()
	}
}

// NewQueueFailedCmd lists buried jobs.
func NewQueueFailedCmd(config *config.Config) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "queue:failed",
		Short: "List buried jobs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, databaseConnection, err := store(config)
			if err != nil {
				return err
			}
			defer closeDB(databaseConnection)
			jobs, err := s.List(context.Background(), limit)
			if err != nil {
				return err
			}
			if len(jobs) == 0 {
				cmd.Println("no failed jobs")
				return nil
			}
			cmd.Printf("%-36s %-20s %4s %-19s %s\n", "ID", "JOB", "TRY", "FAILED AT", "EXCEPTION")
			for _, j := range jobs {
				exc := strings.ReplaceAll(j.Exception, "\n", " ")
				if len(exc) > 60 {
					exc = exc[:57] + "..."
				}
				cmd.Printf("%-36s %-20s %4d %-19s %s\n",
					j.ID, j.Name, j.Attempts, j.FailedAt.Format("2006-01-02 15:04:05"), exc)
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&limit, "limit", 100, "Max rows to show")
	return cmd
}

// NewQueueRetryCmd re-queues a buried job (or all) with attempts reset.
func NewQueueRetryCmd(config *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "queue:retry <id|all>",
		Short: "Re-queue a buried job (or all)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, databaseConnection, err := store(config)
			if err != nil {
				return err
			}
			defer closeDB(databaseConnection)
			ctx := context.Background()
			var jobs []queue.FailedJob
			if args[0] == "all" {
				// Drain page by page (List caps a single call).
				broker, err := openBroker(config)
				if err != nil {
					return err
				}
				defer func() { _ = broker.Close() }()
				q := broker.Queue
				total := 0
				for {
					page, err := s.List(ctx, 500)
					if err != nil {
						return err
					}
					if len(page) == 0 {
						break
					}
					var ids []string
					for _, j := range page {
						if _, err := q.Push(ctx, j.Name, j.Payload); err != nil {
							return fmt.Errorf("retry %s: %w", j.ID, err)
						}
						ids = append(ids, j.ID)
					}
					if err := s.Delete(ctx, ids...); err != nil {
						return err
					}
					total += len(ids)
				}
				cmd.Printf("retried %d job(s)\n", total)
				return nil
			}
			var job queue.FailedJob
			job, err = s.Get(ctx, args[0])
			if err != nil {
				return fmt.Errorf("failed job %q: %w", args[0], err)
			}
			jobs = []queue.FailedJob{job}
			if len(jobs) == 0 {
				cmd.Println("nothing to retry")
				return nil
			}
			broker, err := openBroker(config)
			if err != nil {
				return err
			}
			defer func() { _ = broker.Close() }()
			q := broker.Queue
			var ids []string
			for _, j := range jobs {
				if _, err := q.Push(ctx, j.Name, j.Payload); err != nil {
					return fmt.Errorf("retry %s: %w", j.ID, err)
				}
				ids = append(ids, j.ID)
			}
			if err := s.Delete(ctx, ids...); err != nil {
				return err
			}
			cmd.Printf("retried %d job(s)\n", len(ids))
			return nil
		},
	}
}

// NewQueueForgetCmd deletes one buried job without retrying.
func NewQueueForgetCmd(config *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "queue:forget <id>",
		Short: "Delete a buried job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, databaseConnection, err := store(config)
			if err != nil {
				return err
			}
			defer closeDB(databaseConnection)
			if err := s.Delete(context.Background(), args[0]); err != nil {
				return err
			}
			cmd.Printf("forgot %s\n", args[0])
			return nil
		},
	}
}

// NewQueueFlushCmd deletes every buried job (requires --force).
func NewQueueFlushCmd(config *config.Config) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "queue:flush",
		Short: "Delete all buried jobs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !force {
				return fmt.Errorf("refusing without --force (deletes every buried job)")
			}
			s, databaseConnection, err := store(config)
			if err != nil {
				return err
			}
			defer closeDB(databaseConnection)
			if err := s.Flush(context.Background()); err != nil {
				return err
			}
			cmd.Println("flushed failed jobs")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Confirm deletion of every buried job")
	return cmd
}

// BrokerResources owns the live queue broker plus any client opened for
// it, so retry commands close exactly what they dialed.
type BrokerResources struct {
	Queue queue.Queue
	Close func() error
}

func noopClose() error { return nil }

// openBroker connects the live queue broker for retries (selected driver only).
func openBroker(config *config.Config) (BrokerResources, error) {
	if config.Queue.Connection == "redis" {
		client := redisPkg.DialOrNil(config.Database.Redis, 10*time.Second)
		if client == nil {
			return BrokerResources{}, fmt.Errorf("redis unreachable at %s", config.Database.Redis.Addr())
		}
		q, err := queue.Open(config.Queue, nil, client)
		if err != nil {
			_ = client.Close()
			return BrokerResources{}, err
		}
		return BrokerResources{Queue: q, Close: client.Close}, nil
	}
	var databaseConnection *gorm.DB
	if config.Queue.Connection == "database" {
		var err error
		databaseConnection, err = database.Connect(config.Database.Driver, config.Database.DSN())
		if err != nil {
			return BrokerResources{}, fmt.Errorf("database: %w", err)
		}
	}
	if config.Queue.Connection == "" || config.Queue.Connection == "sync" {
		return BrokerResources{Queue: queue.NewSync(queue.Default()), Close: noopClose}, nil
	}
	q, err := queue.Open(config.Queue, databaseConnection, nil)
	if err != nil {
		if databaseConnection != nil {
			if sqlDatabase, serr := databaseConnection.DB(); serr == nil {
				_ = sqlDatabase.Close()
			}
		}
		return BrokerResources{}, err
	}
	return BrokerResources{Queue: q, Close: func() error {
		sqlDatabase, err := databaseConnection.DB()
		if err != nil {
			return err
		}
		return sqlDatabase.Close()
	}}, nil
}
