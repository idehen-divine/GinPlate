package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// DefaultDir is used when the file driver gets an empty directory.
const DefaultDir = "storage/framework/sessions"

// fileSession is the on-disk shape: one JSON file per access jti.
type fileSession struct {
	RefreshJTI       string    `json:"refresh_jti"`
	UserID           string    `json:"user_id"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

// fileStore is a Store backed by one JSON file per session. Reads scan the
// directory for the refresh half, so this driver suits single-instance dev,
// not high-traffic fleets. Development-only: files are 0600 under a 0700
// directory, and refresh consumes are serialized by mu within one process;
// the driver does not coordinate across processes or replicas.
type fileStore struct {
	dir string
	now func() time.Time
	mu  sync.Mutex
}

// File returns a file-backed Store rooted at dir (created on demand).
// An empty dir selects DefaultDir. Development-only: not suitable for
// multi-user hosts or multi-instance production (no shared revocation).
func File(dir string) (Store, error) {
	if strings.TrimSpace(dir) == "" {
		dir = DefaultDir
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &fileStore{dir: dir, now: time.Now}, nil
}

// path maps an access jti to its file.
func (s *fileStore) path(accessJti string) string {
	return filepath.Join(s.dir, accessJti+".json")
}

// Link writes the session file.
func (s *fileStore) Link(_ context.Context, accessJti, refreshJti, userID string, accessTTL, refreshTTL time.Duration) error {
	if accessJti == "" || refreshJti == "" {
		return errors.New("session: empty session id")
	}
	now := s.now()
	raw, err := json.Marshal(fileSession{
		RefreshJTI:       refreshJti,
		UserID:           userID,
		AccessExpiresAt:  now.Add(accessTTL),
		RefreshExpiresAt: now.Add(refreshTTL),
	})
	if err != nil {
		return err
	}
	return os.WriteFile(s.path(accessJti), raw, 0o600)
}

// read loads and expiry-checks a session file, deleting it when expired.
func (s *fileStore) read(accessJti string) (fileSession, bool) {
	var fs fileSession
	raw, err := os.ReadFile(s.path(accessJti))
	if err != nil {
		return fs, false
	}
	if err := json.Unmarshal(raw, &fs); err != nil {
		return fileSession{}, false
	}
	return fs, true
}

// live reports whether the half pair is still valid at now.
func live(fs fileSession, access bool, now time.Time) bool {
	if access {
		return now.Before(fs.AccessExpiresAt)
	}
	return now.Before(fs.RefreshExpiresAt)
}

// AccessValid returns the linked refresh jti for a live access half.
func (s *fileStore) AccessValid(_ context.Context, accessJti string) (string, bool) {
	if accessJti == "" || strings.ContainsAny(accessJti, `/\.`) {
		return "", false
	}
	fs, ok := s.read(accessJti)
	if !ok || !live(fs, true, s.now()) {
		if ok {
			_ = os.Remove(s.path(accessJti))
		}
		return "", false
	}
	return fs.RefreshJTI, true
}

// RefreshValid scans session files for a live refresh half and returns its
// access jti.
func (s *fileStore) RefreshValid(_ context.Context, refreshJti string) (string, bool) {
	if refreshJti == "" {
		return "", false
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return "", false
	}
	now := s.now()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		accessJti := strings.TrimSuffix(e.Name(), ".json")
		fs, ok := s.read(accessJti)
		if !ok {
			continue
		}
		if fs.RefreshJTI == refreshJti && live(fs, false, now) {
			return accessJti, true
		}
	}
	return "", false
}

// Unlink deletes the access file and any file linked to the refresh half.
// Missing files are not errors; other removal failures are returned.
func (s *fileStore) Unlink(_ context.Context, accessJti, refreshJti string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if accessJti != "" && !strings.ContainsAny(accessJti, `/\.`) {
		if err := os.Remove(s.path(accessJti)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if refreshJti != "" {
		if accessJti, ok := s.refreshValidLocked(refreshJti); ok {
			if err := os.Remove(s.path(accessJti)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}

// ReplaceRefresh validates the old refresh half and records the
// replacement session in one mutex-guarded step. The old file is consumed
// first so concurrent callers cannot both succeed; if the replacement write
// then fails, the rotation reports an error and the user must re-login
// (fail closed). Single process only; cross-process rotation still needs
// redis or database.
func (s *fileStore) ReplaceRefresh(_ context.Context, oldRefreshJti, newAccessJti, newRefreshJti, userID string, accessTTL, refreshTTL time.Duration) (string, bool, error) {
	if oldRefreshJti == "" || newAccessJti == "" || newRefreshJti == "" {
		return "", false, errEmptySessionID
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	oldAccessJti, ok := s.refreshValidLocked(oldRefreshJti)
	if !ok {
		return "", false, nil
	}
	now := s.now()
	raw, err := json.Marshal(fileSession{
		RefreshJTI:       newRefreshJti,
		UserID:           userID,
		AccessExpiresAt:  now.Add(accessTTL),
		RefreshExpiresAt: now.Add(refreshTTL),
	})
	if err != nil {
		return "", false, err
	}
	if err := os.Remove(s.path(oldAccessJti)); err != nil && !os.IsNotExist(err) {
		return "", false, err
	}
	if err := os.WriteFile(s.path(newAccessJti), raw, 0o600); err != nil {
		return "", false, err
	}
	return oldAccessJti, true, nil
}

// ConsumeRefresh validates the refresh half and deletes the session in one
// mutex-guarded step so concurrent callers cannot both succeed. Single
// process only; cross-process rotation still needs redis or database.
func (s *fileStore) ConsumeRefresh(_ context.Context, refreshJti string) (string, bool, error) {
	if refreshJti == "" {
		return "", false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	accessJti, ok := s.refreshValidLocked(refreshJti)
	if !ok {
		return "", false, nil
	}
	if err := os.Remove(s.path(accessJti)); err != nil && !os.IsNotExist(err) {
		return "", false, err
	}
	return accessJti, true, nil
}

// refreshValidLocked is RefreshValid without locking (caller holds mu).
func (s *fileStore) refreshValidLocked(refreshJti string) (string, bool) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return "", false
	}
	now := s.now()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		accessJti := strings.TrimSuffix(e.Name(), ".json")
		fs, ok := s.read(accessJti)
		if !ok {
			continue
		}
		if fs.RefreshJTI == refreshJti && live(fs, false, now) {
			return accessJti, true
		}
	}
	return "", false
}
