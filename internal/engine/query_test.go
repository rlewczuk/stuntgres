package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func queryCoreFixture(t *testing.T, sql string) (*core.Engine, *plan.Query, *exec.Source) {
	t.Helper()
	// Parsing may terminate the test; never do it while holding e.mu, since
	// deferred session teardown needs that mutex before WaitEnd can return.
	ast := statement(t, sql)
	e := New()
	run(t, e, "CREATE TABLE t(id INT NOT NULL,v INT,f BOOL)")
	run(t, e, "INSERT INTO t VALUES(1,1,TRUE),(2,NULL,NULL),(3,2,FALSE),(4,1,TRUE)")
	captured := run(t, e, "SELECT * FROM t").Rows.(*exec.FinalRows)
	source, captureErr := exec.CaptureSource(context.Background(), captured.Source.View, captured.Source.Ref)
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	captured.Close()
	t.Cleanup(source.Close)
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	from := ast.(*parser.Select).From
	if alias, ok := from.(*parser.AliasReference); ok {
		from = alias.Input
	}
	ref, err := e.ResolveReference(auth, from.(*parser.TableReference).Name)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	b, err := core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return e, b.Query, source
}

type queryProbeExpression struct {
	expr.Info
	eval func(*exec.Context) (sqltype.Value, error)
}

func (e queryProbeExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	return e.eval(evalContext(ctx))
}

func queryProbe(usage sqltype.Usage, eval func(*exec.Context) (sqltype.Value, error)) expr.Expr {
	return queryProbeExpression{Info: expr.Info{Usage: usage, Pos: parser.Position{Offset: -1}}, eval: eval}
}
