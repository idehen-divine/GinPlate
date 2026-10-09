package tenancy

import (
	"context"
	"reflect"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

// TenantScoped is implemented by models carrying their tenant: the GORM
// callbacks use it as a fast path, falling back to schema reflection for
// models that only declare the TenantID field.
type TenantScoped interface {
	GetTenantID() uuid.UUID
}

// ScopeToTenant clones db with the tenant identity in its context, so every
// query through the clone is auto-scoped once RegisterTenantScopes ran on
// the originating handle. The underlying *gorm.DB (and its pool) is shared.
func ScopeToTenant(db *gorm.DB, id uuid.UUID, slug string) *gorm.DB {
	if db == nil {
		return nil
	}
	ctx := db.Statement.Context
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = WithTenantID(ctx, id)
	if slug != "" {
		ctx = context.WithValue(ctx, tenantSlugKey, slug)
	}
	return db.WithContext(ctx)
}

// RegisterTenantScopes installs row-level tenant isolation on one handle:
// creates auto-fill tenant_id from ctx (unless already set), and
// queries/updates/deletes gain WHERE tenant_id = ?. The callbacks are
// silent when ctx carries no tenant, carries WithoutTenantScope, or the
// model has no TenantID field — shared infrastructure tables (jobs,
// caches, migrations, control tables) and tenant-less tests pass through
// untouched. Call once per DBManager-opened tenant/pool handle, never per
// request.
func RegisterTenantScopes(db *gorm.DB) {
	if db == nil {
		return
	}
	db.Callback().Create().Before("gorm:create").Register("tenancy:fill_tenant", fillTenant)
	db.Callback().Query().Before("gorm:query").Register("tenancy:scope_query", scopeWhere)
	db.Callback().Update().Before("gorm:update").Register("tenancy:scope_update", scopeWhere)
	db.Callback().Delete().Before("gorm:delete").Register("tenancy:scope_delete", scopeWhere)
}

// lookupTenantField returns the schema's TenantID field, or nil for tables
// that are not tenant data.
func lookupTenantField(db *gorm.DB) *schema.Field {
	if db == nil || db.Statement == nil || db.Statement.Schema == nil {
		return nil
	}
	return db.Statement.Schema.LookUpField("TenantID")
}

// tenantIDFor resolves the scoping tenant, or false when callbacks must
// stay silent (no tenant, explicit opt-out, or tenant-less model).
func tenantIDFor(db *gorm.DB) (uuid.UUID, bool) {
	if db == nil || db.Statement == nil || scopeSkipped(db.Statement.Context) {
		return uuid.Nil, false
	}
	if lookupTenantField(db) == nil {
		return uuid.Nil, false
	}
	return TenantIDFrom(db.Statement.Context)
}

// fillTenant stamps tenant_id on create from ctx when the caller left it
// zero (explicit values from seeds and the Migrator are preserved).
func fillTenant(db *gorm.DB) {
	id, ok := tenantIDFor(db)
	if !ok || db.Statement.Dest == nil {
		return
	}
	field := lookupTenantField(db)
	forEachDest(db.Statement.Dest, func(elem reflect.Value) {
		f := elem.FieldByIndex(field.StructField.Index)
		if !f.CanSet() || !f.IsZero() {
			return
		}
		f.Set(tenantValue(field, id))
	})
}

// scopeWhere constrains reads, updates, and deletes to the ctx tenant,
// appending to (never replacing) existing conditions.
func scopeWhere(db *gorm.DB) {
	id, ok := tenantIDFor(db)
	if !ok {
		return
	}
	field := lookupTenantField(db)
	if field == nil {
		return
	}
	cond := clause.Eq{Column: clause.Column{Name: field.DBName}, Value: id}
	stmt := db.Statement
	if where, ok := stmt.Clauses["WHERE"]; ok {
		if w, ok := where.Expression.(clause.Where); ok {
			w.Exprs = append(w.Exprs, cond)
			stmt.Clauses["WHERE"] = clause.Clause{Name: "WHERE", Expression: w}
			return
		}
	}
	stmt.AddClause(clause.Where{Exprs: []clause.Expression{cond}})
}

// tenantValue formats the tenant id for the field kind: uuid-typed columns
// take the UUID (google/uuid implements driver.Valuer), string columns take
// its canonical text form.
func tenantValue(field *schema.Field, id uuid.UUID) reflect.Value {
	if field.FieldType.Kind() == reflect.String {
		return reflect.ValueOf(id.String())
	}
	return reflect.ValueOf(id)
}

// forEachDest visits each struct element of a create destination, which may
// be a struct, a pointer, or a slice of either (batch creates).
func forEachDest(dest interface{}, fn func(elem reflect.Value)) {
	rv := reflect.ValueOf(dest)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		for i := 0; i < rv.Len(); i++ {
			el := rv.Index(i)
			if el.Kind() == reflect.Ptr {
				el = el.Elem()
			}
			if el.Kind() == reflect.Struct {
				fn(el)
			}
		}
		return
	}
	if rv.Kind() == reflect.Struct {
		fn(rv)
	}
}
