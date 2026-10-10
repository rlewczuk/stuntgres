package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceDeleteUsingMigratedCore(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := updateFromRun(t, p, b, b.db)
		run(referenceExec("CREATE TABLE t(id INT NOT NULL,v INT,f BOOL)", 0))
		run(referenceExec("INSERT INTO t VALUES(1,10,TRUE),(2,NULL,NULL),(3,20,FALSE),(4,10,TRUE),(4,10,TRUE)", 5))
		var raw driver.Conn
		var saved driver.Rows
		if !b.pg {
			var err error
			raw, err = p.nativeConnector.Connect(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			saved, err = raw.(driver.QueryerContext).QueryContext(p.ctx, "SELECT id,v,f FROM t ORDER BY id", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer saved.Close()
			prepared, err := raw.(driver.ConnPrepareContext).PrepareContext(p.ctx, "DELETE FROM t AS d WHERE d.v=$1 OR d.v=$1")
			if err != nil {
				t.Fatal(err)
			}
			if prepared.NumInput() != 1 {
				t.Fatal(prepared.NumInput())
			}
			prepared.Close()
		}
		check := func(x referenceExecutor, want [][]any) {
			referenceRunStep(t, p.ctx, x, b.recorder, referenceSelect("SELECT id,v,f FROM t ORDER BY id", []string{"id", "v", "f"}, []string{"INT4", "INT4", "BOOL"}, want))
		}
		run(referenceExec("DELETE FROM t AS d WHERE d.v=10", 3))
		survivors := [][]any{{int64(2), nil, nil}, {int64(3), int64(20), false}}
		check(c, survivors)
		q := "DELETE FROM t d WHERE d.v=$1 OR d.v=$1"
		stmt, err := b.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: q}
		for _, arg := range []any{10, nil} {
			referenceRunStep(t, p.ctx, held, b.recorder, referenceExec(q, 0, arg))
		}
		tx, err := c.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		updateFromRun(t, p, b, tx)(referenceExec("DELETE FROM t WHERE id=2", 1))
		check(tx, [][]any{{int64(3), int64(20), false}})
		var own int64
		if err := tx.QueryRowContext(p.ctx, "SELECT id FROM t").Scan(&own); err != nil || own != 3 {
			t.Fatal(own, err)
		}
		check(b.db, survivors)
		var private bytes.Buffer
		if !b.pg {
			if err := stuntgres.Dump(p.ctx, p.native, &private); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		check(c, survivors)
		ctx, cancel := context.WithCancel(p.ctx)
		cancel()
		result, err := b.db.ExecContext(ctx, "DELETE FROM t")
		if result != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(result, err)
		}
		check(c, survivors)
		var dump bytes.Buffer
		if !b.pg {
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if dump.String() != private.String() || strings.Contains(dump.String(), "DELETE") || !strings.Contains(dump.String(), "NOT NULL") {
				t.Fatal(dump.String(), private.String())
			}
		}
		run(referenceExec("DELETE FROM t", 2))
		check(c, nil)
		run(referenceExec("DELETE FROM t", 0))
		if !b.pg {
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		} else {
			// Independently prescribed SQL state equivalent to restoring the saved survivors.
			run(referenceExec("INSERT INTO t VALUES(2,NULL,NULL),(3,20,FALSE)", 2))
		}
		check(c, survivors)
		if saved != nil {
			if !reflect.DeepEqual(saved.Columns(), []string{"id", "v", "f"}) {
				t.Fatal(saved.Columns())
			}
			for i, name := range []string{"INT4", "INT4", "BOOL"} {
				if saved.(driver.RowsColumnTypeDatabaseTypeName).ColumnTypeDatabaseTypeName(i) != name {
					t.Fatal(i)
				}
			}
			dst := make([]driver.Value, 3)
			for _, want := range [][]driver.Value{{int64(1), int64(10), true}, {int64(2), nil, nil}, {int64(3), int64(20), false}, {int64(4), int64(10), true}, {int64(4), int64(10), true}} {
				if err := saved.Next(dst); err != nil || !reflect.DeepEqual(dst, want) {
					t.Fatal(dst, err, want)
				}
			}
			if err := saved.Next(dst); err != io.EOF {
				t.Fatal(err)
			}
		}
		want := controlsReferenceDiagnostic("23502", `null value in column "id" of relation "t" violates not-null constraint`)
		want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (null, 1, t).", "public", "t", "id"
		run(referenceErrorDiagnostic("INSERT INTO t VALUES(NULL,1,TRUE)", want))
		check(c, survivors)
	})
}
