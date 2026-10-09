package reference_test

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceCompareNullCapturedMetadataControlsAndCancellation(t *testing.T) {
	p := expressionReferenceSetup(t)
	t.Run("native-controls", func(t *testing.T) {
		db := p.native
		ctx := context.Background()
		scalarNativeRaw(t, p, func(raw driver.QueryerContext) {
			cursor, err := raw.QueryContext(ctx, "SELECT v IS NULL,f IS UNKNOWN,v IS DISTINCT FROM b FROM t ORDER BY id", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
			scalarNativeExec(t, db, "INSERT INTO t VALUES(5,5,5,'later',TRUE)")
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
			scalarNativeTuples(t, db, "SELECT id FROM t WHERE v IS NULL", [][]any{{int64(2)}})
			if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('new')")); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cursor.Columns(), []string{"?column?", "?column?", "?column?"}) {
				t.Fatal(cursor.Columns())
			}
			meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
			for i := range 3 {
				if meta.ColumnTypeDatabaseTypeName(i) != "BOOL" {
					t.Fatal("retained metadata changed")
				}
			}
			dst := make([]driver.Value, 3)
			for _, want := range [][]driver.Value{{false, false, true}, {true, true, true}, {false, false, true}, {false, false, true}} {
				if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
					t.Fatal(dst, want, err)
				}
			}
			if err := cursor.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
			scalarNativeTuples(t, db, "SELECT replacement IS NOT NULL FROM t", [][]any{{true}})
			if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES($1)")); err == nil {
				t.Fatal("parameter Restore accepted")
			}
			scalarNativeTuples(t, db, "SELECT replacement FROM t", [][]any{{"new"}})
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if rows, err := db.QueryContext(canceled, "SELECT replacement IS NULL FROM t"); !errors.Is(err, context.Canceled) {
				if rows != nil {
					rows.Close()
				}
				t.Fatal(err)
			}
			iteration, cancel := context.WithCancel(ctx)
			rows, err := raw.QueryContext(iteration, "SELECT replacement IS NULL FROM t", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			cancel()
			if err := rows.Next(make([]driver.Value, 1)); !errors.Is(err, context.Canceled) {
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
			run(referenceSelect("SELECT v IS NULL,f IS UNKNOWN,v IS DISTINCT FROM b FROM t ORDER BY id", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, false, true}, {true, true, true}, {false, false, true}, {false, false, true}}))
			run(referenceExec("INSERT INTO t VALUES(5,5,5,'later',TRUE)", 1))
			run(referenceSelect("SELECT id FROM t WHERE v IS NULL", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
			run(referenceExec("DROP TABLE t", 0))
			run(referenceExec("CREATE TABLE t(replacement TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES('new')", 1))
			run(referenceSelect("SELECT replacement IS NOT NULL FROM t", []string{"?column?"}, []string{"BOOL"}, [][]any{{true}}))
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err := backend.db.QueryContext(canceled, "SELECT replacement IS NULL FROM t")
			if rows != nil {
				rows.Close()
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}
