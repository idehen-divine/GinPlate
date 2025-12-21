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

// store opens the failed-jobs table. A missing table is a usage error, not
// a crash: it means the failed_jobs migration never ran.
func store(cfg *config.Config) (queue.FailedStore, *gorm.DB, error) {
	db, err := database.Connect(cfg.Database.Driver, cfg.Database.DSN())
	if err != nil {
		return nil, nil, fmt.Errorf("database: %w", err)
	}
	if !db.Migrator().HasTable("failed_jobs") {
		return nil, nil, fmt.Errorf("no failed_jobs table: run `ginplate migrate up` first")
	}
	return queue.NewDatabaseFailedStore(db), db, nil
}

func closeDB(db *gorm.DB) {
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// NewQueueFailedCmd shows buried jobs: id, job, attempts, when, and the
// exception that killed each (truncated for the table).
func NewQueueFailedCmd(cfg *config.Config) *cobra.Command {
	var limit int
	cmd := &cobra.Command{
		Use:   "queue:failed",
		Short: "List buried jobs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			s, db, err := store(cfg)
			if err != nil {
				return err
			}
			defer closeDB(db)
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

// NewQueueRetryCmd re-pushes a buried job (or all) onto the live broker and
// deletes its row. The job runs fresh: attempts restart at 1.
func NewQueueRetryCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "queue:retry <id|all>",
		Short: "Re-queue a buried job (or all)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, db, err := store(cfg)
			if err != nil {
				return err
			}
			defer closeDB(db)
			ctx := context.Background()
			var jobs []queue.FailedJob
			if args[0] == "all" {
				// List caps a single call, so drain page by page: push and
				// delete each page before reading the next.
				q, err := openBroker(cfg)
				if err != nil {
					return err
				}
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
			q, err := openBroker(cfg)
			if err != nil {
				return err
			}
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

// NewQueueForgetCmd deletes one buried job without retrying it.
func NewQueueForgetCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "queue:forget <id>",
		Short: "Delete a buried job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			s, db, err := store(cfg)
			if err != nil {
				return err
			}
			defer closeDB(db)
			if err := s.Delete(context.Background(), args[0]); err != nil {
				return err
			}
			cmd.Printf("forgot %s\n", args[0])
			return nil
		},
	}
}

// NewQueueFlushCmd deletes every buried job. Refuses without --force, like
// other destructive commands.
func NewQueueFlushCmd(cfg *config.Config) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "queue:flush",
		Short: "Delete all buried jobs",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !force {
				return fmt.Errorf("refusing without --force (deletes every buried job)")
			}
			s, db, err := store(cfg)
			if err != nil {
				return err
			}
			defer closeDB(db)
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

// openBroker connects the live queue broker for retries, mirroring the
// worker's wiring: only the selected driver is dialed. Sync retries
// dispatch inline through the registered handlers (blank-imported above),
// like any other sync push.
func openBroker(cfg *config.Config) (queue.Queue, error) {
	if cfg.Queue.Connection == "redis" {
		client := redisPkg.DialOrNil(cfg.Database.Redis, 10*time.Second)
		if client == nil {
			return nil, fmt.Errorf("redis unreachable at %s", cfg.Database.Redis.Addr())
		}
		return queue.Open(cfg.Queue, nil, client)
	}
	var db *gorm.DB
	if cfg.Queue.Connection == "database" {
		var err error
		db, err = database.Connect(cfg.Database.Driver, cfg.Database.DSN())
		if err != nil {
			return nil, fmt.Errorf("database: %w", err)
		}
	}
	if cfg.Queue.Connection == "" || cfg.Queue.Connection == "sync" {
		return queue.NewSync(queue.Default()), nil
	}
	return queue.Open(cfg.Queue, db, nil)
}
