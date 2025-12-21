package queue

import (
	"context"

	"github.com/google/uuid"
)

// syncQueue runs jobs inline on Push and reports an empty queue on Reserve.
// Use it in tests and backend-less dev; nothing is ever persisted.
type syncQueue struct {
	reg *Registry
}

// NewSync returns an inline queue dispatching through reg. A nil registry
// means every Push buries (unknown handler), matching worker behavior.
func NewSync(reg ...*Registry) Queue {
	var r *Registry
	if len(reg) > 0 {
		r = reg[0]
	}
	return &syncQueue{reg: r}
}

// Push dispatches immediately through the registry instead of persisting.
// The returned id is synthesized: inline jobs have no broker row.
func (q *syncQueue) Push(ctx context.Context, name string, payload []byte) (string, error) {
	if q.reg == nil {
		return "", ErrJobBuried
	}
	h, ok := q.reg.Lookup(name)
	if !ok {
		return "", ErrJobBuried
	}
	job := Job{ID: uuid.NewString(), Name: name, Payload: payload, Attempts: 1}
	if err := h(ctx, job); err != nil {
		return "", err
	}
	return job.ID, nil
}

// Reserve always reports empty: sync jobs never wait in a broker.
func (q *syncQueue) Reserve(_ context.Context) (Job, bool, error) {
	return Job{}, false, nil
}

// Ack is a no-op: inline jobs complete on Push.
func (q *syncQueue) Ack(_ context.Context, _ string) error { return nil }

// Fail always reports burial: with no broker there is nothing to requeue
// into, so a failed inline job is dropped by definition.
func (q *syncQueue) Fail(_ context.Context, _ Job, _ error) error { return ErrJobBuried }
