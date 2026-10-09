package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceExpressionCoreCapturedProjectionAndRestoreAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	t.Run("native-controls", func(t *testing.T) {
		db := p.native
		scalarNativeExec(t, db, "CREATE TABLE t(id INT NOT NULL)")
		scalarNativeExec(t, db, "INSERT INTO t VALUES(1),(2)")
		scalarNativeRaw(t, p, func(raw driver.QueryerContext) {
			owned := []byte("before")
			cursor, err := raw.QueryContext(context.Background(), "SELECT id,$1,NULL FROM t ORDER BY id", []driver.NamedValue{{Ordinal: 1, Value: owned}})
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
			owned[0] = 'X'
			scalarNativeExec(t, db, "INSERT INTO t VALUES(3)")
			if err := stuntgres.Reset(context.Background(), db); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(context.Background(), db, strings.NewReader("CREATE TABLE t(replacement TEXT); INSERT INTO t VALUES('after')")); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(cursor.Columns(), []string{"id", "?column?", "?column?"}) {
				t.Fatal(cursor.Columns())
			}
			meta := cursor.(driver.RowsColumnTypeDatabaseTypeName)
			for i, name := range []string{"INT4", "TEXT", "TEXT"} {
				if meta.ColumnTypeDatabaseTypeName(i) != name {
					t.Fatal("metadata replaced")
				}
			}
			dst := make([]driver.Value, 3)
			for i := range 2 {
				if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst, []driver.Value{int64(i + 1), "before", nil}) {
					t.Fatal(dst, err)
				}
			}
			if err := cursor.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
			scalarNativeTuples(t, db, "SELECT replacement FROM t", [][]any{{"after"}})
			err = stuntgres.Restore(context.Background(), db, strings.NewReader("CREATE TABLE replacement(i INT); INSERT INTO replacement VALUES($1)"))
			scalarNativeDiagnostic(t, err, stuntgres.Error{Code: "42P02", Message: "parameters are not allowed in restore", Position: "65"})
			scalarNativeTuples(t, db, "SELECT replacement FROM t", [][]any{{"after"}})
		})
	})
	for _, backend := range p.backends() {
		if !backend.pg {
			continue
		}
		t.Run(backend.name, func(t *testing.T) {
			run := func(step referenceStep) { scalarRunStep(t, p.ctx, backend.db, backend.recorder, step) }
			run(referenceExec("CREATE TABLE t(id INT NOT NULL)", 0))
			run(referenceExec("INSERT INTO t VALUES(1),(2)", 2))
			owned := []byte("before")
			rows, err := backend.db.QueryContext(p.ctx, "SELECT id,$1,NULL FROM t ORDER BY id", owned)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			owned[0] = 'X'
			run(referenceExec("INSERT INTO t VALUES(3)", 1))
			scalarRows(t, true, rows, []string{"id", "?column?", "?column?"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "before", nil}, {int64(2), "before", nil}}, false)
			run(referenceExec("DROP TABLE t", 0))
			run(referenceExec("CREATE TABLE t(replacement TEXT)", 0))
			run(referenceExec("INSERT INTO t VALUES('after')", 1))
			run(referenceSelect("SELECT replacement FROM t", []string{"replacement"}, []string{"TEXT"}, [][]any{{"after"}}))
		})
	}
}

func TestReferenceExpressionCoreCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	scalarStep(t, p, referenceExec("CREATE TABLE t(id INT,s TEXT,f BOOL)", 0))
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			q := "SELECT id FROM t WHERE $1"
			held, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			bad := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "truth"`, Context: "unnamed portal parameter $1 = '...'"}, "truth")
			bad.selectRows = true
			bad.errorStage = "execute"
			scalarRunStep(t, p.ctx, referenceHeldStatement{backend.db, held, q}, backend.recorder, bad)
			rows, err := held.QueryContext(p.ctx)
			if rows != nil {
				rows.Close()
			}
			if err == nil {
				t.Fatal("missing actual arguments accepted")
			}
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("argument count acquired SQLSTATE", err)
			}
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err = backend.db.QueryContext(canceled, "SELECT id FROM t")
			if rows != nil {
				rows.Close()
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
	scalarStep(t, p, referenceExec("INSERT INTO t VALUES(1,'x',TRUE)", 1))
	t.Run("native-raw", func(t *testing.T) {
		scalarNativeRaw(t, p, func(raw driver.QueryerContext) {
			ctx, cancel := context.WithCancel(p.ctx)
			cursor, err := raw.QueryContext(ctx, "SELECT 7 FROM t", nil)
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			defer cursor.Close()
			cancel()
			if err := cursor.Next(make([]driver.Value, 1)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	})
}
