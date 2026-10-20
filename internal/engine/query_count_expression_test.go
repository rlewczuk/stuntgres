package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCountExpressionArgumentVeto(t *testing.T) {
	for _, cancelArgument := range []bool{false, true} {
		_, query, source := queryCoreFixture(t, "SELECT count(v) FROM t")
		node := query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
		original := node.Calls[0].Argument
		ctx, cancel := context.WithCancel(context.Background())
		sentinel := errors.New("count argument failed")
		calls := 0
		node.Calls[0].Argument = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
			calls++
			if cancelArgument {
				cancel()
				return sqltype.Null(original.ResultUsage().Type), nil
			}
			return sqltype.Value{}, sentinel
		})
		rows, count, err := exec.Finalize(ctx, query.Root, source, nil)
		cancel()
		want := error(sentinel)
		if cancelArgument {
			want = context.Canceled
		}
		if rows != nil || count != 0 || err != want || calls != 1 || source.View != nil || source.Anonymous != nil {
			t.Fatal(rows, count, err, calls, source)
		}
	}
}

func TestCountExpressionSlotsAndMetadata(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT count(*),count(v),count(f),count(NULL),count(v) FROM t")
	node := query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	if len(node.Calls) != 4 {
		t.Fatal("distinct calls collapsed or immutable call not shared", len(node.Calls))
	}
	for _, column := range query.Root.Schema() {
		if column.Name != "count" || column.Usage.Type.ID() != sqltype.Int8 || column.Usage.Typmod != -1 || column.OriginTable != 0 || column.OriginAttribute != 0 || !column.NullabilityKnown || column.Nullable {
			t.Fatal(column)
		}
	}
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil || count != 1 || rows == nil {
		t.Fatal(rows, count, err)
	}
	defer rows.Close()
	values := make([]sqltype.Value, 5)
	present, err := rows.Next(context.Background(), values)
	if !present || err != nil {
		t.Fatal(present, err)
	}
	for i, want := range []int64{4, 3, 3, 0, 3} {
		got, _ := values[i].Integer()
		if got != want || values[i].IsNull() {
			t.Fatal(i, got, want)
		}
	}
}

func TestCountExpressionInsertArgumentFailure(t *testing.T) {
	b, source := insertSelectEvaluationFixture(t, "SELECT count(v) FROM t")
	node := b.query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	original := node.Calls[0].Argument
	sentinel := errors.New("late INSERT count argument")
	calls := 0
	node.Calls[0].Argument = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		calls++
		if calls == 3 {
			return sqltype.Value{}, sentinel
		}
		return original.Eval(c)
	})
	rows, err := evaluateInsertQuery(context.Background(), b, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
	if rows.values != nil || err != sentinel || calls != 3 || source.View != nil || source.Anonymous != nil {
		t.Fatal(rows, err, calls, source)
	}
}

func TestCountExpressionVolatileCalls(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT count(random()),count(random()) FROM t")
	node := query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	if len(node.Calls) != 2 {
		t.Fatal("volatile calls shared", len(node.Calls))
	}
	// The low-level source fixture has no execution scope. Random requires
	// the same real session/statement authority as ordinary query execution.
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	borrowQueryAuthority(source, scope)
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil || count != 1 || rows == nil {
		t.Fatal(rows, count, err)
	}
	defer rows.Close()
	values := make([]sqltype.Value, 2)
	present, err := rows.Next(context.Background(), values)
	if !present || err != nil {
		t.Fatal(present, err)
	}
	for _, value := range values {
		n, _ := value.Integer()
		if n != 4 {
			t.Fatal(n)
		}
	}
}
