package functions_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func TestQuerySubquerySortedProjectionCacheOnce(t *testing.T) {
	_, q, source := queryJoinsFixture(t, "SELECT abs(k) FROM a ORDER BY a.k")
	project := q.Root.(*plan.ProjectNode)
	calls := 0
	subqueryTestTarget(t, project, enginetest.QueryProbe(project.Schema()[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
		calls++
		return sqltype.Integer(project.Schema()[0].Usage.Type, int64(calls))
	}))
	state := exec.State{Source: source}
	stage, err := exec.OpenQueryProjectStage(context.Background(), &state, project)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	for range 2 {
		present, err := stage.NextProjected(context.Background(), nil)
		if err != nil || !present || calls != 4 {
			t.Fatal("sort input projection was skipped or reevaluated", present, err, calls)
		}
	}
}

func queryJoinsFixture(t *testing.T, q string) (*core.Engine, *plan.Query, *exec.Source) {
	t.Helper()
	e, query, source, _ := queryJoinsAdmittedFixture(t, q, false)
	return e, query, source
}

// Replace an actual scheduler-owned target rather than mocking a query result.
func subqueryTestTarget(t *testing.T, project *plan.ProjectNode, replacement expr.Expr) {
	t.Helper()
	for node := project.Child; node != nil; {
		switch n := node.(type) {
		case *plan.EvaluationNode:
			if len(n.Expressions) > 0 {
				n.Expressions[0] = replacement
				return
			}
		case *plan.SortNode:
			if len(n.Outputs) > 0 {
				n.Outputs[0] = replacement
				return
			}
		}
		left, right := plan.QueryInputs(node)
		if right != nil {
			t.Fatal("target scheduler crossed a join")
		}
		node = left
	}
	if len(project.Expressions) == 0 {
		t.Fatal("missing target")
	}
	project.Expressions[0] = replacement
}

// Explicit admission allows immutable captured rows to outlive catalog replacement
// without bypassing the replacement reservation held by implicit statements.
func queryJoinsAdmittedFixture(t *testing.T, q string, explicit bool) (*core.Engine, *plan.Query, *exec.Source, func()) {
	t.Helper()
	ast := enginetest.Statement(t, q)
	e := engine.New()
	for _, sql := range []string{"CREATE TABLE a(l TEXT,k INT,q INT)", "CREATE TABLE b(r TEXT,q INT,k INT)", "CREATE TABLE c(k INT,c TEXT)", "CREATE TABLE wide(k BIGINT)", "CREATE TABLE chars(k VARCHAR(3))", "CREATE TABLE longer(k VARCHAR(5))", "CREATE TABLE same(k VARCHAR(3))", "CREATE TABLE unused(v INT)", "INSERT INTO a VALUES('a',1,9),('a',1,9),('n',NULL,9),('z',2,8)", "INSERT INTO b VALUES('x',9,1),('x',9,1),('y',8,3),('n',9,NULL)", "INSERT INTO c VALUES(1,'c'),(1,'c')", "INSERT INTO wide VALUES(1)", "INSERT INTO chars VALUES('x')", "INSERT INTO longer VALUES('x')", "INSERT INTO same VALUES('x')"} {
		enginetest.Run(t, e, sql)
	}
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var scope *core.StatementScope
	var source *exec.Source
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		if source != nil {
			source.Close()
		}
		if scope != nil && !scope.Finished {
			_ = scope.Finish(context.Canceled)
		}
		done()
	}
	t.Cleanup(release)
	if explicit {
		if _, err = e.Begin(op, session.TxOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	scope, err = e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = scope.Describe(ast); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	var b analyze.Binding
	if err == nil {
		b, err = core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, scope.LockedRef, scope.Input, nil, &scope.QueryLockAdmission)
	}
	if err == nil {
		// Standalone stages supply contexts, but still need live admission authority.
		source, err = e.CaptureQuerySourcesForInvokerLocked(scope, b.QuerySources, false, authorization.PrincipalRef{}, plan.RowMark{}, scope.QueryLockContext)
	}
	e.Mu.Unlock()
	if err == nil {
		err = exec.SelectQuerySources(op.Ctx, source)
	}
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	return e, b.Query, source, release
}
