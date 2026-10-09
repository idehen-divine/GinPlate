package tenancy

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/idehen-divine/GinPlate/pkg/cache"
)

// TestTenancy covers the engine-independent tenancy contracts: target
// resolution, slug validation, payload envelopes, and cache namespacing.
func TestTenancy(t *testing.T) {
	t.Run("target/shared-defaults", func(t *testing.T) {
		kind, ref, err := TenantTarget("", "", "acme", "shared_1", "")
		if err != nil || kind != "pool" || ref != "shared_1" {
			t.Fatalf("got %q,%q,%v", kind, ref, err)
		}
		kind, ref, err = TenantTarget("shared", "shared_2", "acme", "shared_1", "")
		if err != nil || kind != "pool" || ref != "shared_2" {
			t.Fatalf("got %q,%q,%v", kind, ref, err)
		}
	})

	t.Run("target/dedicated", func(t *testing.T) {
		kind, ref, err := TenantTarget("dedicated", "", "acme", "shared_1", "db_%s")
		if err != nil || kind != "dedicated" || ref != "db_acme" {
			t.Fatalf("got %q,%q,%v", kind, ref, err)
		}
		if _, _, err := TenantTarget("dedicated", "", "acme", "shared_1", ""); err == nil {
			t.Fatal("dedicated without template must fail")
		}
		if _, _, err := TenantTarget("dedicated", "", "../evil", "shared_1", "db_%s"); err == nil {
			t.Fatal("bad slug must fail template expansion")
		}
		if _, _, err := TenantTarget("shard", "", "acme", "shared_1", ""); err == nil {
			t.Fatal("unknown placement must fail")
		}
	})

	t.Run("context/round-trip", func(t *testing.T) {
		id := uuid.New()
		ctx := WithTenant(context.Background(), &Tenant{ID: id, Slug: "acme"})
		if got, ok := TenantIDFrom(ctx); !ok || got != id {
			t.Fatalf("id = %v,%v", got, ok)
		}
		if got, ok := TenantSlugFrom(ctx); !ok || got != "acme" {
			t.Fatalf("slug = %q,%v", got, ok)
		}
		if _, ok := TenantIDFrom(WithoutTenantScope(ctx)); !ok {
			t.Fatal("id must survive scope opt-out")
		}
		if _, ok := TenantIDFrom(context.Background()); ok {
			t.Fatal("empty ctx must have no tenant")
		}
	})

	t.Run("envelope/round-trip", func(t *testing.T) {
		raw, err := WrapPayload("acme", []byte(`{"n":1}`))
		if err != nil {
			t.Fatal(err)
		}
		slug, payload, err := UnwrapPayload(raw)
		if err != nil || slug != "acme" || string(payload) != `{"n":1}` {
			t.Fatalf("got %q,%s,%v", slug, payload, err)
		}
		if _, _, err := UnwrapPayload([]byte(`{"n":1}`)); err == nil {
			t.Fatal("unenveloped payload must fail")
		}
		if _, err := WrapPayload("", []byte(`{}`)); err == nil {
			t.Fatal("empty slug must fail")
		}
	})

	t.Run("cache/namespace", func(t *testing.T) {
		base := cache.NewMemory("")
		a := NamespacedCacheFor(base, "acme")
		b := NamespacedCacheFor(base, "globex")
		ctx := context.Background()
		if err := a.Set(ctx, "k", []byte("va"), time.Minute); err != nil {
			t.Fatal(err)
		}
		if _, ok, _ := b.Get(ctx, "k"); ok {
			t.Fatal("tenant b read tenant a's entry")
		}
		if val, ok, _ := a.Get(ctx, "k"); !ok || string(val) != "va" {
			t.Fatalf("own entry = %q,%v", val, ok)
		}
	})
}
