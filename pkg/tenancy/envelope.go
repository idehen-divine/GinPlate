package tenancy

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/idehen-divine/GinPlate/pkg/cache"
)

// TenantEnvelope carries background-job payloads with their tenant, so the
// worker re-resolves tenancy per job instead of inheriting HTTP context
// (which does not exist off-request).
type TenantEnvelope struct {
	Tenant  string          `json:"tenant"`
	Payload json.RawMessage `json:"payload"`
}

// WrapPayload envelopes a job payload with its tenant slug.
func WrapPayload(slug string, payload []byte) ([]byte, error) {
	if slug == "" {
		return nil, fmt.Errorf("tenancy: envelope needs a tenant slug")
	}
	raw, err := json.Marshal(TenantEnvelope{Tenant: slug, Payload: payload})
	if err != nil {
		return nil, fmt.Errorf("tenancy: wrap payload: %w", err)
	}
	return raw, nil
}

// UnwrapPayload splits a tenant envelope, erroring when the payload is not
// enveloped so tenant-aware handlers never run tenantless by accident.
func UnwrapPayload(raw []byte) (slug string, payload []byte, err error) {
	var env TenantEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", nil, fmt.Errorf("tenancy: unwrap payload: %w", err)
	}
	if env.Tenant == "" {
		return "", nil, fmt.Errorf("tenancy: payload has no tenant")
	}
	return env.Tenant, []byte(env.Payload), nil
}

// NamespacedCache decorates a cache.Store by prefixing every key with the
// tenant slug, so shared brokers never leak entries across tenants. It
// implements cache.Store and composes with any backend.
type NamespacedCache struct {
	base   cache.Store
	prefix string
}

// NamespacedCacheFor opens the configured store and namespaces it for slug.
// It mirrors cache.Open selection so tenant code needs no backend switch.
func NamespacedCacheFor(base cache.Store, slug string) cache.Store {
	return &NamespacedCache{base: base, prefix: "tenant:" + slug + ":"}
}

func (c *NamespacedCache) key(key string) string { return c.prefix + key }

// Get fetches a namespaced key.
func (c *NamespacedCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	return c.base.Get(ctx, c.key(key))
}

// Set stores a namespaced key.
func (c *NamespacedCache) Set(ctx context.Context, key string, val []byte, ttl time.Duration) error {
	return c.base.Set(ctx, c.key(key), val, ttl)
}

// Delete removes namespaced keys.
func (c *NamespacedCache) Delete(ctx context.Context, keys ...string) error {
	prefixed := make([]string, 0, len(keys))
	for _, k := range keys {
		prefixed = append(prefixed, c.key(k))
	}
	return c.base.Delete(ctx, prefixed...)
}

// Exists reports a namespaced key.
func (c *NamespacedCache) Exists(ctx context.Context, key string) (bool, error) {
	return c.base.Exists(ctx, c.key(key))
}
