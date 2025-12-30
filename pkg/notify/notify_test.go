package notify

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/queue"
	"gorm.io/gorm"
)

// stubNotification is a scriptable Notification for tests.
type stubNotification struct {
	typ     string
	via     []string
	mail    pkgmail.Message
	mailErr error
	data    map[string]any
	dataErr error
}

func (s stubNotification) Type() string  { return s.typ }
func (s stubNotification) Via() []string { return s.via }
func (s stubNotification) ToMail() (pkgmail.Message, error) {
	return s.mail, s.mailErr
}
func (s stubNotification) ToDatabase() (map[string]any, error) {
	return s.data, s.dataErr
}

// recordChannel captures deliveries for fan-out assertions.
type recordChannel struct {
	name string
	mu   sync.Mutex
	tos  []Notifiable
}

func (r *recordChannel) Name() string { return r.name }
func (r *recordChannel) Send(_ context.Context, _ Deps, to Notifiable, _ Notification) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tos = append(r.tos, to)
	return nil
}

func (r *recordChannel) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.tos)
}

// recordSender captures mail for channel tests.
type recordSender struct {
	mu   sync.Mutex
	msgs []pkgmail.Message
}

func (r *recordSender) Send(_ context.Context, msg pkgmail.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, msg)
	return nil
}

// fakeStore is an in-memory Store for hermetic tests.
type fakeStore struct {
	mu   sync.Mutex
	rows []Record
}

func (f *fakeStore) Create(_ context.Context, to Notifiable, notifType string, _ map[string]any) (*Record, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec := &Record{NotifiableType: to.Type, NotifiableID: to.ID, Type: notifType, CreatedAt: time.Now()}
	_ = rec.BeforeCreate(nil)
	f.rows = append(f.rows, *rec)
	return rec, nil
}

func (f *fakeStore) List(_ context.Context, to Notifiable, limit, offset int) ([]Record, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Record
	for _, r := range f.rows {
		if r.NotifiableType == to.Type && r.NotifiableID == to.ID {
			out = append(out, r)
		}
	}
	total := int64(len(out))
	if offset > len(out) {
		return nil, total, nil
	}
	out = out[offset:]
	if limit > 0 && limit < len(out) {
		out = out[:limit]
	}
	return out, total, nil
}

