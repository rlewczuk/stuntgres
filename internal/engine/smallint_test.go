package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestSmallintStagedCancellation(t *testing.T) {
	// The established three-row fixture gains one check per immutable INT4
	// to INT2 cast. After all rows are staged, constraints start at 19 and
	// the final publication check is 25 (the INT4 fixture uses 16 and 22).
	for _, at := range []int32{19, 20, 25} {
		e := New()
		run(t, e, "CREATE TABLE t(id SMALLINT NOT NULL,label TEXT,flag BOOL)")
		run(t, e, "INSERT INTO t VALUES(99,'sentinel',TRUE)")
		base, cancel := context.WithCancel(context.Background())
		op, cleanup, err := testOperation(e, base)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		ctx := &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: at}
		op.Ctx = ctx
		_, err = e.Execute(op, statement(t, "INSERT INTO t VALUES(1,'one'),(2,'two'),(3,'three')"), 0, nil)
		cleanup()
		cancel()
		if !errors.Is(err, context.Canceled) || ctx.checks.Load() != at {
			t.Fatal(at, err, ctx.checks.Load())
		}
		if got := tuples(run(t, e, "SELECT * FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(99), "sentinel", true}}) {
			t.Fatal(got)
		}
		run(t, e, "INSERT INTO t VALUES(4)")
		if got := tuples(run(t, e, "SELECT * FROM t ORDER BY id")); !reflect.DeepEqual(got, [][]driver.Value{{int64(4), nil, nil}, {int64(99), "sentinel", true}}) {
			t.Fatal(got)
		}
	}
}

func TestSmallintLiteralInference(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(s SMALLINT)")
	result := run(t, e, "SELECT 7,32768,2147483648 FROM t")
	defer result.Rows.Close()
	for i, id := range []sqltype.ID{sqltype.Int4, sqltype.Int4, sqltype.Int8} {
		if result.Rows.Schema()[i].Usage.Type.ID() != id {
			t.Fatal(result.Rows.Schema()[i])
		}
	}
}
