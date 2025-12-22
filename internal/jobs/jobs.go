// Package jobs holds application background-job handlers. Register each
// handler in init() via queue.Handle, and each recurring push via
// scheduler.Schedule: the queue:work and schedule:work commands blank-import
// this package, so both attach with no manual wiring. Copy LogHello to add
// your own, or run `ginplate make:job Billing.Charge`.
package jobs

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/database"
	"github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/notify"
	"github.com/idehen-divine/GinPlate/pkg/queue"
	"github.com/idehen-divine/GinPlate/pkg/scheduler"
)

func init() {
	queue.Handle("log.hello", LogHello)
	queue.Handle(mail.JobName, SendMail)
	queue.Handle(notify.JobName, SendNotification)
	scheduler.Schedule(
		scheduler.New("log.hello").
			Named("hello-greeting").
			WithPayload([]byte(`{"to":"world"}`)).
			DailyAt(6, 0).
			WithoutOverlapping(),
	)
}

// SendMail delivers a mail.send job through the configured MAIL_MAILER.
// Deps resolve lazily from config per job so the worker needs no extra
// wiring; failures return errors for retry.
func SendMail(ctx context.Context, job queue.Job) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	sender, err := mail.OpenSender(cfg.Mail, cfg.Filesystem.S3)
	if err != nil {
		return err
	}
	return mail.HandlerFor(sender)(ctx, job)
}

// SendNotification delivers a notification.send job: the database row is
// written first, then mail sends. Deps resolve lazily from config per job
// so the worker needs no extra wiring; failures return errors for retry.
func SendNotification(ctx context.Context, job queue.Job) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	db, err := database.Connect(cfg.Database.Driver, cfg.Database.DSN())
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer sqlDB.Close()
	sender, err := mail.OpenSender(cfg.Mail, cfg.Filesystem.S3)
	if err != nil {
		return err
	}
	return notify.HandlerFor(notify.Deps{DB: db, Sender: sender})(ctx, job)
}

// LogHello is the example handler: it logs its payload and succeeds. Push
// one from code via a Queue, e.g. q.Push(ctx, "log.hello", []byte(`{"to":"world"}`)).
// Scheduled payloads unwrap with scheduler.Data (plain pushes pass through).
func LogHello(ctx context.Context, job queue.Job) error {
	var payload struct {
		To string `json:"to"`
	}
	if err := json.Unmarshal(scheduler.Data(job.Payload), &payload); err != nil {
		return err
	}
	slog.Info("hello job", "id", job.ID, "to", payload.To)
	return nil
}
