package engine

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func rangeAggregateUnitProvider(t *testing.T, d *sqltype.Descriptor, operation sqltype.RangeAggregateOperation) functions.RangeAggregateProvider {
	t.Helper()
	bound, err := sqltype.BindRangeAggregate(d, operation)
	if err != nil {
		t.Fatal(err)
	}
	return functions.NewRangeAggregateProvider(d.Usage(), bound.ResultUsage(), operation, bound)
}

func TestRangeAggregateProvider(t *testing.T) {
	ctx := context.Background()
	e := New()
	for _, id := range []sqltype.ID{sqltype.Int4Range, sqltype.Int8Range, sqltype.NumRange, sqltype.Int4Multirange, sqltype.Int8Multirange, sqltype.NumMultirange} {
		d, _ := e.CatalogRead().LookupType(id)
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		b.Clause.AggregateClause = "SELECT"
		b.Params.Parameters[1] = d.Usage()
		for _, name := range []string{"range_agg", "range_agg", "range_intersect_agg"} {
			raw := parser.AggregateExpr{Name: name, Args: []parser.Expr{parser.ParameterExpr{Number: 1}}, Pos: parser.Position{Character: 8}}
			result, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			if err != nil {
				t.Fatal(err)
			}
			if !result.Properties().StageDependent || result.ResultUsage().Typmod != -1 || result.ResultUsage().Collation != (sqltype.CollationRef{}) {
				t.Fatal("result usage", result.ResultUsage())
			}
		}
		if len(b.Grouping.Aggregates) != 2 || b.Grouping.Aggregates[0].Operation != functions.AggregateRangeUnion || b.Grouping.Aggregates[1].Operation != functions.AggregateRangeIntersection || b.Grouping.Aggregates[0].IncludeNulls || b.Grouping.Aggregates[1].IncludeNulls {
			t.Fatal("operation/strict call identity", b.Grouping.Aggregates)
		}
		binder := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		binder.Params.Parameters[1] = d.Usage()
		query, err := binder.BindSelect(&parser.Select{Targets: []parser.Expr{
			parser.AggregateExpr{Name: "range_agg", Args: []parser.Expr{parser.ParameterExpr{Number: 1}}},
			parser.AggregateExpr{Name: "range_intersect_agg", Args: []parser.Expr{parser.ParameterExpr{Number: 1}}},
		}}, false)
		if err != nil {
			t.Fatal(err)
		}
		unionID := map[sqltype.ID]sqltype.ID{
			sqltype.Int4Range: sqltype.Int4Multirange, sqltype.Int4Multirange: sqltype.Int4Multirange,
			sqltype.Int8Range: sqltype.Int8Multirange, sqltype.Int8Multirange: sqltype.Int8Multirange,
			sqltype.NumRange: sqltype.NumMultirange, sqltype.NumMultirange: sqltype.NumMultirange,
		}[id]
		for i, c := range query.Root.Schema() {
			wantID := unionID
			if i == 1 {
				wantID = id
			}
			if c.Usage.Type.ID() != wantID || c.Usage.Typmod != -1 || c.Usage.Collation != (sqltype.CollationRef{}) ||
				!c.Nullable || !c.NullabilityKnown || c.OriginTable != 0 || c.OriginAttribute != 0 {
				t.Fatal("aggregate result descriptor", c)
			}
		}
	}
	rangeAggregateEagerUnit(t)
}