func (f *fakeStore) MarkRead(_ context.Context, to Notifiable, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.rows {
		if f.rows[i].ID == id && f.rows[i].NotifiableType == to.Type && f.rows[i].NotifiableID == to.ID {
			now := time.Now()
			f.rows[i].ReadAt = &now
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

func (f *fakeStore) MarkAllRead(_ context.Context, to Notifiable) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.rows {
		if f.rows[i].NotifiableType == to.Type && f.rows[i].NotifiableID == to.ID && f.rows[i].ReadAt == nil {
			now := time.Now()
			f.rows[i].ReadAt = &now
		}
	}
	return nil
}

func (f *fakeStore) UnreadCount(_ context.Context, to Notifiable) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, r := range f.rows {
		if r.NotifiableType == to.Type && r.NotifiableID == to.ID && r.ReadAt == nil {
			n++
		}
	}
	return n, nil
}

func TestNotifier(t *testing.T) {
	to := UserNotifiable("u1", "ada@example.com")

	t.Run("send-fans-out-to-via", func(t *testing.T) {
		nt := &Notifier{deps: Deps{}, channels: map[string]Channel{}}
		ra, rb := &recordChannel{name: "a"}, &recordChannel{name: "b"}
		nt.RegisterChannel(ra)
		nt.RegisterChannel(rb)
		n := stubNotification{typ: "x", via: []string{"a", "b"}}
		if err := nt.Send(context.Background(), to, n); err != nil {
			t.Fatal(err)
		}
		if ra.count() != 1 || rb.count() != 1 {
			t.Fatalf("channels got %d/%d deliveries, want 1/1", ra.count(), rb.count())
		}
	})

	t.Run("send-rejects-unknown-channel", func(t *testing.T) {
		nt := NewNotifier(nil, nil)
		bad := stubNotification{typ: "x", via: []string{"pager"}}
		if err := nt.Send(context.Background(), to, bad); err == nil {
			t.Fatal("expected error for unknown channel")
		}
	})

	t.Run("register-replaces-channel", func(t *testing.T) {
		nt := NewNotifier(nil, nil)
		custom := &recordChannel{name: ChannelMail}
		nt.RegisterChannel(custom)
		m := stubNotification{typ: "x", via: []string{"mail"}}
		if err := nt.Send(context.Background(), to, m); err != nil {
			t.Fatal(err)
		}
		if custom.count() != 1 {
			t.Fatal("replacement channel did not run")
		}
	})

	t.Run("database-channel-stores-row", func(t *testing.T) {
		store := &fakeStore{}
		nt := NewNotifier(nil, nil)
		nt.deps.Store = store
		n := stubNotification{typ: "welcome", via: []string{"database"}, data: map[string]any{"title": "Hi"}}
		if err := nt.Send(context.Background(), to, n); err != nil {
			t.Fatal(err)
		}
		rows, total, err := store.List(context.Background(), to, 25, 0)
		if err != nil || total != 1 || rows[0].Type != "welcome" {
			t.Fatalf("row missing: total=%d err=%v", total, err)
		}
		// Another notifiable's rows never leak in.
		other := UserNotifiable("u2", "b@example.com")
		if _, total, _ := store.List(context.Background(), other, 25, 0); total != 0 {
			t.Fatalf("cross-notifiable leak: total=%d", total)
		}
	})

	t.Run("concurrent-register-and-send", func(t *testing.T) {
		nt := NewNotifier(nil, nil)
		store := &fakeStore{}
		nt.deps.Store = store
		done := make(chan struct{})
		go func() {
			defer close(done)
			for i := 0; i < 50; i++ {
				nt.RegisterChannel(&recordChannel{name: "hot"})
			}
		}()
		n := stubNotification{typ: "x", via: []string{"hot", "database"}, data: map[string]any{}}
		for i := 0; i < 50; i++ {
			_ = nt.Send(context.Background(), to, n)
		}
		<-done
	})

	t.Run("queue-rejects-empty-mail-route", func(t *testing.T) {
		nt := NewNotifier(nil, nil)
		reg := queue.NewRegistry()
		q := queue.NewSync(reg)
		routeless := stubNotification{
			typ:  "welcome",
			via:  []string{"mail"},
			mail: pkgmail.Message{Subject: "Hi", Text: "hello"},
		}
		to := Notifiable{Type: "user", ID: "u1"}
		if _, err := nt.Queue(context.Background(), q, to, routeless); err == nil {
			t.Fatal("mail without Email route at enqueue: expected error")
		}
	})
}

func TestMailChannel(t *testing.T) {
	t.Run("addresses-from-notifiable", func(t *testing.T) {
		s := &recordSender{}
		n := stubNotification{
			typ:  "welcome",
			via:  []string{"mail"},
			mail: pkgmail.Message{Subject: "Hi", Text: "hello"},
		}
		to := UserNotifiable("u1", "ada@example.com")
		if err := (MailChannel{}).Send(context.Background(), Deps{Sender: s}, to, n); err != nil {
			t.Fatal(err)
		}
		if len(s.msgs) != 1 || len(s.msgs[0].To) != 1 || s.msgs[0].To[0] != "ada@example.com" {
			t.Fatalf("mail not addressed: %+v", s.msgs)
		}
	})

	t.Run("keeps-mailable-recipients", func(t *testing.T) {
		s := &recordSender{}
		n := stubNotification{
			typ:  "x",
			via:  []string{"mail"},
			mail: pkgmail.Message{To: []string{"keep@example.com"}, Subject: "Hi", Text: "x"},
		}
		if err := (MailChannel{}).Send(context.Background(), Deps{Sender: s}, UserNotifiable("u", "a@b.c"), n); err != nil {
			t.Fatal(err)
		}
		if s.msgs[0].To[0] != "keep@example.com" {
			t.Fatalf("mailable recipients overwritten: %+v", s.msgs)
		}
	})

	t.Run("rejects-empty-route", func(t *testing.T) {
		s := &recordSender{}
		n := stubNotification{typ: "x", via: []string{"mail"}, mail: pkgmail.Message{Subject: "Hi", Text: "x"}}
		if err := (MailChannel{}).Send(context.Background(), Deps{Sender: s}, Notifiable{Type: "user", ID: "u1"}, n); err == nil {
			t.Fatal("empty email route: expected error")
		}
	})

	t.Run("needs-sender", func(t *testing.T) {
		n := stubNotification{typ: "x", via: []string{"mail"}}
		if err := (MailChannel{}).Send(context.Background(), Deps{}, UserNotifiable("u", "a@b.c"), n); err == nil {
			t.Fatal("nil sender: expected error")
		}
	})
}

func TestQueuedRoundTrip(t *testing.T) {
	store, sender := &fakeStore{}, &recordSender{}
	nt := NewNotifier(nil, sender)
	nt.deps.Store = store
	reg := queue.NewRegistry()
	Register(reg, nt.deps)
	q := queue.NewSync(reg)

	to := UserNotifiable("u1", "ada@example.com")
	n := stubNotification{
		typ:  "welcome",
		via:  []string{"database", "mail"},
		data: map[string]any{"title": "Hi"},
		mail: pkgmail.Message{Subject: "Hi", Text: "hello"},
	}
	if _, err := nt.Queue(context.Background(), q, to, n); err != nil {
		t.Fatal(err)
	}
	// Sync broker runs the job inline: row stored + mail sent.
	rows, total, err := store.List(context.Background(), to, 25, 0)
	if err != nil || total != 1 || rows[0].Type != "welcome" {
		t.Fatalf("row missing after queued job: total=%d err=%v", total, err)
	}
	if len(sender.msgs) != 1 || sender.msgs[0].To[0] != "ada@example.com" {
		t.Fatalf("mail missing after queued job: %+v", sender.msgs)
	}
}

func TestQueueRejectsUnknownChannel(t *testing.T) {
	nt := NewNotifier(nil, nil)
	reg := queue.NewRegistry()
	q := queue.NewSync(reg)
	bad := stubNotification{typ: "x", via: []string{"pager"}}
	if _, err := nt.Queue(context.Background(), q, UserNotifiable("u", "a@b.c"), bad); err == nil {
		t.Fatal("unknown channel at enqueue: expected error")
	}
}
