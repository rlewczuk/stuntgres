package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSourceProtocolSubstitution(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	original := jsonRecordUnitCall(t, b, "json_to_record", true)
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	value, err := original.Args[0].Eval(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// An equivalent executable child is not authority to select a new provider.
	// Its analysis descriptor is from another owner, but its evaluated datum is
	// exactly the original generation. Rebinding would silently retarget the
	// selected JSONRecordPlan and then reject that otherwise valid datum.
	fresh := jsonObjectUnitTypes()[sqltype.JSON]
	if fresh == value.Type() {
		t.Fatal("test requires distinct descriptor owners")
	}
	replacement := queryProbe(fresh.Usage(), func(*exec.Context) (sqltype.Value, error) { return value, nil })
	node := &plan.FunctionFromNode{Calls: []expr.Expr{original}}
	mapped, err := plan.RewriteQueryExpressions(node, func(child expr.Expr) (expr.Expr, bool, error) {
		if child == original.Args[0] {
			return replacement, true, nil
		}
		return child, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	call := mapped.(*plan.FunctionFromNode).Calls[0].(functions.JSONRecord)
	staged, err := b.MapQuerySetStage(original, func(child expr.Expr) (expr.Expr, bool, error) {
		if child == original.Args[0] {
			return replacement, true, nil
		}
		return child, false, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []functions.JSONRecord{original, call, staged.(functions.JSONRecord)} {
		got, err := candidate.Eval(ctx)
		if err != nil {
			t.Fatal("equivalent stage-child substitution rebound selected provider", err)
		}
		n, err := got.CompositeField(0).Integer()
		if err != nil || n != 2 || !got.CompositeField(1).IsNull() || got.Type() != original.ResultUsage().Type {
			t.Fatal("record value/layout changed", got, err)
		}
	}
	if call.Plan != original.Plan || call.SourcePosition() != original.SourcePosition() || call.Defined != original.Defined || node.Calls[0].(functions.JSONRecord).Args[0] != original.Args[0] {
		t.Fatal("selected provider, provenance or immutable template changed")
	}
	t.Run("surviving scalar across lowering and preflight", sourceProtocolSurvivorSubstitution)
}

func TestSourceProtocolOccurrence(t *testing.T) {
	d := seriesUnitType(t, sqltype.Int4)
	first := seriesUnit(t, d, 1, 3)
	otherArity := seriesUnit(t, d, 1, 3, 1)
	otherUsage := seriesUnit(t, seriesUnitType(t, sqltype.Int8), 1, 3)
	otherFamily := plan.SubscriptsExpr{Info: first.Info, Args: first.Args}
	for _, other := range []expr.Expr{otherArity, otherUsage, otherFamily} {
		if analyze.SameQuerySetOccurrence(first, other) || analyze.SameQuerySetOccurrence(other, first) {
			t.Fatal("distinct occurrence collapsed", other)
		}
	}
	binder := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	members, err := binder.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "pg_get_multixact_members"}}}, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}})
	if err != nil {
		t.Fatal(err)
	}
	if analyze.SameQuerySetOccurrence(members, members) {
		t.Fatal("legacy identityMembers nonreflexive occurrence changed")
	}
	// RegexpSet deliberately compares only family after the set/position guard.
	left := functions.RegexpSet{RegexpCall: functions.RegexpCall{Info: first.Info}}
	right := functions.RegexpSet{RegexpCall: functions.RegexpCall{Info: first.Info, Args: []expr.Expr{first.Args[0]}}}
	if !analyze.SameQuerySetOccurrence(left, right) {
		t.Fatal("legacy RegexpSet occurrence narrowed")
	}
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	record := jsonRecordUnitCall(t, b, "json_to_recordset", true)
	defined := record
	defined.Defined = !record.Defined
	if analyze.SameQuerySetOccurrence(record, defined) {
		t.Fatal("record definition shape collapsed")
	}
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "different", Usage: d.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	differentUsage := record
	differentUsage.Usage = shape.Usage()
	if analyze.SameQuerySetOccurrence(record, differentUsage) || analyze.SameQuerySetOccurrence(differentUsage, record) {
		t.Fatal("record usages collapsed")
	}
	engine, _ := intaggUnitInstalled(t)
	installed := intaggUnitEnum(t, engine, "{1,2}")
	changed := installed
	changed.Ref.Epoch++
	if analyze.SameQuerySetOccurrence(installed, changed) {
		t.Fatal("installed owner epoch collapsed")
	}
	copyProcedure := *installed.Procedure
	changed = installed
	changed.Procedure = &copyProcedure
	if !analyze.SameQuerySetOccurrence(installed, changed) {
		t.Fatal("equivalent installed descriptor identity changed")
	}
	copyProcedure.ObjectRevision++
	if analyze.SameQuerySetOccurrence(installed, changed) {
		t.Fatal("installed procedure revision collapsed")
	}
	changed = installed
	changed.Procedure = nil
	if analyze.SameQuerySetOccurrence(installed, changed) {
		t.Fatal("installed and builtin unnest collapsed")
	}
}

