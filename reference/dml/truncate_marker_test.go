package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

const truncateMarkerClear = "TRUNCATE schema_migrations"
const truncateMarkerInsert = "INSERT INTO schema_migrations(version,dirty) VALUES($1,$2)"
const truncateMarkerSelect = "SELECT version,dirty FROM schema_migrations"

func truncateMarkerCreate(t *testing.T, f fixture.ControlsReferenceFixture, seed bool) {
	t.Helper()
	truncateExec(t, f, f.A, "CREATE TABLE schema_migrations(version bigint NOT NULL,dirty boolean NOT NULL)", 0)
	if seed {
		s := fixture.Exec(truncateMarkerInsert, 1)
		s.Args = []any{int64(7), true}
		truncateRun(t, f, f.A, s)
	}
}

// Held routes prepare each handle once, before any savepoint, and retain both
// through all rollback/release operations. RunStep's Prepared flag stays false.
func truncateMarkerRoutes(t *testing.T, p *fixture.Pair, tx *sql.Tx, held bool) (fixture.Executor, fixture.Executor) {
	t.Helper()
	return truncateRestartRoute(t, p, tx, truncateMarkerClear, held),
		truncateRestartRoute(t, p, tx, truncateMarkerInsert, held)
}

func truncateMarkerPairs(t *testing.T, f fixture.ControlsReferenceFixture, clear, insert fixture.Executor, first, last int) {
	t.Helper()
	for i := first; i <= last; i++ {
		truncateExec(t, f, clear, truncateMarkerClear, 0)
		s := fixture.Exec(truncateMarkerInsert, 1)
		s.Args = []any{int64(i), false}
		truncateRun(t, f, insert, s)
	}
}

func truncateMarkerMetadata(t *testing.T, pg bool, columns []*sql.ColumnType) {
	t.Helper()
	fixture.ScalarAssertMetadata(t, pg, columns)
	if len(columns) != 2 {
		t.Fatalf("metadata width %d, want 2", len(columns))
	}
	for i, name := range []string{"version", "dirty"} {
		wantType := []string{"INT8", "BOOL"}[i]
		if columns[i].Name() != name || columns[i].DatabaseTypeName() != wantType {
			t.Fatalf("column %d: %s/%s, want %s/%s", i, columns[i].Name(), columns[i].DatabaseTypeName(), name, wantType)
		}
		if nullable, known := columns[i].Nullable(); nullable || known != !pg {
			t.Fatalf("%s Nullable=(%v,%v), want (false,%v)", name, nullable, known, !pg)
		}
	}
}

func truncateMarkerRow(t *testing.T, f fixture.ControlsReferenceFixture, x fixture.Executor, pg bool, version int64, dirty bool) {
	t.Helper()
	s := fixture.Select(truncateMarkerSelect, []string{"version", "dirty"}, []string{"INT8", "BOOL"}, [][]any{{version, dirty}})
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		truncateMarkerMetadata(t, pg, columns)
	}
	truncateRun(t, f, x, s)
}

func TestReferenceTruncateMarkerReplay(t *testing.T) {
	for _, privateDDL := range []bool{false, true} {
		name := "Plain"
		if privateDDL {
			name = "PrivateDDL"
		}
		t.Run(name, func(t *testing.T) {
			truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
				truncateMarkerCreate(t, f, false)
				tx := truncateBegin(t, p, f.A, nil)
				if privateDDL {
					// Genuine unrelated migration DDL, not a marker-table rewrite.
					truncateExec(t, f, tx, "CREATE TABLE marker_unrelated(id bigint)", 0)
				}
				clear, insert := truncateMarkerRoutes(t, p, tx, held)
				truncateMarkerPairs(t, f, clear, insert, 1, 256)
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				truncateMarkerRow(t, f, f.A, b.PG, 256, false)
				truncateNotices(t, f)
			})
		})
	}
}

func TestReferenceTruncateMarkerRollback(t *testing.T) {
	truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
		truncateMarkerCreate(t, f, true)
		tx := truncateBegin(t, p, f.A, nil)
		clear, insert := truncateMarkerRoutes(t, p, tx, held)
		truncateMarkerPairs(t, f, clear, insert, 1, 256)
		truncateMarkerRow(t, f, tx, b.PG, 256, false)
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		truncateMarkerRow(t, f, f.A, b.PG, 7, true)
		tx = truncateBegin(t, p, f.A, nil)
		clear, insert = truncateMarkerRoutes(t, p, tx, held)
		truncateMarkerPairs(t, f, clear, insert, 1, 256)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		truncateMarkerRow(t, f, f.A, b.PG, 256, false)
		truncateNotices(t, f)
	})
}

