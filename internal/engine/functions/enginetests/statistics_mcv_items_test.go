package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func mcvItemsUnitCall(t *testing.T) functions.StatisticsMCVItems {
	t.Helper()
	e := engine.New()
	b := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(binderOperation(b))
	if err != nil {
		t.Fatal(err)
	}
	b.Query.Auth, b.Query.Frame = &auth, 1
	raw := enginetest.Statement(t, "SELECT pg_mcv_list_items(NULL::pg_mcv_list)").(*parser.Select).Targets[0].(parser.FunctionExpr)
	bound, _, err := b.BindFrom(&parser.FunctionTable{Calls: []parser.FunctionExpr{raw}})
	if err != nil {
		t.Fatal(err)
	}
	return bound.(*plan.FunctionFromNode).Calls[0].(functions.StatisticsMCVItems)
}

func TestStatisticsMCVItemsBinding(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	auth, err := e.AuthorizationLocked(binderOperation(b))
	if err != nil {
		t.Fatal(err)
	}
	b.Query.Auth, b.Query.Frame = &auth, 1
	var prior functions.StatisticsMCVItems
	for _, source := range []string{"pg_mcv_list_items(NULL::pg_mcv_list)", "pg_catalog.pg_mcv_list_items(mcv_list => NULL::pg_mcv_list)"} {
		raw := enginetest.Statement(t, "SELECT "+source).(*parser.Select).Targets[0].(parser.FunctionExpr)
		bound, ns, err := b.BindFrom(&parser.FunctionTable{Calls: []parser.FunctionExpr{raw}})
		if err != nil {
			t.Fatal(err)
		}
		call := bound.(*plan.FunctionFromNode).Calls[0].(functions.StatisticsMCVItems)
		if err := call.ValidateStoredProvider(); err != nil || call.Arg.ResultUsage() != b.Type(sqltype.PgMCVList).Usage() || call.RoutineReference == nil || call.RoutineReference.RoutineOID() != 3427 || call.IntrinsicVolatility() != expr.Stable || call.Properties().Volatility != expr.Stable || call.PlanningFold() != expr.FoldNever || call.PreflightFold() != expr.PreflightKeep || call.StrictNull() != expr.StrictAfterBare {
			t.Fatal(call, err)
		}
		admitted, set := call.SourceAdmission()
		if !admitted || !set || len(ns.Columns) != 5 {
			t.Fatal("source admission/shape", admitted, set, ns)
		}
		for i, id := range []sqltype.ID{23, 1009, 1000, 701, 701} {
			name := []string{"index", "values", "nulls", "frequency", "base_frequency"}[i]
			member := call.Usage.Type.CompositeMember(i)
			column := ns.Columns[i].Descriptor
			if member.Attribute != int32(i+1) || member.Name != name || member.Usage.Type != b.Type(id) || column.Name != name || column.Usage != member.Usage || column.Nullable || column.NullabilityKnown || column.OriginTable != 0 || column.OriginAttribute != 0 {
				t.Fatal("fixed computed OUT", i, member, column)
			}
		}
		if prior.Arg != nil && call.Usage.Type == prior.Usage.Type {
			t.Fatal("distinct call occurrences reused an OUT descriptor")
		}
		prior = call
		rebuilt, err := call.Rebuild(call.Info, call.Children())
		if err != nil || !call.SameSourceOccurrence(rebuilt) || !call.SameGroup(rebuilt, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, true) {
			t.Fatal("rebuild lost ownership", rebuilt, err)
		}
		rendering, err := call.Render()
		if err != nil || rendering.Name != "pg_mcv_list_items" || rendering.Replay.Validate() != nil || len(rendering.Inputs) != 1 {
			t.Fatal(rendering, err)
		}
		capture, ok, err := call.Capture(expr.CaptureFunction)
		if err != nil || !ok || capture.Count != 1 || capture.Check(call, 1) != nil {
			t.Fatal(capture, ok, err)
		}
		if operand, err := call.CaptureOperand(0); err != nil || !reflect.DeepEqual(operand, call.Arg) {
			t.Fatal("captured formal", operand, err)
		}
		if analysis, output, visibility := call.SnapshotDemand(func(expr.Expr) bool { return true }); analysis || output || visibility {
			t.Fatal("strict empty call acquired output authority")
		}
		if analysis, output, visibility := call.SnapshotDemand(func(expr.Expr) bool { return false }); analysis || !output || visibility {
			t.Fatal("typed dictionary lost presentation demand")
		}
		if _, err := call.Eval(nil); err == nil {
			t.Fatal("SRF scalar evaluation accepted")
		}
	}
	declaration := routine.BuiltinRoutine(3427)
	if declaration == nil || declaration.Result != 2249 || declaration.Identity.Name != "pg_mcv_list_items" || declaration.Identity.Signature != "5017" || !declaration.Strict || declaration.Volatility != expr.Stable || declaration.Parallel != 's' || !declaration.ReturnsSet || declaration.Providers != functions.EmbeddedRoutineProvider || declaration.Variadic != 0 || declaration.Defaults != 0 || declaration.Outputs == nil || declaration.Outputs.Inputs() != 1 || declaration.Outputs.Len() != 5 {
		t.Fatal("callable declaration", declaration)
	}
	for i, name := range []string{"mcv_list", "index", "values", "nulls", "frequency", "base_frequency"} {
		if declaration.Outputs.Name(i) != name {
			t.Fatal("formal/OUT names", i, declaration.Outputs)
		}
	}
	for i, id := range []sqltype.ID{23, 1009, 1000, 701, 701} {
		if sqltype.ID(declaration.Outputs.Type(i)) != id {
			t.Fatal("OUT identity", i)
		}
	}
}

