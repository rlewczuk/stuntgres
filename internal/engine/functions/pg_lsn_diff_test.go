package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

type pgLSNDiffUnitRoutine uint32

func (r pgLSNDiffUnitRoutine) RoutineOID() uint32 { return uint32(r) }

func TestPGLSNDiffProviderLifecycle(t *testing.T) {
	types := inspectCarrierTypes()
	var order []int
	argument := func(d *sqltype.Descriptor, bits uint64, index int) stringUnitArgument {
		v, err := sqltype.PGLSNValue(d, bits)
		if err != nil {
			t.Fatal(err)
		}
		return stringUnitArgument{Info: expr.Info{Usage: d.Usage()}, value: v, order: &order, index: index}
	}
	args := [2]expr.Expr{argument(types[sqltype.PgLSN], ^uint64(0), 0), argument(types[sqltype.PgLSN], 0, 1)}
	info := expr.Info{Usage: types[sqltype.Numeric].Usage(), RoutineReference: pgLSNDiffUnitRoutine(3165)}
	n, err := newPGLSNDiff(info, args)
	if err != nil {
		t.Fatal(err)
	}
	c := &stringUnitContext{Context: context.Background()}
	for _, tc := range []struct {
		left, right uint64
		want        string
	}{
		{^uint64(0), 0, "18446744073709551615"},
		{0, ^uint64(0), "-18446744073709551615"},
		{^uint64(0), ^uint64(0), "0"},
		{1 << 63, (1 << 63) - 1, "1"},
	} {
		current, err := newPGLSNDiff(info, [2]expr.Expr{argument(types[sqltype.PgLSN], tc.left, 0), argument(types[sqltype.PgLSN], tc.right, 1)})
		if err != nil {
			t.Fatal(err)
		}
		order = nil
		v, err := current.Eval(c)
		if err != nil || v.Type() != types[sqltype.Numeric] || !reflect.DeepEqual(order, []int{0, 1}) {
			t.Fatal("exact numeric evaluation", v, order, err)
		}
		out, err := v.Type().Output(c.Context, v)
		if err != nil || out != tc.want {
			t.Fatal(out, tc.want, err)
		}
	}
	if n.ValidateStoredProvider() != nil || n.IntrinsicVolatility() != expr.Immutable || !n.LooksLikeFunction() {
		t.Fatal("immutable provider contract")
	}
	if folding, ok := any(n).(expr.Folding); ok && folding.PlanningFold() != expr.FoldConstant {
		t.Fatal("immutable arithmetic must remain planning-foldable")
	}
	// A second descriptor generation must replace all arithmetic backing pointers.
	nextTypes := inspectCarrierTypes()
	nextArgs := [2]expr.Expr{argument(nextTypes[sqltype.PgLSN], 0, 0), argument(nextTypes[sqltype.PgLSN], 1, 1)}
	nextInfo := info
	nextInfo.Usage = nextTypes[sqltype.Numeric].Usage()
	wantProvider, err := sqltype.BindArithmetic(sqltype.ArithmeticSubtract, nextTypes[sqltype.PgLSN], nextTypes[sqltype.PgLSN], nextTypes[sqltype.Numeric])
	if err != nil || wantProvider == n.Provider {
		t.Fatal("independent descriptor generations", err)
	}
	for _, reconstruct := range []func(expr.Info, []expr.Expr) (expr.Expr, error){n.Rebuild, n.Substitute} {
		next, err := reconstruct(nextInfo, nextArgs[:])
		if err != nil {
			t.Fatal(err)
		}
		got := next.(PGLSNDiff)
		if got.Provider != wantProvider || got.CallRoutineReference() != info.RoutineReference || got.ValidateStoredProvider() != nil {
			t.Fatal("reconstruction retained stale authority", got)
		}
		if !reflect.DeepEqual(n.Children(), args[:]) || n.Usage != info.Usage {
			t.Fatal("reconstruction mutated original operands")
		}
		for _, children := range [][]expr.Expr{nil, {nil, args[1]}, {stringUnitArgument{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}}, args[1]}} {
			if _, err := reconstruct(info, children); err == nil {
				t.Fatal("invalid children admitted", children)
			}
		}
		badInfo := info
		badInfo.Usage = types[sqltype.Int4].Usage()
		if _, err := reconstruct(badInfo, args[:]); err == nil {
			t.Fatal("wrong result admitted")
		}
	}
	for _, bad := range []PGLSNDiff{
		{Info: info, Args: args},
		{Info: nextInfo, Args: nextArgs, Provider: n.Provider},
		{Info: info, Args: [2]expr.Expr{nil, args[1]}, Provider: n.Provider},
		{Info: expr.Info{Usage: types[sqltype.Int4].Usage()}, Args: args, Provider: n.Provider},
	} {
		if bad.ValidateStoredProvider() == nil {
			t.Fatal("invalid provider admitted", bad)
		}
	}
	rendering, err := n.Render()
	if err != nil || rendering.Namespace != "pg_catalog" || !rendering.Replay.QuoteNamespace || rendering.Replay.Validate == nil || rendering.Replay.Validate() != nil {
		t.Fatal("replay rendering", rendering, err)
	}
	capture, ok, err := n.Capture(expr.CaptureFunction)
	if err != nil || !ok || capture.Count != 2 || !reflect.DeepEqual(capture.Inline, args) {
		t.Fatal("written capture", capture, ok, err)
	}
	if _, ok, err := n.Capture(expr.CaptureOperator); err != nil || ok {
		t.Fatal("operator capture", ok, err)
	}
	same := func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }
	if !n.SameGroup(n, same, false) {
		t.Fatal("group identity")
	}
	changed := n
	changed.Args[0] = args[1]
	if n.SameGroup(changed, same, false) {
		t.Fatal("changed operand grouped")
	}
	changed = n
	changed.Provider = wantProvider
	if n.SameGroup(changed, same, false) {
		t.Fatal("changed provider grouped")
	}
	left := args[0].(stringUnitArgument)
	left.value = sqltype.Null(left.Usage.Type)
	n.Args[0] = left
	order = nil
	v, err := n.Eval(c)
	if err != nil || !v.IsNull() || v.Type() != types[sqltype.Numeric] || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("runtime strict NULL", v, order, err)
	}
	right := args[1].(stringUnitArgument)
	failure := errors.New("LSN difference child")
	right.err = failure
	n.Args[1] = right
	order = nil
	if _, err := n.Eval(c); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal("runtime NULL hid sibling error", order, err)
	}
	left.literal = true
	n.Args[0] = left
	order = nil
	v, err = n.Eval(c)
	if err != nil || !v.IsNull() || v.Type() != types[sqltype.Numeric] || len(order) != 0 {
		t.Fatal("planning-known strict NULL", v, order, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Context = ctx
	if _, err := n.Eval(c); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancellation", err)
	}
}

