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

// presignTTL is how long S3 download links stay valid.
const presignTTL = 15 * time.Minute

// s3Disk stores files in an S3 bucket (or S3-compatible store with
// path-style addressing, e.g. MinIO). Object keys are the relative paths
// as given, without a leading slash.
type s3Disk struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

// NewS3 opens the s3 disk. An empty Key falls back to the SDK default
// credential chain (env, shared config, IAM role); Bucket is required.
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

// newS3 wires SDK clients around a bucket. Split out so tests inject an
// SDK config without touching the network (presigning is local).
func newS3(sdk aws.Config, bucket string, pathStyle bool) *s3Disk {
	client := s3.NewFromConfig(sdk, func(o *s3.Options) {
		o.UsePathStyle = pathStyle
	})
	return &s3Disk{client: client, presign: s3.NewPresignClient(client), bucket: bucket}
}

// key normalizes a relative path into an object key.
func (d *s3Disk) key(path string) (string, error) {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return "", fmt.Errorf("storage: invalid path %q", path)
	}
	return strings.TrimPrefix(path, "./"), nil
}

// Put uploads r to key, creating it (or replacing it) server-side.
func (d *s3Disk) Put(ctx context.Context, path string, r io.Reader) error {
	key, err := d.key(path)
	if err != nil {
		return err
	}
	_, err = d.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &d.bucket, Key: &key, Body: r})
	return err
}

// Get downloads key, or ErrNotFound when the object doesn't exist.
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

// Delete removes key. A missing object is success.
func (d *s3Disk) Delete(ctx context.Context, path string) error {
	key, err := d.key(path)
	if err != nil {
		return err
	}
	_, err = d.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &d.bucket, Key: &key})
	return err
}

// Exists probes key with a HEAD request.
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

// URL presigns a GET link valid for presignTTL. Use it for private buckets;
// point PublicURL-style traffic here when objects aren't world-readable.
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

// compile-time interface checks.
var (
	_ Storage = (*localDisk)(nil)
	_ URLer   = (*localDisk)(nil)
	_ Storage = (*s3Disk)(nil)
	_ URLer   = (*s3Disk)(nil)
)
