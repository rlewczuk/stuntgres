package enginetests_test

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

var walUnitCalls = []string{"pg_available_wal_summaries()", "pg_wal_summary_contents(1,'0/0','0/1')", "pg_get_wal_summarizer_state()"}

func TestWALSummaryDeclarations(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	labels := [][]string{{"tli", "start_lsn", "end_lsn"}, {"relfilenode", "reltablespace", "reldatabase", "relforknumber", "relblocknumber", "is_limit_block"}, {"summarized_tli", "summarized_lsn", "pending_lsn", "summarizer_pid"}}
	types := [][]ids.OID{{20, 3220, 3220}, {26, 26, 26, 21, 20, 16}, {20, 3220, 3220, 23}}
	for i, call := range walUnitCalls {
		node, _, _ := strictNullUnitBind(t, e, op, scope, call)
		n := node.(functions.WALSummary)
		d, err := exec.ExpressionRoutineDescriptor(n)
		rows := float32(100)
		if i == 2 {
			rows = 0
		}
		if err != nil || d == nil || d.Address() != (ids.ObjectAddress{ClassID: 1255, ObjectID: ids.OID(6321 + i)}) || d.Result != 2249 || d.ReturnsSet() != (i != 2) || d.RowEstimate() != rows || d.Cost != 1 || !d.Strict || d.Volatility != expr.Volatile || d.Builtin == nil || d.Builtin.Parallel != 's' {
			t.Fatal("released declaration", d, err)
		}
		outputs := d.OutputDeclaration()
		inputs := 0
		if i == 1 {
			inputs = 3
		}
		if outputs == nil || outputs.Inputs() != inputs || outputs.Len() != len(labels[i]) {
			t.Fatal("declaration vector", outputs)
		}
		for j, label := range labels[i] {
			m := n.Usage.Type.CompositeMember(j)
			if m.Name != label || m.Attribute != int32(j+1) || m.Usage.Type.ID() != sqltype.ID(types[i][j]) || outputs.Name(inputs+j) != label || outputs.Type(j) != types[i][j] {
				t.Fatal("OUT member", m)
			}
		}
		if admitted, set := n.SourceAdmission(); !admitted || set != (i != 2) {
			t.Fatal("scalar/set admission")
		}
		if n.PlanningFold() != expr.FoldNever || n.PreflightFold() != expr.PreflightChildren || n.IntrinsicVolatility() != expr.Volatile || n.ClosedDefault(func(expr.Expr) bool { return true }) {
			t.Fatal("recipe planning policy")
		}
	}
}
func TestWALSummaryShapeValidation(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, call := range walUnitCalls {
		node, _, _ := strictNullUnitBind(t, e, op, scope, call)
		n := node.(functions.WALSummary)
		members := make([]sqltype.CompositeMember, n.Usage.Type.CompositeLen())
		for i := range members {
			members[i] = n.Usage.Type.CompositeMember(i)
		}
		for _, kind := range []string{"nil", "scalar", "count", "name", "type", "attribute", "kind", "arity", "slot", "child"} {
			bad := n
			switch kind {
			case "nil":
				bad.Usage.Type = nil
			case "scalar":
				bad.Usage.Type, _ = e.CatalogRead().LookupType(sqltype.Text)
			case "kind":
				bad.Kind = 255
			case "arity":
				bad.Arity++
			case "slot":
				if bad.Arity == 0 {
					bad.Slots[0] = 1
				} else {
					bad.Slots[1] = bad.Slots[0]
				}
			case "child":
				if bad.Arity == 0 {
					bad.Args[0] = n
				} else {
					bad.Args[0] = nil
				}
			default:
				changed := append([]sqltype.CompositeMember(nil), members...)
				switch kind {
				case "count":
					changed = changed[:len(changed)-1]
				case "name":
					changed[0].Name = "wrong"
				case "type":
					textType, _ := e.CatalogRead().LookupType(sqltype.Text)
					changed[0].Usage = textType.Usage()
				case "attribute":
					changed[0].Attribute = 9
				}
				shape, err := sqltype.RecordShape(changed)
				if err != nil {
					t.Fatal(err)
				}
				bad.Usage = shape.Usage()
			}
			if bad.Validate() == nil {
				t.Fatal("accepted malformed recipe", kind)
			}
		}
		if _, err := n.Rebuild(n.Info, append(n.Children(), n)); err == nil {
			t.Fatal("accepted child count")
		}
		bad := n.Info
		bad.Usage.Type = nil
		if _, err := n.Rebuild(bad, n.Children()); err == nil {
			t.Fatal("accepted malformed rebuilding")
		}
		if n.Arity == 3 {
			children := n.Children()
			children[0] = n
			if _, err := n.Rebuild(n.Info, children); err == nil {
				t.Fatal("accepted wrong operand usage")
			}
			bad := n
			bad.Slots[0] = 3
			if bad.Validate() == nil {
				t.Fatal("accepted out-of-range formal slot")
			}
		}
	}
}
func TestWALSummaryEvaluation(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, call := range walUnitCalls {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, call)
		n := node.(functions.WALSummary)
		before := n
		for range 2 {
			source, err := n.OpenSource(&evaluation)
			if err == nil || source.Type != nil || source.Values != nil || source.State != nil || source.Advance != nil {
				t.Fatal("published physical cursor", source, err)
			}
			state, ok := err.(interface{ SQLState() string })
			if !ok || state.SQLState() != "0A000" {
				t.Fatal(err)
			}
		}
		if !reflect.DeepEqual(before, n) {
			t.Fatal("mutated recipe")
		}
		canceled, cancel := context.WithCancel(context.Background())
		cancel()
		evaluation.Context = canceled
		if _, err := n.OpenSource(&evaluation); err != context.Canceled {
			t.Fatal("cancellation", err)
		}
	}
	// Genuine volatile children expose both written-order evaluation and exactly-once use.
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_wal_summary_contents(tli=>NULL::bigint,end_lsn=>('0/0'::pg_lsn+nextval('strict_null_steps'::regclass)::numeric),start_lsn=>('0/0'::pg_lsn+nextval('strict_null_steps'::regclass)::numeric))")
	n := node.(functions.WALSummary)
	sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "strict_null_steps")
	for iteration := int64(1); iteration <= 2; iteration++ {
		source, err := n.OpenSource(&evaluation)
		if err != nil || source.Type != n.Usage.Type || !source.Exhausted {
			t.Fatal("strict typed source", source, err)
		}
		eof, present, err := source.Next(context.Background())
		if err != nil || present || !eof.IsNull() || eof.Type() != n.Usage.Type {
			t.Fatal("typed EOF", eof, present, err)
		}
		source.Close()
		// The real sequence is read through its supported provider under the same owner.
		current, _, ctx := strictNullUnitBind(t, e, op, scope, "currval('strict_null_steps'::regclass)")
		value, err := current.Eval(&ctx)
		if err != nil {
			t.Fatal(err)
		}
		got, err := value.Integer()
		if err != nil || got != iteration*2 {
			t.Fatal("each written operand exactly once", got, err, sequence)
		}
	}
	node, _, evaluation = strictNullUnitBind(t, e, op, scope, "pg_wal_summary_contents(NULL::bigint,('0/0'::pg_lsn + (-1)::numeric),'0/1')")
	if _, err := node.(functions.WALSummary).OpenSource(&evaluation); err == nil {
		t.Fatal("sibling error suppressed by NULL")
	}
	for _, call := range []string{"pg_wal_summary_contents(0,'0/0','0/1')", "pg_wal_summary_contents(-1,'0/0','0/1')", "pg_wal_summary_contents(2147483648,'0/0','0/1')"} {
		node, _, evaluation := strictNullUnitBind(t, e, op, scope, call)
		source, err := node.(functions.WALSummary).OpenSource(&evaluation)
		if err == nil || source.Type != nil || source.State != nil || source.Values != nil {
			t.Fatal("range failure published cursor", source, err)
		}
		state, ok := err.(interface{ SQLState() string })
		if !ok || state.SQLState() != "22023" {
			t.Fatal("timeline range diagnostic", err)
		}
	}
}
func TestWALSummaryOwnership(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, _, evaluation := strictNullUnitBind(t, e, op, scope, "pg_wal_summary_contents(end_lsn=>'0/1',tli=>NULL::bigint,start_lsn=>'0/0')")
	n := node.(functions.WALSummary)
	before := n
	descriptor := n.Usage.Type
	children := n.Children()
	children[0] = nil
	if n.Args[0] == nil {
		t.Fatal("borrowed child array escaped")
	}
	children = n.Children()
	info := n.Info
	info.Occurrence++
	rebuilt, err := n.Rebuild(info, children)
	if err != nil {
		t.Fatal(err)
	}
	next := rebuilt.(functions.WALSummary)
	children[0] = nil
	if next.Args[0] != n.Args[0] || next.Usage.Type != descriptor || next.RoutineReference != n.RoutineReference || next.Slots != n.Slots || next.Occurrence != info.Occurrence || !reflect.DeepEqual(n, before) {
		t.Fatal("owned reconstruction/provenance")
	}
	captured, ok, err := n.Capture(expr.CaptureFunction)
	if err != nil || !ok || captured.Count != 3 || !captured.Indexed {
		t.Fatal("capture", captured, err)
	}
	for i := range 3 {
		child, err := n.CaptureOperand(i)
		if err != nil || child != n.Args[i] {
			t.Fatal("written capture identity", i, err)
		}
	}
	rendering, err := n.Render()
	if err != nil || rendering.Args[0].Label != "end_lsn" || rendering.Args[1].Label != "tli" || rendering.Args[2].Label != "start_lsn" {
		t.Fatal("formal presentation", rendering, err)
	}
	for range 2 {
		source, err := next.OpenSource(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		source.Close()
	}
	bad := info
	bad.Usage.Type = nil
	if _, err := n.Rebuild(bad, n.Children()); err == nil || n.Usage.Type != descriptor || !reflect.DeepEqual(n, before) {
		t.Fatal("rejected publication mutated original")
	}
}

func TestWALSummaryPreflight(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	node, b, evaluation := strictNullUnitBind(t, e, op, scope, "pg_wal_summary_contents(NULL::bigint,('0/0'::pg_lsn+$1::numeric),'0/1')")
	n := node.(functions.WALSummary)
	before := n
	if exec.OperatorExecutionConstant(n.Args[1], &evaluation) {
		t.Fatal("missing parameter treated as available")
	}
	for _, text := range []string{"-1", "0", "-1", "1"} {
		value, err := b.Params.Parameters[1].Type.Input(context.Background(), text, -1)
		if err != nil {
			t.Fatal(err)
		}
		evaluation.Parameters = []sqltype.Value{value}
		if !exec.OperatorExecutionConstant(n.Args[1], &evaluation) {
			t.Fatal("immutable supplied parameter unavailable")
		}
		next, _, err := exec.PreflightCoercions(&evaluation, n)
		if text == "-1" {
			state, ok := err.(interface{ SQLState() string })
			if !ok || state.SQLState() != "22023" {
				t.Fatal("immutable child preflight must fail", err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			rebuilt, ok := next.(functions.WALSummary)
			if !ok || rebuilt.RoutineReference != n.RoutineReference || rebuilt.Slots != n.Slots || rebuilt.Usage.Type != n.Usage.Type || rebuilt.Occurrence != n.Occurrence {
				t.Fatal("preflight lost parent recipe/provenance")
			}
			source, err := rebuilt.OpenSource(&evaluation)
			if err != nil || !source.Exhausted || source.Type != n.Usage.Type {
				t.Fatal("preflight lost strict typed empty", source, err)
			}
			source.Close()
		}
		if !reflect.DeepEqual(n, before) {
			t.Fatal("execution frame mutated original recipe")
		}
	}
	// These are actual bound providers, not synthetic evaluation probes.
	for _, text := range []string{
		"pg_wal_summary_contents(NULL::bigint,('0/0'::pg_lsn+((random())::numeric-2::numeric)),'0/1')",
		"pg_wal_summary_contents(NULL::bigint,('0/0'::pg_lsn+v::numeric),'0/1')",
	} {
		node, _, ctx := strictNullUnitBind(t, e, op, scope, text)
		runtime := node.(functions.WALSummary)
		if exec.OperatorExecutionConstant(runtime.Args[1], &ctx) {
			t.Fatal("runtime child admitted to planning", text)
		}
		next, _, err := exec.PreflightCoercions(&ctx, runtime)
		if err != nil {
			t.Fatal("runtime child evaluated during preflight", err)
		}
		if exec.OperatorExecutionConstant(next.(functions.WALSummary).Args[1], &ctx) {
			t.Fatal("preflight erased runtime dependence")
		}
	}
	// Conservative stage/domain facts on the actual child must remain barriers.
	child := n.Args[1].(plan.ArithmeticExpr)
	for _, property := range []expr.Properties{{StageDependent: true}, {DomainDependent: true}, {RuntimeDomainConversion: true}} {
		blocked := child
		blocked.Property = expr.MergeProperties(child.Property, property)
		if exec.OperatorExecutionConstant(blocked, &evaluation) {
			t.Fatal("stage/domain child admitted to planning", property)
		}
	}
	numeric, b, ctx := strictNullUnitBind(t, e, op, scope, "1::numeric+$1::numeric")
	value, err := b.Params.Parameters[1].Type.Input(context.Background(), "2", -1)
	if err != nil {
		t.Fatal(err)
	}
	ctx.Parameters = []sqltype.Value{value}
	next, _, err := exec.PreflightCoercions(&ctx, numeric)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := next.(plan.LiteralExpr); ok {
		t.Fatal("unrelated numeric arithmetic opted into frame folding")
	}
}
