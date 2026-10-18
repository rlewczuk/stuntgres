package engine

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestAggregateModifiersTupleDistinct(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	typ := b.typ(sqltype.Text)
	family, err := sqltype.BindGrouping(typ.Usage())
	if err != nil {
		t.Fatal(err)
	}
	grouping := []queryGrouping{{Grouping: family}, {Grouping: family}}
	a, comma, slash := sqltype.String(typ, "a"), sqltype.String(typ, ","), sqltype.String(typ, "/")
	null := sqltype.Null(typ)
	var state aggregateDistinctState
	for i, tc := range []struct {
		tuple []sqltype.Value
		admit bool
	}{
		{[]sqltype.Value{a, comma}, true}, {[]sqltype.Value{a, comma}, false},
		{[]sqltype.Value{a, slash}, true}, {[]sqltype.Value{a, null}, true}, {[]sqltype.Value{a, null}, false},
		{[]sqltype.Value{null, comma}, true}, {[]sqltype.Value{null, comma}, false},
	} {
		admit, err := state.admit(context.Background(), grouping, tc.tuple)
		if err != nil || admit != tc.admit {
			t.Fatal(i, admit, err)
		}
	}
	// Force unequal tuples into the same hash candidate list. Exact family
	// equality, rather than the bucket hit, decides admission.
	tuple := []sqltype.Value{sqltype.String(typ, "b"), comma}
	hash := uint64(14695981039346656037)
	for i, f := range grouping {
		h, err := f.Hash(context.Background(), tuple[i])
		if err != nil {
			t.Fatal(err)
		}
		hash = (hash ^ h) * 1099511628211
	}
	state.buckets[hash] = []int{0}
	admit, err := state.admit(context.Background(), grouping, tuple)
	if err != nil || !admit || len(state.tuples) != 10 {
		t.Fatal(admit, err, state.tuples)
	}
	admit, err = state.admit(context.Background(), grouping, tuple)
	if err != nil || admit {
		t.Fatal(admit, err)
	}
	var other aggregateDistinctState
	if admit, err := other.admit(context.Background(), grouping, []sqltype.Value{a, comma}); err != nil || !admit {
		t.Fatal("cross-call/group/execution state", admit, err)
	}
	if len(state.tuples) != 10 {
		t.Fatal("independent state mutated retained tuples")
	}
}

func TestAggregateModifiersBindingIdentity(t *testing.T) {
	e := New()
	run(t, e, "CREATE SEQUENCE am_unit_sequence")
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	b.aggregateClause = "SELECT"
	for i, tc := range []struct {
		call string
		slot int
	}{
		{"count(1)", 0}, {"count(ALL 1)", 0}, {"count(DISTINCT 1)", 1},
		{"count(1) FILTER(WHERE TRUE)", 2}, {"count(1) FILTER(WHERE FALSE)", 3},
		{"count(1 ORDER BY 1)", 4}, {"count(1 ORDER BY 1 DESC)", 5},
		{"count(1 ORDER BY 1 NULLS FIRST)", 6}, {"count(1 ORDER BY 1)", 4},
		{"count(1) FILTER(WHERE nextval('am_unit_sequence')>0)", 7},
		{"count(1) FILTER(WHERE nextval('am_unit_sequence')>0)", 8},
		{"count(1 ORDER BY nextval('am_unit_sequence'))", 9},
		{"count(1 ORDER BY nextval('am_unit_sequence'))", 10},
	} {
		raw := statement(t, "SELECT "+tc.call).(*parser.Select).Targets[0]
		expr, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil || expr.(aggregateExpression).slot != tc.slot {
			t.Fatal(i, expr, err)
		}
	}
	raw := statement(t, "SELECT count(DISTINCT nextval('am_unit_sequence') ORDER BY nextval('am_unit_sequence'),nextval('am_unit_sequence'))").(*parser.Select).Targets[0]
	expr, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	call := b.aggregates[expr.(aggregateExpression).slot]
	if call.order[0].slot != 0 || call.order[1].slot != 0 || expr.Properties().Volatility != Volatile {
		t.Fatal(call, expr.Properties())
	}
	altered := call
	altered.provider = functions.NewCountProvider(b.typ(sqltype.Int4).Usage())
	if b.sameAggregateCall(call, altered) {
		t.Fatal("result Usage omitted from identity")
	}
}

func TestAggregateModifiersParameterTargets(t *testing.T) {
	for _, query := range []string{
		"SELECT count($1 ORDER BY $1)",
		"SELECT count(DISTINCT $1 ORDER BY $1)",
	} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		b.aggregateClause = "SELECT"
		raw := statement(t, query).(*parser.Select).Targets[0]
		expression, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(query, err)
		}
		parameters, err := b.finish()
		if err != nil {
			t.Fatal(query, err)
		}
		call := b.aggregates[expression.(aggregateExpression).slot]
		if parameters.Count != 1 || parameters.Types[0].Type.ID() != sqltype.Text ||
			call.argument.ResultUsage().Type.ID() != sqltype.Text ||
			call.order[0].slot != 0 || call.order[0].cache != 0 || len(b.anyParameters) != 0 {
			t.Fatal(query, parameters, call, b.anyParameters)
		}
	}
}

