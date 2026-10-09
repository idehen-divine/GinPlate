package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/queue"
)

const JobName = "notification.send"

type jobPayload struct {
	To   Notifiable       `json:"to"`
	Type string           `json:"type"`
	Data map[string]any   `json:"data,omitempty"`
	Mail *pkgmail.Message `json:"mail,omitempty"`
}

// resolvePayload renders payloads once, at enqueue time (unknown channels
// fail here, not in the worker).
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
			// Custom channels are send-only until they learn a serializable form.
			return jobPayload{}, fmt.Errorf("notify: channel %q is send-only (not queueable yet)", name)
		}
	}
	return p, nil
}

func mustMarshal(p jobPayload) []byte {
	raw, err := json.Marshal(p)
	if err != nil {
		panic(fmt.Sprintf("notify: marshal payload: %v", err))
	}
	return raw
}

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
