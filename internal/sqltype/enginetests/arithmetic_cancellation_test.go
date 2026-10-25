package enginetests_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestExpressionArithmeticCancellationBeforePublication(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT (v::smallint+1::smallint)*2::smallint FROM t WHERE id=1")
	project := query.Root.(*plan.ProjectNode)
	real := project.Expressions[0]
	schema := project.Schema()[0]
	if schema.Name != "?column?" || schema.Usage.Type.ID() != sqltype.Int2 || schema.NullabilityKnown || schema.OriginTable != 0 || schema.OriginAttribute != 0 {
		t.Fatal(schema)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	evaluated := false
	project.Expressions[0] = queryProbe(real.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		v, err := real.Eval(c)
		if err != nil {
			return v, err
		}
		n, err := v.Integer()
		if err != nil || n != 4 {
			t.Fatal(v, err)
		}
		evaluated = true
		cancel()
		return v, nil
	})
	rows, _, err := exec.Finalize(ctx, query.Root, source, nil)
	if !evaluated || rows != nil || !errors.Is(err, context.Canceled) || source.View != nil {
		t.Fatal(evaluated, rows, err)
	}
}

// Restore uses this same ReplaceCatalog installation fence after its isolated
// stage has executed. This semantic seam test does not pretend to interrupt a
// running Restore call; it proves cancellation after real arithmetic staging.
func TestExpressionArithmeticCanceledRestoreInstallationFence(t *testing.T) {
	source, target := engine.New(), engine.New()
	enginetest.Run(t, source, "CREATE TABLE t(i SMALLINT,b BIGINT)")
	enginetest.Run(t, source, "INSERT INTO t VALUES((7::smallint+1::smallint)*2::smallint,-7::smallint%2::bigint),(NULL,NULL)")
	enginetest.Run(t, target, "CREATE TABLE t(i SMALLINT,b BIGINT)")
	enginetest.Run(t, target, "INSERT INTO t VALUES(99,99)")
	held := enginetest.Run(t, target, "SELECT i+1::smallint,b*2::smallint FROM t").Rows
	defer held.Close()
	before := target.CatalogRead()
	staged := enginetest.Run(t, source, "SELECT i,b FROM t ORDER BY i")
	if !reflect.DeepEqual(enginetest.Tuples(staged), [][]driver.Value{{int64(16), int64(-1)}, {nil, nil}}) {
		t.Fatal("real arithmetic stage missing")
	}
	staged.Rows.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := target.ReplaceCatalog(ctx, source, source.CatalogRead())
	if !errors.Is(err, context.Canceled) || target.CatalogRead().Epoch() != before.Epoch() {
		t.Fatal(err)
	}
	retained := engine.Result{Rows: held}
	if !reflect.DeepEqual(enginetest.Tuples(retained), [][]driver.Value{{int64(100), int64(198)}}) {
		t.Fatal("retained computed rows changed")
	}
	current := enginetest.Run(t, target, "SELECT i,b FROM t")
	if !reflect.DeepEqual(enginetest.Tuples(current), [][]driver.Value{{int64(99), int64(99)}}) {
		t.Fatal("canceled installation published stage")
	}
	current.Rows.Close()
}

func TestExpressionArithmeticCanceledInsertFinish(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(i SMALLINT,b BIGINT)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(99,99)")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, done, err := enginetest.Operation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(enginetest.Statement(t, "INSERT INTO t VALUES((7::smallint+1::smallint)*2::smallint,-7::smallint%2::bigint)"), 0, nil)
	if err != nil || result.Count != 1 {
		_ = scope.Finish(err)
		t.Fatal(result, err)
	}
	// Arithmetic has executed and Writes.Apply has staged the tuple, but
	// StatementScope.Finish still owns the implicit publication fence.
	cancel()
	err = scope.Finish(nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	current := enginetest.Run(t, e, "SELECT i,b FROM t")
	if !reflect.DeepEqual(enginetest.Tuples(current), [][]driver.Value{{int64(99), int64(99)}}) {
		t.Fatal("canceled Finish published arithmetic tuple")
	}
	current.Rows.Close()
}

func TestExpressionArithmeticNullAndEmptyOrigins(t *testing.T) {
	for _, q := range []string{"SELECT v::smallint+1::smallint FROM t WHERE id=2", "SELECT v::smallint+1::smallint FROM t WHERE FALSE"} {
		_, query, source := queryCoreFixture(t, q)
		schema := query.Root.Schema()[0]
		if schema.Name != "?column?" || schema.Usage.Type.ID() != sqltype.Int2 || schema.NullabilityKnown || schema.OriginTable != 0 || schema.OriginAttribute != 0 {
			t.Fatal(q, schema)
		}
		rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
		if err != nil {
			t.Fatal(err)
		}
		rows.Close()
		expected := 1
		if q == "SELECT v::smallint+1::smallint FROM t WHERE FALSE" {
			expected = 0
		}
		if count != expected {
			t.Fatal(q, count)
		}
	}
}

func queryCoreFixture(t *testing.T, sql string) (*engine.Engine, *plan.Query, *exec.Source) {
	t.Helper()
	// Parsing may terminate the test; never do it while holding e.mu, since
	// deferred session teardown needs that mutex before WaitEnd can return.
	ast := enginetest.Statement(t, sql)
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(id INT NOT NULL,v INT,f BOOL)")
	enginetest.Run(t, e, "INSERT INTO t VALUES(1,1,TRUE),(2,NULL,NULL),(3,2,FALSE),(4,1,TRUE)")
	captured := enginetest.Run(t, e, "SELECT * FROM t").Rows.(*exec.FinalRows)
	source, captureErr := exec.CaptureSource(context.Background(), captured.Source.View, captured.Source.Ref)
	if captureErr != nil {
		t.Fatal(captureErr)
	}
	captured.Close()
	t.Cleanup(source.Close)
	op, done, err := enginetest.Operation(e, context.Background())
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
	b, err := engine.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	return e, b.Query, source
}

func queryProbe(usage sqltype.Usage, eval func(*exec.Context) (sqltype.Value, error)) expr.Expr {
	return queryProbeExpression{Info: expr.Info{Usage: usage, Pos: parser.Position{Offset: -1}}, eval: eval}
}

type queryProbeExpression struct {
	expr.Info
	eval func(*exec.Context) (sqltype.Value, error)
}

func (e queryProbeExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	return e.eval(evalContext(ctx))
}

// evalContext recovers the engine evaluation context in test probes. A nil or
// foreign context yields nil.
func evalContext(ctx expr.Context) *exec.Context {
	c, _ := ctx.(*exec.Context)
	return c
}
