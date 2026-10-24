package exec

import (
	"context"
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func invocationUnitExists(t *testing.T, frame *Context, root *plan.ProjectNode) bool {
	t.Helper()
	boolean := asyncUnitType(t, sqltype.Bool)
	value, err := frame.EvalSubquery(plan.SubqueryExpr{Info: expr.Info{Usage: boolean.Usage()}, Kind: parser.SubqueryExists, PresenceOnly: true, QueryPlan: &plan.Query{Root: root}})
	if err != nil {
		t.Fatal(err)
	}
	present, err := value.Boolean()
	if err != nil {
		t.Fatal(err)
	}
	return present
}
func invocationUnitNoOptionalResources(t *testing.T, f *invocationUnitFixture) {
	t.Helper()
	for _, p := range f.providers {
		if p.peakRequests != 0 {
			t.Fatal("excluded invocation owned optional async requests", p.peakRequests)
		}
	}
}
func TestFDWAsyncInvocationReuse(t *testing.T) {
	t.Run("exists_parameters_and_reuse", func(t *testing.T) {
		f := newInvocationUnitFixture(t, true)
		for _, id := range []int64{3, 4, 9, 3} {
			frame := f.frame(t, id, 3)
			if got := invocationUnitExists(t, &frame, f.root); got != (id != 9) {
				t.Fatalf("correlation %d matched=%v", id, got)
			}
			f.checkReusable(t)
			for _, p := range f.providers {
				if len(p.scans) != 0 {
					parameters := p.scans[len(p.scans)-1].parameters
					if !reflect.DeepEqual(parameters.Values, []driver.Value{int64(3)}) || !reflect.DeepEqual(parameters.Correlation, []driver.Value{id}) {
						t.Fatalf("stale exported parameters for %d: %+v", id, parameters)
					}
				}
			}
		}
		invocationUnitNoOptionalResources(t, f)
		if err := f.owner.Finish(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, p := range f.providers {
			if p.liveScans != 0 || p.liveRequests != 0 || p.ends != 1 {
				t.Fatal("sole owner did not retire the reusable frontier", p.liveScans, p.liveRequests, p.ends)
			}
		}
	})
	t.Run("duplicate_multiplicity_and_current_rescan", func(t *testing.T) {
		f := newInvocationUnitFixture(t, true)
		for _, id := range []int64{1, 3, 9, 1} {
			frame := f.frame(t, id, 1)
			child := f.state
			child.Outer = &frame
			stage, err := OpenQueryProjectStage(context.Background(), &child, f.root)
			if err != nil {
				t.Fatal(err)
			}
			var got []int64
			row := make([]sqltype.Value, 1)
			for {
				present, err := stage.Next(context.Background(), row)
				if err != nil {
					_ = stage.Close()
					t.Fatal(err)
				}
				if !present {
					break
				}
				value, err := row[0].Integer()
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, value)
			}
			if err := child.finishInvocation(&stage, nil); err != nil {
				t.Fatal(err)
			}
			var want []int64
			if id == 1 {
				want = []int64{1, 1}
			} else if id == 3 {
				want = []int64{3}
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("id %d got %v want %v", id, got, want)
			}
			f.checkReusable(t)
		}
		invocationUnitNoOptionalResources(t, f)
	})
}

func invocationUnitDescendant(t *testing.T, f *invocationUnitFixture, shape string) *plan.ProjectNode {
	t.Helper()
	root := f.root
	switch shape {
	case "set":
		return root
	case "nested_set":
		empty := &plan.ProjectNode{QuerySchema: f.columns, Frame: 5, Child: &plan.ValuesNode{QuerySchema: f.columns}, Expressions: []expr.Expr{f.column(5)}}
		set := &plan.SetNode{QuerySchema: f.columns, Left: root, Right: empty, Operation: "union", All: true, LeftExpressions: root.Expressions, RightExpressions: empty.Expressions}
		return &plan.ProjectNode{QuerySchema: f.columns, Frame: 6, Child: set, Expressions: []expr.Expr{f.column(6)}}
	case "derived":
		derived := &plan.DerivedNode{QuerySchema: f.columns, Query: &plan.Query{Root: root}, ScanFrame: 5}
		return &plan.ProjectNode{QuerySchema: f.columns, Frame: 5, Child: derived, Expressions: []expr.Expr{f.column(5)}}
	case "view":
		input := &Source{Sources: f.sources}
		view := &Source{FlatWidth: 1, Materialized: true, NestedRoot: root, NestedInputs: input}
		f.state.Source = &Source{Sources: []*Source{view}}
		return &plan.ProjectNode{QuerySchema: f.columns, Frame: 5, Child: &plan.SourceNode{QuerySchema: f.columns}, Expressions: []expr.Expr{f.column(5)}}
	case "rule_invocation":
		copy := *root
		copy.RuleInvocation = true
		return &copy
	case "materialized_cte":
		definition := &plan.CTEDefinition{Query: &plan.Query{Root: root}, Owner: 7, Hint: parser.CTEMaterialized, References: 1}
		scan := &plan.DerivedNode{QuerySchema: f.columns, Query: definition.Query, CTE: definition, ScanFrame: 7}
		return &plan.ProjectNode{QuerySchema: f.columns, Frame: 7, CTEOwner: true, Child: scan, Expressions: []expr.Expr{f.column(7)}}
	case "recursive_seed":
		definition := &plan.CTEDefinition{Owner: 6, Recursive: true, UserWidth: 1}
		output := &Source{FlatWidth: 1, Materialized: true}
		owner := &CTEInvocation{Frame: 6, Producers: map[*plan.CTEDefinition]*CTEProducer{definition: {Source: output}}}
		f.state.Ctes = owner
		empty := &plan.ProjectNode{QuerySchema: f.columns, Frame: 5, Child: &plan.ValuesNode{QuerySchema: f.columns}, Expressions: []expr.Expr{f.column(5)}}
		recursive := &plan.RecursiveUnionNode{SetNode: plan.SetNode{QuerySchema: f.columns, Left: root, Right: empty, All: true, Operation: "union", LeftExpressions: root.Expressions, RightExpressions: empty.Expressions}, Definition: definition}
		return &plan.ProjectNode{QuerySchema: f.columns, Frame: 6, Child: recursive, Expressions: []expr.Expr{f.column(6)}}
	}
	t.Fatalf("unknown descendant %s", shape)
	return nil
}

func TestFDWAsyncInvocationDescendants(t *testing.T) {
	for _, shape := range []string{"set", "nested_set", "derived", "view", "rule_invocation", "materialized_cte", "recursive_seed"} {
		for _, enabled := range []bool{false, true} {
			name := shape + "/disabled"
			if enabled {
				name = shape + "/enabled"
			}
			t.Run(name, func(t *testing.T) {
				f := newInvocationUnitFixture(t, false)
				f.state.AsyncAppend = enabled
				root := invocationUnitDescendant(t, f, shape)
				for _, id := range []int64{3, 4, 9, 3} {
					frame := f.frame(t, id, 3)
					if !invocationUnitExists(t, &frame, root) {
						t.Fatal("descendant lost its first actual row")
					}
					f.checkReusable(t)
				}
				invocationUnitNoOptionalResources(t, f)
			})
		}
	}
	t.Run("safe_enclosing_cte_owned_by_owner_not_reader", func(t *testing.T) {
		f := newInvocationUnitFixture(t, false)
		definition := &plan.CTEDefinition{Query: &plan.Query{Root: f.root}, Owner: 7, Hint: parser.CTEMaterialized, References: 2}
		owner := &CTEInvocation{Frame: 7, State: f.state}
		owner.State.Ctes = owner
		t.Cleanup(func() {
			if err := owner.Close(); err != nil {
				t.Error(err)
			}
		})
		frame := f.frame(t, 3, 3)
		readerState := f.state
		readerState.Outer = &frame
		readerState.Ctes = owner
		reader, err := OpenCTEScan(context.Background(), &readerState, &plan.DerivedNode{QuerySchema: f.columns, Query: definition.Query, CTE: definition})
		if err != nil {
			t.Fatal(err)
		}
		if _, present, err := reader.NextOccurrence(context.Background()); err != nil || !present {
			t.Fatal("safe CTE demand", present, err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		producer := owner.Producers[definition]
		if producer == nil || producer.Closed {
			t.Fatal("excluded reader disposed its enclosing producer")
		}
		totalPeak := 0
		for _, p := range f.providers {
			totalPeak += p.peakRequests
			if p.ends != 0 {
				t.Fatal("reader Ended shared source")
			}
		}
		if totalPeak == 0 {
			t.Fatal("safe enclosing CTE lost async capability")
		}
		if err := f.owner.Quiesce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("safe_top_level_view", func(t *testing.T) {
		f := newInvocationUnitFixture(t, false)
		root := invocationUnitDescendant(t, f, "view")
		reader, err := OpenQueryProjectStage(context.Background(), &f.state, root)
		if err != nil {
			t.Fatal(err)
		}
		if _, present, err := reader.NextOccurrence(context.Background()); err != nil || !present {
			t.Fatal("safe view demand", present, err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		view := f.state.Source.Sources[0]
		if view.NestedStage == nil || view.NestedStage.Closed {
			t.Fatal("borrowed reader closed retained view producer")
		}
		totalPeak := 0
		for _, p := range f.providers {
			totalPeak += p.peakRequests
			if p.ends != 0 {
				t.Fatal("reader Ended safe view source")
			}
		}
		if totalPeak == 0 {
			t.Fatal("safe top-level view lost async capability")
		}
		if err := f.owner.Quiesce(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("safe_frontier_with_expression_invocations", testInvocationInsideSafeFrontier)
}

type invocationUnitProjection struct {
	plan.ColumnExpr
	subquery plan.SubqueryExpr
	fixture  *invocationUnitFixture
	t        *testing.T
}

func (p invocationUnitProjection) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := p.subquery.Eval(c); err != nil {
		return sqltype.Value{}, err
	}
	p.fixture.checkReusable(p.t)
	return p.ColumnExpr.Eval(c)
}
func testInvocationInsideSafeFrontier(t *testing.T) {
	f := newInvocationUnitFixture(t, false)
	inner := f.root
	boolean := asyncUnitType(t, sqltype.Bool)
	subquery := plan.SubqueryExpr{Info: expr.Info{Usage: boolean.Usage()}, Kind: parser.SubqueryExists, PresenceOnly: true, QueryPlan: &plan.Query{Root: inner}}
	var outerProviders []*invocationUnitProvider
	outerSources := append([]*Source{}, f.sources...)
	for _, rows := range [][]int64{{3, 4}, {9}} {
		provider := &invocationUnitProvider{rows: rows}
		outerProviders = append(outerProviders, provider)
		template := f.sources[0].Foreign
		foreign := &ForeignQuerySource{Wrapper: provider, Columns: f.columns, Request: template.Request, ExportValue: template.ExportValue, ImportValue: template.ImportValue}
		f.owner.Add(foreign)
		outerSources = append(outerSources, &Source{Foreign: foreign})
	}
	f.state.Source = &Source{Sources: outerSources}
	root := f.union(2, 3)
	set := root.Child.(*plan.SetNode)
	for _, arm := range []*plan.ProjectNode{set.Left, set.Right} {
		arm.Expressions = []expr.Expr{invocationUnitProjection{ColumnExpr: f.column(2).(plan.ColumnExpr), subquery: subquery, fixture: f, t: t}}
	}
	set.LeftExpressions, set.RightExpressions = set.Left.Expressions, set.Right.Expressions
	stage, err := OpenQueryProjectStage(context.Background(), &f.state, root)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	var got []int64
	row := make([]sqltype.Value, 1)
	for {
		present, err := stage.Next(context.Background(), row)
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		id, err := row[0].Integer()
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	if len(got) != 3 {
		t.Fatal("safe parent lost rows", got)
	}
	seen := map[int64]int{}
	for _, id := range got {
		seen[id]++
	}
	if !reflect.DeepEqual(seen, map[int64]int{3: 1, 4: 1, 9: 1}) {
		t.Fatal("safe parent bag", got)
	}
	f.checkReusable(t)
	invocationUnitNoOptionalResources(t, f)
	totalPeak := 0
	for _, p := range outerProviders {
		totalPeak += p.peakRequests
	}
	if totalPeak == 0 {
		t.Fatal("invocation exclusion disabled its safe enclosing append")
	}
}

var _ catalog.ForeignDataWrapper = (*invocationUnitProvider)(nil)
