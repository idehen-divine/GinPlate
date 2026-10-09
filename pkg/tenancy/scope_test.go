package tenancy

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// scopedWidget is a tenant-data stand-in; plainWidget has no tenant column.
type scopedWidget struct {
	ID       string    `gorm:"primaryKey"`
	TenantID uuid.UUID `gorm:"column:tenant_id"`
	Name     string
}

func (scopedWidget) TableName() string { return "widgets" }

type plainWidget struct {
	ID   string `gorm:"primaryKey"`
	Name string
}

func (plainWidget) TableName() string { return "plain_widgets" }

// openDry opens a fully-wired handle that never touches the network: the
// real mysql dialector with SkipInitializeWithVersion (no version probe)
// plus DryRun (no statement execution) and no automatic ping. SQL text,
// quoting, bindings, and callback clauses are all faithful; only the
// server round trip is skipped.
func openDry(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(gormmysql.New(gormmysql.Config{
		DSN:                       "u:p@tcp(127.0.0.1:1)/db?parseTime=True&loc=Local",
		SkipInitializeWithVersion: true,
		// DryRun builds SQL without executing; SkipDefaultTransaction avoids
		// the implicit begin (which would dial); no automatic ping.
	}), &gorm.Config{DryRun: true, DisableAutomaticPing: true, SkipDefaultTransaction: true})
	if err != nil {
		t.Fatal(err)
	}
	RegisterTenantScopes(db)
	return db
}

// TestScope exercises GORM tenant callbacks in DryRun mode: SQL is built
// but never executed, so no database is needed.
func TestScope(t *testing.T) {
	id := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	withTenant := WithTenantID(context.Background(), id)

	t.Run("query-scoped", func(t *testing.T) {
		db := openDry(t)
		var rows []scopedWidget
		stmt := db.WithContext(withTenant).Model(&scopedWidget{}).
			Where("name = ?", "x").Find(&rows).Statement
		if !strings.Contains(stmt.SQL.String(), "tenant_id") {
			t.Fatalf("SQL lacks tenant scope: %s", stmt.SQL.String())
		}
		if !strings.Contains(stmt.SQL.String(), "name") {
			t.Fatalf("original condition lost: %s", stmt.SQL.String())
		}
	})

	t.Run("create-filled", func(t *testing.T) {
		db := openDry(t)
		w := &scopedWidget{ID: "1", Name: "w"}
		if err := db.WithContext(withTenant).Create(w).Error; err != nil {
			t.Fatal(err)
		}
		if w.TenantID != id {
			t.Fatalf("tenant not filled: %v", w.TenantID)
		}
	})

	t.Run("silent-without-tenant", func(t *testing.T) {
		db := openDry(t)
		var rows []scopedWidget
		stmt := db.WithContext(context.Background()).Model(&scopedWidget{}).Find(&rows).Statement
		if strings.Contains(stmt.SQL.String(), "tenant_id") {
			t.Fatalf("tenant-less query scoped: %s", stmt.SQL.String())
		}
	})

	t.Run("silent-opt-out", func(t *testing.T) {
		db := openDry(t)
		var rows []scopedWidget
		stmt := db.WithContext(WithoutTenantScope(withTenant)).Model(&scopedWidget{}).Find(&rows).Statement
		if strings.Contains(stmt.SQL.String(), "tenant_id") {
			t.Fatalf("opt-out query scoped: %s", stmt.SQL.String())
		}
	})

	t.Run("silent-tenant-less-model", func(t *testing.T) {
		db := openDry(t)
		var rows []plainWidget
		stmt := db.WithContext(withTenant).Model(&plainWidget{}).Find(&rows).Statement
		if strings.Contains(stmt.SQL.String(), "tenant_id") {
			t.Fatalf("tenant-less model scoped: %s", stmt.SQL.String())
		}
	})

	t.Run("explicit-value-preserved", func(t *testing.T) {
		db := openDry(t)
		other := uuid.New()
		w := &scopedWidget{ID: "1", TenantID: other}
		db.WithContext(withTenant).Create(w)
		if w.TenantID != other {
			t.Fatalf("explicit tenant overwritten: %v", w.TenantID)
		}
	})
}
