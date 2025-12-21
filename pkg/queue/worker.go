package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// uuidString mints a job id. UUIDs keep ids unique across workers without
// a central allocator.
func uuidString() string { return uuid.NewString() }

// pollInterval spaces empty polls so table-backed drivers don't hot-loop
// when the queue is idle. Blocking drivers (redis BRPOP) mostly sleep in
// the broker instead.
const pollInterval = time.Second

// OnSettled runs after a job reaches a terminal state (acked, failed, or
// buried). The scheduler uses it to release overlap locks; pass nil (or
// nothing) when no post-settlement work is needed.
type OnSettled func(job Job)

// settled runs every hook; hooks must not fail the job outcome.
func settled(job Job, hooks []OnSettled) {
	for _, h := range hooks {
		if h != nil {
			h(job)
		}
	}
}

// Dispatch runs one reserved job through the registry: unknown names bury
// (retrying code that does not exist would poison the queue), handler
// errors Fail (retry or bury), success Acks.
func Dispatch(ctx context.Context, q Queue, reg *Registry, job Job, logf func(format string, args ...interface{}), hooks ...OnSettled) {
	DispatchBuried(ctx, q, reg, job, logf, nil, hooks...)
}

// DispatchBuried is Dispatch with burial reporting: when a job exhausts
// its retries, every OnBuried hook runs with the handler's last error
// (failed-jobs recording lives here). Unknown-name burials report a
// descriptive cause instead of a nil error.
func DispatchBuried(ctx context.Context, q Queue, reg *Registry, job Job, logf func(format string, args ...interface{}), buried []OnBuried, hooks ...OnSettled) {
	notifyBuried := func(cause error) {
		for _, h := range buried {
			if h != nil {
				h(job, cause)
			}
		}
	}
	h, ok := reg.Lookup(job.Name)
	if !ok {
		cause := fmt.Errorf("queue: no handler for %q", job.Name)
		logf("queue: no handler for %q, burying job %s", job.Name, job.ID)
		_ = q.Ack(ctx, job.ID)
		notifyBuried(cause)
		settled(job, hooks)
		return
	}
	if err := h(ctx, job); err != nil {
		ferr := q.Fail(ctx, job, err)
		switch {
		case ferr == nil:
			logf("queue: job %s (%s) failed (attempt %d), requeued: %v", job.ID, job.Name, job.Attempts, err)
		case errors.Is(ferr, ErrJobBuried):
			logf("queue: buried job %s (%s) after %d attempts: %v", job.ID, job.Name, job.Attempts, err)
			notifyBuried(err)
		default:
			logf("queue: fail job %s (%s): %v", job.ID, job.Name, ferr)
		}
		settled(job, hooks)
		return
	}
	_ = q.Ack(ctx, job.ID)
	settled(job, hooks)
}

// Run pops and dispatches until ctx ends: empty queues and backend hiccups
// sleep pollInterval, shutdown is immediate on cancel.
func Run(ctx context.Context, q Queue, reg *Registry, logf func(format string, args ...interface{}), hooks ...OnSettled) error {
	return RunBuried(ctx, q, reg, logf, nil, hooks...)
}

// RunBuried is Run with burial reporting: buried jobs notify every OnBuried
// hook (see DispatchBuried).
func RunBuried(ctx context.Context, q Queue, reg *Registry, logf func(format string, args ...interface{}), buried []OnBuried, hooks ...OnSettled) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		job, ok, err := q.Reserve(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			logf("queue: reserve: %v", err)
			if !sleep(ctx, pollInterval) {
				return nil
			}
			continue
		}
		if !ok {
			if !sleep(ctx, pollInterval) {
				return nil
			}
			continue
		}
		DispatchBuried(ctx, q, reg, job, logf, buried, hooks...)
	}
}

// sleep waits d unless ctx ends first, reporting whether to continue.
func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
