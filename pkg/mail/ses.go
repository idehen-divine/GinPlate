package mail

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sesv2"
	"github.com/aws/aws-sdk-go-v2/service/sesv2/types"
	"github.com/idehen-divine/GinPlate/pkg/config"
)

// sesMailer delivers through AWS SESv2 as a raw message, so MIME building
// (attachments, inline embeds) is shared with the SMTP driver. Credentials
// follow the same chain as pkg/storage: explicit AWS_* keys when set,
// SDK default chain (env, shared config, IAM role) otherwise.
type sesMailer struct {
	client   *sesv2.Client
	fromAddr string
	fromName string
}

// NewSES builds the SES driver. Region is required (SES is regional);
// credentials fall back to the SDK default chain when AWS keys are empty.
func NewSES(mailCfg config.Mail, awsCfg config.S3) (Sender, error) {
	if strings.TrimSpace(awsCfg.Region) == "" {
		return nil, fmt.Errorf("mail: ses needs AWS_DEFAULT_REGION")
	}
	var optFns []func(*awsconfig.LoadOptions) error
	optFns = append(optFns, awsconfig.WithRegion(awsCfg.Region))
	if strings.TrimSpace(awsCfg.Key) != "" {
		optFns = append(optFns, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(awsCfg.Key, awsCfg.Secret, ""),
		))
	}
	timeout := time.Duration(mailCfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	sdk, err := awsconfig.LoadDefaultConfig(ctx, optFns...)
	if err != nil {
		return nil, err
	}
	return &sesMailer{
		client:   sesv2.NewFromConfig(sdk),
		fromAddr: mailCfg.From.Address,
		fromName: mailCfg.From.Name,
	}, nil
}

// newSESWithClient wires a client directly, so tests inject a stub without
// touching AWS. Kept unexported: production always goes through NewSES.
func newSESWithClient(client *sesv2.Client, fromAddr, fromName string) *sesMailer {
	return &sesMailer{client: client, fromAddr: fromAddr, fromName: fromName}
}

// Send renders msg to MIME and submits it as SESv2 raw mail.
func (m *sesMailer) Send(ctx context.Context, msg Message) error {
	raw, err := buildRaw(m.fromAddr, m.fromName, msg)
	if err != nil {
		return err
	}
	from := m.fromAddr
	if msg.FromAddr != "" {
		from = msg.FromAddr
	}
	name := m.fromName
	if msg.FromName != "" {
		name = msg.FromName
	}
	fromHeader := formatAddr(name, from)
	input := &sesv2.SendEmailInput{
		Content: &types.EmailContent{
			Raw: &types.RawMessage{Data: raw},
		},
		FromEmailAddress: aws.String(fromHeader),
	}
	if len(msg.Tags) > 0 {
		var tags []types.MessageTag
		for k, v := range msg.Tags {
			k, v := k, v
			tags = append(tags, types.MessageTag{Name: aws.String(k), Value: aws.String(v)})
		}
		input.EmailTags = tags
	}
	if _, err := m.client.SendEmail(ctx, input); err != nil {
		return fmt.Errorf("mail: ses send: %w", err)
	}
	return nil
}

var _ Sender = (*sesMailer)(nil)
