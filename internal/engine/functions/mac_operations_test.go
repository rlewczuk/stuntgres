package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This probe deliberately exposes only the binder seams exercised by set7bit.
// All values and expected bytes remain independent of the binder's output.
type macOperationsBinder struct {
	Binder
	types             map[sqltype.ID]*sqltype.Descriptor
	arg               Argument
	analyzed, coerced int
	formal            sqltype.ID
}

func (b *macOperationsBinder) Type(id sqltype.ID) *sqltype.Descriptor { return b.types[id] }
func (b *macOperationsBinder) AnalyzeArgument(parser.Expr, AnalysisRequest) (Argument, error) {
	b.analyzed++
	return b.arg, nil
}
func (b *macOperationsBinder) AnalyzeFilter(parser.Expr) error                { return nil }
func (b *macOperationsBinder) BuiltinVisible(parser.ObjectName) (bool, error) { return true, nil }
func (b *macOperationsBinder) CoerceArgument(a Argument, request CoercionRequest) (expr.Expr, error) {
	b.coerced++
	if a.Usage != request.Target {
		return nil, errors.New("unexpected MAC coercion target")
	}
	return a.Expression, nil
}
func (b *macOperationsBinder) Info(u sqltype.Usage, p parser.Position, properties expr.Properties) expr.Info {
	return expr.Info{Usage: u, Pos: p, Property: properties}
}
func (b *macOperationsBinder) BuiltinRoutineInfo(info expr.Info, provider RoutineProvider, name string, formals ...sqltype.ID) (expr.Info, error) {
	if provider != NetworkRoutineProvider || name != "macaddr8_set7bit" || len(formals) != 1 {
		return expr.Info{}, errors.New("unexpected MAC routine authority")
	}
	b.formal = formals[0]
	info.RoutineReference = macaddr8UnitRoutine(4125)
	return info, nil
}

