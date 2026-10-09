// Package storage provides file storage behind a driver interface
// (local/public/s3).
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/idehen-divine/GinPlate/pkg/config"
)

// ErrNotFound reports a missing file.
var ErrNotFound = errors.New("storage: file not found")

// Storage is the file-store contract, addressed by relative path. Absolute
// paths and ".." escapes are rejected.
type Storage interface {
	Put(ctx context.Context, path string, r io.Reader) error
	Get(ctx context.Context, path string) (io.ReadCloser, error)
	Delete(ctx context.Context, path string) error
	Exists(ctx context.Context, path string) (bool, error)
}

type URLer interface {
	URL(ctx context.Context, path string) (string, error)
}

func Open(cfg config.Filesystem) (Storage, error) {
	switch cfg.Disk {
	case "", "local":
		return NewLocal(cfg.Root, "")
	case "public":
		return NewLocal(cfg.PublicRoot, cfg.PublicURL)
	case "s3":
		return NewS3(cfg.S3)
	default:
		return nil, fmt.Errorf("storage: unsupported disk driver %q", cfg.Disk)
	}
}
