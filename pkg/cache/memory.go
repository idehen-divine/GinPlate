package cache

import (
	"context"
	"sync"
	"time"
)

// memItem is one cached value with its absolute expiry (zero = forever).
type memItem struct {
	val []byte
	exp time.Time
}

// memoryStore is a process-local Store for tests and backend-less dev.
// A nil clock reads time.Now; tests inject a fake.
type memoryStore struct {
	mu     sync.Mutex
	prefix string
	now    func() time.Time
	items  map[string]memItem
}

// NewMemory returns an in-memory Store namespaced by prefix.
func NewMemory(prefix string) Store {
	return &memoryStore{prefix: prefix, now: time.Now, items: map[string]memItem{}}
}

// Get returns the value, treating expired items as missing (and dropping them).
func (s *memoryStore) Get(_ context.Context, key string) ([]byte, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	it, ok := s.items[s.prefix+key]
	if !ok {
		return nil, false, nil
	}
	if !it.exp.IsZero() && !s.now().Before(it.exp) {
		delete(s.items, s.prefix+key)
		return nil, false, nil
	}
	return it.val, true, nil
}

// Set stores a copy of val; ttl <= 0 means no expiry.
func (s *memoryStore) Set(_ context.Context, key string, val []byte, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := append([]byte(nil), val...)
	var exp time.Time
	if ttl > 0 {
		exp = s.now().Add(ttl)
	}
	s.items[s.prefix+key] = memItem{val: cp, exp: exp}
	return nil
}

// Delete removes keys; missing keys are not an error.
func (s *memoryStore) Delete(_ context.Context, keys ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, k := range keys {
		delete(s.items, s.prefix+k)
	}
	return nil
}

// Exists reports whether key is present and unexpired.
func (s *memoryStore) Exists(ctx context.Context, key string) (bool, error) {
	_, ok, err := s.Get(ctx, key)
	return ok, err
}
