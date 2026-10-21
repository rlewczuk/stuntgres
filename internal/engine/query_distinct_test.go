package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestQueryDistinctProjectedSlotsAndMetadata(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT DISTINCT v AS shown FROM t ORDER BY shown,shown DESC")
	project := query.Root.(*plan.ProjectNode)
	sort := project.Child.(*plan.SortNode)
	node := sort.Child.(*plan.DistinctNode)
	if len(node.Expressions) != 1 || len(node.Grouping) != 1 || project.Schema()[0].OriginTable == 0 || project.Schema()[0].OriginAttribute != 2 || !project.Schema()[0].Nullable || !project.Schema()[0].NullabilityKnown {
		t.Fatal(project.Schema(), node)
	}
	for _, key := range sort.Keys {
		if key.Slot != 0 || key.Cache != -1 {
			t.Fatal("ORDER must read projected slot", key)
		}
	}
	original := node.Expressions[0]
	calls := 0
	node.Expressions[0] = queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { calls++; return original.Eval(c) })
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil || count != 3 || calls != 4 || source.View != nil {
		t.Fatal(rows, count, calls, err, source.View)
	}
	defer rows.Close()
	scratch := make([]sqltype.Value, 1)
	for _, want := range []int64{1, 2} {
		present, err := rows.Next(context.Background(), scratch)
		value, _ := scratch[0].Integer()
		if err != nil || !present || value != want {
			t.Fatal(present, value, err)
		}
	}
	present, err := rows.Next(context.Background(), scratch)
	if err != nil || !present || !scratch[0].IsNull() {
		t.Fatal(present, scratch, err)
	}
}

func TestQueryDistinctCollisionCandidates(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT DISTINCT v FROM t")
	defer source.Close()
	node := query.Root.(*plan.ProjectNode).Child.(*plan.DistinctNode)
	d := node.Schema()[0].Usage.Type
	one, _ := sqltype.Integer(d, 1)
	two, _ := sqltype.Integer(d, 2)
	flat := []sqltype.Value{one, two}
	if index, err := exec.FindDistinctTuple(context.Background(), node.Grouping, []sqltype.Value{two}, flat, []int{0}); err != nil || index != -1 {
		t.Fatal("collision suppressed unequal tuple", index, err)
	}
	if index, err := exec.FindDistinctTuple(context.Background(), node.Grouping, []sqltype.Value{two}, flat, []int{0, 1}); err != nil || index != 1 {
		t.Fatal(index, err)
	}
	ctx := &aggregateCancelContext{Context: context.Background(), armed: true, at: 4}
	if _, err := exec.FindDistinctTuple(ctx, node.Grouping, []sqltype.Value{two}, flat, []int{0, 1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestQueryDistinctFailureAndCancellationFrontiers(t *testing.T) {
	for _, phase := range []string{"projection", "late failure", "prepublication", "hash"} {
		t.Run(phase, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT DISTINCT v FROM t")
			project := query.Root.(*plan.ProjectNode)
			node := project.Child.(*plan.DistinctNode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := node.Expressions[0]
			calls := 0
			sentinel := errors.New("late distinct projection")
			var evaluationContext context.Context = ctx
			if phase == "hash" {
				var text *sqltype.Descriptor
				for _, d := range sqltype.Builtins() {
					if d.ID() == sqltype.Text {
						text = d
					}
				}
				usage := text.Usage()
				node.QuerySchema[0].Usage = usage
				node.Grouping[0].Grouping, _ = sqltype.BindGrouping(usage)
				armed := &aggregateCancelContext{Context: ctx, at: 4}
				evaluationContext = armed
				node.Expressions[0] = queryProbe(usage, func(*exec.Context) (sqltype.Value, error) {
					armed.armed = true
					return sqltype.String(text, strings.Repeat("x", 4096)), nil
				})
			} else {
				node.Expressions[0] = queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
					calls++
					if phase == "projection" {
						cancel()
					}
					if phase == "late failure" && calls == 4 {
						return sqltype.Value{}, sentinel
					}
					return original.Eval(c)
				})
				if phase == "prepublication" {
					slot := project.Expressions[0]
					published := 0
					project.Expressions[0] = queryProbe(slot.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
						published++
						if published == 3 {
							cancel()
						}
						return slot.Eval(c)
					})
				}
			}
			rows, count, err := exec.Finalize(evaluationContext, query.Root, source, nil)
			want := error(context.Canceled)
			if phase == "late failure" {
				want = sentinel
			}
			if rows != nil || count != 0 || !errors.Is(err, want) || source.View != nil {
				t.Fatal(rows, count, err, want, source.View)
			}
		})
	}
}

func TestQueryDistinctInsertNoBatchOnLateProjectionFailure(t *testing.T) {
	b, source := insertSelectEvaluationFixture(t, "SELECT DISTINCT v FROM t")
	node := b.Query.Root.(*plan.ProjectNode).Child.(*plan.DistinctNode)
	original := node.Expressions[0]
	calls := 0
	sentinel := errors.New("late distinct INSERT source")
	node.Expressions[0] = queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		calls++
		if calls == 3 {
			return sqltype.Value{}, sentinel
		}
		return original.Eval(c)
	})
	rows, err := evaluateInsertQuery(context.Background(), b, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
	if rows.Values != nil || err != sentinel || source.View != nil || source.Anonymous != nil {
		t.Fatal(rows, err, source)
	}
}
