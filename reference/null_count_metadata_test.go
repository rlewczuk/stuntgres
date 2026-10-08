package reference_test

import (
	"database/sql"
	"reflect"
	"testing"
)

// Validate raw adapter representation before the recording fixture normalizes it.
func nullCountReferenceRows(t *testing.T, rows *sql.Rows, err error, pg bool, step referenceStep, arity int64) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, step.columns) {
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
	if _, ok := referenceCompareRows(got, step.rows, step.bag); !ok {
		t.Fatal(got, step.rows)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func nullCountReferenceStep(t *testing.T, p *referencePair, step referenceStep, arity int64) {
	t.Helper()
	if step.selectRows {
		for _, backend := range p.backends() {
			if step.prepared {
				stmt, err := backend.db.PrepareContext(p.ctx, step.query)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer stmt.Close()
					rows, err := stmt.QueryContext(p.ctx, step.args...)
					nullCountReferenceRows(t, rows, err, backend.pg, step, arity)
				}()
			} else {
				rows, err := backend.db.QueryContext(p.ctx, step.query, step.args...)
				nullCountReferenceRows(t, rows, err, backend.pg, step, arity)
			}
		}
	}
	p.step(t, step)
}
