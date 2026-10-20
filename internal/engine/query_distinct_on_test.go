package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryDistinctOnSlotsMetadataAndFreshProviders(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT DISTINCT ON(v) id AS shown FROM t ORDER BY v,id DESC")
	defer source.Close()
	project := query.Root.(*plan.ProjectNode)
	node := project.Child.(*plan.DistinctOnNode)
	sorted := node.Child.(*plan.SortNode)
	if len(project.Schema()) != 1 || project.Schema()[0].OriginAttribute != 1 || project.Schema()[0].OriginTable == 0 || !project.Schema()[0].NullabilityKnown || project.Schema()[0].Nullable || len(node.Expressions) != 1 || len(sorted.Keys) != 2 {
		t.Fatal(project.Schema(), node, sorted)
	}
	fresh := map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		fresh[d.ID()] = d
	}
	root, err := remapViewQuery(New().CatalogRead(), query.Root, func(r ids.ObjectRef) ids.ObjectRef { return r }, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
	if err != nil {
		t.Fatal(err)
	}
	remapped := root.(*plan.ProjectNode).Child.(*plan.DistinctOnNode)
	v, _ := sqltype.Integer(fresh[sqltype.Int4], 1)
	if _, err := remapped.Grouping[0].Hash(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	old, _ := sqltype.Integer(node.Expressions[0].ResultUsage().Type, 1)
	if _, err := remapped.Grouping[0].Hash(context.Background(), old); err == nil {
		t.Fatal("obsolete key generation retained")
	}
}

func TestQueryDistinctOnRequiredOutputIdentity(t *testing.T) {
	for _, q := range []string{"SELECT DISTINCT ON(y) random() AS x,random() AS y FROM t ORDER BY y,id", "SELECT DISTINCT ON(1,2) random() AS x,random() AS y FROM t ORDER BY 1,2"} {
		_, query, source := queryCoreFixture(t, q)
		defer source.Close()
		p := query.Root.(*plan.ProjectNode)
		n := p.Child.(*plan.DistinctOnNode)
		sorted := n.Child.(*plan.SortNode)
		x, y := p.Expressions[0].(plan.CachedOrderExpr), p.Expressions[1].(plan.CachedOrderExpr)
		if len(n.Expressions) == 1 {
			if x.Slot != y.Slot || sorted.Keys[0].OutputOrdinal != 2 {
				t.Fatal("undemanded sharing lost", x, y, sorted.Keys)
			}
		} else if x.Slot == y.Slot || sorted.Keys[0].TargetIdentity == sorted.Keys[1].TargetIdentity {
			t.Fatal("required output IDs collapsed", x, y, sorted.Keys)
		}
	}
}

func TestQueryDistinctOnCollisionCandidates(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT DISTINCT ON(v) id FROM t")
	defer source.Close()
	node := query.Root.(*plan.ProjectNode).Child.(*plan.DistinctOnNode)
	typ := node.Expressions[0].ResultUsage().Type
	one, _ := sqltype.Integer(typ, 1)
	two, _ := sqltype.Integer(typ, 2)
	flat := []sqltype.Value{one, two}
	if index, err := exec.FindDistinctTuple(context.Background(), node.Grouping, []sqltype.Value{two}, flat, []int{0}); err != nil || index != -1 {
		t.Fatal("hash collision suppressed unequal ON key", index, err)
	}
	if index, err := exec.FindDistinctTuple(context.Background(), node.Grouping, []sqltype.Value{two}, flat, []int{0, 1}); err != nil || index != 1 {
		t.Fatal(index, err)
	}
	ctx := &aggregateCancelContext{Context: context.Background(), armed: true, at: 4}
	if _, err := exec.FindDistinctTuple(ctx, node.Grouping, []sqltype.Value{two}, flat, []int{0, 1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestQueryDistinctOnFailureCancellationAndPublication(t *testing.T) {
	for _, phase := range []string{"key", "late key", "hash", "prepublication"} {
		t.Run(phase, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT DISTINCT ON(v) id FROM t")
			project := query.Root.(*plan.ProjectNode)
			node := project.Child.(*plan.DistinctOnNode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := node.Expressions[0]
			calls := 0
			sentinel := errors.New("DISTINCT ON key failure")
			var execution context.Context = ctx
			if phase == "hash" {
				var text *sqltype.Descriptor
				for _, d := range sqltype.Builtins() {
					if d.ID() == sqltype.Text {
						text = d
					}
				}
				usage := text.Usage()
				node.Grouping[0].Grouping, _ = sqltype.BindGrouping(usage)
				armed := &aggregateCancelContext{Context: ctx, at: 4}
				execution = armed
				node.Expressions[0] = queryProbe(usage, func(*exec.Context) (sqltype.Value, error) {
					armed.armed = true
					return sqltype.String(text, strings.Repeat("x", 4096)), nil
				})
			} else {
				node.Expressions[0] = queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
					calls++
					if phase == "key" {
						cancel()
					}
					if phase == "late key" && calls == 4 {
						return sqltype.Value{}, sentinel
					}
					return original.Eval(c)
				})
				if phase == "prepublication" {
					expr := project.Expressions[0]
					published := 0
					project.Expressions[0] = queryProbe(expr.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
						published++
						if published == 3 {
							cancel()
						}
						return expr.Eval(c)
					})
				}
			}
			rows, count, err := exec.Finalize(execution, query.Root, source, nil)
			want := error(context.Canceled)
			if phase == "late key" {
				want = sentinel
			}
			if rows != nil || count != 0 || !errors.Is(err, want) || source.View != nil {
				t.Fatal(rows, count, err, want, source.View)
			}
		})
	}
}

func TestQueryDistinctOnInsertKeyFailureNoBatch(t *testing.T) {
	b, source := insertSelectEvaluationFixture(t, "SELECT DISTINCT ON(v) id FROM t")
	node := b.Query.Root.(*plan.ProjectNode).Child.(*plan.DistinctOnNode)
	original := node.Expressions[0]
	calls := 0
	sentinel := errors.New("late ON INSERT key")
	node.Expressions[0] = queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		calls++
		if calls == 3 {
			return sqltype.Value{}, sentinel
		}
		return original.Eval(c)
	})
	rows, err := evaluateInsertQuery(context.Background(), b, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
	if rows.values != nil || err != sentinel || source.View != nil || source.Anonymous != nil {
		t.Fatal(rows, err, source)
	}
}

func TestQueryDistinctOnLaterComponentDemand(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT DISTINCT ON(v,id) id FROM t")
	node := query.Root.(*plan.ProjectNode).Child.(*plan.DistinctOnNode)
	first := node.Expressions[0]
	node.Expressions[0] = queryProbe(first.ResultUsage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.Integer(first.ResultUsage().Type, 1) })
	second := node.Expressions[1]
	calls := 0
	sentinel := errors.New("later ON component")
	node.Expressions[1] = queryProbe(second.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		calls++
		if calls == 4 {
			return sqltype.Value{}, sentinel
		}
		return second.Eval(c)
	})
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if rows != nil || count != 0 || err != sentinel || calls != 4 || source.View != nil {
		t.Fatal(rows, count, err, calls, source.View)
	}
}
