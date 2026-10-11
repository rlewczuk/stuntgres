package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// Validate raw adapter representation before the recording fixture normalizes it.
func nullCountReferenceRows(t *testing.T, rows *sql.Rows, err error, pg bool, step fixture.Step, arity int64) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, step.Columns) {
		t.Fatal(names, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 {
		t.Fatal(types, err)
	}
	scan := reflect.TypeFor[int64]()
	if pg {
		scan = reflect.TypeFor[int32]()
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "INT4" || typ.ScanType() != scan {
			t.Fatal(typ)
		}
	}
	got := make([][]any, 0)
	for rows.Next() {
		var a, b any
		if err := rows.Scan(&a, &b); err != nil {
			t.Fatal(err)
		}
		x, xok := a.(int64)
		y, yok := b.(int64)
		if !xok || !yok || x+y != arity {
			t.Fatal("raw counts/arity", a, b, arity)
		}
		got = append(got, []any{a, b})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if _, ok := fixture.CompareRows(got, step.Rows, step.Bag); !ok {
		t.Fatal(got, step.Rows)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func nullCountReferenceStep(t *testing.T, p *fixture.Pair, step fixture.Step, arity int64) {
	t.Helper()
	if step.SelectRows {
		for _, backend := range p.Backends() {
			if step.Prepared {
				stmt, err := backend.DB.PrepareContext(p.Ctx, step.Query)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer stmt.Close()
					rows, err := stmt.QueryContext(p.Ctx, step.Args...)
					nullCountReferenceRows(t, rows, err, backend.PG, step, arity)
				}()
			} else {
				rows, err := backend.DB.QueryContext(p.Ctx, step.Query, step.Args...)
				nullCountReferenceRows(t, rows, err, backend.PG, step, arity)
			}
		}
	}
	p.Step(t, step)
}
