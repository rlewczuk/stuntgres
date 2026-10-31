package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/sqltype"
)

func TestCheckCancellationBeforePublication(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	peer := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, peer, "CREATE TABLE t(v INT CHECK(v>0))")
	enginetest.SessionSQL(t, peer, "INSERT INTO t VALUES(1),(2)")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, scope, b, source := updateBoundSource(t, e, s, ctx, "UPDATE t SET v=v")
	calls := 0
	b.Update.Assignments[0] = enginetest.QueryProbe(b.Relation.Columns[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
		calls++
		if calls == 2 {
			cancel()
			return sqltype.Value{}, ctx.Err()
		}
		return sqltype.Integer(c.Row[0].Type(), 9)
	})
	r, err := core.ExecuteUpdate(ctx, scope, b, source, nil, nil, "public")
	if !errors.Is(err, context.Canceled) || r.Count != 0 || r.Rows != nil || calls != 2 {
		scope.Finish(err)
		op.Done()
		t.Fatal(r, err, calls)
	}
	scope.Finish(err)
	op.Done()
	enginetest.TransactionRows(t, peer, "SELECT v FROM t ORDER BY v", 1, 2)
	enginetest.LockEmpty(t, e)
	enginetest.SessionSQL(t, peer, "UPDATE t SET v=7")
	enginetest.LockEmpty(t, e)
}
