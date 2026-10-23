package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestGroupingSetsExpansion(t *testing.T) {
	key := func(i int) analyze.AnalyzedGroupItem {
		return analyze.AnalyzedGroupItem{Kind: parser.GroupExpression, Key: i}
	}
	rollup := func(items ...analyze.AnalyzedGroupItem) analyze.AnalyzedGroupItem {
		return analyze.AnalyzedGroupItem{Kind: parser.GroupRollup, Items: items}
	}
	items := []analyze.AnalyzedGroupItem{rollup(key(0), key(1)), rollup(key(0), key(2))}
	sets, err := analyze.ExpandGroupingItems(context.Background(), items)
	want := [][]int{{0, 1, 2}, {0, 1}, {0, 1}, {0, 2}, {0}, {0}, {0, 2}, {0}, {}}
	if err != nil || !reflect.DeepEqual(sets, want) {
		t.Fatal(sets, err)
	}
	units := []analyze.AnalyzedGroupItem{{Kind: parser.GroupList, Items: []analyze.AnalyzedGroupItem{key(0), key(1)}}, key(2)}
	sets, err = analyze.ExpandGroupingItems(context.Background(), []analyze.AnalyzedGroupItem{{Kind: parser.GroupCube, Items: units}})
	if err != nil || !reflect.DeepEqual(sets, [][]int{{}, {0, 1}, {2}, {0, 1, 2}}) {
		t.Fatal(sets, err)
	}
	sets, err = analyze.ExpandGroupingItems(context.Background(), []analyze.AnalyzedGroupItem{{Kind: parser.GroupSets, Items: []analyze.AnalyzedGroupItem{key(0), {Kind: parser.GroupSets, Items: []analyze.AnalyzedGroupItem{{Kind: parser.GroupEmpty}, key(1)}}}}})
	if err != nil || !reflect.DeepEqual(sets, [][]int{{0}, {}, {1}}) {
		t.Fatal(sets, err)
	}
	if union := analyze.GroupingUnion([]int{0, 2}, []int{0, 1, 2}); !reflect.DeepEqual(union, []int{0, 1, 2}) {
		t.Fatal(union)
	}
}

func TestGroupingSetsRawLimits(t *testing.T) {
	units := make([]analyze.AnalyzedGroupItem, 12)
	for i := range units {
		units[i] = analyze.AnalyzedGroupItem{Kind: parser.GroupExpression, Key: 0}
	}
	cube := analyze.AnalyzedGroupItem{Kind: parser.GroupCube, Items: units}
	sets, err := analyze.ExpandGroupingItems(context.Background(), []analyze.AnalyzedGroupItem{cube})
	if err != nil || len(sets) != 4096 {
		t.Fatal(len(sets), err)
	}
	cube.Items = append(cube.Items, units[0])
	_, err = analyze.ExpandGroupingItems(context.Background(), []analyze.AnalyzedGroupItem{cube})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "54011" {
		t.Fatal(err)
	}
	cube.Items = units
	_, err = analyze.ExpandGroupingItems(context.Background(), []analyze.AnalyzedGroupItem{cube, {Kind: parser.GroupCube, Items: units[:1]}})
	if !errors.As(err, &diagnostic) || diagnostic.Code != "54001" || diagnostic.Position != "" {
		t.Fatal(err)
	}
	empty := make([]analyze.AnalyzedGroupItem, 4097)
	for i := range empty {
		empty[i].Kind = parser.GroupEmpty
	}
	_, err = analyze.ExpandGroupingItems(context.Background(), []analyze.AnalyzedGroupItem{{Kind: parser.GroupSets, Items: empty}})
	if !errors.As(err, &diagnostic) || diagnostic.Code != "54001" {
		t.Fatal(err)
	}
}