// Observe real family entry, not pipeline Err-call counts or fake providers.
type aggregateModifierFamilyCancelContext struct {
	context.Context
	cancel  context.CancelFunc
	target  string
	entered bool
}

func (c *aggregateModifierFamilyCancelContext) Err() error {
	if !c.entered {
		var pcs [32]uintptr
		n := runtime.Callers(2, pcs[:])
		frames := runtime.CallersFrames(pcs[:n])
		for {
			frame, more := frames.Next()
			if strings.HasSuffix(frame.Function, c.target) {
				c.entered = true
				c.cancel()
				break
			}
			if !more {
				break
			}
		}
	}
	return c.Context.Err()
}

func TestAggregateModifiersFamilyCancellation(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	typ := b.typ(sqltype.Text)
	family, err := sqltype.BindGrouping(typ.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{".Grouping.PrepareKey", ".Grouping.Hash", ".Grouping.Equal"} {
		t.Run(phase, func(t *testing.T) {
			var state aggregateDistinctState
			grouping := []queryGrouping{{Grouping: family}}
			value := sqltype.String(typ, strings.Repeat("x", 8192))
			if _, err := state.admit(context.Background(), grouping, []sqltype.Value{value}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			probe := &aggregateModifierFamilyCancelContext{Context: ctx, cancel: cancel, target: phase}
			admit, err := state.admit(probe, grouping, []sqltype.Value{value})
			if !probe.entered || !errors.Is(err, context.Canceled) || admit || len(state.tuples) != 1 {
				t.Fatal(phase, probe.entered, admit, err, state.tuples)
			}
		})
	}
}

func TestAggregateModifiersEvaluationPublication(t *testing.T) {
	for _, phase := range []string{"success", "filter", "key", "hash", "equality", "sort", "replay", "finalize"} {
		t.Run(phase, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT count(DISTINCT v ORDER BY v) FILTER(WHERE TRUE) FROM t")
			node := query.Root.(*projectNode).child.(*aggregateNode)
			call := &node.calls[0]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			filters, values, keys, advances, finalizes := 0, 0, 0, 0, 0
			original := call.argument
			call.argument = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { values++; return original.Eval(c) })
			boolType := call.filter.ResultUsage().Type
			call.filter = queryProbe(boolType.Usage(), func(c *EvalContext) (sqltype.Value, error) {
				filters++
				if phase == "filter" {
					cancel()
				}
				return sqltype.Boolean(boolType, filters != 2), nil
			})
			// A matched order expression must never be evaluated separately.
			call.order[0].expression = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
				keys++
				if phase == "key" {
					cancel()
				}
				return original.Eval(c)
			})
			if phase == "key" {
				call.order[0].slot = -1
			}
			provider := call.provider
			call.provider = aggregateProbeProvider{aggregateProvider: provider, advance: func(ctx context.Context, s any, v, d sqltype.Value) error {
				advances++
				err := provider.Advance(ctx, s, v, d)
				if phase == "replay" {
					cancel()
				}
				return err
			}, finalize: func(ctx context.Context, s any) (sqltype.Value, error) {
				finalizes++
				v, err := provider.Finalize(ctx, s)
				if phase == "finalize" {
					cancel()
				}
				return v, err
			}}
			var execution context.Context = ctx
			var familyProbe *aggregateModifierFamilyCancelContext
			if phase == "hash" || phase == "equality" {
				target := ".Grouping.Hash"
				if phase == "equality" {
					target = ".Grouping.Equal"
				}
				familyProbe = &aggregateModifierFamilyCancelContext{Context: ctx, cancel: cancel, target: target}
				execution = familyProbe
			}
			var sortProbe *aggregateSortCancelContext
			if phase == "sort" {
				sortProbe = &aggregateSortCancelContext{Context: ctx, cancel: cancel, aggregateOrdering: true}
				execution = sortProbe
			}
			rows, count, err := finalizeQuery(execution, query.Root, source, nil)
			if phase == "success" {
				if err != nil || count != 1 || filters != 4 || values != 3 || keys != 0 || finalizes != 1 {
					t.Fatal(rows, count, err, filters, values, keys, advances, finalizes)
				}
				out := make([]sqltype.Value, 1)
				present, err := rows.Next(ctx, out)
				// The retained fixture values are (1,NULL,2,1); row2 is rejected.
				value, _ := out[0].Integer()
				if err != nil || !present || value != 2 {
					t.Fatal(out, present, err)
				}
				if filters != 4 || values != 3 || keys != 0 {
					t.Fatal("Next evaluated aggregate inputs")
				}
				rows.Close()
			} else {
				if rows != nil || count != 0 || !errors.Is(err, context.Canceled) {
					t.Fatal("partial publication", rows, count, err)
				}
				if familyProbe != nil && !familyProbe.entered {
					t.Fatal("typed family not entered")
				}
				if sortProbe != nil && !sortProbe.comparisonEntered {
					t.Fatal("sort comparator not entered")
				}
			}
			if source.view != nil || source.anonymous != nil {
				t.Fatal("source retained", source)
			}
		})
	}
}

