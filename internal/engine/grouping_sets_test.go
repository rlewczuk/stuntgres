package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestGroupingSetsExpansion(t *testing.T) {
	key := func(i int) analyzedGroupItem { return analyzedGroupItem{kind: parser.GroupExpression, key: i} }
	rollup := func(items ...analyzedGroupItem) analyzedGroupItem {
		return analyzedGroupItem{kind: parser.GroupRollup, items: items}
	}
	items := []analyzedGroupItem{rollup(key(0), key(1)), rollup(key(0), key(2))}
	sets, err := expandGroupingItems(context.Background(), items)
	want := [][]int{{0, 1, 2}, {0, 1}, {0, 1}, {0, 2}, {0}, {0}, {0, 2}, {0}, {}}
	if err != nil || !reflect.DeepEqual(sets, want) {
		t.Fatal(sets, err)
	}
	units := []analyzedGroupItem{{kind: parser.GroupList, items: []analyzedGroupItem{key(0), key(1)}}, key(2)}
	sets, err = expandGroupingItems(context.Background(), []analyzedGroupItem{{kind: parser.GroupCube, items: units}})
	if err != nil || !reflect.DeepEqual(sets, [][]int{{}, {0, 1}, {2}, {0, 1, 2}}) {
		t.Fatal(sets, err)
	}
	sets, err = expandGroupingItems(context.Background(), []analyzedGroupItem{{kind: parser.GroupSets, items: []analyzedGroupItem{key(0), {kind: parser.GroupSets, items: []analyzedGroupItem{{kind: parser.GroupEmpty}, key(1)}}}}})
	if err != nil || !reflect.DeepEqual(sets, [][]int{{0}, {}, {1}}) {
		t.Fatal(sets, err)
	}
	if union := groupingUnion([]int{0, 2}, []int{0, 1, 2}); !reflect.DeepEqual(union, []int{0, 1, 2}) {
		t.Fatal(union)
	}
}

func TestGroupingSetsRawLimits(t *testing.T) {
	units := make([]analyzedGroupItem, 12)
	for i := range units {
		units[i] = analyzedGroupItem{kind: parser.GroupExpression, key: 0}
	}
	cube := analyzedGroupItem{kind: parser.GroupCube, items: units}
	sets, err := expandGroupingItems(context.Background(), []analyzedGroupItem{cube})
	if err != nil || len(sets) != 4096 {
		t.Fatal(len(sets), err)
	}
	cube.items = append(cube.items, units[0])
	_, err = expandGroupingItems(context.Background(), []analyzedGroupItem{cube})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "54011" {
		t.Fatal(err)
	}
	cube.items = units
	_, err = expandGroupingItems(context.Background(), []analyzedGroupItem{cube, {kind: parser.GroupCube, items: units[:1]}})
	if !errors.As(err, &diagnostic) || diagnostic.Code != "54001" || diagnostic.Position != "" {
		t.Fatal(err)
	}
	empty := make([]analyzedGroupItem, 4097)
	for i := range empty {
		empty[i].kind = parser.GroupEmpty
	}
	_, err = expandGroupingItems(context.Background(), []analyzedGroupItem{{kind: parser.GroupSets, items: empty}})
	if !errors.As(err, &diagnostic) || diagnostic.Code != "54001" {
		t.Fatal(err)
	}
}

