package mail

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/idehen-divine/GinPlate/pkg/queue"
)

// JobName is the queue job for background delivery. Payload is the Message
// as JSON (attachment bytes ride base64-encoded by encoding/json).
const JobName = "mail.send"

// mustMarshal encodes msg for the queue. Messages are validated before
// queueing, so a marshal failure here is a programmer error and panics
// rather than silently dropping mail.
func mustMarshal(msg Message) []byte {
	raw, err := json.Marshal(msg)
	if err != nil {
		panic(fmt.Sprintf("mail: marshal message: %v", err))
	}
	return raw
}

// ParsePayload decodes a "mail.send" job payload.
func ParsePayload(raw []byte) (Message, error) {
	var msg Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		return Message{}, fmt.Errorf("mail: decode job payload: %w", err)
	}
	return msg, nil
}

// HandlerFor returns a queue handler delivering jobs through sender. Use it
// to attach mail delivery to any registry (worker, sync inline, tests).
func HandlerFor(sender Sender) queue.Handler {
	return func(ctx context.Context, job queue.Job) error {
		msg, err := ParsePayload(job.Payload)
		if err != nil {
			return err
		}
		return sender.Send(ctx, msg)
	}
}

// Register attaches the "mail.send" handler to reg.
func Register(reg *queue.Registry, sender Sender) {
	reg.Handle(JobName, HandlerFor(sender))
}
