package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCompareNullTemporalSymmetryAndHeldFreeze(t *testing.T) {
	p := fixture.ExpressionReferenceSetup(t)
	for _, expr := range []string{
		"'12:34:56' IS DISTINCT FROM TIME '12:34:56'",
		"TIME '12:34:56' IS DISTINCT FROM '12:34:56'",
		"'2024-12-31 12:34:56' IS DISTINCT FROM TIMESTAMP '2024-12-31 12:34:56'",
		"TIMESTAMP '2024-12-31 12:34:56' IS DISTINCT FROM '2024-12-31 12:34:56'",
	} {
		step := fixture.Select("SELECT "+expr+" FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{false}, {false}, {false}, {false}})
		fixture.ScalarStep(t, p, step)
		fixture.ScalarStep(t, p, fixture.Prepared(step))
	}
	observed := [][][][]any{}
	for _, backend := range p.Backends() {
		db := backend.DB
		conn, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if _, err := conn.ExecContext(p.Ctx, "BEGIN"); err != nil {
			t.Fatal(err)
		}
		defer conn.ExecContext(p.Ctx, "ROLLBACK")
		rows, err := conn.QueryContext(p.Ctx, "SELECT TIMESTAMP 'now' FROM t")
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
		stmt, err := conn.PrepareContext(p.Ctx, "SELECT 'now' IS DISTINCT FROM TIMESTAMP 'now',TIMESTAMP 'now' IS DISTINCT FROM $1,'now' IS DISTINCT FROM $1 FROM t")
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := stmt.Close(); err != nil {
				t.Error(err)
			}
		}()
		if _, err := conn.ExecContext(p.Ctx, "COMMIT"); err != nil {
			t.Fatal(err)
		}
		executions := [][][]any{}
		for range 2 {
			rows, err := stmt.QueryContext(p.Ctx, captured)
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
			fixture.ScalarAssertMetadata(t, backend.PG, types)
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
