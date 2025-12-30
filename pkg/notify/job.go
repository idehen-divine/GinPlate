package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/queue"
)

// JobName is the queue job for background delivery. The payload carries
// pre-resolved channel data (not the notification struct), so workers
// deliver with no notification-type registry.
const JobName = "notification.send"

// jobPayload is the resolved work: who, what kind, the database row data,
// and the rendered mail when Via includes mail.
type jobPayload struct {
	To   Notifiable       `json:"to"`
	Type string           `json:"type"`
	Data map[string]any   `json:"data,omitempty"`
	Mail *pkgmail.Message `json:"mail,omitempty"`
}

// resolvePayload renders ToDatabase/ToMail once, at enqueue time, failing
// fast on unknown channels so workers never bury for typos. Channel
// lookups go through the notifier's read lock.
func (n *Notifier) resolvePayload(to Notifiable, notif Notification) (jobPayload, error) {
	p := jobPayload{To: to, Type: notif.Type()}
	for _, name := range notif.Via() {
		if _, ok := n.channel(name); !ok {
			return jobPayload{}, fmt.Errorf("notify: unknown channel %q", name)
		}
		switch name {
		case ChannelDatabase:
			data, err := notif.ToDatabase()
			if err != nil {
				return jobPayload{}, err
			}
			if data == nil {
				data = map[string]any{}
			}
			p.Data = data
		case ChannelMail:
			if strings.TrimSpace(to.Email) == "" {
				return jobPayload{}, fmt.Errorf("notify: mail channel needs Notifiable.Email")
			}
			msg, err := notif.ToMail()
			if err != nil {
				return jobPayload{}, err
			}
			if len(msg.To) == 0 {
				msg.To = []string{to.Email}
			}
			p.Mail = &msg
		default:
			// Custom channels deliver inline only for now: queueing an
			// unknown payload shape would silently drop content. Extend
			// resolvePayload when the channel learns a serializable form.
			return jobPayload{}, fmt.Errorf("notify: channel %q is send-only (not queueable yet)", name)
		}
	}
	return p, nil
}

// mustMarshal encodes the payload for the queue.
func mustMarshal(p jobPayload) []byte {
	raw, err := json.Marshal(p)
	if err != nil {
		panic(fmt.Sprintf("notify: marshal payload: %v", err))
	}
	return raw
}

// parsePayload decodes a "notification.send" job payload.
func parsePayload(raw []byte) (jobPayload, error) {
	var p jobPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		return jobPayload{}, fmt.Errorf("notify: decode job payload: %w", err)
	}
	return p, nil
}

// HandlerFor returns a queue handler delivering jobs through deps: the
// database row is written first, then mail sends. Use it to attach
// notification delivery to any registry (worker, sync inline, tests).
func HandlerFor(deps Deps) queue.Handler {
	return func(ctx context.Context, job queue.Job) error {
		p, err := parsePayload(job.Payload)
		if err != nil {
			return err
		}
		// resolvePayload leaves Data nil for mail-only jobs: no row then.
		if p.Data != nil {
			store, err := deps.storeFor()
			if err != nil {
				return err
			}
			if _, err := store.Create(ctx, p.To, p.Type, p.Data); err != nil {
				return err
			}
		}
		if p.Mail != nil {
			if deps.Sender == nil {
				return fmt.Errorf("notify: worker needs a sender")
			}
			return deps.Sender.Send(ctx, *p.Mail)
		}
		return nil
	}
}

// Register attaches the "notification.send" handler to reg.
func Register(reg *queue.Registry, deps Deps) {
	reg.Handle(JobName, HandlerFor(deps))
}
