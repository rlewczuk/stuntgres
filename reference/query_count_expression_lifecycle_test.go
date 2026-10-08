package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres"
)

func TestReferenceCountExpressionHeldFramesAndControls(t *testing.T) {
	p := newReferenceScenario(t)
	for _, step := range []referenceStep{
		referenceExec("CREATE TABLE t(k TEXT,v INT)", 0),
		referenceExec("INSERT INTO t VALUES('a',1),('a',NULL),(NULL,3)", 3),
		referenceExec("CREATE SEQUENCE s", 0),
		referenceExec("CREATE VIEW counted(n,z,r) AS SELECT count(v),count(NULLIF(v,1)),count(CAST('s' AS regclass)) FROM t", 0),
		referenceExec("ALTER TABLE t RENAME COLUMN v TO renamed", 0),
		referenceExec("ALTER TABLE t RENAME TO renamed_table", 0),
	} {
		p.step(t, step)
	}
	view := referenceSelect("SELECT n,z,r FROM counted", []string{"n", "z", "r"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(2), int64(1), int64(3)}})
	p.step(t, view)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	referenceRunStep(t, p.ctx, p.memory, p.memoryRecord, view)
	for _, backend := range p.backends() {
		func() {
			const q = "SELECT count(renamed) FROM renamed_table"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held, err := stmt.QueryContext(context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder))
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			backend.recorder.mu.Lock()
			heldRecord := backend.recorder.operations[len(backend.recorder.operations)-1]
			backend.recorder.mu.Unlock()
			executor := referenceHeldStatement{backend.db, stmt, q}
			step := referenceSelect(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}})
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO renamed_table VALUES('b',NULL)", 1))
			referenceRunStep(t, p.ctx, executor, backend.recorder, step)
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referenceExec("INSERT INTO renamed_table VALUES('b',4)", 1))
			step.rows = [][]any{{int64(3)}}
			referenceRunStep(t, p.ctx, executor, backend.recorder, step)
			if !backend.pg {
				if err := stuntgres.Reset(p.ctx, p.native); err != nil {
					t.Fatal(err)
				}
			}
			queryCountHeldRows(t, held, 2)
			backend.recorder.mu.Lock()
			recorded := heldRecord.Closed && heldRecord.CloseErr == nil && reflect.DeepEqual(heldRecord.Columns, []string{"count"}) && len(heldRecord.Rows) == 1 && len(heldRecord.Rows[0]) == 1 && heldRecord.Rows[0][0] == int64(2)
			backend.recorder.mu.Unlock()
			if !recorded {
				t.Fatal("held count consumed output/closure not recorded")
			}
			if !backend.pg {
				if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, view)
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
