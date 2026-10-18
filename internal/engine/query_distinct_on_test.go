package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryDistinctOnSlotsMetadataAndFreshProviders(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT DISTINCT ON(v) id AS shown FROM t ORDER BY v,id DESC")
	defer source.close()
	project := query.Root.(*projectNode)
	node := project.child.(*distinctOnNode)
	sorted := node.child.(*sortNode)
	if len(project.Schema()) != 1 || project.Schema()[0].OriginAttribute != 1 || project.Schema()[0].OriginTable == 0 || !project.Schema()[0].NullabilityKnown || project.Schema()[0].Nullable || len(node.expressions) != 1 || len(sorted.keys) != 2 {
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
	remapped := root.(*projectNode).child.(*distinctOnNode)
	v, _ := sqltype.Integer(fresh[sqltype.Int4], 1)
	if _, err := remapped.grouping[0].Hash(context.Background(), v); err != nil {
		t.Fatal(err)
	}
	old, _ := sqltype.Integer(node.expressions[0].ResultUsage().Type, 1)
	if _, err := remapped.grouping[0].Hash(context.Background(), old); err == nil {
		t.Fatal("obsolete key generation retained")
	}
}

func TestQueryDistinctOnRequiredOutputIdentity(t *testing.T) {
	for _, q := range []string{"SELECT DISTINCT ON(y) random() AS x,random() AS y FROM t ORDER BY y,id", "SELECT DISTINCT ON(1,2) random() AS x,random() AS y FROM t ORDER BY 1,2"} {
		_, query, source := queryCoreFixture(t, q)
		defer source.close()
		p := query.Root.(*projectNode)
		n := p.child.(*distinctOnNode)
		sorted := n.child.(*sortNode)
		x, y := p.expressions[0].(cachedOrderExpression), p.expressions[1].(cachedOrderExpression)
		if len(n.expressions) == 1 {
			if x.slot != y.slot || sorted.keys[0].outputOrdinal != 2 {
				t.Fatal("undemanded sharing lost", x, y, sorted.keys)
			}
		} else if x.slot == y.slot || sorted.keys[0].targetIdentity == sorted.keys[1].targetIdentity {
			t.Fatal("required output IDs collapsed", x, y, sorted.keys)
		}
	}
}

func TestQueryDistinctOnCollisionCandidates(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT DISTINCT ON(v) id FROM t")
	defer source.close()
	node := query.Root.(*projectNode).child.(*distinctOnNode)
	typ := node.expressions[0].ResultUsage().Type
	one, _ := sqltype.Integer(typ, 1)
	two, _ := sqltype.Integer(typ, 2)
	flat := []sqltype.Value{one, two}
	if index, err := findDistinctTuple(context.Background(), node.grouping, []sqltype.Value{two}, flat, []int{0}); err != nil || index != -1 {
		t.Fatal("hash collision suppressed unequal ON key", index, err)
	}
	if index, err := findDistinctTuple(context.Background(), node.grouping, []sqltype.Value{two}, flat, []int{0, 1}); err != nil || index != 1 {
		t.Fatal(index, err)
	}
	ctx := &aggregateCancelContext{Context: context.Background(), armed: true, at: 4}
	if _, err := findDistinctTuple(ctx, node.grouping, []sqltype.Value{two}, flat, []int{0, 1}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestQueryDistinctOnFailureCancellationAndPublication(t *testing.T) {
	for _, phase := range []string{"key", "late key", "hash", "prepublication"} {
		t.Run(phase, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT DISTINCT ON(v) id FROM t")
			project := query.Root.(*projectNode)
			node := project.child.(*distinctOnNode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			original := node.expressions[0]
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
				node.grouping[0].Grouping, _ = sqltype.BindGrouping(usage)
				armed := &aggregateCancelContext{Context: ctx, at: 4}
				execution = armed
				node.expressions[0] = queryProbe(usage, func(*EvalContext) (sqltype.Value, error) {
					armed.armed = true
					return sqltype.String(text, strings.Repeat("x", 4096)), nil
				})
			} else {
				node.expressions[0] = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
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
					expr := project.expressions[0]
					published := 0
					project.expressions[0] = queryProbe(expr.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
						published++
						if published == 3 {
							cancel()
						}
						return expr.Eval(c)
					})
				}
			}
			rows, count, err := finalizeQuery(execution, query.Root, source, nil)
			want := error(context.Canceled)
			if phase == "late key" {
				want = sentinel
			}
			if rows != nil || count != 0 || !errors.Is(err, want) || source.view != nil {
				t.Fatal(rows, count, err, want, source.view)
			}
		})
	}
}

func TestQueryDistinctOnInsertKeyFailureNoBatch(t *testing.T) {
	b, source := insertSelectEvaluationFixture(t, "SELECT DISTINCT ON(v) id FROM t")
	node := b.query.Root.(*projectNode).child.(*distinctOnNode)
	original := node.expressions[0]
	calls := 0
	sentinel := errors.New("late ON INSERT key")
	node.expressions[0] = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		calls++
		if calls == 3 {
			return sqltype.Value{}, sentinel
		}
		return original.Eval(c)
	})
	rows, err := evaluateInsertQuery(context.Background(), b, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
	if rows.values != nil || err != sentinel || source.view != nil || source.anonymous != nil {
		t.Fatal(rows, err, source)
	}
}

func TestQueryDistinctOnLaterComponentDemand(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT DISTINCT ON(v,id) id FROM t")
	node := query.Root.(*projectNode).child.(*distinctOnNode)
	first := node.expressions[0]
	node.expressions[0] = queryProbe(first.ResultUsage(), func(*EvalContext) (sqltype.Value, error) { return sqltype.Integer(first.ResultUsage().Type, 1) })
	second := node.expressions[1]
	calls := 0
	sentinel := errors.New("later ON component")
	node.expressions[1] = queryProbe(second.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		calls++
		if calls == 4 {
			return sqltype.Value{}, sentinel
		}
		return second.Eval(c)
	})
	rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
	if rows != nil || count != 0 || err != sentinel || calls != 4 || source.view != nil {
		t.Fatal(rows, count, err, calls, source.view)
	}
}
