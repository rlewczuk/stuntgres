package enginetest

import (
	"context"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// QueryFixture creates table t(id INT NOT NULL, v INT, f BOOL) with four rows
// on e and binds sql, a SELECT over one table, through core.Describe. It
// returns the bound query and a captured source of t whose planner costs
// request the real parallel worker alternative; the source closes at test
// cleanup.
func QueryFixture(t testing.TB, e *core.Engine, sql string) (*plan.Query, *exec.Source) {
	t.Helper()
	// Parsing may terminate the test; never do it while holding e.mu, since
	// deferred session teardown needs that mutex before WaitEnd can return.
	ast := Statement(t, sql)
	Run(t, e, "CREATE TABLE t(id INT NOT NULL,v INT,f BOOL)")
	Run(t, e, "INSERT INTO t VALUES(1,1,TRUE),(2,NULL,NULL),(3,2,FALSE),(4,1,TRUE)")
	captured := Run(t, e, "SELECT * FROM t").Rows.(*exec.FinalRows)
	source, captureErr := exec.CaptureSource(context.Background(), captured.Source.View, captured.Source.Ref)
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	captured.Close()
	// This low-level fixture explicitly requests the real worker alternative.
	source.PlannerCosts = plan.DefaultPlannerCosts()
	source.PlannerCosts.ParallelSetup, source.PlannerCosts.ParallelTuple, source.PlannerCosts.MinParallelTableBlocks = 0, 0, 0
	source.PlannerRows, source.PlannerColumns = 1000, 3
	t.Cleanup(source.Close)
	op, done, err := Operation(e, context.Background())
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
	return b.Query, source
}