func TestGroupingSetsMasksFramesAndNoArgumentEvaluation(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.Query.Frame = 1
	b.Grouping.GroupSets = [][]int{{0}, {}}
	usage := b.Type(sqltype.Int4).Usage()
	key := plan.ColumnExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}
	operation := plan.GroupingExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{StageDependent: true}}, Frame: 1, Args: []expr.Expr{key, key}}
	expression, err := b.RewriteGrouping(operation, []expr.Expr{key})
	if err != nil {
		t.Fatal(err)
	}
	grouping := expression.(plan.GroupingExpr)
	if !reflect.DeepEqual(grouping.Masks, []int32{0, 3}) || grouping.Slot != 1 {
		t.Fatal(grouping)
	}
	grouping.Args = []expr.Expr{queryProbe(usage, func(*exec.Context) (sqltype.Value, error) {
		t.Fatal("GROUPING evaluated its argument")
		return sqltype.Value{}, nil
	})}
	occurrence, _ := sqltype.Integer(usage.Type, 1)
	outer := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.Null(usage.Type), occurrence}}}
	value, err := grouping.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{Scope: 1, Frame: 2, Outer: outer}})
	mask, _ := value.Integer()
	if err != nil || mask != 3 || value.Type().ID() != sqltype.Int4 {
		t.Fatal(mask, err)
	}
	if operation.Masks != nil || operation.Slot != 0 {
		t.Fatal("analysis mutated original operation")
	}
}

func TestGroupingSetsRuntimeOccurrencesAndFreshState(t *testing.T) {
	for range 2 {
		_, query, source := queryCoreFixture(t, "SELECT v,GROUPING(v),count(*) FROM t GROUP BY GROUPING SETS((v),(v),())")
		schema := query.Root.Schema()
		if len(schema) != 3 || schema[0].OriginTable == 0 || schema[0].OriginAttribute != 2 || !schema[0].Nullable || schema[1].OriginTable != 0 || !schema[1].NullabilityKnown || schema[1].Nullable {
			t.Fatal(schema)
		}
		rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
		if err != nil || count != 7 {
			t.Fatal(count, err)
		}
		bag := map[string]int{}
		out := make([]sqltype.Value, 3)
		for {
			present, err := rows.Next(context.Background(), out)
			if err != nil {
				t.Fatal(err)
			}
			if !present {
				break
			}
			key := "NULL"
			if !out[0].IsNull() {
				n, _ := out[0].Integer()
				key = fmt.Sprint(n)
			}
			g, _ := out[1].Integer()
			n, _ := out[2].Integer()
			bag[fmt.Sprintf("%s/%d/%d", key, g, n)]++
		}
		rows.Close()
		want := map[string]int{"1/0/2": 2, "2/0/1": 2, "NULL/0/1": 2, "NULL/1/4": 1}
		if !reflect.DeepEqual(bag, want) {
			t.Fatal(bag)
		}
	}
}

func TestGroupingSetsOncePerSourceModifiers(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT count(DISTINCT v ORDER BY v) FILTER(WHERE TRUE) FROM t GROUP BY GROUPING SETS((),(),())")
	node := query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
	call := &node.Calls[0]
	filter, argument := call.Filter, call.Arguments[0]
	filters, values := 0, 0
	call.Filter = queryProbe(filter.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { filters++; return filter.Eval(c) })
	call.Arguments[0] = queryProbe(argument.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { values++; return argument.Eval(c) })
	rows, count, err := exec.Finalize(context.Background(), query.Root, source, nil)
	if err != nil || count != 3 || filters != 4 || values != 4 {
		t.Fatal(count, err, filters, values)
	}
	out := make([]sqltype.Value, 1)
	for range 3 {
		present, err := rows.Next(context.Background(), out)
		n, _ := out[0].Integer()
		if !present || err != nil || n != 2 {
			t.Fatal(present, n, err)
		}
	}
	rows.Close()
}

