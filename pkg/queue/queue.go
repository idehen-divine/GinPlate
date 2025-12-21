// Package queue provides background jobs behind a selectable backend, so
// the broker can change without touching callers. Three backends ship:
// sync (runs inline, no broker), database (jobs table), and redis
// (list-based). Jobs self-register handlers by name; the queue:work command
// pops, dispatches, and acks in a loop until its context ends.
package queue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// ErrJobBuried reports a job that exhausted its retries and was dropped.
// Drivers return it from Fail so the worker can log the burial distinctly
// from transient backend errors.
var ErrJobBuried = errors.New("queue: job buried after max attempts")

// Job is one unit of background work. Payload is opaque JSON by
// convention; Attempts counts pops including the current one.
type Job struct {
	ID          string
	Name        string
	Payload     []byte
	Attempts    int
	AvailableAt time.Time
}

// Handler runs a job. A nil error means success (ack); any other error
// means failure (retry with backoff, bury past max attempts).
type Handler func(ctx context.Context, job Job) error

// Registry maps job names to handlers. Register at init() in your jobs
// package; the queue:work command blank-imports it so handlers attach with no
// manual wiring.
type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

// NewRegistry returns an empty handler registry.
func NewRegistry() *Registry {
	return &Registry{handlers: map[string]Handler{}}
}

// Handle registers h for name, replacing any previous handler.
func (r *Registry) Handle(name string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[name] = h
}

// Lookup returns the handler for name, or false when none is registered.
// Unknown job names are buried, never retried: retrying code that does
// not exist can only poison the queue.
func (r *Registry) Lookup(name string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[name]
	return h, ok
}

var defaultRegistry = NewRegistry()

// Handle registers h on the default registry. Call it from init() in
// internal/jobs (or your own jobs package) to attach handlers without
// touching the worker command.
func Handle(name string, h Handler) {
	defaultRegistry.Handle(name, h)
}

// Default returns the default handler registry.
func Default() *Registry { return defaultRegistry }

// Queue is the broker contract. Reserve returns the next available job;
// ok=false means empty (not an error). Ack marks success; Fail requeues
// with backoff or buries past max attempts (returning ErrJobBuried).
type Queue interface {
	Push(ctx context.Context, name string, payload []byte) (string, error)
	Reserve(ctx context.Context) (Job, bool, error)
	Ack(ctx context.Context, id string) error
	Fail(ctx context.Context, job Job, jobErr error) error
}

// Open returns the configured queue backend. Missing handles fail fast: a
// worker that cannot reach its broker must not silently run inline instead.
func Open(cfg config.Queue, db *gorm.DB, rdb *redis.Client) (Queue, error) {
	switch cfg.Connection {
	case "", "sync":
		return NewSync(), nil
	case "redis":
		if rdb == nil {
			return nil, fmt.Errorf("queue: redis backend needs a reachable client")
		}
		return NewRedis(rdb, "", cfg.Tries), nil
	case "database":
		if db == nil {
			return nil, fmt.Errorf("queue: database backend needs a *gorm.DB")
		}
		return NewDatabase(db, cfg.Tries), nil
	default:
		return nil, fmt.Errorf("queue: unsupported connection %q", cfg.Connection)
	}
}

// backoff delays the next attempt exponentially from 30s, capped at 1h,
// so a failing job retries without hammering the backend.
func backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	d := 30 * time.Second << (attempts - 1)
	if d <= 0 || d > time.Hour {
		return time.Hour
	}
	return d
}

// retryDelay is the backoff policy, extracted as a variable so tests run
// retries with zero delay instead of waiting out real backoffs.
var retryDelay = backoff
