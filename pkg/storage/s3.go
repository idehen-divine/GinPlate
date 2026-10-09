package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/idehen-divine/GinPlate/pkg/config"
)

const presignTTL = 15 * time.Minute

type s3Disk struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// NewS3 opens the s3 disk (Bucket required).
func NewS3(cfg config.S3) (Storage, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("storage: s3 disk needs AWS_BUCKET")
	}
	var optFns []func(*awsconfig.LoadOptions) error
	if strings.TrimSpace(cfg.Region) != "" {
		optFns = append(optFns, awsconfig.WithRegion(cfg.Region))
	}
	if strings.TrimSpace(cfg.Key) != "" {
		optFns = append(optFns, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.Key, cfg.Secret, ""),
		))
	}
	sdk, err := awsconfig.LoadDefaultConfig(context.Background(), optFns...)
	if err != nil {
		return nil, err
	}
	return newS3(sdk, cfg.Bucket, cfg.PathStyle), nil
}

func newS3(sdk aws.Config, bucket string, pathStyle bool) *s3Disk {
	client := s3.NewFromConfig(sdk, func(o *s3.Options) {
		o.UsePathStyle = pathStyle
	})
	return &s3Disk{client: client, presign: s3.NewPresignClient(client), bucket: bucket}
}

func (d *s3Disk) key(path string) (string, error) {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return "", fmt.Errorf("storage: invalid path %q", path)
	}
	return strings.TrimPrefix(path, "./"), nil
}

func (d *s3Disk) Put(ctx context.Context, path string, r io.Reader) error {
	key, err := d.key(path)
	if err != nil {
		return err
	}
	_, err = d.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &d.bucket, Key: &key, Body: r})
	return err
}

func (d *s3Disk) Get(ctx context.Context, path string) (io.ReadCloser, error) {
	key, err := d.key(path)
	if err != nil {
		return nil, err
	}
	out, err := d.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &d.bucket, Key: &key})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return out.Body, nil
}

func (d *s3Disk) Delete(ctx context.Context, path string) error {
	key, err := d.key(path)
	if err != nil {
		return err
	}
	_, err = d.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &d.bucket, Key: &key})
	return err
}

func (d *s3Disk) Exists(ctx context.Context, path string) (bool, error) {
	key, err := d.key(path)
	if err != nil {
		return false, err
	}
	_, err = d.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &d.bucket, Key: &key})
	if err != nil {
		var nsk *types.NoSuchKey
		var nf *types.NotFound
		if errors.As(err, &nsk) || errors.As(err, &nf) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (d *s3Disk) URL(ctx context.Context, path string) (string, error) {
	key, err := d.key(path)
	if err != nil {
		return "", err
	}
	req, err := d.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &d.bucket, Key: &key}, s3.WithPresignExpires(presignTTL))
	if err != nil {
		return "", err
	}
	return req.URL, nil
}

var (
	_ Storage = (*localDisk)(nil)
	_ URLer   = (*localDisk)(nil)
	_ Storage = (*s3Disk)(nil)
	_ URLer   = (*s3Disk)(nil)
)
