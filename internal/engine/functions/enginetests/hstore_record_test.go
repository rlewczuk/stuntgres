package enginetests

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestHstoreRecordExpressionProtocols(t *testing.T) {
	var text *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == 25 {
			text = d
		}
	}
	h, err := sqltype.Hstore(65000, "hstore", "public.hstore")
	if err != nil {
		t.Fatal(err)
	}
	pair, err := sqltype.Composite(65001, "pair", "public.pair", 65002, []sqltype.CompositeMember{{Attribute: 1, Name: "n", Usage: text.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	base, err := sqltype.CompositeValue(pair, []sqltype.Value{sqltype.String(text, "keep")})
	if err != nil {
		t.Fatal(err)
	}
	replacements, err := h.Input(context.Background(), "n=>changed", -1)
	if err != nil {
		t.Fatal(err)
	}
	literal := func(v sqltype.Value) expr.Expr {
		return plan.LiteralExpr{Info: expr.Info{Usage: v.Type().Usage()}, Value: v}
	}
	for _, member := range []uint8{51, 52} {
		call := functions.HstoreRecord{Info: expr.Info{Usage: h.Usage(), Occurrence: 19}, Hstore: h, Member: member, Namespace: "public", Name: "hstore", Ref: ids.ObjectRef{Address: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 65003}}}
		call.RoutineReference = &catalog.Procedure{ObjectAddress: call.Ref.Address}
		call.Args[0], call.Formals[0] = literal(base), pair
		if member == 52 {
			call.Name = "populate_record"
			call.Usage = pair.Usage()
			call.Args[1], call.Formals[1] = literal(replacements), h
		}
		if err := call.Validate(); err != nil {
			t.Fatal(err)
		}
		if call.StrictNull() != expr.StrictNone || call.PreflightFold() != expr.PreflightEvaluate || call.PlanningFold() != expr.FoldConstant {
			t.Fatal("nonstrict immutable protocols")
		}
		for _, f := range []func(expr.Info, []expr.Expr) (expr.Expr, error){call.Rebuild, call.Substitute} {
			next, err := f(call.Info, call.Children())
			if err != nil || !reflect.DeepEqual(next, call) || !call.SameGroup(next, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, false) {
				t.Fatal("reconstruction changed provenance", err)
			}
		}
		if _, err := call.Rebuild(call.Info, nil); err == nil {
			t.Fatal("invalid arity accepted")
		}
		node, changed, err := call.Retag(call.Info, call.Children(), false, expr.Retag{Type: func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }})
		if err != nil || changed || !reflect.DeepEqual(node, call) {
			t.Fatal("identity retag", err)
		}
		spec, err := call.Render()
		if err != nil || spec.Name != call.Name || spec.Namespace != "public" || len(spec.Args) != call.Arity() || spec.Args[0].TransportType != pair.Usage() {
			t.Fatal("effective replay usage", err)
		}
		for _, kind := range []expr.CaptureKind{expr.CaptureFunction, expr.CaptureOperator} {
			capture, ok, err := call.Capture(kind)
			if err != nil || !ok || capture.Count != call.Arity() {
				t.Fatal("capture", err)
			}
			for i := range call.Arity() {
				v, err := capture.At(call, i)
				if err != nil || !reflect.DeepEqual(v, call.Args[i]) {
					t.Fatal("ordered capture", err)
				}
			}
		}
		value, err := call.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		if err != nil {
			t.Fatal(err)
		}
		want := `"n"=>"keep"`
		if member == 52 {
			want = "(changed)"
		}
		got, err := value.Type().Output(context.Background(), value)
		if err != nil || got != want {
			t.Fatal("typed evaluation", got, err)
		}
		admitted, set := call.SourceAdmission()
		if admitted != (member == 52) || set {
			t.Fatal("scalar record source admission became an SRF")
		}
		if admitted {
			for _, wholeNull := range []bool{false, true} {
				source := call
				if wholeNull {
					source.Args[0], source.Args[1] = literal(sqltype.Null(pair)), literal(sqltype.Null(h))
				}
				cursor, err := source.OpenSource(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
				if err != nil {
					t.Fatal(err)
				}
				v, available, err := cursor.Next(context.Background())
				if err != nil || !available || v.Type() != pair || v.IsNull() != wholeNull {
					t.Fatal("scalar source lost its one tuple or whole-NULL framing", err)
				}
				if _, available, err := cursor.Next(context.Background()); err != nil || available {
					t.Fatal("scalar source returned more than one tuple", err)
				}
				cursor.Close()
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if v, err := call.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}); !errors.Is(err, context.Canceled) || v.Type() != nil {
			t.Fatal("cancellation", err)
		}
		foreign := *pair
		bad := call
		bad.Formals[0] = &foreign
		if err := bad.Validate(); err == nil {
			t.Fatal("foreign formal accepted")
		}
	}
}