func TestGroupingSetsCancellationPublication(t *testing.T) {
	for _, phase := range []string{"fanout", "finalize", "sort", "prepare", "hash", "equality"} {
		t.Run(phase, func(t *testing.T) {
			_, query, source := queryCoreFixture(t, "SELECT v,count(DISTINCT v ORDER BY v) FROM t GROUP BY GROUPING SETS((v),(v),()) ORDER BY v")
			project := query.Root.(*plan.ProjectNode)
			node := project.Child.(*plan.SortNode).Child.(*plan.AggregateNode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var execution context.Context = ctx
			var familyProbe *aggregateModifierFamilyCancelContext
			var sortProbe *aggregateSortCancelContext
			if phase == "prepare" || phase == "hash" || phase == "equality" {
				target := map[string]string{"prepare": ".Grouping.PrepareKey", "hash": ".Grouping.Hash", "equality": ".Grouping.Equal"}[phase]
				familyProbe = &aggregateModifierFamilyCancelContext{Context: ctx, cancel: cancel, target: target}
				execution = familyProbe
			}
			if phase == "sort" {
				sortProbe = &aggregateSortCancelContext{Context: ctx, cancel: cancel}
				execution = sortProbe
			}
			provider := node.Calls[0].Provider
			advances := 0
			node.Calls[0].Provider = aggregateProbeProvider{AggregateProvider: provider, advance: func(ctx context.Context, s any, v, d sqltype.Value) error {
				advances++
				err := provider.Advance(ctx, s, []sqltype.Value{v, d}, nil)
				if phase == "fanout" && advances == 2 {
					cancel()
				}
				return err
			}, finalize: func(ctx context.Context, s any) (sqltype.Value, error) {
				value, err := provider.Finalize(ctx, s)
				if phase == "finalize" {
					cancel()
				}
				return value, err
			}}
			if phase == "fanout" {
				node.Calls[0].Order = nil
				node.Calls[0].Distinct = nil
			}
			rows, count, err := exec.Finalize(execution, query.Root, source, nil)
			if rows != nil || count != 0 || !errors.Is(err, context.Canceled) || source.View != nil || source.Anonymous != nil {
				t.Fatal(rows, count, err)
			}
			if phase == "fanout" && advances != 2 {
				t.Fatal("fanout did not enter second group", advances)
			}
			if familyProbe != nil && !familyProbe.entered {
				t.Fatal("typed family was not entered")
			}
			if sortProbe != nil && !sortProbe.comparisonEntered {
				t.Fatal("actual merge comparator was not entered")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := &aggregateModifierFamilyCancelContext{Context: ctx, cancel: cancel, target: ".expandGroupingItem"}
	_, err := analyze.ExpandGroupingItems(probe, []analyze.AnalyzedGroupItem{{Kind: parser.GroupCube, Items: []analyze.AnalyzedGroupItem{{Kind: parser.GroupExpression, Key: 0}}}})
	if !probe.entered || !errors.Is(err, context.Canceled) {
		t.Fatal(probe.entered, err)
	}
}

func TestGroupingSetsRestoreWhitelist(t *testing.T) {
	ordinary := statement(t, "SELECT setval('s',1,true)").(*parser.Select)
	if !sequence.RestoresState(ordinary) {
		t.Fatal("dump's unqualified setval statement was not whitelisted")
	}
	qualified := statement(t, "SELECT pg_catalog.setval('s',1,true)").(*parser.Select)
	if sequence.RestoresState(qualified) {
		t.Fatal("qualified call widened the existing whitelist")
	}
	for _, clause := range []string{" GROUP BY ()", " GROUP BY ALL ()", " GROUP BY DISTINCT ()"} {
		grouped := statement(t, "SELECT setval('s',1,true)"+clause).(*parser.Select)
		if sequence.RestoresState(grouped) {
			t.Fatal("explicit empty grouping bypassed restore whitelist", clause)
		}
	}
}

func TestGroupingSetsInsertLateFailureAndActiveCancellation(t *testing.T) {
	for _, phase := range []string{"late_finalize", "fanout_cancel"} {
		t.Run(phase, func(t *testing.T) {
			b, source := insertSelectEvaluationFixture(t, "SELECT count(*) FROM t GROUP BY GROUPING SETS((v),(v),())")
			node := b.Query.Root.(*plan.ProjectNode).Child.(*plan.AggregateNode)
			provider := node.Calls[0].Provider
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("late grouped INSERT finalize")
			advances, finalizations := 0, 0
			node.Calls[0].Provider = aggregateProbeProvider{AggregateProvider: provider,
				advance: func(ctx context.Context, s any, v, d sqltype.Value) error {
					advances++
					err := provider.Advance(ctx, s, []sqltype.Value{v, d}, nil)
					if phase == "fanout_cancel" && advances == 2 {
						cancel()
					}
					return err
				},
				finalize: func(ctx context.Context, s any) (sqltype.Value, error) {
					finalizations++
					if phase == "late_finalize" && finalizations == 2 {
						return sqltype.Value{}, sentinel
					}
					return provider.Finalize(ctx, s)
				},
			}
			rows, err := dml.EvaluateInsertQuery(ctx, b, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
			want := error(sentinel)
			if phase == "fanout_cancel" {
				want = context.Canceled
			}
			if rows.Values != nil || !errors.Is(err, want) || source.View != nil || source.Anonymous != nil {
				t.Fatal(rows, err, want, source)
			}
			if phase == "fanout_cancel" && advances != 2 {
				t.Fatal("actual second occurrence was not advanced", advances)
			}
			if phase == "late_finalize" && finalizations != 2 {
				t.Fatal("late second group was not finalized", finalizations)
			}
		})
	}
}
