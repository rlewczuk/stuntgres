package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestExpressionFloatingArithmeticCancellationBeforePublication(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT (v::real+1)^2 FROM t WHERE id=1")
	project := query.Root.(*projectNode)
	real := project.expressions[0]
	schema := project.Schema()[0]
	if schema.Name != "?column?" || schema.Usage.Type.ID() != sqltype.Float8 || schema.NullabilityKnown || schema.OriginTable != 0 || schema.OriginAttribute != 0 {
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
		n, err := v.Float()
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
func TestExpressionFloatingArithmeticCanceledRestoreInstallationFence(t *testing.T) {
	source, target := New(), New()
	run(t, source, "CREATE TABLE t(i REAL,b DOUBLE PRECISION)")
	run(t, source, "INSERT INTO t VALUES(('7'::real+'1'::real)*'2'::real,(-2)^3),(NULL,NULL)")
	run(t, target, "CREATE TABLE t(i REAL,b DOUBLE PRECISION)")
	run(t, target, "INSERT INTO t VALUES(99,99)")
	held := run(t, target, "SELECT i+1,b*2 FROM t").Rows
	defer held.Close()
	metadata := append([]ColumnDescriptor(nil), held.Schema()...)
	before := target.CatalogRead()
	staged := run(t, source, "SELECT i,b FROM t ORDER BY i")
	assertFloatingArithmeticRows(t, staged.Rows, [][]any{{float64(16), float64(-8)}, {nil, nil}})
	staged.Rows.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := target.ReplaceCatalog(ctx, source.CatalogRead())
	if !errors.Is(err, context.Canceled) || target.CatalogRead().Epoch() != before.Epoch() {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(metadata, held.Schema()) {
		t.Fatal("canceled installation changed retained metadata")
	}
	for _, column := range metadata {
		if column.Usage.Type.ID() != sqltype.Float8 || column.NullabilityKnown || column.OriginTable != 0 || column.OriginAttribute != 0 {
			t.Fatal(column)
		}
	}
	assertFloatingArithmeticRows(t, held, [][]any{{float64(100), float64(198)}})
	current := run(t, target, "SELECT i,b FROM t")
	assertFloatingArithmeticRows(t, current.Rows, [][]any{{float64(99), float64(99)}})
	current.Rows.Close()
}

func TestExpressionFloatingArithmeticCanceledInsertFinish(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE t(i REAL,b DOUBLE PRECISION)")
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
	result, err := scope.Execute(statement(t, "INSERT INTO t VALUES(('7'::real+'1'::real)*'2'::real,(-2)^3)"), 0, nil)
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
	assertFloatingArithmeticRows(t, current.Rows, [][]any{{float64(99), float64(99)}})
	current.Rows.Close()
}

// Keep these seam witnesses in native typed values: the unrelated legacy
// testExport helper intentionally covers only its original primitive types.
func assertFloatingArithmeticRows(t *testing.T, rows RowResult, expected [][]any) {
	t.Helper()
	if len(rows.Schema()) != 2 {
		t.Fatal("floating row width", rows.Schema())
	}
	var values [2]sqltype.Value
	for _, want := range expected {
		next, err := rows.Next(context.Background(), values[:])
		if err != nil || !next {
			t.Fatal("missing floating row", err)
		}
		for i, value := range values {
			if value.Type() != rows.Schema()[i].Usage.Type {
				t.Fatal("floating descriptor changed", value)
			}
			if want[i] == nil {
				if !value.IsNull() {
					t.Fatal("expected typed NULL", value)
				}
				continue
			}
			got, err := value.Float()
			if err != nil || got != want[i].(float64) {
				t.Fatal("floating value", got, want[i], err)
			}
		}
	}
	next, err := rows.Next(context.Background(), values[:])
	if err != nil || next {
		t.Fatal("unexpected floating row", next, err)
	}
}
