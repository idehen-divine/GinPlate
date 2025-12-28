package mailcmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/idehen-divine/GinPlate/internal/mail/welcome"
	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/queue"
)

// NewMailTestCmd builds `ginplate mail:test --to a@b.c`: sends the Welcome
// mailable through the configured MAIL_MAILER (or via the queued job path
// with --queue) to verify delivery without touching code.
func NewMailTestCmd(cfg *config.Config) *cobra.Command {
	var to, subject string
	var useQueue bool
	cmd := &cobra.Command{
		Use:   "mail:test",
		Short: "Send a test email through the configured mailer",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cfg == nil {
				return fmt.Errorf("config not loaded: check .env (APP_KEY required)")
			}
			mailable := welcome.Welcome{AppName: cfg.App.Name, Name: "Tester", Email: to, AppURL: cfg.App.URL}
			msg, err := mailable.Build()
			if err != nil {
				return err
			}
			if subject != "" {
				msg.Subject = subject
			}
			sender, err := mail.OpenSender(cfg.Mail, cfg.Filesystem.S3)
			if err != nil {
				return err
			}
			if !useQueue {
				return sender.Send(cmd.Context(), msg)
			}
			// Queued path: an in-memory broker bound to the mail handler,
			// exercising job encode -> dispatch -> Send without a broker.
			reg := queue.NewRegistry()
			mail.Register(reg, sender)
			m, err := mail.Open(cfg.Mail, cfg.Filesystem.S3, queue.NewSync(reg))
			if err != nil {
				return err
			}
			_, err = m.Queue(cmd.Context(), msg)
			return err
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "Recipient address (required)")
	_ = cmd.MarkFlagRequired("to")
	cmd.Flags().StringVar(&subject, "subject", "", "Subject line (overrides the mailable's)")
	cmd.Flags().BoolVar(&useQueue, "queue", false, "Deliver via the queued mail.send job instead of inline")
	return cmd
}
