package core_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

// Borrow the real fired map only when the demanded CTE's immediate host entry
// observes the main modifier's earlier event. No simulated host or SQL results.
type beforeStatementCTEProbe struct {
	context.Context
	scope     *core.StatementScope
	auxiliary map[core.BeforeStatementTestKey]struct{}
}

func (p *beforeStatementCTEProbe) Err() error {
	var pcs [64]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	before, cte := false, false
	for {
		frame, more := frames.Next()
		before = before || strings.HasSuffix(frame.Function, ".(*statementHost).BeforeStatement")
		cte = cte || strings.Contains(frame.Function, ".ExecutePreparedCTECommandsLocked.func")
		if !more {
			break
		}
	}
	if before && cte && p.scope != nil {
		if fired := core.ScopeBeforeTestMap(p.scope); len(fired) != 0 {
			p.auxiliary = fired
		}
	}
	return p.Context.Err()
}
func beforeStatementCTEOwner(t *testing.T) {
	e := engine.New()
	owner := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE bst_cte_owner(id integer,a integer,b integer)",
		"INSERT INTO bst_cte_owner VALUES(1,10,20),(2,30,40)",
		"CREATE FUNCTION bst_cte_unreachable() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM 1/0; RETURN NULL; END$$",
		"CREATE TRIGGER a BEFORE UPDATE OF a ON bst_cte_owner EXECUTE FUNCTION bst_cte_unreachable()",
	} {
		enginetest.SessionSQL(t, owner, q)
	}
	op, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(errors.New("owner proof veto"))
	probe := &beforeStatementCTEProbe{Context: op.Ctx, scope: scope}
	op.Ctx = probe
	out, err := scope.Execute(enginetest.Statement(t, "WITH x AS (UPDATE bst_cte_owner SET a=a+1 WHERE id=1 RETURNING id) UPDATE bst_cte_owner SET b=b+1 WHERE id IN(SELECT id+1 FROM x)"), 0, nil)
	if err != nil || out.Count != 1 || out.Rows != nil {
		t.Fatal("actual CTE mutation did not execute both producers under the main lexical target", out, err)
	}
	root := core.ScopeBeforeTestMap(scope)
	if len(root) != 1 || len(probe.auxiliary) != 1 || len(scope.BeforeCaptures) != 0 {
		t.Fatal("CTE did not borrow main query-owned statement state")
	}
	for key := range probe.auxiliary {
		delete(probe.auxiliary, key)
	}
	if len(root) != 0 {
		t.Fatal("CTE lexical mutation retained copied fired backing")
	}
}
