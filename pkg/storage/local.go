package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// localDisk stores files on the local filesystem under root. An empty
// urlBase means private (URL reports an error); otherwise files are
// addressed as urlBase + "/" + path.
type localDisk struct {
	root    string
	urlBase string
}

// NewLocal opens the local disk rooted at dir, created on demand.
// urlBase attaches public addressing; pass "" for a private disk.
func NewLocal(dir, urlBase string) (Storage, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return nil, err
	}
	return &localDisk{root: abs, urlBase: strings.TrimSuffix(urlBase, "/")}, nil
}

// resolve maps a relative path into the root, rejecting absolute paths
// and ".." escapes so callers can never leave the disk. It also resolves
// symlinks (EvalSymlinks) and re-checks containment, so a symlink planted
// beneath the root cannot redirect reads/writes outside it.
func (d *localDisk) resolve(path string) (string, error) {
	if path == "" || filepath.IsAbs(path) {
		return "", fmt.Errorf("storage: invalid path %q", path)
	}
	full := filepath.Join(d.root, filepath.FromSlash(path))
	rel, err := filepath.Rel(d.root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("storage: path escapes disk root %q", path)
	}
	// Resolve symlinks in the root and in the target's parent: lexical
	// containment alone is bypassed by `root/link -> /etc`.
	realRoot, err := filepath.EvalSymlinks(d.root)
	if err != nil {
		return "", fmt.Errorf("storage: resolve root: %w", err)
	}
	parent := filepath.Dir(full)
	realParent := parent
	if st, err := os.Lstat(parent); err == nil && st.Mode()&os.ModeSymlink != 0 {
		if rp, err := filepath.EvalSymlinks(parent); err == nil {
			realParent = rp
		}
	} else if _, err := os.Lstat(full); err == nil {
		if rf, err := filepath.EvalSymlinks(full); err == nil {
			realParent = filepath.Dir(rf)
		}
	}
	if rel, err := filepath.Rel(realRoot, filepath.Join(realParent, filepath.Base(full))); err != nil ||
		rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("storage: path escapes disk root %q", path)
	}
	// Refuse to follow a symlink at the final component itself.
	if st, err := os.Lstat(full); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("storage: refusing symlink path %q", path)
	}
	return full, nil
}

// Put writes r to path, creating parent directories as needed.
func (d *localDisk) Put(ctx context.Context, path string, r io.Reader) error {
	full, err := d.resolve(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(full, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, r)
	return err
}

// Get opens path for reading, or ErrNotFound when missing.
func (d *localDisk) Get(_ context.Context, path string) (io.ReadCloser, error) {
	full, err := d.resolve(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

// Delete removes path. A missing file is success.
func (d *localDisk) Delete(_ context.Context, path string) error {
	full, err := d.resolve(path)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Exists reports whether path is a regular file on the disk.
func (d *localDisk) Exists(_ context.Context, path string) (bool, error) {
	full, err := d.resolve(path)
	if err != nil {
		return false, err
	}
	st, err := os.Stat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return !st.IsDir(), nil
}

// URL addresses path under the public base URL. Private disks (empty base)
// have no URL to give.
func (d *localDisk) URL(_ context.Context, path string) (string, error) {
	if d.urlBase == "" {
		return "", fmt.Errorf("storage: no public URL for a private disk")
	}
	if _, err := d.resolve(path); err != nil {
		return "", err
	}
	return d.urlBase + "/" + strings.TrimPrefix(filepath.ToSlash(path), "/"), nil
}
