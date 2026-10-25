package core_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/sqltype"
)

func TestSmallintStagedCancellation(t *testing.T) {
	// The established three-row fixture gains one check per immutable INT4
	// to INT2 cast. After all rows are staged, constraints start at 19 and
	// the final publication check is 25 (the INT4 fixture uses 16 and 22).
	for _, at := range []int32{19, 20, 25} {
		e := engine.New()
		enginetest.Run(t, e, "CREATE TABLE t(id SMALLINT NOT NULL,label TEXT,flag BOOL)")
		enginetest.Run(t, e, "INSERT INTO t VALUES(99,'sentinel',TRUE)")
		base, cancel := context.WithCancel(context.Background())
		op, cleanup, err := enginetest.Operation(e, base)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		ctx := &cancelAtErrCheck{Context: op.Context(), cancel: cancel, at: at}
		op.Ctx = ctx
		_, err = e.Execute(op, enginetest.Statement(t, "INSERT INTO t VALUES(1,'one'),(2,'two'),(3,'three')"), 0, nil)
		cleanup()
		cancel()
		if !errors.Is(err, context.Canceled) || ctx.checks.Load() != at {
			t.Fatal(at, err, ctx.checks.Load())
		}
		if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(99), "sentinel", true}}) {
			t.Fatal(got)
		}
		enginetest.Run(t, e, "INSERT INTO t VALUES(4)")
		if got := enginetest.Tuples(enginetest.Run(t, e, "SELECT * FROM t ORDER BY id")); !reflect.DeepEqual(got, [][]driver.Value{{int64(4), nil, nil}, {int64(99), "sentinel", true}}) {
			t.Fatal(got)
		}
	}
}

func TestSmallintLiteralInference(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(s SMALLINT)")
	result := enginetest.Run(t, e, "SELECT 7,32768,2147483648 FROM t")
	defer result.Rows.Close()
	for i, id := range []sqltype.ID{sqltype.Int4, sqltype.Int4, sqltype.Int8} {
		if result.Rows.Schema()[i].Usage.Type.ID() != id {
			t.Fatal(result.Rows.Schema()[i])
		}
	}
}

// cancelAtErrCheck drives existing cooperative cancellation boundaries without
// sleeps or production hooks. Its embedded real context owns Done and Err state.
type cancelAtErrCheck struct {
	context.Context
	cancel context.CancelFunc
	at     int32
	checks atomic.Int32
}

func (c *cancelAtErrCheck) Err() error {
	if c.checks.Add(1) == c.at {
		c.cancel()
	}
	return c.Context.Err()
}