type pgLSNDiffJumbleFields struct{ fields []any }

func (s *pgLSNDiffJumbleFields) JumbleText(v string)      { s.fields = append(s.fields, v) }
func (s *pgLSNDiffJumbleFields) JumbleUint64(v uint64)    { s.fields = append(s.fields, v) }
func (s *pgLSNDiffJumbleFields) JumbleBool(v bool)        { s.fields = append(s.fields, v) }
func (s *pgLSNDiffJumbleFields) JumbleConst(v sqltype.ID) { s.fields = append(s.fields, v) }

func TestPGLSNDiffStatementJumble(t *testing.T) {
	types := inspectCarrierTypes()
	info := expr.Info{Usage: types[sqltype.Numeric].Usage(), RoutineReference: pgLSNDiffUnitRoutine(3165)}
	args := [2]expr.Expr{
		stringUnitArgument{Info: expr.Info{Usage: types[sqltype.PgLSN].Usage()}, index: 0},
		stringUnitArgument{Info: expr.Info{Usage: types[sqltype.PgLSN].Usage()}, index: 1},
	}
	bound, err := newPGLSNDiff(info, args)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		node PGLSNDiff
	}{
		{"zero", PGLSNDiff{}},
		{"bound", bound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var projection expr.StatementJumbler = tc.node
			var sink pgLSNDiffJumbleFields
			projection.JumbleFields(&sink)
			if !reflect.DeepEqual(sink.fields, []any{"functions.PGLSNDiff"}) {
				t.Fatalf("static discriminator stream: got %#v", sink.fields)
			}
		})
	}
}