func TestSourceProtocolBorrowedLifetime(t *testing.T) {
	call := arrayRelationalUnitUnnest(t, "{1,NULL,3}")
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	first, err := exec.OpenFunctionSource(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := exec.OpenFunctionSource(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	retained, present, err := first.Next(ctx.Context)
	if err != nil || !present {
		t.Fatal(retained, present, err)
	}
	first.Close()
	for i, want := range []int64{1, 0, 3} {
		got, present, err := second.Next(ctx.Context)
		if err != nil || !present {
			t.Fatal(i, got, present, err)
		}
		if i == 1 {
			if !got.IsNull() {
				t.Fatal("present NULL lost")
			}
		} else if n, err := got.Integer(); err != nil || n != want {
			t.Fatal(i, got, err)
		}
	}
	if n, err := retained.Integer(); err != nil || n != 1 || retained.Type() != call.ResultUsage().Type {
		t.Fatal("close invalidated emitted borrowed datum", retained, err)
	}
	second.Close()
	canceled, cancel := context.WithCancel(ctx.Context)
	cancel()
	if _, _, err := second.Next(canceled); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation must win even after close", err)
	}
	multirange := moreUnitExpression(t, New().CatalogRead())
	members, err := exec.OpenFunctionSource(ctx, multirange)
	if err != nil {
		t.Fatal(err)
	}
	member, present, err := members.Next(ctx.Context)
	if err != nil || !present {
		t.Fatal(member, present, err)
	}
	members.Close()
	text, err := member.Type().Output(ctx.Context, member)
	if err != nil || text != "[1,3)" || member.Type() != multirange.ResultUsage().Type {
		t.Fatal("close invalidated borrowed multirange member", text, err)
	}
}

func TestSourceProtocolCursorOwnership(t *testing.T) {
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	d := seriesUnitType(t, sqltype.Numeric)
	call := numericSeriesUnit(t, d, "0.1", "0.3", "0.10")
	first, err := exec.OpenFunctionSource(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := exec.OpenFunctionSource(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	read := func(cursor *expr.SourceCursor, want string) sqltype.Value {
		t.Helper()
		value, present, err := cursor.Next(ctx.Context)
		if err != nil || !present {
			t.Fatal(value, present, err)
		}
		text, err := d.Output(ctx.Context, value)
		if err != nil || text != want || value.Type() != d {
			t.Fatal(text, want, err)
		}
		return value
	}
	retained := read(&first, "0.1")
	read(&first, "0.20")
	read(&second, "0.1")
	first.Close()
	read(&second, "0.20")
	read(&second, "0.30")
	if value, present, err := second.Next(ctx.Context); err != nil || present || !value.IsNull() || value.Type() != d {
		t.Fatal("typed numeric EOF", value, present, err)
	}
	text, err := d.Output(ctx.Context, retained)
	if err != nil || text != "0.1" {
		t.Fatal("mutable native state changed published value", text, err)
	}
	reopened, err := exec.OpenFunctionSource(ctx, call)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	read(&reopened, "0.1")
}

func TestSourceProtocolPresence(t *testing.T) {
	ctx := context.Background()
	integer := seriesUnitType(t, sqltype.Int4)
	empty, err := sqltype.Composite(50000, "empty", "empty", 50001, nil)
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(50002, "_empty", empty)
	if err != nil {
		t.Fatal(err)
	}
	// Presence survives zero physical width and SQL NULL in either zip direction.
	for _, input := range []string{"{}", "{()}", "{(),NULL,NULL}"} {
		value, err := array.Input(ctx, input, -1)
		if err != nil {
			t.Fatal(err)
		}
		call := plan.UnnestExpr{Info: expr.Info{Usage: empty.Usage()}, Args: []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: array.Usage()}, Value: value}}}
		for _, reverse := range []bool{false, true} {
			borrowed, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}, call)
			if err != nil {
				t.Fatal(err)
			}
			scalar := expr.ScalarSource(sqltype.Null(integer))
			calls := []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}}, call}
			invocation := exec.FunctionSetInvocation{Cursors: []expr.SourceCursor{scalar, borrowed}}
			if reverse {
				calls[0], calls[1] = calls[1], calls[0]
				invocation.Cursors[0], invocation.Cursors[1] = invocation.Cursors[1], invocation.Cursors[0]
			}
			row := make([]sqltype.Value, 1)
			want := max(1, value.ArrayLen())
			for i := 0; i <= want; i++ {
				present, err := invocation.NextFrom(ctx, calls, row)
				if err != nil || present != (i < want) || !row[0].IsNull() || row[0].Type() != integer {
					t.Fatal(input, reverse, i, present, row, err)
				}
			}
			invocation.Close()
		}
	}
}

