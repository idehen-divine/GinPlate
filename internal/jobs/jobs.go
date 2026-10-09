// Package jobs holds background-job handlers. Register handlers via
// queue.Handle and recurring pushes via scheduler.Schedule in init()
// (or run `ginplate make:job`).
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

// SendMail delivers a mail.send job (deps resolve lazily per job).
func SendMail(ctx context.Context, job queue.Job) error {
	config, err := config.Load()
	if err != nil {
		return err
	}
	sender, err := mail.OpenSender(config.Mail, config.Filesystem.S3)
	if err != nil {
		return err
	}
	return mail.HandlerFor(sender)(ctx, job)
}

// SendNotification delivers a notification.send job (row first, then mail).
func SendNotification(ctx context.Context, job queue.Job) error {
	config, err := config.Load()
	if err != nil {
		return err
	}
	databaseConnection, err := database.Connect(config.Database.Driver, config.Database.DSN())
	if err != nil {
		return err
	}
	sqlDatabase, err := databaseConnection.DB()
	if err != nil {
		return err
	}
	defer sqlDatabase.Close()
	sender, err := mail.OpenSender(config.Mail, config.Filesystem.S3)
	if err != nil {
		return err
	}
	return notify.HandlerFor(notify.Deps{DB: databaseConnection, Sender: sender})(ctx, job)
}

// LogHello is the example handler.
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
