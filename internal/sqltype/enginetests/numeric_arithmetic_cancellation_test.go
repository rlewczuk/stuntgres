package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestExpressionNumericArithmeticCancellationBeforePublication(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT 0.1+0.2 FROM t WHERE id=1")
	project := query.Root.(*plan.ProjectNode)
	real := project.Expressions[0]
	schema := project.Schema()[0]
	if schema.Name != "?column?" || schema.Usage.Type.ID() != sqltype.Numeric || schema.Usage.Typmod != -1 || schema.NullabilityKnown || schema.OriginTable != 0 || schema.OriginAttribute != 0 {
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
		text, err := v.Type().Output(context.Background(), v)
		if err != nil || text != "0.3" {
			t.Fatal(text, err)
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

func TestExpressionNumericArithmeticCanceledInsertFinish(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE t(n NUMERIC,b NUMERIC)")
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
	result, err := scope.Execute(enginetest.Statement(t, "INSERT INTO t VALUES(0.1+0.2,9223372036854775808+1)"), 0, nil)
	if err != nil || result.Count != 1 {
		_ = scope.Finish(err)
		t.Fatal(result, err)
	}
	// Real exact numeric values are staged; Finish alone owns publication.
	cancel()
	err = scope.Finish(nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	current := enginetest.Run(t, e, "SELECT n,b FROM t")
	defer current.Rows.Close()
	assertNumericArithmeticRows(t, current.Rows, [][]any{{"99", "99"}})
}

func TestExpressionNumericArithmeticCanceledRestoreInstallationFence(t *testing.T) {
	source, target := engine.New(), engine.New()
	enginetest.Run(t, source, "CREATE TABLE t(n NUMERIC,b NUMERIC)")
	enginetest.Run(t, source, "INSERT INTO t VALUES(0.1+0.2,9223372036854775808+1),(NULL,NULL)")
	enginetest.Run(t, target, "CREATE TABLE t(n NUMERIC,b NUMERIC)")
	enginetest.Run(t, target, "INSERT INTO t VALUES(99,99)")
	held := enginetest.Run(t, target, "SELECT n+0.1,b*2 FROM t").Rows
	defer held.Close()
	metadata := append([]catalog.Column(nil), held.Schema()...)
	before := target.CatalogRead()
	staged := enginetest.Run(t, source, "SELECT n,b FROM t ORDER BY n")
	assertNumericArithmeticRows(t, staged.Rows, [][]any{{"0.3", "9223372036854775809"}, {nil, nil}})
	staged.Rows.Close()
	// Restore's real isolated catalog reaches this same installation fence. This
	// does not claim to interrupt an in-flight Restore or indivisible big.Int op.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := target.ReplaceCatalog(ctx, source, source.CatalogRead())
	if !errors.Is(err, context.Canceled) || target.CatalogRead().Epoch() != before.Epoch() {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(metadata, held.Schema()) {
		t.Fatal("retained schema changed")
	}
	for _, column := range metadata {
		if column.Usage.Type.ID() != sqltype.Numeric || column.Usage.Typmod != -1 || column.NullabilityKnown || column.OriginTable != 0 || column.OriginAttribute != 0 {
			t.Fatal(column)
		}
	}
	assertNumericArithmeticRows(t, held, [][]any{{"99.1", "198"}})
	current := enginetest.Run(t, target, "SELECT n,b FROM t")
	defer current.Rows.Close()
	assertNumericArithmeticRows(t, current.Rows, [][]any{{"99", "99"}})
}

func assertNumericArithmeticRows(t *testing.T, rows exec.RowResult, expected [][]any) {
	t.Helper()
	if len(rows.Schema()) != 2 {
		t.Fatal(rows.Schema())
	}
	var values [2]sqltype.Value
	for _, want := range expected {
		next, err := rows.Next(context.Background(), values[:])
		if err != nil || !next {
			t.Fatal(next, err)
		}
		for i, v := range values {
			if v.Type() != rows.Schema()[i].Usage.Type || v.Type().ID() != sqltype.Numeric {
				t.Fatal("numeric descriptor ownership")
			}
			if want[i] == nil {
				if !v.IsNull() {
					t.Fatal("expected typed NULL")
				}
				continue
			}
			if v.IsNull() {
				t.Fatal("unexpected NULL")
			}
			text, err := v.Type().Output(context.Background(), v)
			if err != nil || text != want[i].(string) {
				t.Fatal(text, want[i], err)
			}
		}
	}
	next, err := rows.Next(context.Background(), values[:])
	if err != nil || next {
		t.Fatal(next, err)
	}
}