func TestSourceProtocolCancellation(t *testing.T) {
	d := seriesUnitType(t, sqltype.Int4)
	call := seriesUnit(t, d, 1, 2)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	call.Args[0] = queryProbe(d.Usage(), func(*exec.Context) (sqltype.Value, error) {
		t.Fatal("precanceled open evaluated an argument")
		return sqltype.Value{}, nil
	})
	if cursor, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: canceled}}, call); !errors.Is(err, context.Canceled) || cursor.Type != nil {
		t.Fatal("precanceled source published state", cursor, err)
	}
	healthy := seriesUnit(t, d, 1, 1)
	cursor, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, healthy)
	if err != nil {
		t.Fatal(err)
	}
	defer cursor.Close()
	if _, present, err := cursor.Next(context.Background()); err != nil || !present {
		t.Fatal(present, err)
	}
	if _, present, err := cursor.Next(canceled); present || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost at exhausted boundary", present, err)
	}
	reopened, err := exec.OpenFunctionSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, healthy)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	value, present, err := reopened.Next(context.Background())
	n, _ := value.Integer()
	if err != nil || !present || n != 1 {
		t.Fatal("same node cannot recover", value, present, err)
	}
}

func TestSourceProtocolCleanup(t *testing.T) {
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	d := seriesUnitType(t, sqltype.Numeric)
	first := numericSeriesUnit(t, d, "0.1", "0.3", "0.10")
	failure := errors.New("later source argument")
	later := seriesUnit(t, seriesUnitType(t, sqltype.Int4), 1, 2)
	later.Args[0] = queryProbe(later.Args[0].ResultUsage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.Value{}, failure })
	invocation, err := exec.OpenFunctionInvocation(ctx, &plan.FunctionFromNode{Calls: []expr.Expr{first, later}})
	if !errors.Is(err, failure) || invocation.Cursors != nil {
		t.Fatal("partial open exposed an invocation", invocation, err)
	}
	// A failed sibling never consumes or mutates the retained numeric recipe.
	reopened, err := exec.OpenFunctionSource(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	value, present, err := reopened.Next(ctx.Context)
	text, outputErr := d.Output(ctx.Context, value)
	if err != nil || outputErr != nil || !present || text != "0.1" {
		t.Fatal("partial open changed retained recipe", text, present, err, outputErr)
	}
}

