package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestExpressionArithmeticCancellationBeforePublication(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT (v+1)*2 FROM t WHERE id=1")
	project := query.Root.(*projectNode)
	real := project.expressions[0]
	schema := project.Schema()[0]
	if schema.Name != "?column?" || schema.NullabilityKnown || schema.OriginTable != 0 || schema.OriginAttribute != 0 {
		t.Fatal(schema)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	evaluated := false
	project.expressions[0] = queryProbe(real.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
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
	rows, _, err := finalizeQuery(ctx, query.Root, source, nil)
	if !evaluated || rows != nil || !errors.Is(err, context.Canceled) || source.view != nil {
		t.Fatal(evaluated, rows, err)
	}
}

// Restore uses this same ReplaceCatalog installation fence after its isolated
// stage has executed. This semantic seam test does not pretend to interrupt a
// running Restore call; it proves cancellation after real arithmetic staging.
func TestExpressionArithmeticCanceledRestoreInstallationFence(t *testing.T) {
	source, target := New(), New()
	run(t, source, "CREATE TABLE t(i INT,b BIGINT)")
	run(t, source, "INSERT INTO t VALUES((7+1)*2,-7%2),(NULL,NULL)")
	run(t, target, "CREATE TABLE t(i INT,b BIGINT)")
	run(t, target, "INSERT INTO t VALUES(99,99)")
	held := run(t, target, "SELECT i+1,b*2 FROM t").Rows
	defer held.Close()
	before := target.CatalogRead()
	staged := run(t, source, "SELECT i,b FROM t ORDER BY i")
	if !reflect.DeepEqual(tuples(staged), [][]driver.Value{{int64(16), int64(-1)}, {nil, nil}}) {
		t.Fatal("real arithmetic stage missing")
	}
	staged.Rows.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := target.ReplaceCatalog(ctx, source.CatalogRead())
	if !errors.Is(err, context.Canceled) || target.CatalogRead().Epoch() != before.Epoch() {
		t.Fatal(err)
	}
	retained := Result{Rows: held}
	if !reflect.DeepEqual(tuples(retained), [][]driver.Value{{int64(100), int64(198)}}) {
		t.Fatal("retained computed rows changed")
	}
	current := run(t, target, "SELECT i,b FROM t")
	if !reflect.DeepEqual(tuples(current), [][]driver.Value{{int64(99), int64(99)}}) {
		t.Fatal("canceled installation published stage")
	}
	current.Rows.Close()
}

func TestExpressionArithmeticCanceledInsertFinish(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(i INT,b BIGINT)")
	run(t, e, "INSERT INTO t VALUES(99,99)")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, done, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	result, err := scope.Execute(statement(t, "INSERT INTO t VALUES((7+1)*2,-7%2)"), 0, nil)
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
	current := run(t, e, "SELECT i,b FROM t")
	if !reflect.DeepEqual(tuples(current), [][]driver.Value{{int64(99), int64(99)}}) {
		t.Fatal("canceled Finish published arithmetic tuple")
	}
	current.Rows.Close()
}

func TestExpressionArithmeticNullAndEmptyOrigins(t *testing.T) {
	for _, q := range []string{"SELECT v+1 FROM t WHERE id=2", "SELECT v+1 FROM t WHERE FALSE"} {
		_, query, source := queryCoreFixture(t, q)
		schema := query.Root.Schema()[0]
		if schema.Name != "?column?" || schema.Usage.Type.ID() != sqltype.Int4 || schema.NullabilityKnown || schema.OriginTable != 0 || schema.OriginAttribute != 0 {
			t.Fatal(q, schema)
		}
		rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
		if err != nil {
			t.Fatal(err)
		}
		rows.Close()
		expected := 1
		if q == "SELECT v+1 FROM t WHERE FALSE" {
			expected = 0
		}
		if count != expected {
			t.Fatal(q, count)
		}
	}
}
