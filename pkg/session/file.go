package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const DefaultDir = "storage/framework/sessions"

// maxFileSessions caps file-driver sessions (var for tests). Development
// sessions must not grow without bound: refresh scans the directory.
var maxFileSessions = 10000

type fileSession struct {
	RefreshJTI       string    `json:"refresh_jti"`
	UserID           string    `json:"user_id"`
	AccessExpiresAt  time.Time `json:"access_expires_at"`
	RefreshExpiresAt time.Time `json:"refresh_expires_at"`
}

// fileStore is a development-only Store (one JSON file per session, no
// cross-process coordination).
type fileStore struct {
	dir string
	now func() time.Time
	mu  sync.Mutex
}

// File returns a file-backed Store rooted at dir (development-only).
func File(dir string) (Store, error) {
	if strings.TrimSpace(dir) == "" {
		dir = DefaultDir
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &fileStore{dir: dir, now: time.Now}, nil
}

func (s *fileStore) path(accessJti string) string {
	return filepath.Join(s.dir, accessJti+".json")
}

func (s *fileStore) Link(_ context.Context, accessJti, refreshJti, userID string, accessTTL, refreshTTL time.Duration) error {
	if accessJti == "" || refreshJti == "" {
		return errors.New("session: empty session id")
	}
	if entries, err := os.ReadDir(s.dir); err == nil && len(entries) >= maxFileSessions {
		if pruned := s.pruneExpired(); pruned == 0 {
			return fmt.Errorf("session: file store full (%d sessions)", maxFileSessions)
		}
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

// pruneExpired deletes expired session files, returning the count removed.
func (s *fileStore) pruneExpired() int {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0
	}
	now := s.now()
	pruned := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var fs fileSession
		if err := json.Unmarshal(raw, &fs); err != nil || !live(fs, false, now) {
			if os.Remove(filepath.Join(s.dir, e.Name())) == nil {
				pruned++
			}
		}
	}
	return pruned
}

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

func live(fs fileSession, access bool, now time.Time) bool {
	if access {
		return now.Before(fs.AccessExpiresAt)
	}
	return now.Before(fs.RefreshExpiresAt)
}

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

func (s *fileStore) RevokeUser(_ context.Context, userID string) error {
	if userID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		accessJti := strings.TrimSuffix(e.Name(), ".json")
		fs, ok := s.read(accessJti)
		if !ok || fs.UserID != userID {
			continue
		}
		if err := os.Remove(s.path(accessJti)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// ReplaceRefresh validates and replaces in one mutex-guarded step (fail
// closed; single process only).
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
