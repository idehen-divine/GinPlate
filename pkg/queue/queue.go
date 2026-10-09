// Package queue provides background jobs behind a selectable backend
// (sync/database/redis). Handlers self-register by name via init().
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

// Job is one unit of background work (opaque JSON payload).
type Job struct {
	ID          string
	Name        string
	Payload     []byte
	Attempts    int
	AvailableAt time.Time
}

// Handler runs a job: nil error acks, any other error retries then buries.
type Handler func(ctx context.Context, job Job) error

type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: map[string]Handler{}}
}

func (r *Registry) Handle(name string, h Handler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers[name] = h
}

// Lookup returns the handler for name. Unknown names are buried, never retried.
func (r *Registry) Lookup(name string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	h, ok := r.handlers[name]
	return h, ok
}

var defaultRegistry = NewRegistry()

// Handle registers h on the default registry (call from init()).
func Handle(name string, h Handler) {
	defaultRegistry.Handle(name, h)
}

func Default() *Registry { return defaultRegistry }

// Queue is the broker contract: Reserve pops, Ack completes, Fail requeues
// with backoff or buries past max attempts.
type Queue interface {
	Push(ctx context.Context, name string, payload []byte) (string, error)
	Reserve(ctx context.Context) (Job, bool, error)
	Ack(ctx context.Context, id string) error
	Fail(ctx context.Context, job Job, jobErr error) error
}

// Open returns the configured backend (missing handles fail fast, never inline).
func Open(config config.Queue, db *gorm.DB, rdb *redis.Client) (Queue, error) {
	switch config.Connection {
	case "", "sync":
		return NewSync(), nil
	case "redis":
		if rdb == nil {
			return nil, fmt.Errorf("queue: redis backend needs a reachable client")
		}
		return NewRedis(rdb, "", config.Tries), nil
	case "database":
		if db == nil {
			return nil, fmt.Errorf("queue: database backend needs a *gorm.DB")
		}
		return NewDatabase(db, config.Tries), nil
	default:
		return nil, fmt.Errorf("queue: unsupported connection %q", config.Connection)
	}
}

// backoff delays retries exponentially from 30s, capped at 1h.
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

// retryDelay is a var so tests run retries with zero delay.
var retryDelay = backoff
