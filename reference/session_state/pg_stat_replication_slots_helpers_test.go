package reference_test

import (
	"context"
	"database/sql"
	"math"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const replicationSlotsSQL = "SELECT * FROM pg_catalog.pg_stat_replication_slots"

var replicationSlotsNames = []string{"slot_name", "spill_txns", "spill_count", "spill_bytes", "stream_txns", "stream_count", "stream_bytes", "total_txns", "total_bytes", "stats_reset"}
var replicationSlotsTypes = []string{"TEXT", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "TIMESTAMPTZ"}

func replicationSlotsMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, replicationSlotsNames) {
		t.Fatal("ten PG18 columns", names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 10 {
		t.Fatal("ten descriptors", types, err)
	}
	for i, typ := range types {
		scan := reflect.TypeFor[int64]()
		if i == 0 {
			scan = reflect.TypeFor[string]()
		} else if i == 9 {
			scan = reflect.TypeFor[time.Time]()
		}
		if typ.Name() != names[i] || typ.DatabaseTypeName() != replicationSlotsTypes[i] || typ.ScanType() != scan {
			t.Fatalf("column %d: %s/%s/%v", i, typ.Name(), typ.DatabaseTypeName(), typ.ScanType())
		}
		if nullable, known := typ.Nullable(); nullable != !pg || known != !pg {
			t.Fatalf("column %d nullable %t/%t", i, nullable, known)
		}
		length, known := typ.Length()
		if i == 0 {
			if length != math.MaxInt64 || !known {
				t.Fatalf("unbounded TEXT length %d/%t", length, known)
			}
		} else if length != 0 || known {
			t.Fatalf("column %d length %d/%t", i, length, known)
		}
		if precision, scale, known := typ.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatalf("column %d decimal %d/%d/%t", i, precision, scale, known)
		}
	}
}

func replicationSlotsEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor) {
	t.Helper()
	rows, err := executor.QueryContext(p.Ctx, replicationSlotsSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	replicationSlotsMetadata(t, rows, b.PG)
	if rows.Next() || rows.Err() != nil {
		t.Fatal("absent logical slots must not publish a row", rows.Err())
	}
}

func replicationSlotsSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM replication_slots_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
}

func replicationSlotsMutationDiagnostic(operation string) fixture.Diagnostic {
	action := map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[operation]
	message := map[string]string{"INSERT": `cannot insert into view "pg_stat_replication_slots"`, "UPDATE": `cannot update view "pg_stat_replication_slots"`, "DELETE": `cannot delete from view "pg_stat_replication_slots"`}[operation]
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: message, Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + action + " the view, provide an INSTEAD OF " + operation + " trigger or an unconditional ON " + operation + " DO INSTEAD rule."}
}

// replicationSlotsBoundary is the native outcome of creating a slot: physical
// slots are unsupported, and no native output plugin provides test_decoding.
func replicationSlotsBoundary(logical bool) fixture.Diagnostic {
	if logical {
		return activityDiagnostic("58P01", `could not access file "test_decoding": No such file or directory`, 0)
	}
	return activityDiagnostic("0A000", "pg_create_physical_replication_slot physical replication slots and WAL reservation are unsupported in embedded mode", 0)
}

// Register before creating genuine permanent slots; the owner stays pinned until
// drops complete, and fallback teardown uses the backend's independent pool.
func replicationSlotsCleanup(t *testing.T, p *fixture.Pair, b fixture.Backend, names ...string) {
	t.Helper()
	if !b.PG {
		return
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, name := range names {
			var exists bool
			if err := p.Postgres.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_replication_slots WHERE slot_name=$1)", name).Scan(&exists); err != nil {
				t.Error(err)
				continue
			}
			if exists {
				if _, err := p.Postgres.ExecContext(ctx, "SELECT pg_drop_replication_slot($1)", name); err != nil {
					t.Error(err)
				}
			}
		}
	})
}
