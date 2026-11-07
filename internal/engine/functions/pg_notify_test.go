package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type pgNotifyUnitRuntime struct {
	events [][2]string
	err    error
}

func (r *pgNotifyUnitRuntime) QueueNotification(channel, payload string) error {
	if r.err != nil {
		return r.err
	}
	r.events = append(r.events, [2]string{channel, payload})
	return nil
}

type pgNotifyUnitContext struct {
	formatUnitContext
	*pgNotifyUnitRuntime
}

func pgNotifyText(types map[sqltype.ID]*sqltype.Descriptor) *sqltype.Descriptor {
	return types[sqltype.Text]
}

func pgNotifyUnitNode(types map[sqltype.ID]*sqltype.Descriptor, order *[]int, channel, payload sqltype.Value) PgNotify {
	text := pgNotifyText(types)
	return PgNotify{
		Info: expr.Info{Usage: types[sqltype.Void].Usage(), Property: expr.Properties{Volatility: expr.Volatile}},
		Args: [2]expr.Expr{
			formatUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: channel, order: order, index: 0},
			formatUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: payload, order: order, index: 1},
		},
	}
}

func TestPGNotifyNodeProtocol(t *testing.T) {
	types := formatUnitTypes()
	text := pgNotifyText(types)
	var order []int
	n := pgNotifyUnitNode(types, &order,
		sqltype.String(text, "chan"), sqltype.String(text, "payload"))

	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	badResult := n
	badResult.Usage = text.Usage()
	missing := n
	missing.Args[1] = nil
	wrongType := n
	wrongType.Args[0] = formatUnitArgument{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}}
	for name, node := range map[string]PgNotify{"result": badResult, "missing": missing, "type": wrongType} {
		if node.Validate() == nil {
			t.Fatalf("%s signature accepted", name)
		}
	}

	// Evaluation order is written order; NULL participates as the empty string.
	runtime := &pgNotifyUnitRuntime{}
	ctx := &pgNotifyUnitContext{formatUnitContext: formatUnitContext{Context: context.Background()}, pgNotifyUnitRuntime: runtime}
	value, err := n.Eval(ctx)
	if err != nil || value.Type() != types[sqltype.Void] || value.IsNull() || !reflect.DeepEqual(order, []int{0, 1}) || !reflect.DeepEqual(runtime.events, [][2]string{{"chan", "payload"}}) {
		t.Fatal("ordinary call", value, order, runtime.events, err)
	}
	empty := pgNotifyUnitNode(types, &order, sqltype.Null(text), sqltype.Null(text))
	order, runtime.events = nil, nil
	value, err = empty.Eval(ctx)
	if err != nil || value.IsNull() || !reflect.DeepEqual(runtime.events, [][2]string{{"", ""}}) {
		t.Fatal("NULL arguments", value, runtime.events, err)
	}
	// A child failure precedes the runtime call, and a runtime failure is bare.
	failure := errors.New("reached pg_notify child")
	failing := n
	failing.Args[0] = formatUnitArgument{Info: expr.Info{Usage: text.Usage()}, order: &order, index: 0, err: failure}
	order, runtime.events = nil, nil
	if _, err := failing.Eval(ctx); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0}) || len(runtime.events) != 0 {
		t.Fatal("child failure queued", order, runtime.events, err)
	}
	runtime.err = failure
	order = nil
	if _, err := n.Eval(ctx); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("queue failure", order, err)
	}
	if _, err := n.Eval(&formatUnitContext{Context: context.Background()}); err == nil {
		t.Fatal("queued without notification runtime")
	}

	first, second := n.SourceAdmission()
	if n.IntrinsicVolatility() != expr.Volatile || n.PlanningFold() != expr.FoldNever || n.StrictNull() != expr.StrictNone || n.PreflightFold() != expr.PreflightKeep || !n.LooksLikeFunction() || n.ClosedDefault(nil) || !first || second {
		t.Fatal("node declaration protocol")
	}
	if len(n.Children()) != 2 {
		t.Fatal("children", n.Children())
	}

	a := &captureMarker{Info: expr.Info{Usage: text.Usage()}, index: 0}
	b := &captureMarker{Info: a.Info, index: 1}
	recipe := PgNotify{Info: n.Info, Args: [2]expr.Expr{a, b}}
	info := n.Info
	mapped, err := expr.Substitute(recipe, info, []expr.Expr{b, a})
	if err != nil || mapped.(PgNotify).Args[0] != b || mapped.(PgNotify).Args[1] != a || recipe.Args[0] != a {
		t.Fatal("substitution copied caller storage", mapped, err)
	}
	if _, err := recipe.Rebuild(info, nil); err == nil {
		t.Fatal("short rebuild accepted")
	}
	if _, err := recipe.Rebuild(info, []expr.Expr{a, nil}); err == nil {
		t.Fatal("nil rebuild accepted")
	}
	rebuilt, err := recipe.Rebuild(info, []expr.Expr{b, a})
	rb, rebuiltOK := rebuilt.(PgNotify)
	if err != nil || !rebuiltOK || !reflect.DeepEqual(rb.Args[:], []expr.Expr{b, a}) {
		t.Fatal("rebuild", rebuilt, err)
	}
	rendered, err := n.Render()
	if err != nil || rendered.Tree != "PgNotify" || rendered.Form != expr.FormCall || rendered.Name != "pg_notify" || len(rendered.Args) != 2 || rendered.Args[0].Expr != n.Args[0] || rendered.Args[1].Expr != n.Args[1] {
		t.Fatal("render", rendered, err)
	}
	operands, ok, err := n.Capture(expr.CaptureFunction)
	if err != nil || !ok || operands.Check(n, 2) != nil {
		t.Fatal("capture", operands, ok, err)
	}
	for i, want := range n.Args {
		if got, err := n.CaptureOperand(i); err != nil || got != want {
			t.Fatal("capture operand", i, got, err)
		}
	}
	if _, err := n.CaptureOperand(2); err == nil {
		t.Fatal("extra capture operand accepted")
	}
	same := func(a, b expr.Expr) bool { return a == b }
	other := n
	other.Args = [2]expr.Expr{n.Args[1], n.Args[0]}
	if !n.SameGroup(n, same, false) || n.SameGroup(other, same, false) {
		t.Fatal("group identity")
	}
	if !n.SameSourceOccurrence(n) {
		t.Fatal("identical source occurrence")
	}
	otherInfo := n
	otherInfo.Usage = text.Usage()
	if n.SameSourceOccurrence(otherInfo) {
		t.Fatal("result type ignored")
	}
	if err := n.ValidateStoredProvider(); err != nil {
		t.Fatal(err)
	}
}

func TestPGNotifyRegistry(t *testing.T) {
	definition, ok := Lookup("pg_notify")
	if !ok || definition.Bind == nil || !definition.ExactSpelling || !definition.SharedGates || definition.OwnsLabels || definition.OwnsSpelling || definition.Claims != nil || definition.ProbeCall != nil || definition.NamedCall != nil {
		t.Fatalf("pg_notify registry row: %#v", definition)
	}
	for _, spelling := range []string{"PG_NOTIFY", "Pg_Notify", "pg_notif"} {
		if _, ok := Lookup(spelling); ok {
			t.Fatalf("%q claimed by the exact family", spelling)
		}
	}
}
