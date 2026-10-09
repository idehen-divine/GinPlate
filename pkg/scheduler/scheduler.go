// Package scheduler fires registered entries on cron specs by pushing jobs
// onto the queue broker. Entries overlap by default; WithoutOverlapping
// holds a lock for the whole run, OnOneServer only for the fire decision.
// Fleet-wide locks need RedisLocker; MemoryLocker guards one process.
package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/idehen-divine/GinPlate/pkg/queue"
)

// DefaultExpireAfter caps an overlap lock so a crash delays, never wedges.
const DefaultExpireAfter = 24 * time.Hour

// serverLockTTL covers one fire decision plus Push.
const serverLockTTL = 2 * time.Minute

// tickInterval spaces fire checks.
const tickInterval = time.Minute

// Entry is one recurring push. Name defaults to Job; set it when two
// entries push the same job (lock keys derive from Name).
type Entry struct {
	Name        string
	Job         string
	Payload     []byte
	Spec        string
	Overlap     bool
	OneServer   bool
	ExpireAfter time.Duration
}

func New(job string) *Entry {
	return &Entry{Name: job, Job: job}
}

func (e *Entry) Named(name string) *Entry { e.Name = name; return e }

func (e *Entry) WithPayload(p []byte) *Entry { e.Payload = p; return e }

func (e *Entry) EveryMinute() *Entry { e.Spec = "* * * * *"; return e }

func (e *Entry) Hourly() *Entry { e.Spec = "0 * * * *"; return e }

func (e *Entry) DailyAt(h, m int) *Entry { e.Spec = fmt.Sprintf("%d %d * * *", m, h); return e }

func (e *Entry) Weekly(day time.Weekday, h, m int) *Entry {
	e.Spec = fmt.Sprintf("%d %d * * %d", m, h, int(day))
	return e
}

func (e *Entry) Cron(expr string) *Entry { e.Spec = expr; return e }

// WithoutOverlapping skips ticks while a previous run is unsettled.
func (e *Entry) WithoutOverlapping() *Entry { e.Overlap = true; return e }

// OnOneServer fires once per tick across replicas (locks only the decision).
func (e *Entry) OnOneServer() *Entry { e.OneServer = true; return e }

func (e *Entry) WithExpireAfter(d time.Duration) *Entry { e.ExpireAfter = d; return e }

var (
	regMu   sync.RWMutex
	entries []Entry
)

// Registry is an explicit per-application schedule (isolates tests and
// multiple app instances sharing one process).
type Registry struct {
	mu      sync.RWMutex
	entries []Entry
}

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) Add(es ...*Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range es {
		if e != nil {
			r.entries = append(r.entries, *e)
		}
	}
}

func (r *Registry) All() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Entry(nil), r.entries...)
}

// Schedule registers entries (usually from init()); validated at Prepare.
func Schedule(es ...*Entry) {
	regMu.Lock()
	defer regMu.Unlock()
	for _, e := range es {
		if e != nil {
			entries = append(entries, *e)
		}
	}
}

func Registered() []Entry {
	regMu.RLock()
	defer regMu.RUnlock()
	return append([]Entry(nil), entries...)
}

type prepared struct {
	entry Entry
	sched cron.Schedule
	last  time.Time
}

type State struct {
	items []prepared
}

// Prepare validates entries and stamps last-fire at now (no backfiring).
func Prepare(es []Entry, reg *queue.Registry, now time.Time) (*State, error) {
	st := &State{}
	for _, e := range es {
		if reg != nil {
			if _, ok := reg.Lookup(e.Job); !ok {
				return nil, fmt.Errorf("scheduler: entry %q pushes unknown job %q", e.Name, e.Job)
			}
		}
		sched, err := cron.ParseStandard(e.Spec)
		if err != nil {
			return nil, fmt.Errorf("scheduler: entry %q has bad spec %q: %w", e.Name, e.Spec, err)
		}
		st.items = append(st.items, prepared{entry: e, sched: sched, last: now})
	}
	return st, nil
}

func due(p prepared, now time.Time) bool {
	return !p.sched.Next(p.last).After(now)
}

func overlapKey(name string) string { return "sched:overlap:" + name }
func serverKey(name string) string  { return "sched:server:" + name }

