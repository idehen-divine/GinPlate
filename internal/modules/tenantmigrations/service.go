package tenantmigrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/idehen-divine/GinPlate/pkg/database"
	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/notify"
	"github.com/idehen-divine/GinPlate/pkg/queue"
	"github.com/idehen-divine/GinPlate/pkg/tenancy"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// maxMigrationAttempts bounds automatic retries before a move is marked
// failed and the tenant restored to shared/active.
const maxMigrationAttempts = 10

// JobMigrate is the queue job advancing one migration ledger row. The
// payload carries only the migration id; tenant and placement resolve from
// the control database at execution time.
const JobMigrate = "tenant:migrate"

// MigratePayload is the tenant:migrate job payload.
type MigratePayload struct {
	MigrationID string `json:"migration_id"`
}

// MarshalMigratePayload encodes a migrate job payload.
func MarshalMigratePayload(id uuid.UUID) ([]byte, error) {
	raw, err := json.Marshal(MigratePayload{MigrationID: id.String()})
	if err != nil {
		return nil, fmt.Errorf("tenants: marshal migrate job: %w", err)
	}
	return raw, nil
}

// insertBatchSize caps rows per INSERT statement so chunked copies stay
// under packet limits on wide/TEXT-heavy rows.
const insertBatchSize = 100

// tableSpec describes one tenant-data table for copy/verify/cleanup.
// Columns are identical in both dialects; PK is the conflict target and
// the keyset pagination key.
type tableSpec struct {
	Name    string
	Columns []string
	PK      string
}

// CopyOrder lists tenant tables parents-first (sessions references
// users); cleanup walks it in reverse.
var CopyOrder = []tableSpec{
	{Name: "users", Columns: []string{"id", "tenant_id", "name", "email", "password_hash", "role", "is_active", "created_at", "updated_at"}, PK: "id"},
	{Name: "notifications", Columns: []string{"id", "tenant_id", "notifiable_type", "notifiable_id", "type", "data", "read_at", "created_at"}, PK: "id"},
	{Name: "sessions", Columns: []string{"id", "tenant_id", "refresh_jti", "user_id", "ip_address", "user_agent", "last_activity", "access_expires_at", "refresh_expires_at"}, PK: "id"},
	{Name: "password_reset_tokens", Columns: []string{"email", "kind", "tenant_id", "token_hash", "expires_at", "used_at", "created_at"}, PK: "email"},
}

// TenantMigrationsOpts tunes a Migrator.
type TenantMigrationsOpts struct {
	Driver     string
	ChunkSize  int
	RetainDays int
	AppUser    string
	AppPass    string
	Logf       func(format string, args ...any)
}

// Migrator moves one tenant shared → dedicated through ledgered phases:
// provision → copy → verify → cutover → retaining → done (via Cleanup).
// Every phase persists before mutating, so reruns resume instead of
// restarting; the ledger row is the audit trail.
type TenantMigrations struct {
	mgr      *tenancy.DBManager
	db       *gorm.DB
	tenants  TenantStore
	ledger   LedgerStore
	opts     TenantMigrationsOpts
	queue    queue.Queue
	notifier *notify.Notifier
}

