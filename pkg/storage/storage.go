// Package storage provides file storage behind a small interface so the
// disk driver can change without touching callers. Three disks ship:
//
//	local  - private files under Filesystem.Root, no public URLs.
//	public - local files under Filesystem.PublicRoot, served at PublicURL.
//	s3     - object storage via the AWS SDK v2 (or S3-compatible stores
//	         with path-style addressing, e.g. MinIO).
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/idehen-divine/GinPlate/pkg/config"
)

// ErrNotFound reports a missing file. Drivers translate native
// not-exists conditions into it so callers branch portably.
var ErrNotFound = errors.New("storage: file not found")

// Storage is the file-store contract: put/get/delete/exists addressed by
// relative path (e.g. "avatars/ada.png"). Paths are always scoped to the
// driver's root; absolute paths and ".." escapes are rejected.
type Storage interface {
	Put(ctx context.Context, path string, r io.Reader) error
	Get(ctx context.Context, path string) (io.ReadCloser, error)
	Delete(ctx context.Context, path string) error
	Exists(ctx context.Context, path string) (bool, error)
}

// URLer is implemented by disks that can hand out a download URL
// (public files, presigned S3 links). The private local disk does not.
type URLer interface {
	URL(ctx context.Context, path string) (string, error)
}

// Open returns the configured disk driver. Unknown drivers fail fast so a
// typo in FILESYSTEM_DISK surfaces at startup, not on first upload.
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
