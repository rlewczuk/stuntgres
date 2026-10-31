package executor_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestCountExpressionArgumentVeto(t *testing.T) {
	for _, cancelArgument := range []bool{false, true} {
		query, source := enginetest.QueryFixture(t, engine.New(), "SELECT count(v) FROM t")
		node := query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
		original := node.Calls[0].Arguments[0]
		ctx, cancel := context.WithCancel(context.Background())
		sentinel := errors.New("count argument failed")
		calls := 0
		node.Calls[0].Arguments[0] = enginetest.QueryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
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
	query, source := enginetest.QueryFixture(t, engine.New(), "SELECT count(*),count(v),count(f),count(NULL),count(v) FROM t")
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
	node := b.Query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	original := node.Calls[0].Arguments[0]
	sentinel := errors.New("late INSERT count argument")
	calls := 0
	node.Calls[0].Arguments[0] = enginetest.QueryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		calls++
		if calls == 3 {
			return sqltype.Value{}, sentinel
		}
		return original.Eval(c)
	})
	rows, err := dml.EvaluateInsertQuery(context.Background(), b, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
	if rows.Values != nil || err != sentinel || calls != 3 || source.View != nil || source.Anonymous != nil {
		t.Fatal(rows, err, calls, source)
	}
}

func insertSelectEvaluationFixture(t *testing.T, sql string) (analyze.Binding, *exec.Source) {
	t.Helper()
	e := engine.New()
	q, source := enginetest.QueryFixture(t, e, sql)
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "t")
	e.Mu.Lock()
	relation := e.Catalog.State.MustRelation(ref.Address)
	e.Mu.Unlock()
	return analyze.Binding{Query: q, Relation: relation, Ref: ref, Targets: []int{0}}, source
}