func TestGroupingSetsMasksFramesAndNoArgumentEvaluation(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.query.frame = 1
	b.grouping.groupSets = [][]int{{0}, {}}
	usage := b.typ(sqltype.Int4).Usage()
	key := columnExpression{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, scope: 1, frame: 1, slot: 0}
	operation := groupingExpression{Info: expr.Info{Usage: usage, Property: expr.Properties{StageDependent: true}}, frame: 1, args: []expr.Expr{key, key}}
	expression, err := b.rewriteGrouping(operation, []expr.Expr{key})
	if err != nil {
		t.Fatal(err)
	}
	grouping := expression.(groupingExpression)
	if !reflect.DeepEqual(grouping.masks, []int32{0, 3}) || grouping.slot != 1 {
		t.Fatal(grouping)
	}
	grouping.args = []expr.Expr{queryProbe(usage, func(*EvalContext) (sqltype.Value, error) {
		t.Fatal("GROUPING evaluated its argument")
		return sqltype.Value{}, nil
	})}
	occurrence, _ := sqltype.Integer(usage.Type, 1)
	outer := &EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.Null(usage.Type), occurrence}}}
	value, err := grouping.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 2, Outer: outer}})
	mask, _ := value.Integer()
	if err != nil || mask != 3 || value.Type().ID() != sqltype.Int4 {
		t.Fatal(mask, err)
	}
	if operation.masks != nil || operation.slot != 0 {
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
		rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
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
	node := query.Root.(*projectNode).child.(*aggregateNode)
	call := &node.calls[0]
	filter, argument := call.filter, call.argument
	filters, values := 0, 0
	call.filter = queryProbe(filter.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { filters++; return filter.Eval(c) })
	call.argument = queryProbe(argument.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { values++; return argument.Eval(c) })
	rows, count, err := finalizeQuery(context.Background(), query.Root, source, nil)
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
			project := query.Root.(*projectNode)
			node := project.child.(*sortNode).child.(*aggregateNode)
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
			provider := node.calls[0].provider
			advances := 0
			node.calls[0].provider = aggregateProbeProvider{AggregateProvider: provider, advance: func(ctx context.Context, s any, v, d sqltype.Value) error {
				advances++
				err := provider.Advance(ctx, s, v, d)
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
				node.calls[0].order = nil
				node.calls[0].distinct = nil
			}
			rows, count, err := finalizeQuery(execution, query.Root, source, nil)
			if rows != nil || count != 0 || !errors.Is(err, context.Canceled) || source.view != nil || source.anonymous != nil {
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
	_, err := expandGroupingItems(probe, []analyzedGroupItem{{kind: parser.GroupCube, items: []analyzedGroupItem{{kind: parser.GroupExpression, key: 0}}}})
	if !probe.entered || !errors.Is(err, context.Canceled) {
		t.Fatal(probe.entered, err)
	}
}

func TestGroupingSetsRestoreWhitelist(t *testing.T) {
	ordinary := statement(t, "SELECT setval('s',1,true)").(*parser.Select)
	if !restoreSequenceState(ordinary) {
		t.Fatal("dump's unqualified setval statement was not whitelisted")
	}
	qualified := statement(t, "SELECT pg_catalog.setval('s',1,true)").(*parser.Select)
	if restoreSequenceState(qualified) {
		t.Fatal("qualified call widened the existing whitelist")
	}
	for _, clause := range []string{" GROUP BY ()", " GROUP BY ALL ()", " GROUP BY DISTINCT ()"} {
		grouped := statement(t, "SELECT setval('s',1,true)"+clause).(*parser.Select)
		if restoreSequenceState(grouped) {
			t.Fatal("explicit empty grouping bypassed restore whitelist", clause)
		}
	}
}

func TestGroupingSetsInsertLateFailureAndActiveCancellation(t *testing.T) {
	for _, phase := range []string{"late_finalize", "fanout_cancel"} {
		t.Run(phase, func(t *testing.T) {
			b, source := insertSelectEvaluationFixture(t, "SELECT count(*) FROM t GROUP BY GROUPING SETS((v),(v),())")
			node := b.query.Root.(*projectNode).child.(*aggregateNode)
			provider := node.calls[0].provider
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("late grouped INSERT finalize")
			advances, finalizations := 0, 0
			node.calls[0].provider = aggregateProbeProvider{AggregateProvider: provider,
				advance: func(ctx context.Context, s any, v, d sqltype.Value) error {
					advances++
					err := provider.Advance(ctx, s, v, d)
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
			rows, err := evaluateInsertQuery(ctx, b, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
			want := error(sentinel)
			if phase == "fanout_cancel" {
				want = context.Canceled
			}
			if rows.values != nil || !errors.Is(err, want) || source.view != nil || source.anonymous != nil {
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