// CheckAndFire pushes every due entry and advances its last-fire clock.
func (s *State) CheckAndFire(ctx context.Context, q queue.Queue, locker Locker, now time.Time, logf func(format string, args ...interface{})) error {
	for i := range s.items {
		p := &s.items[i]
		if !due(*p, now) {
			continue
		}
		p.last = now
		e := p.entry
		if e.OneServer {
			token, held, err := locker.Acquire(ctx, serverKey(e.Name), serverLockTTL)
			if err != nil {
				return fmt.Errorf("scheduler: server lock %q: %w", e.Name, err)
			}
			if !held {
				logf("scheduler: entry %q skipped (another server fired)", e.Name)
				continue
			}
			id, err := q.Push(ctx, e.Job, e.Payload)
			_ = locker.Release(ctx, serverKey(e.Name), token)
			if err != nil {
				return fmt.Errorf("scheduler: push %q: %w", e.Name, err)
			}
			logf("scheduler: entry %q pushed job %s", e.Name, id)
			continue
		}
		payload := e.Payload
		if e.Overlap {
			ttl := e.ExpireAfter
			if ttl <= 0 {
				ttl = DefaultExpireAfter
			}
			token, held, err := locker.Acquire(ctx, overlapKey(e.Name), ttl)
			if err != nil {
				return fmt.Errorf("scheduler: overlap lock %q: %w", e.Name, err)
			}
			if !held {
				logf("scheduler: entry %q skipped (previous run still going)", e.Name)
				continue
			}
			payload = wrapPayload(e.Payload, overlapKey(e.Name), token)
			id, err := q.Push(ctx, e.Job, payload)
			if err != nil {
				// Release the just-acquired lock so a push failure can't wedge the entry.
				releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = locker.Release(releaseCtx, overlapKey(e.Name), token)
				cancel()
				return fmt.Errorf("scheduler: push %q: %w", e.Name, err)
			}
			logf("scheduler: entry %q pushed job %s", e.Name, id)
			continue
		}
		id, err := q.Push(ctx, e.Job, payload)
		if err != nil {
			return fmt.Errorf("scheduler: push %q: %w", e.Name, err)
		}
		logf("scheduler: entry %q pushed job %s", e.Name, id)
	}
	return nil
}

// Run prepares the registered entries and ticks until ctx ends. Failed ticks
// log and continue; only Prepare failures (bad specs, unknown jobs) are fatal.
func Run(ctx context.Context, q queue.Queue, locker Locker, logf func(format string, args ...interface{})) error {
	st, err := Prepare(Registered(), queue.Default(), time.Now())
	if err != nil {
		return err
	}
	fire := func(now time.Time) {
		if err := st.CheckAndFire(ctx, q, locker, now, logf); err != nil {
			logf("scheduler: tick failed (retrying next tick): %v", err)
		}
	}
	fire(time.Now())
	t := time.NewTicker(tickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-t.C:
			fire(now)
		}
	}
}

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Sched *schedMeta      `json:"sched,omitempty"`
}

type schedMeta struct {
	Lock  string `json:"lock"`
	Token string `json:"token"`
}

func wrapPayload(data []byte, lock, token string) []byte {
	raw, err := json.Marshal(envelope{Data: data, Sched: &schedMeta{Lock: lock, Token: token}})
	if err != nil {
		return data
	}
	return raw
}

// Data unwraps a scheduled payload (plain payloads pass through).
func Data(payload []byte) []byte {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil || env.Sched == nil {
		return payload
	}
	return []byte(env.Data)
}

// ReleaseHook returns a queue.OnSettled hook releasing overlap locks. Wire it
// into the worker: the scheduler acquires, the worker releases on settle.
func ReleaseHook(locker Locker, logf func(format string, args ...interface{})) queue.OnSettled {
	return func(job queue.Job) {
		var env envelope
		if err := json.Unmarshal(job.Payload, &env); err != nil || env.Sched == nil {
			return
		}
		if err := locker.Release(context.Background(), env.Sched.Lock, env.Sched.Token); err != nil && logf != nil {
			logf("scheduler: release %q: %v", env.Sched.Lock, err)
		}
	}
}
