package reference_test

import (
	"reflect"
	"testing"
)

func TestReferenceCompareNullTemporalSymmetryAndHeldFreeze(t *testing.T) {
	p := expressionReferenceSetup(t)
	for _, expr := range []string{
		"'12:34:56' IS DISTINCT FROM TIME '12:34:56'",
		"TIME '12:34:56' IS DISTINCT FROM '12:34:56'",
		"'2024-12-31 12:34:56' IS DISTINCT FROM TIMESTAMP '2024-12-31 12:34:56'",
		"TIMESTAMP '2024-12-31 12:34:56' IS DISTINCT FROM '2024-12-31 12:34:56'",
	} {
		step := referenceSelect("SELECT "+expr+" FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {false}, {false}, {false}})
		scalarStep(t, p, step)
		scalarStep(t, p, referencePrepared(step))
	}
	observed := [][][][]any{}
	for _, backend := range p.backends() {
		db := backend.db
		conn, err := db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(p.ctx, "BEGIN"); err != nil {
			t.Fatal(err)
		}
		defer conn.ExecContext(p.ctx, "ROLLBACK")
		rows, err := conn.QueryContext(p.ctx, "SELECT TIMESTAMP 'now' FROM t")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var captured any
		if !rows.Next() {
			t.Fatal("missing clock row")
		}
		if err := rows.Scan(&captured); err != nil {
			t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		stmt, err := conn.PrepareContext(p.ctx, "SELECT 'now' IS DISTINCT FROM TIMESTAMP 'now',TIMESTAMP 'now' IS DISTINCT FROM $1,'now' IS DISTINCT FROM $1 FROM t")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		}()
		if _, err := conn.ExecContext(p.ctx, "COMMIT"); err != nil {
			t.Fatal(err)
		}
		executions := [][][]any{}
		for range 2 {
			rows, err := stmt.QueryContext(p.ctx, captured)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			names, err := rows.Columns()
			if err != nil || !reflect.DeepEqual(names, []string{"?column?", "?column?", "?column?"}) {
				t.Fatal(names, err)
			}
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for _, typ := range types {
				if typ.DatabaseTypeName() != "BOOL" {
					t.Fatal(typ)
				}
			}
			scalarAssertMetadata(t, backend.pg, types)
			got := [][]any{}
			for rows.Next() {
				var a, b, c any
				if err := rows.Scan(&a, &b, &c); err != nil {
					t.Fatal(err)
				}
				got = append(got, []any{a, b, c})
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			want := [][]any{{false, false, false}, {false, false, false}, {false, false, false}, {false, false, false}}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("independent frozen predicate result", got, want)
			}
			executions = append(executions, got)
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		observed = append(observed, executions)
	}
	if len(observed) == 2 && !reflect.DeepEqual(observed[0], observed[1]) {
		t.Fatal("frozen predicate backend parity", observed)
	}
}