func TestMACOperationsProviderLifecycle(t *testing.T) {
	types := inspectCarrierTypes()
	ctx := &stringUnitContext{Context: context.Background()}
	for _, tc := range []struct {
		name          string
		input, result sqltype.ID
		bits, want    uint64
		oid           macaddr8UnitRoutine
	}{
		{"trunc", sqltype.Macaddr, sqltype.Macaddr, 0x1234567890ab, 0x123456000000, 753},
		{"trunc", sqltype.Macaddr8, sqltype.Macaddr8, 0x9234567890abcdef, 0x9234560000000000, 4112},
		{"macaddr8_set7bit", sqltype.Macaddr8, sqltype.Macaddr8, 0x8034567890abcdef, 0x8234567890abcdef, 4125},
		{"macaddr8_set7bit", sqltype.Macaddr, sqltype.Macaddr8, 0x003456abcdef, 0x023456fffeabcdef, 4125},
	} {
		t.Run(tc.name+"/"+types[tc.input].Name(), func(t *testing.T) {
			order := []int{}
			var value sqltype.Value
			var err error
			if tc.input == sqltype.Macaddr {
				value, err = sqltype.MACADDRValue(types[tc.input], tc.bits)
			} else {
				value, err = sqltype.MACADDR8Value(types[tc.input], tc.bits)
			}
			if err != nil {
				t.Fatal(err)
			}
			arg := stringUnitArgument{Info: expr.Info{Usage: types[tc.input].Usage()}, value: value, order: &order, index: 0}
			info := expr.Info{Usage: types[tc.result].Usage(), RoutineReference: tc.oid}
			n, err := newMACUnary(info, tc.name, arg)
			if err != nil {
				t.Fatal(err)
			}
			v, err := n.Eval(ctx)
			if err != nil || !reflect.DeepEqual(order, []int{0}) {
				t.Fatal(order, err)
			}
			var bits uint64
			if tc.result == sqltype.Macaddr {
				bits, err = v.MACADDR()
			} else {
				bits, err = v.MACADDR8()
			}
			if err != nil || bits != tc.want {
				t.Fatal(bits, tc.want, err)
			}
			if n.IntrinsicVolatility() != expr.Immutable || n.ValidateStoredProvider() != nil || !n.LooksLikeFunction() {
				t.Fatal("function protocol")
			}
			for _, rebuild := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
				next, err := rebuild(info, n.Children())
				if err != nil {
					t.Fatal(err)
				}
				got := next.(MACUnary)
				if got.Provider != n.Provider || got.CallRoutineReference() != tc.oid || got.ValidateStoredProvider() != nil {
					t.Fatal("rebuild authority", got)
				}
				if _, err := rebuild(info, nil); err == nil {
					t.Fatal("missing child admitted")
				}
			}
			rendering, err := n.Render()
			if err != nil || rendering.Namespace != "pg_catalog" || rendering.Replay.Validate == nil || rendering.Replay.Validate() != nil {
				t.Fatal(rendering, err)
			}
			capture, ok, err := n.Capture(expr.CaptureFunction)
			if err != nil || !ok || capture.Count != 1 || capture.Inline[0] != arg || capture.Inline[1] != nil {
				t.Fatal(capture, ok, err)
			}
			if _, ok, err := n.Capture(expr.CaptureOperator); err != nil || ok {
				t.Fatal(ok, err)
			}
			same := func(a, b expr.Expr) bool { return a.ResultUsage() == b.ResultUsage() }
			if !n.SameGroup(n, same, false) {
				t.Fatal("self equality")
			}
			for _, mutate := range []func(MACUnary) MACUnary{
				func(e MACUnary) MACUnary { e.Name = "abs"; return e },
				func(e MACUnary) MACUnary { e.Arg = nil; return e },
				func(e MACUnary) MACUnary { e.Usage = types[sqltype.Int4].Usage(); return e },
				func(e MACUnary) MACUnary { e.Provider = sqltype.MACUnary{}; return e },
				func(e MACUnary) MACUnary { e.RoutineReference = nil; return e },
				func(e MACUnary) MACUnary { e.RoutineReference = macaddr8UnitRoutine(830); return e },
			} {
				if mutate(n).ValidateStoredProvider() == nil {
					t.Fatal("corrupt provider admitted")
				}
			}
			foreign := inspectCarrierTypes()
			foreignValue, err := value.Retag(foreign[tc.input])
			if err != nil {
				t.Fatal(err)
			}
			foreignArg := arg
			foreignArg.Usage, foreignArg.value = foreign[tc.input].Usage(), foreignValue
			foreignInfo := info
			foreignInfo.Usage = foreign[tc.result].Usage()
			rebuilt, err := n.Rebuild(foreignInfo, []expr.Expr{foreignArg})
			if err != nil || rebuilt.(MACUnary).Provider == n.Provider || rebuilt.(MACUnary).ValidateStoredProvider() != nil {
				t.Fatal("fresh-descriptor reconstruction", err)
			}
			if n.SameGroup(rebuilt, same, false) {
				t.Fatal("provider ownership omitted from grouping")
			}
			bad := n
			bad.Provider = rebuilt.(MACUnary).Provider
			if bad.ValidateStoredProvider() == nil {
				t.Fatal("foreign provider admitted")
			}
			arg.value = sqltype.Null(types[tc.input])
			n.Arg, order = arg, nil
			v, err = n.Eval(ctx)
			if err != nil || !v.IsNull() || v.Type() != types[tc.result] || !reflect.DeepEqual(order, []int{0}) {
				t.Fatal("strict NULL", v, order, err)
			}
			failure := errors.New("MAC child failure")
			arg.err = failure
			n.Arg = arg
			if _, err := n.Eval(ctx); !errors.Is(err, failure) {
				t.Fatal("child error", err)
			}
		})
	}
	// The actual family binder analyzes and coerces exactly once, retaining829
	// as child usage and774 as both formal and result.
	v, err := sqltype.MACADDRValue(types[sqltype.Macaddr], 0x003456abcdef)
	if err != nil {
		t.Fatal(err)
	}
	arg := stringUnitArgument{Info: expr.Info{Usage: types[sqltype.Macaddr].Usage()}, value: v}
	b := &macOperationsBinder{types: types, arg: Argument{Expression: arg, Usage: arg.ResultUsage()}}
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "macaddr8_set7bit"}}}, Args: []parser.Expr{parser.ParameterExpr{Number: 1}}}
	node, err := bindMACSet7Bit(b, raw)
	if err != nil || b.analyzed != 1 || b.coerced != 1 || b.formal != sqltype.Macaddr8 || node.ResultUsage() != types[sqltype.Macaddr8].Usage() || node.(MACUnary).Arg.ResultUsage() != types[sqltype.Macaddr].Usage() {
		t.Fatal("binding protocol", node, b.analyzed, b.coerced, b.formal, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := node.Eval(&stringUnitContext{Context: canceled}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation", err)
	}
}
