package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"stuntgres"
	"testing"
)

func TestReferenceCompareOpsCapturedCursorAndCancellation(t *testing.T) {
	p := compareReferenceSetup(t)
	t.Run("native-controls", func(t *testing.T) {
		db := p.native
		ctx := context.Background()
		scalarNativeRaw(t, p, func(raw driver.QueryerContext) {
			cursor, err := raw.QueryContext(ctx, "SELECT i<j,$1<i FROM t ORDER BY id", []driver.NamedValue{{Ordinal: 1, Value: int64(7)}})
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
			scalarNativeExec(t, db, "INSERT INTO t VALUES(5,9,10,'later',TRUE)")
			var dump bytes.Buffer
			if err := stuntgres.Dump(ctx, db, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(ctx, db); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			scalarNativeTuples(t, db, "SELECT id FROM t WHERE i<j ORDER BY id", [][]any{{int64(1)}, {int64(5)}})
			if !reflect.DeepEqual(cursor.Columns(), []string{"?column?", "?column?"}) {
				t.Fatal(cursor.Columns())
			}
			meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
			for i := range 2 {
				if meta.ColumnTypeDatabaseTypeName(i) != "BOOL" {
					t.Fatal("captured metadata replaced")
				}
			}
			dst := make([]driver.Value, 2)
			for _, want := range [][]driver.Value{{true, true}, {false, false}, {nil, nil}, {false, false}} {
				if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
					t.Fatal(dst, want, err)
				}
			}
			if err := cursor.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if rows, err := db.QueryContext(canceled, "SELECT i<j FROM t"); !errors.Is(err, context.Canceled) {
				if rows != nil {
					rows.Close()
				}
				t.Fatal(err)
			}
		})
	})
	for _, backend := range p.backends() {
		if !backend.pg {
			continue
		}
		t.Run(backend.name, func(t *testing.T) {
			run := func(step referenceStep) { scalarRunStep(t, p.ctx, backend.db, backend.recorder, step) }
			run(referenceSelect("SELECT i<j,$1<i FROM t ORDER BY id", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}, {false, false}, {nil, nil}, {false, false}}, 7))
			run(referenceExec("INSERT INTO t VALUES(5,9,10,'later',TRUE)", 1))
			run(referenceSelect("SELECT id FROM t WHERE i<j ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(5)}}))
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err := backend.db.QueryContext(canceled, "SELECT i<j FROM t")
			if rows != nil {
				rows.Close()
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}
