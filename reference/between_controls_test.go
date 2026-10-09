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

func TestReferenceBetweenControlsAndCancellation(t *testing.T) {
	p := betweenReferenceSetup(t)
	t.Run("native-controls", func(t *testing.T) {
		db := p.native
		ctx := context.Background()
		scalarNativeRaw(t, p, func(raw driver.QueryerContext) {
			cursor, err := raw.QueryContext(ctx, "SELECT v BETWEEN 1 AND 3 FROM t ORDER BY id", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cursor.Close()
			scalarNativeExec(t, db, "CREATE TABLE defaults(b BOOL DEFAULT (2 BETWEEN 1 AND 3),s BOOL DEFAULT (2 BETWEEN SYMMETRIC 3 AND 1),n BOOL DEFAULT (2 NOT BETWEEN 1 AND 3))")
			scalarNativeExec(t, db, "INSERT INTO defaults DEFAULT VALUES")
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
			scalarNativeExec(t, db, "INSERT INTO defaults DEFAULT VALUES")
			scalarNativeTuples(t, db, "SELECT * FROM defaults", [][]any{{true, true, false}, {true, true, false}})
			if err := stuntgres.Restore(ctx, db, strings.NewReader("CREATE TABLE bad(i INT); INSERT INTO bad VALUES('oops')")); err == nil {
				t.Fatal("invalid restore accepted")
			}
			scalarNativeTuples(t, db, "SELECT * FROM defaults", [][]any{{true, true, false}, {true, true, false}})
			scalarNativeExec(t, db, "DELETE FROM t WHERE v BETWEEN 1 AND 3")
			scalarNativeTuples(t, db, "SELECT id FROM t ORDER BY id", [][]any{{int64(1)}, {int64(5)}, {int64(6)}})
			dst := make([]driver.Value, 1)
			for _, want := range []driver.Value{false, true, true, true, false, nil} {
				if err := cursor.Next(dst); err != nil || !reflect.DeepEqual(dst[0], want) {
					t.Fatal(dst, want, err)
				}
			}
			if err := cursor.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if rows, err := db.QueryContext(canceled, "SELECT v BETWEEN 1 AND 3 FROM t"); !errors.Is(err, context.Canceled) {
				if rows != nil {
					rows.Close()
				}
				t.Fatal(err)
			}
			stmt, err := db.Prepare("DELETE FROM t WHERE v NOT BETWEEN 1 AND 3")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			if _, err := stmt.ExecContext(canceled); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(canceled, "DELETE FROM t WHERE v NOT BETWEEN 1 AND 3"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			scalarNativeTuples(t, db, "SELECT id FROM t ORDER BY id", [][]any{{int64(1)}, {int64(5)}, {int64(6)}})
		})
	})
	for _, backend := range p.backends() {
		if !backend.pg {
			continue
		}
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { scalarRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE defaults(b BOOL DEFAULT (2 BETWEEN 1 AND 3),s BOOL DEFAULT (2 BETWEEN SYMMETRIC 3 AND 1),n BOOL DEFAULT (2 NOT BETWEEN 1 AND 3))", 0))
			run(referenceExec("INSERT INTO defaults DEFAULT VALUES", 1))
			run(referenceExec("INSERT INTO defaults DEFAULT VALUES", 1))
			run(referenceSelect("SELECT * FROM defaults", []string{"b", "s", "n"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false}, {true, true, false}}))
			run(referenceExec("DELETE FROM t WHERE v BETWEEN 1 AND 3", 3))
			run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(5)}, {int64(6)}}))
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err := backend.db.QueryContext(canceled, "SELECT v BETWEEN 1 AND 3 FROM t")
			if rows != nil {
				rows.Close()
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			held, err := backend.db.PrepareContext(p.ctx, "DELETE FROM t WHERE v NOT BETWEEN 1 AND 3")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if _, err := held.ExecContext(canceled); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if _, err := backend.db.ExecContext(canceled, "DELETE FROM t WHERE v NOT BETWEEN 1 AND 3"); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			run(referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(5)}, {int64(6)}}))
		})
	}
}
