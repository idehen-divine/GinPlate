// Package scheduler fires registered entries on cron specs by pushing
// jobs onto the queue broker, Laravel-style. Handlers stay ordinary queue
// handlers in internal/jobs; entries self-register via Schedule in init(),
// mirroring queue.Handle, so generated job files are self-contained.
//
// Overlap follows Laravel: entries overlap by default; WithoutOverlapping
// holds a lock for the whole run (released by the worker on settle, TTL as
// crash safety), OnOneServer locks only the fire decision so a fleet of
// schedulers fires once. Both need a shared locker (RedisLocker) to mean
// anything across processes; MemoryLocker guards a single process.
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

// DefaultExpireAfter caps an overlap lock like Laravel's 24h default: a
// crashed worker can delay a schedule, never wedge it forever.
const DefaultExpireAfter = 24 * time.Hour

// serverLockTTL covers one fire decision plus Push. It is deliberately
// short: onOneServer must not block later ticks if a scheduler dies
// mid-push.
const serverLockTTL = 2 * time.Minute

// tickInterval spaces fire checks. Minute granularity matches cron specs
// and Laravel's scheduler cadence.
const tickInterval = time.Minute

// Entry is one recurring push: when Spec is due, Job is pushed with
// Payload. Name defaults to Job; set it explicitly when two entries push
// the same job (lock keys derive from Name).
type Entry struct {
	Name        string
	Job         string
	Payload     []byte
	Spec        string
	Overlap     bool
	OneServer   bool
	ExpireAfter time.Duration
}

// New starts an entry pushing job. Chain a Spec builder (EveryMinute,
// Hourly, DailyAt, Weekly, Cron), then overlap flags.
func New(job string) *Entry {
	return &Entry{Name: job, Job: job}
}

// Named overrides the entry name (lock keys + logs).
func (e *Entry) Named(name string) *Entry { e.Name = name; return e }

// WithPayload sets the bytes pushed on every fire.
func (e *Entry) WithPayload(p []byte) *Entry { e.Payload = p; return e }

// EveryMinute fires each minute.
func (e *Entry) EveryMinute() *Entry { e.Spec = "* * * * *"; return e }

// Hourly fires at minute 0 of every hour.
func (e *Entry) Hourly() *Entry { e.Spec = "0 * * * *"; return e }

// DailyAt fires at h:m every day.
func (e *Entry) DailyAt(h, m int) *Entry { e.Spec = fmt.Sprintf("%d %d * * *", m, h); return e }

// Weekly fires at h:m on weekday (time.Sunday = 0).
func (e *Entry) Weekly(day time.Weekday, h, m int) *Entry {
	e.Spec = fmt.Sprintf("%d %d * * %d", m, h, int(day))
	return e
}

// Cron sets a raw 5-field spec, validated at Schedule/Prepare time.
func (e *Entry) Cron(expr string) *Entry { e.Spec = expr; return e }

// WithoutOverlapping skips ticks while a previous run is unsettled. The
// lock is held until the worker settles the job; ExpireAfter bounds it.
func (e *Entry) WithoutOverlapping() *Entry { e.Overlap = true; return e }

// OnOneServer fires once per tick across scheduler replicas. It locks only
// the fire decision, so slow runs still overlap on the firing replica.
func (e *Entry) OnOneServer() *Entry { e.OneServer = true; return e }

// WithExpireAfter bounds an overlap lock (default DefaultExpireAfter).
func (e *Entry) WithExpireAfter(d time.Duration) *Entry { e.ExpireAfter = d; return e }

var (
	regMu   sync.RWMutex
	entries []Entry
)

// Registry is an explicit per-application schedule. Prefer it over the
// package-global Schedule/Registered helpers (kept for generated code):
// explicit registries isolate tests and multiple app instances in one
// process instead of sharing global state.
type Registry struct {
	mu      sync.RWMutex
	entries []Entry
}

// NewRegistry returns an empty schedule registry.
func NewRegistry() *Registry { return &Registry{} }

// Add registers entries on this registry.
func (r *Registry) Add(es ...*Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range es {
		if e != nil {
			r.entries = append(r.entries, *e)
		}
	}
}

// All returns the registry entries in registration order.
func (r *Registry) All() []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Entry(nil), r.entries...)
}

// Schedule registers entries, usually from init() in internal/jobs.
// Specs and job names are validated when the scheduler prepares to run.
// Entries are copied: later mutation of the builder does not affect the
// registered schedule.
func Schedule(es ...*Entry) {
	regMu.Lock()
	defer regMu.Unlock()
	for _, e := range es {
		if e != nil {
			entries = append(entries, *e)
		}
	}
}

// Registered returns the init-collected entries in registration order.
func Registered() []Entry {
	regMu.RLock()
	defer regMu.RUnlock()
	return append([]Entry(nil), entries...)
}

// prepared is an entry with its parsed spec and last-fire time.
type prepared struct {
	entry Entry
	sched cron.Schedule
	last  time.Time
}

// State is a prepared schedule ready to tick.
type State struct {
	items []prepared
}

// Prepare validates entries (known job names, parseable specs) and stamps
// last-fire at now, so booting the scheduler never backfires missed ticks.
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

// due reports whether the next fire after last is at or before now.
func due(p prepared, now time.Time) bool {
	return !p.sched.Next(p.last).After(now)
}

// overlapKey and serverKey namespace the two lock kinds per entry.
func overlapKey(name string) string { return "sched:overlap:" + name }
func serverKey(name string) string  { return "sched:server:" + name }

// CheckAndFire pushes every due entry and advances its last-fire clock.
// OneServer entries take a short decision lock; Overlap entries hold their
// lock until the worker settles the job (see ReleaseHook).
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
				// Release the just-acquired lock: a transient push
				// failure must not wedge the entry until the TTL.
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

// Run prepares the registered entries and ticks until ctx ends. A failed
// tick (broker hiccup, lock error) logs and continues on the next tick
// instead of killing the scheduler: transient outages must delay fires,
// never stop them. Only Prepare failures (bad specs, unknown jobs) are
// fatal, since those never heal without a code change.
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

// envelope wraps a scheduled payload with its overlap-lock identity so the
// worker can release the lock when the job settles. Plain entries push raw
// payloads; only overlap-protected entries carry the envelope.
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

// Data unwraps a scheduled payload for handlers: envelope payloads yield
// their inner data, plain payloads pass through untouched.
func Data(payload []byte) []byte {
	var env envelope
	if err := json.Unmarshal(payload, &env); err != nil || env.Sched == nil {
		return payload
	}
	return []byte(env.Data)
}

// ReleaseHook returns a queue.OnSettled hook releasing overlap locks named
// in settled payloads. Wire it into the worker process (see app.RunWorker):
// the scheduler process only acquires, the worker releases on ack, fail, or
// burial, so a dead job never wedges its schedule past the TTL.
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
