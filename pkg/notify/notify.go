// Package notify delivers user notifications across expandable channels,
// mirroring Laravel's notification system: one Notification per event,
// each declaring its channels via Via(). Two channels ship — database
// (persisted rows behind an inbox API) and mail (via pkg/mail) — and new
// ones (sms, slack, broadcast, ...) plug in through RegisterChannel with
// no changes to callers.
//
// Callers pick delivery per call: Send runs channels inline now, Queue
// pushes a "notification.send" job (pre-resolved, so workers need no
// notification registry) for background delivery.
package notify

import (
	"context"
	"fmt"
	"sync"

	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/queue"
	"gorm.io/gorm"
)

// Notifiable names a recipient polymorphically: Type is the audience kind
// ("user" today; teams, admins, ... later), ID is its key as a string.
// Email carries the mail route and may be empty when mail isn't used;
// the mail channel errors loudly on an empty route instead of guessing.
type Notifiable struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Email string `json:"email,omitempty"`
}

// UserNotifiable addresses a user row by id and email.
func UserNotifiable(id, email string) Notifiable {
	return Notifiable{Type: "user", ID: id, Email: email}
}

// Notification is one notifiable event. Via names the channels ("database",
// "mail", ...); ToMail/ToDatabase supply the per-channel payloads. Return
// a zero Message with nil error from ToMail to skip mail content errors —
// the mail channel only runs when listed in Via.
type Notification interface {
	// Type is the stored discriminator (e.g. "welcome", "order-shipped").
	Type() string
	// Via names the channels for this notification.
	Via() []string
	// ToMail renders the mail payload. Called only when Via includes mail.
	ToMail() (pkgmail.Message, error)
	// ToDatabase renders the stored data payload. Called only when Via
	// includes database.
	ToDatabase() (map[string]any, error)
}

// Deps are the handles channels deliver through. Database needs a Store
// (GormStore in production, fakes in tests); mail needs Sender. Channels
// fail fast on the handle they require.
type Deps struct {
	DB     *gorm.DB
	Store  Store
	Sender pkgmail.Sender
}

// storeFor resolves the Store: the injected seam first, else GORM around
// DB (which must then be non-nil).
func (d Deps) storeFor() (Store, error) {
	if d.Store != nil {
		return d.Store, nil
	}
	if d.DB == nil {
		return nil, fmt.Errorf("notify: database channel needs a *gorm.DB")
	}
	return NewStore(d.DB), nil
}

// Channel delivers through one backend. Name matches Via entries.
type Channel interface {
	Name() string
	Send(ctx context.Context, deps Deps, to Notifiable, n Notification) error
}

// Notifier fans notifications out to channels. Build it once per process
// (the API server and each worker resolve their own from config). The
// channel map is mutex-guarded: hot-plugging a channel while deliveries
// run must never fatal on concurrent map access.
type Notifier struct {
	deps     Deps
	mu       sync.RWMutex
	channels map[string]Channel
}

// NewNotifier wires the bundled channels (database, mail) around deps.
// Extra channels attach later via RegisterChannel.
func NewNotifier(db *gorm.DB, sender pkgmail.Sender) *Notifier {
	n := &Notifier{deps: Deps{DB: db, Sender: sender}, channels: map[string]Channel{}}
	n.RegisterChannel(DatabaseChannel{})
	n.RegisterChannel(MailChannel{})
	return n
}

// RegisterChannel attaches (or replaces) a channel by name. Future backends
// (sms, slack, broadcast) land here with no caller changes. Safe for
// concurrent use with Send/Queue.
func (n *Notifier) RegisterChannel(c Channel) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.channels[c.Name()] = c
}

// channel looks up one channel under a read lock.
func (n *Notifier) channel(name string) (Channel, bool) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	c, ok := n.channels[name]
	return c, ok
}

// Send runs every Via channel inline, now. The first failure aborts and
// reports which channel failed.
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

// Queue pre-resolves the channel payloads and pushes a "notification.send"
// job, so workers deliver without a notification-type registry. Unknown
// channels fail here, at enqueue time, not in the worker.
func (n *Notifier) Queue(ctx context.Context, q queue.Queue, to Notifiable, notif Notification) (string, error) {
	payload, err := n.resolvePayload(to, notif)
	if err != nil {
		return "", err
	}
	return q.Push(ctx, JobName, mustMarshal(payload))
}
