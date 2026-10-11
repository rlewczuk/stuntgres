package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCountExpressionHeldFramesAndControls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, step := range []fixture.Step{
		fixture.Exec("CREATE TABLE t(k TEXT,v INT)", 0),
		fixture.Exec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,3)", 3),
		fixture.Exec("CREATE SEQUENCE s", 0),
		fixture.Exec("CREATE VIEW counted(n,z,r) AS SELECT count(v),count(NULLIF(v,1)),count(CAST('s' AS regclass)) FROM t", 0),
		fixture.Exec("ALTER TABLE t RENAME COLUMN v TO renamed", 0),
		fixture.Exec("ALTER TABLE t RENAME TO renamed_table", 0),
	} {
		p.Step(t, step)
	}
	view := fixture.Select("SELECT n,z,r FROM counted", []string{"n", "z", "r"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(2), int64(1), int64(3)}})
	p.Step(t, view)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, p.Memory, p.MemoryRecord, view)
	for _, backend := range p.Backends() {
		func() {
			const q = "SELECT count(renamed) FROM renamed_table"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held, err := stmt.QueryContext(context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder))
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			backend.Recorder.Mu.Lock()
			heldRecord := backend.Recorder.Operations[len(backend.Recorder.Operations)-1]
			backend.Recorder.Mu.Unlock()
			executor := fixture.HeldStatement{backend.DB, stmt, q}
			step := fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}})
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO renamed_table VALUES('b',NULL)", 1))
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, step)
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("INSERT INTO renamed_table VALUES('b',4)", 1))
			step.Rows = [][]any{{int64(3)}}
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, step)
			if !backend.PG {
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
			}
			queryCountHeldRows(t, held, 2)
			backend.Recorder.Mu.Lock()
			recorded := heldRecord.Closed && heldRecord.CloseErr == nil && reflect.DeepEqual(heldRecord.Columns, []string{"count"}) && len(heldRecord.Rows) == 1 && len(heldRecord.Rows[0]) == 1 && heldRecord.Rows[0][0] == int64(2)
			backend.Recorder.Mu.Unlock()
			if !recorded {
				t.Fatal("held count consumed output/closure not recorded")
			}
			if !backend.PG {
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, view)
			}
		}()
	}
}

func queryCountHeldRows(t *testing.T, rows *sql.Rows, want int64) {
	t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, []string{"count"}) {
		t.Fatal(names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "INT8" {
		t.Fatal(types, err)
	}
	if !rows.Next() {
		t.Fatal("missing held count", rows.Err())
	}
	var n any
	if err := rows.Scan(&n); err != nil || n != want {
		t.Fatal(n, want, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("extra held count", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
