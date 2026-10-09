// Package notify delivers user notifications across channels (database,
// mail; more plug in via RegisterChannel). Send runs inline, Queue pushes
// a "notification.send" job for background delivery.
package notify

import (
	"context"
	"fmt"
	"sync"

	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/queue"
	"gorm.io/gorm"
)

// Notifiable names a recipient: Type is the audience kind, ID its key.
type Notifiable struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Email string `json:"email,omitempty"`
}

func UserNotifiable(id, email string) Notifiable {
	return Notifiable{Type: "user", ID: id, Email: email}
}

// Notification is one notifiable event. Via names the channels;
// ToMail/ToDatabase supply the per-channel payloads.
type Notification interface {
	Type() string
	Via() []string
	ToMail() (pkgmail.Message, error)
	ToDatabase() (map[string]any, error)
}

type Deps struct {
	DB     *gorm.DB
	Store  Store
	Sender pkgmail.Sender
}

func (d Deps) storeFor() (Store, error) {
	if d.Store != nil {
		return d.Store, nil
	}
	if d.DB == nil {
		return nil, fmt.Errorf("notify: database channel needs a *gorm.DB")
	}
	return NewStore(d.DB), nil
}

type Channel interface {
	Name() string
	Send(ctx context.Context, deps Deps, to Notifiable, n Notification) error
}

// Notifier fans notifications out to channels (mutex-guarded for hot-plugging).
type Notifier struct {
	deps     Deps
	mu       sync.RWMutex
	channels map[string]Channel
}

func NewNotifier(db *gorm.DB, sender pkgmail.Sender) *Notifier {
	n := &Notifier{deps: Deps{DB: db, Sender: sender}, channels: map[string]Channel{}}
	n.RegisterChannel(DatabaseChannel{})
	n.RegisterChannel(MailChannel{})
	return n
}

// RegisterChannel attaches (or replaces) a channel by name.
func (n *Notifier) RegisterChannel(c Channel) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.channels[c.Name()] = c
}

func (n *Notifier) channel(name string) (Channel, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	c, ok := n.channels[name]
	return c, ok
}

// Send runs every Via channel inline; the first failure aborts.
func (n *Notifier) Send(ctx context.Context, to Notifiable, notif Notification) error {
	for _, name := range notif.Via() {
		c, ok := n.channel(name)
		if !ok {
			return fmt.Errorf("notify: unknown channel %q", name)
		}
		if err := c.Send(ctx, n.deps, to, notif); err != nil {
			return fmt.Errorf("notify: channel %q: %w", name, err)
		}
	}
	return nil
}

// Queue pre-resolves payloads and pushes a "notification.send" job.
func (n *Notifier) Queue(ctx context.Context, q queue.Queue, to Notifiable, notif Notification) (string, error) {
	payload, err := n.resolvePayload(to, notif)
	if err != nil {
		return "", err
	}
	return q.Push(ctx, JobName, mustMarshal(payload))
}