func TestStatisticsMCVItemsCursor(t *testing.T) {
	call := mcvItemsUnitCall(t)
	integer := call.Usage.Type.CompositeMember(0).Usage.Type
	text := call.Usage.Type.CompositeMember(1).Usage.Type.Element()
	one, _ := sqltype.Integer(integer, 1)
	two, _ := sqltype.Integer(integer, 2)
	value, err := sqltype.StatisticsMCVBuild(t.Context(), call.Arg.ResultUsage().Type, []sqltype.Usage{integer.Usage(), text.Usage()}, [8][]sqltype.Value{{one, two}, {sqltype.String(text, "NULL")}}, []sqltype.StatisticsMCVItem{
		{Frequency: .5, BaseFrequency: .25},
		{Indexes: [8]uint16{1}, Nulls: [8]bool{false, true}, Frequency: .25, BaseFrequency: .0625},
	})
	if err != nil {
		t.Fatal(err)
	}
	opened := 0
	call.Arg = queryProbe(call.Arg.ResultUsage(), func(*exec.Context) (sqltype.Value, error) { opened++; return value, nil })
	evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: t.Context()}}
	first, err := call.OpenSource(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := call.OpenSource(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if opened != 2 || first.Position != 0 || second.Position != 0 || first.State == nil || first.State == second.State || first.Values != nil || second.Values != nil {
		t.Fatal("invocation ownership", opened, first, second)
	}
	for index, wanted := range []struct {
		values, nulls       string
		frequency, baseline float64
	}{{`{1,"NULL"}`, "{f,f}", .5, .25}, {"{2,NULL}", "{f,t}", .25, .0625}} {
		row, present, err := first.Next(t.Context())
		if err != nil || !present || row.Type() != call.Usage.Type {
			t.Fatal(row, present, err)
		}
		gotIndex, _ := row.CompositeField(0).Integer()
		actual, err := row.CompositeField(1).Type().Output(t.Context(), row.CompositeField(1))
		flags, flagErr := row.CompositeField(2).Type().Output(t.Context(), row.CompositeField(2))
		frequency, _ := row.CompositeField(3).Float()
		baseline, _ := row.CompositeField(4).Float()
		if gotIndex != int64(index) || actual != wanted.values || flags != wanted.nulls || frequency != wanted.frequency || baseline != wanted.baseline || err != nil || flagErr != nil || second.Position != 0 || opened != 2 {
			t.Fatal("independent row facts", gotIndex, actual, flags, frequency, baseline, err, flagErr)
		}
		for _, slot := range []int{1, 2} {
			array := row.CompositeField(slot)
			if array.ArrayRank() != 1 || array.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: 1, Length: 2}) {
				t.Fatal("array shape", array)
			}
		}
		if row.CompositeField(1).ArrayElement(1).IsNull() != (index == 1) || row.CompositeField(2).ArrayElement(1).IsNull() {
			t.Fatal("SQL NULL versus text NULL")
		}
	}
	for range 2 {
		row, present, err := first.Next(t.Context())
		if err != nil || present || !row.IsNull() || row.Type() != call.Usage.Type || first.State != nil || first.Value.Type() != nil || !first.Exhausted {
			t.Fatal("typed EOF released borrowed state", row, present, err, first)
		}
	}
	second.Close()
	if second.State != nil || second.Value.Type() != nil || !second.Exhausted {
		t.Fatal("early close retained typed construction")
	}
	for _, limit := range []int{1, 3} {
		cursor, err := call.OpenSource(evaluation)
		if err != nil {
			t.Fatal(err)
		}
		ctx := &mcvItemsCursorBudget{Context: context.Background(), remaining: limit}
		if _, present, err := cursor.Next(ctx); present || !errors.Is(err, context.Canceled) || cursor.State != nil || cursor.Value.Type() != nil || cursor.Position != 0 {
			t.Fatal("cancellation published prefix/retained owner", limit, present, err, cursor)
		}
	}
	failure := errors.New("demanded MCV operand")
	call.Arg = queryProbe(call.Arg.ResultUsage(), func(*exec.Context) (sqltype.Value, error) { return sqltype.Value{}, failure })
	if _, err := call.OpenSource(evaluation); !errors.Is(err, failure) {
		t.Fatal("operand error suppressed", err)
	}
	formal := value.Type().Usage()
	call.Arg = plan.LiteralExpr{Info: expr.Info{Usage: formal}, Value: sqltype.Null(formal.Type)}
	// Literal evaluation uses the real runtime; strict empty opening must not
	// request its deliberately unavailable output capability.
	empty, err := call.OpenSource(mcvItemsNullContext{Context: evaluation})
	if err != nil {
		t.Fatal(err)
	}
	if row, present, err := empty.Next(t.Context()); err != nil || present || row.Type() != call.Usage.Type || !row.IsNull() {
		t.Fatal("strict typed empty", row, present, err)
	}
	raw, err := value.Type().AppendBinaryWithEnvironment(t.Context(), nil, value, sqltype.OutputEnvironment{})
	if err != nil {
		t.Fatal(err)
	}
	image, err := sqltype.StatisticsMCVValue(formal.Type, raw)
	if err != nil {
		t.Fatal(err)
	}
	call.Arg = plan.LiteralExpr{Info: expr.Info{Usage: formal}, Value: image}
	if cursor, err := call.OpenSource(evaluation); err == nil || cursor.State != nil {
		t.Fatal("raw image masqueraded as typed construction", cursor, err)
	}
	t.Run("presentation-and-output-error", func(t *testing.T) {
		var binaryType *sqltype.Descriptor
		for _, typ := range sqltype.Builtins() {
			if typ.ID() == sqltype.Bytea {
				binaryType = typ
			}
		}
		binary, err := sqltype.Binary(binaryType, "\x00\x01\xff")
		if err != nil {
			t.Fatal(err)
		}
		value, err := sqltype.StatisticsMCVBuild(t.Context(), formal.Type, []sqltype.Usage{binaryType.Usage()}, [8][]sqltype.Value{{binary}}, []sqltype.StatisticsMCVItem{{Frequency: 1, BaseFrequency: 1}})
		if err != nil {
			t.Fatal(err)
		}
		call.Arg = plan.LiteralExpr{Info: expr.Info{Usage: formal}, Value: value}
		evaluation.Output.ByteaOutput = sqltype.ByteaHex
		captured, err := call.OpenSource(evaluation)
		if err != nil {
			t.Fatal(err)
		}
		defer captured.Close()
		evaluation.Output.ByteaOutput = sqltype.ByteaEscape
		current, err := call.OpenSource(evaluation)
		if err != nil {
			t.Fatal(err)
		}
		defer current.Close()
		for _, c := range []struct {
			cursor *expr.SourceCursor
			want   string
		}{{&captured, `\x0001ff`}, {&current, `\000\001\377`}} {
			row, present, err := c.cursor.Next(t.Context())
			if err != nil || !present {
				t.Fatal(present, err)
			}
			text, err := row.CompositeField(1).ArrayElement(0).Text()
			if err != nil || text != c.want {
				t.Fatal("invocation output policy", text, c.want, err)
			}
		}
		evaluation.Output.ByteaOutput = sqltype.ByteaOutput(255)
		broken, err := call.OpenSource(evaluation)
		if err != nil {
			t.Fatal(err)
		}
		if _, present, err := broken.Next(t.Context()); present || err == nil || broken.State != nil || broken.Value.Type() != nil || broken.Position != 0 {
			t.Fatal("output failure published prefix/retained dictionary", present, err, broken)
		}
	})
}

type mcvItemsNullContext struct{ *exec.Context }

func (mcvItemsNullContext) FunctionStringOutput() sqltype.OutputEnvironment {
	panic("strict empty MCV source requested output authority")
}

type mcvItemsCursorBudget struct {
	context.Context
	remaining int
}

func (c *mcvItemsCursorBudget) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}
