package tenantmigrations

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestTenantMigrations covers the engine-independent move contracts: phase
// ordering, table copy order (FK-safe), placeholder rebinding, progress
// serialization, and job payload encoding. Live copy/verify/cutover needs
// MySQL and PostgreSQL handles (see P6 integration notes in TENANCY.md).
func TestMigrator(t *testing.T) {
	t.Run("phase-order", func(t *testing.T) {
		if !phaseReached(PhaseCopy, PhaseProvision) {
			t.Fatal("copy should outrank provision")
		}
		if phaseReached(PhaseProvision, PhaseCopy) {
			t.Fatal("provision must not outrank copy")
		}
		if !phaseReached(PhaseRetaining, PhaseCutover) {
			t.Fatal("retaining should outrank cutover")
		}
		if phaseReached("bogus", PhaseCopy) || phaseReached(PhaseCopy, "bogus") {
			t.Fatal("unknown phases must not compare")
		}
	})

	t.Run("copy-order-parents-first", func(t *testing.T) {
		pos := map[string]int{}
		for i, table := range CopyOrder {
			pos[table.Name] = i
		}
		// sessions references users(id): users must copy first.
		if pos["users"] > pos["sessions"] {
			t.Fatal("users must precede sessions")
		}
		seen := map[string]bool{}
		for _, table := range CopyOrder {
			if len(table.Columns) == 0 || table.PK == "" {
				t.Fatalf("table %q needs columns and a PK", table.Name)
			}
			if seen[table.Name] {
				t.Fatalf("duplicate table %q", table.Name)
			}
			seen[table.Name] = true
			hasPK, hasTenant := false, false
			for _, c := range table.Columns {
				if c == table.PK {
					hasPK = true
				}
				if c == "tenant_id" {
					hasTenant = true
				}
			}
			if !hasPK || !hasTenant {
				t.Fatalf("table %q needs PK and tenant_id columns", table.Name)
			}
			if table.Columns[pkIndex(table)] != table.PK {
				t.Fatalf("table %q PK not at pkIndex", table.Name)
			}
		}
	})

	t.Run("rebind", func(t *testing.T) {
		if got := rebind("mysql", "a = ? AND b = ?", 2); got != "a = ? AND b = ?" {
			t.Fatalf("mysql = %q", got)
		}
		if got := rebind("pgsql", "a = ? AND b = ?", 2); got != "a = $1 AND b = $2" {
			t.Fatalf("pgsql = %q", got)
		}
		if got := rebind("postgres", "x IN (?,?,?)", 3); got != "x IN ($1,$2,$3)" {
			t.Fatalf("postgres = %q", got)
		}
	})

	t.Run("progress-round-trip", func(t *testing.T) {
		in := map[string]int64{"users": 12, "sessions": 3}
		out := decodeProgress(encodeProgress(in))
		if len(out) != 2 || out["users"] != 12 || out["sessions"] != 3 {
			t.Fatalf("progress = %v", out)
		}
		if len(decodeProgress("")) != 0 || len(decodeProgress("{bad")) != 0 {
			t.Fatal("empty/corrupt progress must decode empty")
		}
	})

	t.Run("job-payload", func(t *testing.T) {
		id := uuid.New()
		raw, err := MarshalMigratePayload(id)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), id.String()) {
			t.Fatalf("payload = %s", raw)
		}
		if JobMigrate != "tenant:migrate" {
			t.Fatalf("job name = %q", JobMigrate)
		}
	})

	t.Run("terminal", func(t *testing.T) {
		if !(Migration{Phase: PhaseDone}.Terminal()) || !(Migration{Phase: PhaseFailed}.Terminal()) {
			t.Fatal("done/failed must be terminal")
		}
		if (Migration{Phase: PhaseCopy}.Terminal()) {
			t.Fatal("copy must not be terminal")
		}
	})
}