func rangeAggregateEagerUnit(t *testing.T) {
	t.Helper()
	e := New()
	d, _ := e.CatalogRead().LookupType(sqltype.Int4Range)
	p := rangeAggregateUnitProvider(t, d, sqltype.RangeAggregateIntersection)
	build := func(failure error) (*plan.ProjectNode, *exec.Source, *int) {
		calls := new(int)
		child := &plan.ValuesNode{QuerySchema: plan.QuerySchema{{Name: "v", Usage: d.Usage()}}}
		for _, text := range []string{"empty", "[1,4)", "[2,3)"} {
			v, err := d.Input(context.Background(), text, -1)
			if err != nil {
				t.Fatal(err)
			}
			child.Rows = append(child.Rows, []expr.Expr{queryProbe(d.Usage(), func(*exec.Context) (sqltype.Value, error) { return v, nil })})
		}
		argument := queryProbe(d.Usage(), func(c *exec.Context) (sqltype.Value, error) {
			(*calls)++
			if *calls == 3 && failure != nil {
				return sqltype.Value{}, failure
			}
			return c.Row[0], nil
		})
		schema := plan.QuerySchema{{Name: "i", Usage: p.ResultUsage(), Nullable: true, NullabilityKnown: true}}
		node := &plan.AggregateNode{QuerySchema: schema, Child: child, Calls: []plan.AggregateCall{{Provider: p, Argument: argument, Operation: functions.AggregateRangeIntersection}}}
		project := &plan.ProjectNode{QuerySchema: schema, Child: node, Expressions: []expr.Expr{queryProbe(p.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { return c.Row[0], nil })}}
		return project, &exec.Source{}, calls
	}
	root, source, calls := build(nil)
	rows, count, err := exec.Finalize(context.Background(), root, source, nil)
	if err != nil || count != 1 || *calls != 3 {
		t.Fatal("absorbing empty skipped later evaluation", count, *calls, err)
	}
	defer rows.Close()
	out := make([]sqltype.Value, 1)
	if ok, err := rows.Next(context.Background(), out); !ok || err != nil || *calls != 3 {
		t.Fatal("Next evaluated", ok, err, *calls)
	}
	text, err := d.Output(context.Background(), out[0])
	if err != nil || text != "empty" {
		t.Fatal(text, err)
	}
	failure := errors.New("late range operand")
	root, source, calls = build(failure)
	if rows, count, err := exec.Finalize(context.Background(), root, source, nil); rows != nil || count != 0 || err != failure || *calls != 3 {
		t.Fatal("late error published", rows, count, err, *calls)
	}
	for _, target := range []string{"stuntgres/internal/sqltype.RangeAggregate.Advance", "stuntgres/internal/sqltype.(*Descriptor).normalizeRangeMembers"} {
		root, source, _ = build(nil)
		if strings.Contains(target, "normalizeRangeMembers") {
			union := rangeAggregateUnitProvider(t, d, sqltype.RangeAggregateUnion)
			n := root.Child.(*plan.AggregateNode)
			n.Calls[0].Provider = union
			n.Calls[0].Operation = functions.AggregateRangeUnion
			n.QuerySchema[0].Usage = union.ResultUsage()
			root.QuerySchema[0].Usage = union.ResultUsage()
			root.Expressions[0] = queryProbe(union.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { return c.Row[0], nil })
		}
		ctx, cancel := context.WithCancel(context.Background())
		probe := &rangeAggregateCancelContext{Context: ctx, cancel: cancel, target: target}
		result, count, err := exec.Finalize(probe, root, source, nil)
		cancel()
		if !probe.entered || result != nil || count != 0 || err != context.Canceled {
			t.Fatal("actual typed cancellation publication", target, probe.entered, result, count, err)
		}
	}
}

type rangeAggregateCancelContext struct {
	context.Context
	cancel  context.CancelFunc
	target  string
	entered bool
}

func (c *rangeAggregateCancelContext) Err() error {
	if !c.entered {
		var pcs [32]uintptr
		frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
		for {
			frame, more := frames.Next()
			if frame.Function == c.target {
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

func TestRangeAggregateRemap(t *testing.T) {
	source, destination := New(), New()
	for _, id := range []sqltype.ID{sqltype.Int4Range, sqltype.Int8Range, sqltype.NumRange, sqltype.Int4Multirange, sqltype.Int8Multirange, sqltype.NumMultirange} {
		input, _ := source.CatalogRead().LookupType(id)
		for _, operation := range []sqltype.RangeAggregateOperation{sqltype.RangeAggregateUnion, sqltype.RangeAggregateIntersection} {
			p := rangeAggregateUnitProvider(t, input, operation)
			identity := functions.AggregateRangeUnion
			if operation == sqltype.RangeAggregateIntersection {
				identity = functions.AggregateRangeIntersection
			}
			root := &plan.AggregateNode{QuerySchema: plan.QuerySchema{{Name: "set", Usage: p.ResultUsage(), Nullable: true, NullabilityKnown: true}}, Child: &plan.ValuesNode{}, Calls: []plan.AggregateCall{{Provider: p, Operation: identity}}}
			mapped, err := remapViewQuery(source.CatalogRead(), root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
				next, _ := destination.CatalogRead().LookupType(old.ID())
				return next
			})
			if err != nil {
				t.Fatal(err)
			}
			call := mapped.(*plan.AggregateNode).Calls[0]
			provider := call.Provider.(functions.RangeAggregateProvider)
			newInput, _ := destination.CatalogRead().LookupType(id)
			newResult, _ := destination.CatalogRead().LookupType(p.ResultUsage().Type.ID())
			if provider.InputUsage().Type != newInput || provider.ResultUsage().Type != newResult || mapped.Schema()[0].Usage.Type != newResult || call.Operation != identity || provider.Operation() != operation {
				t.Fatal("incomplete input/result remap")
			}
			if p.InputUsage().Type != input || p.ResultUsage().Type == newResult {
				t.Fatal("retained provider mutated")
			}
			state, err := provider.Init(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.Finalize(context.Background(), state); err == nil {
				t.Fatal("old provider accepted reminted state")
			}
			value, err := provider.Finalize(context.Background(), state)
			if err != nil || value.Type() != newResult || !value.IsNull() {
				t.Fatal(value, err)
			}
		}
	}
}
