package mail

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/idehen-divine/GinPlate/pkg/queue"
)

const JobName = "mail.send"

func mustMarshal(msg Message) []byte {
	raw, err := json.Marshal(msg)
	if err != nil {
		panic(fmt.Sprintf("mail: marshal message: %v", err))
	}
	return raw
}

func ParsePayload(raw []byte) (Message, error) {
	var msg Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		return Message{}, fmt.Errorf("mail: decode job payload: %w", err)
	}
	return msg, nil
}

func HandlerFor(sender Sender) queue.Handler {
	return func(ctx context.Context, job queue.Job) error {
		msg, err := ParsePayload(job.Payload)
		if err != nil {
			return err
		}
		return sender.Send(ctx, msg)
	}
}

func Register(reg *queue.Registry, sender Sender) {
	reg.Handle(JobName, HandlerFor(sender))
}