func sourceProtocolSurvivorSubstitution(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	engine := New()
	b := newTestExpressionBinder(t, ctx, engine, sqltype.InputEnvironment{}, nil)
	record := jsonRecordUnitCall(t, b, "json_to_record", true)
	input, err := record.Args[0].Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}})
	if err != nil {
		t.Fatal(err)
	}
	array, err := input.Type().Input(ctx, `[{"a":1}]`, -1)
	if err != nil {
		t.Fatal(err)
	}
	info := expr.Info{Usage: record.Usage, Property: expr.Properties{Volatility: expr.Stable, StageDependent: true}}
	member, err := functions.NewJSONRecord(info, "json_to_recordset", -1, []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: input.Type().Usage()}, Value: array}})
	if err != nil {
		t.Fatal(err)
	}
	populate, err := functions.NewJSONRecord(info, "json_populate_record", 0, []expr.Expr{member, record.Args[0]})
	if err != nil {
		t.Fatal(err)
	}
	fresh := jsonObjectUnitTypes()[sqltype.JSON]
	replacement := sourceProtocolRetainedDatum{Info: expr.Info{Usage: fresh.Usage()}, value: input}
	substituted, err := expr.Substitute(populate, info, []expr.Expr{member, replacement})
	if err != nil {
		t.Fatal(err)
	}
	call := substituted.(functions.JSONRecord)
	requests := []expr.Expr{call}
	b.Query.Frame = 1
	root, err := b.LowerQuerySets(&plan.SingletonNode{}, requests, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	project := &plan.ProjectNode{QuerySchema: plan.QuerySchema{{Name: "record", Usage: call.Usage}}, Child: root, Expressions: requests, Frame: 1, HasTargetSet: true}
	planned, err := view.PreflightQuery(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: engine.CatalogRead()}}, project)
	if err != nil {
		t.Fatal(err)
	}
	stage, err := exec.OpenNode(ctx, &exec.State{Source: &exec.Source{}, Frame: 1}, planned)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	row := make([]sqltype.Value, 1)
	present, err := stage.Next(ctx, row)
	if err != nil || !present {
		t.Fatal("surviving source scalar cannot execute", present, err)
	}
	n, err := row[0].CompositeField(0).Integer()
	if err != nil || n != 2 || !row[0].CompositeField(1).IsNull() || row[0].Type() != record.Usage.Type {
		t.Fatal("surviving source provider/layout changed", row, err)
	}
	if present, err := stage.Next(ctx, row); err != nil || present {
		t.Fatal("survivor cardinality", present, err)
	}
	if call.Plan != populate.Plan || call.Args[0].(functions.JSONRecord).Plan != member.Plan || populate.Args[1] != record.Args[0] {
		t.Fatal("survivor rewrite mutated retained providers/template")
	}
}

// This analysis probe participates in the lexical protocol, unlike queryProbe,
// so survivor lowering can traverse it without inventing stored SQL syntax.
type sourceProtocolRetainedDatum struct {
	expr.Info
	value sqltype.Value
}

func (e sourceProtocolRetainedDatum) Eval(expr.Context) (sqltype.Value, error) {
	return e.value, nil
}
func (sourceProtocolRetainedDatum) Children() []expr.Expr { return nil }
func (e sourceProtocolRetainedDatum) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if len(children) != 0 {
		return nil, errors.New("retained datum has no children")
	}
	e.Info = info
	return e, nil
}
func (sourceProtocolRetainedDatum) PlanningFold() expr.Fold       { return expr.FoldNever }
func (sourceProtocolRetainedDatum) PreflightFold() expr.Preflight { return expr.PreflightKeep }
