package queue

import (
	"context"

	"github.com/google/uuid"
)

type syncQueue struct {
	reg *Registry
}

// NewSync returns an inline queue dispatching through reg.
func NewSync(reg ...*Registry) Queue {
	var r *Registry
	if len(reg) > 0 {
		r = reg[0]
	}
	return &syncQueue{reg: r}
}

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

func (q *syncQueue) Reserve(_ context.Context) (Job, bool, error) {
	return Job{}, false, nil
}

func (q *syncQueue) Ack(_ context.Context, _ string) error { return nil }

func (q *syncQueue) Fail(_ context.Context, _ Job, _ error) error { return ErrJobBuried }