func TestAggregateModifiersImmutableConsumers(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT count(DISTINCT v ORDER BY v) FILTER(WHERE f) FROM t")
	defer source.close()
	node := query.Root.(*projectNode).child.(*aggregateNode)
	original := node.calls[0]
	rewritten, err := rewriteQueryExpressions(node, func(expr BoundExpr) (BoundExpr, bool, error) {
		column, ok := expr.(columnExpression)
		if !ok || column.slot != 2 {
			return expr, false, nil
		}
		return literalExpression{expressionInfo: expressionInfo{usage: column.ResultUsage()}, value: sqltype.Boolean(column.ResultUsage().Type, false)}, true, nil
	})
	if err != nil || rewritten == node {
		t.Fatal("FILTER rewrite not copied", rewritten, err)
	}
	changed := rewritten.(*aggregateNode).calls[0]
	if _, ok := changed.filter.(literalExpression); !ok {
		t.Fatal("FILTER rewrite lost")
	}
	if _, ok := node.calls[0].filter.(columnExpression); !ok {
		t.Fatal("original FILTER mutated")
	}
	if changed.order[0].slot != original.order[0].slot || changed.order[0].cache != original.order[0].cache || changed.stageWidth != original.stageWidth {
		t.Fatal("target reuse lost across rewrite")
	}
	folded, err := preflightQuery(&EvalContext{Context: context.Background()}, node)
	if err != nil || folded != node {
		t.Fatal("unchanged preflight mutated immutable plan", folded, err)
	}
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
	}
	remapped, err := remapViewQuery(New().CatalogRead(), node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor { return types[old.ID()] })
	if err != nil || remapped == node {
		t.Fatal(remapped, err)
	}
	call := remapped.(*aggregateNode).calls[0]
	if call.filter.ResultUsage().Type == original.filter.ResultUsage().Type || call.argument.ResultUsage().Type == original.argument.ResultUsage().Type {
		t.Fatal("modifier descriptor ownership not remapped")
	}
	value, _ := sqltype.Integer(call.argument.ResultUsage().Type, 1)
	if _, err := call.distinct[0].Hash(context.Background(), value); err != nil {
		t.Fatal("remapped grouping retains old descriptor", err)
	}
	if _, err := original.distinct[0].Hash(context.Background(), value); err == nil {
		t.Fatal("original grouping mutated")
	}
	if sign, err := call.order[0].comparison.Compare(context.Background(), value, value, sqltype.InputEnvironment{}); err != nil || sign != 0 {
		t.Fatal("remapped comparison retains old descriptor", sign, err)
	}
	if original.argument.ResultUsage().Type != node.calls[0].argument.ResultUsage().Type || original.filter.ResultUsage().Type != node.calls[0].filter.ResultUsage().Type {
		t.Fatal("remap mutated original inputs")
	}
}

func TestAggregateModifiersOuterOwnershipSharing(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT (SELECT count(*) FILTER(WHERE o.v=1)),(SELECT count(*) FILTER(WHERE o.v=1)),(SELECT count(*) FILTER(WHERE o.v=2)) FROM t o")
	node := query.Root.(*projectNode).child.(*aggregateNode)
	if len(node.calls) != 2 || node.calls[0].filter == nil || node.calls[1].filter == nil {
		t.Fatal("incomplete outer-owner modifier identity", node.calls)
	}
	rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
	if err != nil || count != 1 {
		t.Fatal(rows, count, err)
	}
	defer rows.Close()
	out := make([]sqltype.Value, 3)
	present, err := rows.Next(context.Background(), out)
	if err != nil || !present {
		t.Fatal(present, err)
	}
	for i, want := range []int64{2, 2, 1} {
		got, err := out[i].Integer()
		if err != nil || got != want {
			t.Fatal(i, got, want, err)
		}
	}
}

func TestAggregateModifiersExecutionStateIsolation(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT f,count(DISTINCT v ORDER BY v) FROM t GROUP BY f ORDER BY f NULLS LAST")
	for execution := range 2 {
		if execution > 0 {
			snapshot := run(t, e, "SELECT * FROM t").Rows.(*finalRows)
			var err error
			source, err = captureQuerySource(context.Background(), snapshot.source.view, snapshot.source.ref)
			snapshot.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
		rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
		if err != nil || count != 3 {
			t.Fatal(execution, rows, count, err)
		}
		out := make([]sqltype.Value, 2)
		for i, want := range []int64{1, 1, 0} {
			present, err := rows.Next(context.Background(), out)
			got, accessErr := out[1].Integer()
			if err != nil || accessErr != nil || !present || got != want {
				t.Fatal(execution, i, got, want, err, accessErr)
			}
			if i == 2 {
				if !out[0].IsNull() {
					t.Fatal(out)
				}
			} else {
				flag, err := out[0].Boolean()
				if err != nil || flag != (i == 1) {
					t.Fatal(out, err)
				}
			}
		}
		if present, err := rows.Next(context.Background(), out); present || err != nil {
			t.Fatal(present, err)
		}
		rows.Close()
	}
}