func TestReferenceTruncateMarkerSavepoints(t *testing.T) {
	truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
		truncateMarkerCreate(t, f, true)
		tx := truncateBegin(t, p, f.A, nil)
		clear, insert := truncateMarkerRoutes(t, p, tx, held)
		truncateMarkerPairs(t, f, clear, insert, 1, 64)
		truncateExec(t, f, tx, "SAVEPOINT outer_mark", 0)
		truncateMarkerPairs(t, f, clear, insert, 65, 128)
		truncateExec(t, f, tx, "SAVEPOINT inner_mark", 0)
		truncateMarkerPairs(t, f, clear, insert, 129, 160)
		truncateExec(t, f, tx, "RELEASE inner_mark", 0)
		truncateMarkerRow(t, f, tx, b.PG, 160, false)
		truncateExec(t, f, tx, "ROLLBACK TO outer_mark", 0)
		truncateMarkerRow(t, f, tx, b.PG, 64, false)
		truncateMarkerPairs(t, f, clear, insert, 161, 192)
		truncateExec(t, f, tx, "ROLLBACK TO outer_mark", 0)
		truncateMarkerRow(t, f, tx, b.PG, 64, false)
		truncateRestartError(t, f, tx, "ROLLBACK TO inner_mark", truncateWant("3B001", `savepoint "inner_mark" does not exist`))
		truncateRestartError(t, f, clear, truncateMarkerClear, truncateWant("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
		truncateExec(t, f, tx, "ROLLBACK TO outer_mark", 0)
		truncateMarkerRow(t, f, tx, b.PG, 64, false)
		truncateMarkerPairs(t, f, clear, insert, 193, 256)
		truncateExec(t, f, tx, "RELEASE outer_mark", 0)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		truncateMarkerRow(t, f, f.A, b.PG, 256, false)
		truncateNotices(t, f)
	})
}

// Keep actual ColumnType objects, and (in the native phase) the unconsumed
// eager cursor itself, rather than treating copied scalar equality as proof of
// backing lifetime. No transaction-owned cursor is used after its Tx ends.
func truncateMarkerRead(t *testing.T, rows *sql.Rows, pg bool) ([]*sql.ColumnType, []any) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	truncateMarkerMetadata(t, pg, columns)
	if !rows.Next() {
		t.Fatalf("missing retained marker: %v", rows.Err())
	}
	var version, dirty any
	if err := rows.Scan(&version, &dirty); err != nil {
		t.Fatal(err)
	}
	if _, ok := version.(int64); !ok {
		t.Fatalf("version transport %T, want int64", version)
	}
	if _, ok := dirty.(bool); !ok {
		t.Fatalf("dirty transport %T, want bool", dirty)
	}
	if !reflect.DeepEqual([]any{version, dirty}, []any{int64(7), true}) {
		t.Fatalf("retained row (%v,%v), want (7,true)", version, dirty)
	}
	if rows.Next() {
		t.Fatal("unexpected second retained marker")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	return columns, []any{version, dirty}
}

func TestReferenceTruncateMarkerRowsLifetime(t *testing.T) {
	truncateRestartScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, f fixture.ControlsReferenceFixture, held bool) {
		truncateMarkerCreate(t, f, true)
		reader := truncateBegin(t, p, f.B, &sql.TxOptions{ReadOnly: true})
		rows, err := reader.QueryContext(p.Ctx, truncateMarkerSelect)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		metadata, values := truncateMarkerRead(t, rows, b.PG)
		// PostgreSQL retains AS until reader transaction end, conflicting with
		// TRUNCATE's AX. Close and finish before any writer operation.
		if err := reader.Commit(); err != nil {
			t.Fatal(err)
		}
		var retained *sql.Rows
		if !b.PG {
			// Native eager SELECT releases its statement locks at return. This
			// real peer-owned cursor remains unconsumed across all replacements.
			retained, err = f.B.QueryContext(p.Ctx, truncateMarkerSelect)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			oldColumns, err := retained.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			truncateMarkerMetadata(t, false, oldColumns)
		}
		tx := truncateBegin(t, p, f.A, nil)
		clear, insert := truncateMarkerRoutes(t, p, tx, held)
		truncateMarkerPairs(t, f, clear, insert, 1, 64)
		truncateExec(t, f, tx, "SAVEPOINT retained_mark", 0)
		truncateMarkerPairs(t, f, clear, insert, 65, 160)
		truncateExec(t, f, tx, "ROLLBACK TO retained_mark", 0)
		truncateMarkerRow(t, f, tx, b.PG, 64, false)
		truncateMarkerPairs(t, f, clear, insert, 161, 256)
		truncateExec(t, f, tx, "RELEASE retained_mark", 0)
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		truncateMarkerRow(t, f, f.A, b.PG, 256, false)
		truncateMarkerMetadata(t, b.PG, metadata)
		if !reflect.DeepEqual(values, []any{int64(7), true}) {
			t.Fatalf("closed-reader values changed: %#v", values)
		}
		if retained != nil {
			truncateMarkerRead(t, retained, false)
		}
		truncateNotices(t, f)
	})
}