// NewTenantMigrations wires a Migrator over the control handle. Zero ChunkSize
// takes a safe default; Logf defaults to discard.
func NewTenantMigrations(mgr *tenancy.DBManager, db *gorm.DB, tenants TenantStore, ledger LedgerStore, opts TenantMigrationsOpts) *TenantMigrations {
	if opts.ChunkSize <= 0 {
		opts.ChunkSize = 5000
	}
	if opts.RetainDays < 0 {
		opts.RetainDays = 0
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	return &TenantMigrations{mgr: mgr, db: db, tenants: tenants, ledger: ledger, opts: opts}
}

// WithQueue attaches the broker for move-job dispatch: RequestMigration
// enqueues a tenant:migrate job so the worker executes the move. Without
// it, moves run via `migrate tenant-migrate` only.
func (m *TenantMigrations) WithQueue(q queue.Queue) *TenantMigrations {
	m.queue = q
	return m
}

// WithNotifier attaches control-admin alert delivery for move lifecycle
// events (requested, completed, failed). Best-effort; never fatal.
func (m *TenantMigrations) WithNotifier(n *notify.Notifier) *TenantMigrations {
	m.notifier = n
	return m
}

// adminAlert is a database-channel lifecycle event for control admins.
// Duplicated from the parent tenants package (rather than imported) to
// keep the dependency direction parent → children: a dozen lines are
// cheaper than an import cycle.
type adminAlert struct {
	event  string
	slug   string
	detail string
}

func (a adminAlert) Type() string                     { return "tenant." + a.event }
func (a adminAlert) Via() []string                    { return []string{notify.ChannelDatabase} }
func (a adminAlert) ToMail() (pkgmail.Message, error) { return pkgmail.Message{}, nil }
func (a adminAlert) ToDatabase() (map[string]any, error) {
	return map[string]any{"slug": a.slug, "detail": a.detail}, nil
}

// alert sends a control-admin move event, logging instead of failing.
func (m *TenantMigrations) alert(ctx context.Context, event, slug, detail string) {
	if m.notifier == nil {
		return
	}
	to := notify.Notifiable{Type: "control_admin", ID: "broadcast"}
	if err := m.notifier.Send(ctx, to, adminAlert{event: event, slug: slug, detail: detail}); err != nil {
		slog.Warn("control alert failed", "event", event, "tenant", slug, "err", err)
	}
}

// RequestMigration opens a move ledger row and marks the tenant migrating
// (writes shed, reads flow). With a queue attached it also enqueues the
// worker job; otherwise execution runs via the migrate CLI. The tenant
// must be shared and without a live (non-terminal) migration.
func (m *TenantMigrations) RequestMigration(ctx context.Context, slug, reason string) (*Migration, error) {
	rec, err := m.tenants.FindBySlug(ctx, m.db, slug)
	if err != nil {
		return nil, fmt.Errorf("tenants: %w", err)
	}
	if rec.Placement != tenancy.PlacementShared {
		return nil, fmt.Errorf("tenants: %q is already %s", slug, rec.Placement)
	}
	if live, err := m.ledger.LatestMigration(ctx, m.db, rec.ID); err == nil && live != nil && !live.Terminal() {
		return nil, fmt.Errorf("tenants: %q has a live migration (%s)", slug, live.Phase)
	}
	mig := &Migration{TenantID: rec.ID, Phase: PhaseProvision, Reason: reason}
	if err := m.ledger.CreateMigration(ctx, m.db, mig); err != nil {
		return nil, fmt.Errorf("tenants: open migration: %w", err)
	}
	rec.Status = tenancy.StatusMigrating
	if err := m.tenants.SaveTenant(ctx, m.db, rec); err != nil {
		return nil, fmt.Errorf("tenants: mark migrating: %w", err)
	}
	if m.queue != nil {
		raw, err := MarshalMigratePayload(mig.ID)
		if err != nil {
			return nil, err
		}
		// Enqueue failure leaves a valid ledger row: the move runs via
		// `migrate tenant-migrate` instead of being lost.
		if _, err := m.queue.Push(ctx, JobMigrate, raw); err != nil {
			m.opts.Logf("tenant migrate %s: enqueue failed (run via CLI): %v", mig.ID, err)
		}
	}
	return mig, nil
}

// HandleMigrateTask advances one migration to retaining (or failed). It is
// idempotent: reruns resume from the persisted phase. Phase errors save to
// the ledger and return err for job retry; after maxMigrationAttempts the
// move is marked failed and the tenant restored to shared/active.
func (m *TenantMigrations) HandleMigrateTask(ctx context.Context, migrationID uuid.UUID) error {
	mig, err := m.ledger.FindMigration(ctx, m.db, migrationID)
	if err != nil {
		return fmt.Errorf("tenants: load migration: %w", err)
	}
	if mig.Terminal() {
		return nil
	}
	mig.Attempts++
	run := func(phase string, do func(context.Context, *Migration, *tenancy.TenantRecord) error) error {
		mig.Phase = phase
		if err := m.ledger.SaveMigration(ctx, m.db, mig); err != nil {
			return err
		}
		rec, err := m.tenants.FindTenantByID(ctx, m.db, mig.TenantID)
		if err != nil {
			return err
		}
		return do(ctx, mig, rec)
	}
	var rec *tenancy.TenantRecord
	fail := func(err error) error {
		mig.Error = err.Error()
		if mig.Attempts >= maxMigrationAttempts {
			mig.Phase = PhaseFailed
			if rec == nil {
				rec, _ = m.tenants.FindTenantByID(ctx, m.db, mig.TenantID)
			}
			if rec != nil {
				rec.Placement = tenancy.PlacementShared
				rec.Status = tenancy.StatusActive
				_ = m.tenants.SaveTenant(ctx, m.db, rec)
				m.mgr.Forget(rec.Slug)
			}
		}
		_ = m.ledger.SaveMigration(ctx, m.db, mig)
		if mig.Phase == PhaseFailed {
			slug := ""
			if rec != nil {
				slug = rec.Slug
			}
			m.alert(ctx, "migration-failed", slug, mig.Error)
			return nil // stop retrying: terminal, tenant restored
		}
		return err
	}
	phases := []struct {
		name string
		do   func(context.Context, *Migration, *tenancy.TenantRecord) error
	}{
		{PhaseProvision, m.doProvision},
		{PhaseCopy, m.doCopy},
		{PhaseVerify, m.doVerify},
		{PhaseCutover, m.doCutover},
	}
	for _, p := range phases {
		if phaseReached(mig.Phase, p.name) {
			continue
		}
		m.opts.Logf("tenant migrate %s: phase %s", migrationID, p.name)
		if err := run(p.name, p.do); err != nil {
			return fail(err)
		}
	}
	mig.Error = ""
	if err := m.ledger.SaveMigration(ctx, m.db, mig); err != nil {
		return err
	}
	return nil
}

// phaseReached reports whether a persisted phase already passed name in
// provision → copy → verify → cutover → retaining order.
func phaseReached(persisted, name string) bool {
	order := map[string]int{
		PhaseProvision: 0, PhaseCopy: 1, PhaseVerify: 2,
		PhaseCutover: 3, PhaseRetaining: 4, PhaseDone: 5,
	}
	a, okA := order[persisted]
	b, okB := order[name]
	return okA && okB && a > b
}

// doProvision creates the dedicated database, migrates the tenant schema
// onto it, and ensures the limited application role when configured.
func (m *TenantMigrations) doProvision(ctx context.Context, mig *Migration, rec *tenancy.TenantRecord) error {
	_, dsn, err := m.dedicatedTarget(rec)
	if err != nil {
		return err
	}
	name := database.DBName(m.opts.Driver, dsn)
	if err := database.CreateDB(m.opts.Driver, database.ServerDSN(m.opts.Driver, dsn), name); err != nil {
		return fmt.Errorf("create tenant db: %w", err)
	}
	if err := database.For(m.opts.Driver, dsn, "tenants").Up(); err != nil {
		return fmt.Errorf("migrate tenant db: %w", err)
	}
	if err := database.EnsureAppRole(m.opts.Driver, dsn, name, m.opts.AppUser, m.opts.AppPass); err != nil {
		return fmt.Errorf("ensure app role: %w", err)
	}
	return nil
}

// dedicatedTarget resolves the dedicated DSN for a tenant record.
func (m *TenantMigrations) dedicatedTarget(rec *tenancy.TenantRecord) (kind, dsn string, err error) {
	def := ""
	return tenancy.TenantTarget(tenancy.PlacementDedicated, "", rec.Slug, def, m.dsnTemplate())
}

// dsnTemplate returns the configured dedicated DSN template.
func (m *TenantMigrations) dsnTemplate() string { return m.mgr.DSNTemplate() }

// doCopy streams the tenant's rows pool → dedicated in idempotent
// delete-then-insert chunks, recording per-table progress for resume.
func (m *TenantMigrations) doCopy(ctx context.Context, mig *Migration, rec *tenancy.TenantRecord) error {
	src, dst, err := m.copyHandles(rec)
	if err != nil {
		return err
	}
	defer src.Close()
	defer dst.Close()
	progress := decodeProgress(mig.Progress)
	for _, table := range CopyOrder {
		count, err := m.copyTable(ctx, src, dst, rec.ID.String(), table)
		if err != nil {
			return fmt.Errorf("copy %s: %w", table.Name, err)
		}
		progress[table.Name] = count
		mig.Progress = encodeProgress(progress)
		if err := m.ledger.SaveMigration(ctx, m.db, mig); err != nil {
			return err
		}
	}
	return nil
}

// copyHandles opens raw sql handles for the source pool and the dedicated
// target. Callers close both.
func (m *TenantMigrations) copyHandles(rec *tenancy.TenantRecord) (*sql.DB, *sql.DB, error) {
	srcDSN, err := m.mgr.PoolDSNFor(rec.Pool)
	if err != nil {
		return nil, nil, err
	}
	_, dstDSN, err := m.dedicatedTarget(rec)
	if err != nil {
		return nil, nil, err
	}
	src, err := database.Open(m.opts.Driver, srcDSN)
	if err != nil {
		return nil, nil, fmt.Errorf("open pool: %w", err)
	}
	dst, err := database.Open(m.opts.Driver, dstDSN)
	if err != nil {
		src.Close()
		return nil, nil, fmt.Errorf("open dedicated: %w", err)
	}
	return src, dst, nil
}

// copyTable streams one table in keyset chunks, returning the total copied.
func (m *TenantMigrations) copyTable(ctx context.Context, src, dst *sql.DB, tenantID string, table tableSpec) (int64, error) {
	cols := strings.Join(table.Columns, ", ")
	var total int64
	last := ""
	for {
		rows, err := src.QueryContext(ctx,
			rebind(m.opts.Driver, fmt.Sprintf(
				"SELECT %s FROM %s WHERE tenant_id = ? AND %s > ? ORDER BY %s LIMIT %d",
				cols, table.Name, table.PK, table.PK, m.opts.ChunkSize), 3),
			tenantID, last)
		if err != nil {
			return total, err
		}
		page, err := readRows(rows, len(table.Columns))
		closeErr := rows.Close()
		if err != nil {
			return total, err
		}
		if closeErr != nil {
			return total, closeErr
		}
		if len(page) == 0 {
			return total, nil
		}
		if err := m.insertChunk(ctx, dst, tenantID, table, page); err != nil {
			return total, err
		}
		total += int64(len(page))
		last = stringValue(page[len(page)-1][pkIndex(table)])
	}
}

// insertChunk deletes then inserts one page in small batches (idempotent).
func (m *TenantMigrations) insertChunk(ctx context.Context, dst *sql.DB, tenantID string, table tableSpec, page [][]any) error {
	ids := make([]any, 0, len(page))
	for _, row := range page {
		ids = append(ids, row[pkIndex(table)])
	}
	if err := m.deleteIDs(ctx, dst, tenantID, table, ids); err != nil {
		return err
	}
	cols := strings.Join(table.Columns, ", ")
	for start := 0; start < len(page); start += insertBatchSize {
		end := start + insertBatchSize
		if end > len(page) {
			end = len(page)
		}
		batch := page[start:end]
		holders := make([]string, 0, len(batch))
		args := make([]any, 0, len(batch)*len(table.Columns))
		for _, row := range batch {
			ph := make([]string, 0, len(table.Columns))
			for _, v := range row {
				ph = append(ph, "?")
				args = append(args, v)
			}
			holders = append(holders, "("+strings.Join(ph, ", ")+")")
		}
		stmt := fmt.Sprintf("INSERT INTO %s (%s) VALUES %s", table.Name, cols, strings.Join(holders, ", "))
		if m.opts.Driver == "pgsql" {
			stmt += fmt.Sprintf(" ON CONFLICT (%s) DO NOTHING", table.PK)
		}
		if _, err := dst.ExecContext(ctx, rebind(m.opts.Driver, stmt, len(args)), args...); err != nil {
			return err
		}
	}
	return nil
}

// deleteIDs removes one page of ids for idempotent reruns.
func (m *TenantMigrations) deleteIDs(ctx context.Context, dst *sql.DB, tenantID string, table tableSpec, ids []any) error {
	ph := make([]string, 0, len(ids))
	args := make([]any, 0, len(ids)+1)
	args = append(args, tenantID)
	for _, id := range ids {
		ph = append(ph, "?")
		args = append(args, id)
	}
	stmt := fmt.Sprintf("DELETE FROM %s WHERE tenant_id = ? AND %s IN (%s)", table.Name, table.PK, strings.Join(ph, ", "))
	_, err := dst.ExecContext(ctx, rebind(m.opts.Driver, stmt, len(args)), args...)
	return err
}

// doVerify counts every table on both sides and records checksums plus a
// cutover watermark. Any mismatch fails the move for retry.
func (m *TenantMigrations) doVerify(ctx context.Context, mig *Migration, rec *tenancy.TenantRecord) error {
	src, dst, err := m.copyHandles(rec)
	if err != nil {
		return err
	}
	defer src.Close()
	defer dst.Close()
	sums := make([]string, 0, len(CopyOrder))
	for _, table := range CopyOrder {
		var sc, dc int64
		q := rebind(m.opts.Driver, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE tenant_id = ?", table.Name), 1)
		if err := src.QueryRowContext(ctx, q, rec.ID.String()).Scan(&sc); err != nil {
			return fmt.Errorf("verify %s source: %w", table.Name, err)
		}
		if err := dst.QueryRowContext(ctx, q, rec.ID.String()).Scan(&dc); err != nil {
			return fmt.Errorf("verify %s target: %w", table.Name, err)
		}
		if sc != dc {
			return fmt.Errorf("verify %s: source=%d target=%d", table.Name, sc, dc)
		}
		sums = append(sums, fmt.Sprintf("%s=%d", table.Name, dc))
	}
	now := time.Now().UTC()
	mig.Checksums = strings.Join(sums, ",")
	mig.Watermark = &now
	return m.ledger.SaveMigration(ctx, m.db, mig)
}

// doCutover flips placement to dedicated, pre-warms the handle, and parks
// the move in retaining until Cleanup (after the retention window) drops
// the pool rows.
func (m *TenantMigrations) doCutover(ctx context.Context, mig *Migration, rec *tenancy.TenantRecord) error {
	rec.Placement = tenancy.PlacementDedicated
	rec.Status = tenancy.StatusActive
	if err := m.tenants.SaveTenant(ctx, m.db, rec); err != nil {
		return fmt.Errorf("cutover tenant: %w", err)
	}
	m.mgr.Forget(rec.Slug)
	db, err := m.mgr.DBForTenant(ctx, rec)
	if err != nil {
		return fmt.Errorf("pre-warm dedicated: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer pingCancel()
	if err := sqlDB.PingContext(pingCtx); err != nil {
		return fmt.Errorf("pre-warm dedicated: %w", err)
	}
	mig.Phase = PhaseRetaining
	if err := m.ledger.SaveMigration(ctx, m.db, mig); err != nil {
		return err
	}
	m.alert(ctx, "migration-completed", rec.Slug, "Cutover done; pool rows retained until cleanup.")
	return nil
}

// PurgeTenantData deletes every tenant-data row for tenantID from the
// database at poolDSN, walking CopyOrder in reverse (children before
// parents). It is shared by Cleanup (post-retention) and explicit tenant
// removal; callers own the decision, this owns the order.
func PurgeTenantData(ctx context.Context, driver, poolDSN, tenantID string) error {
	pool, err := database.Open(driver, poolDSN)
	if err != nil {
		return fmt.Errorf("open pool: %w", err)
	}
	defer pool.Close()
	for i := len(CopyOrder) - 1; i >= 0; i-- {
		table := CopyOrder[i]
		stmt := fmt.Sprintf("DELETE FROM %s WHERE tenant_id = ?", table.Name)
		if database.NormalizeDriver(driver) == "pgsql" {
			stmt = fmt.Sprintf("DELETE FROM %s WHERE tenant_id = $1", table.Name)
		}
		if _, err := pool.ExecContext(ctx, stmt, tenantID); err != nil {
			return fmt.Errorf("purge %s: %w", table.Name, err)
		}
	}
	return nil
}

// Cleanup drops the retained pool rows after the retention window (or with
// force) and marks the move done. Run via `migrate tenant-cleanup`.
func (m *TenantMigrations) Cleanup(ctx context.Context, slug string, force bool) error {
	rec, err := m.tenants.FindBySlug(ctx, m.db, slug)
	if err != nil {
		return fmt.Errorf("tenants: %w", err)
	}
	mig, err := m.ledger.LatestMigration(ctx, m.db, rec.ID)
	if err != nil {
		return fmt.Errorf("tenants: load migration: %w", err)
	}
	if mig.Phase != PhaseRetaining {
		return fmt.Errorf("tenants: migration %s is %s, not retaining", mig.ID, mig.Phase)
	}
	if !force {
		if mig.Watermark == nil || time.Since(*mig.Watermark) < time.Duration(m.opts.RetainDays)*24*time.Hour {
			return fmt.Errorf("tenants: retention window not elapsed (use --force to override)")
		}
	}
	srcDSN, err := m.mgr.PoolDSNFor(rec.Pool)
	if err != nil {
		return err
	}
	if err := PurgeTenantData(ctx, m.opts.Driver, srcDSN, rec.ID.String()); err != nil {
		return fmt.Errorf("tenants: %w", err)
	}
	mig.Phase = PhaseDone
	return m.ledger.SaveMigration(ctx, m.db, mig)
}

// Status reports the newest move ledger row, or nil when the tenant never
// moved. Unknown tenants are a 404.
func (m *TenantMigrations) Status(ctx context.Context, slug string) (*Migration, error) {
	rec, err := m.tenants.FindBySlug(ctx, m.db, strings.ToLower(strings.TrimSpace(slug)))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, web.NotFound("Tenant not found.")
		}
		return nil, web.Wrap(http.StatusInternalServerError, "Could not load tenant.", err)
	}
	led, err := m.ledger.LatestMigration(ctx, m.db, rec.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, web.Wrap(http.StatusInternalServerError, "Could not load migration.", err)
	}
	return led, nil
}

// Rollback aborts a non-terminal move and restores shared/active.
func (m *TenantMigrations) Rollback(ctx context.Context, migrationID uuid.UUID) error {
	mig, err := m.ledger.FindMigration(ctx, m.db, migrationID)
	if err != nil {
		return fmt.Errorf("tenants: load migration: %w", err)
	}
	if mig.Phase == PhaseDone {
		return fmt.Errorf("tenants: migration already done (pool rows cleaned)")
	}
	rec, err := m.tenants.FindTenantByID(ctx, m.db, mig.TenantID)
	if err != nil {
		return fmt.Errorf("tenants: load tenant: %w", err)
	}
	rec.Placement = tenancy.PlacementShared
	rec.Status = tenancy.StatusActive
	if err := m.tenants.SaveTenant(ctx, m.db, rec); err != nil {
		return err
	}
	m.mgr.Forget(rec.Slug)
	mig.Phase = PhaseFailed
	mig.Error = "rolled back by operator"
	return m.ledger.SaveMigration(ctx, m.db, mig)
}

// rebind converts ? placeholders to $n for pgsql; mysql passes through.
// total is the argument count (only needed for the pgsql path).
func rebind(driver, q string, total int) string {
	_ = total
	if database.NormalizeDriver(driver) != "pgsql" {
		return q
	}
	var out strings.Builder
	n := 0
	for _, r := range q {
		if r == '?' {
			n++
			fmt.Fprintf(&out, "$%d", n)
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// readRows scans a page into driver values, normalizing []byte to string so
// TEXT/CHAR round-trips identically on both engines.
func readRows(rows *sql.Rows, cols int) ([][]any, error) {
	var page [][]any
	for rows.Next() {
		vals := make([]any, cols)
		ptrs := make([]any, cols)
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		page = append(page, vals)
	}
	return page, rows.Err()
}

// pkIndex returns the column position of the table PK.
func pkIndex(table tableSpec) int {
	for i, c := range table.Columns {
		if c == table.PK {
			return i
		}
	}
	return 0
}

// stringValue renders a keyset cursor.
func stringValue(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// decodeProgress unmarshals the ledger progress map (tolerating legacy or
// corrupt payloads as empty).
func decodeProgress(raw string) map[string]int64 {
	out := map[string]int64{}
	if raw == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// encodeProgress marshals the ledger progress map.
func encodeProgress(p map[string]int64) string {
	raw, err := json.Marshal(p)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
