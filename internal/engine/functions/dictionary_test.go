package functions

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDictionaryLexizeLifecycle(t *testing.T) {
	types := inspectCarrierTypes()
	dictionary, err := sqltype.RegTSValue(types[sqltype.Regdictionary], 3765)
	if err != nil {
		t.Fatal(err)
	}
	order := []int{}
	args := [2]expr.Expr{stringUnitArgument{Info: expr.Info{Usage: types[sqltype.Regdictionary].Usage()}, value: dictionary, order: &order, index: 0}, stringUnitArgument{Info: expr.Info{Usage: types[sqltype.Text].Usage()}, value: sqltype.String(types[sqltype.Text], "WORD"), order: &order, index: 1}}
	node, err := newDictionaryLexize(expr.Info{Usage: types[1009].Usage()}, args)
	if err != nil {
		t.Fatal(err)
	}
	if err := node.ValidateStoredProvider(); err != nil {
		t.Fatal(err)
	}
	if node.IntrinsicVolatility() != expr.Immutable || node.PreflightFold() != expr.PreflightNullOrEvaluate {
		t.Fatal("lexize intrinsic policy")
	}
	rendering, err := node.Render()
	if err != nil || rendering.Namespace != "pg_catalog" || rendering.Replay.Validate == nil || rendering.Replay.Validate() != nil {
		t.Fatal("lexize replay", err)
	}
	capture, ok, err := node.Capture(expr.CaptureFunction)
	if err != nil || !ok || capture.Check(node, 2) != nil {
		t.Fatal("lexize capture", err)
	}
	for i := range 2 {
		if arg, err := node.CaptureOperand(i); err != nil || arg != args[i] {
			t.Fatal("captured operand", i, err)
		}
	}
	if _, err := node.CaptureOperand(2); err == nil {
		t.Fatal("extra capture operand")
	}
	if _, err := node.Rebuild(node.Info, nil); err == nil {
		t.Fatal("missing operands")
	}
	rebuilt, err := node.Rebuild(node.Info, node.Children())
	if err != nil || rebuilt.(DictionaryLexize).Validate() != nil {
		t.Fatal("rebuilt lexize", err)
	}
	substituted, err := node.Substitute(node.Info, node.Children())
	if err != nil || substituted.(DictionaryLexize).Validate() != nil {
		t.Fatal("substituted lexize", err)
	}
	if !node.SameGroup(node, func(a, b expr.Expr) bool { return a == b }, false) {
		t.Fatal("lexize grouping")
	}
	c := &stringUnitContext{Context: context.Background()}
	if _, err := node.Eval(c); err == nil {
		t.Fatal("lexize invented catalog runtime authority")
	}
	token := args[1].(stringUnitArgument)
	token.value = sqltype.Null(token.Usage.Type)
	token.literal = true
	node.Args[1] = token
	order = nil
	value, err := node.Eval(c)
	if err != nil || !value.IsNull() || value.Type() != types[1009] || len(order) != 0 {
		t.Fatal("typed strict NULL before runtime", value, err, order)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	c.Context = canceled
	if _, err := node.Eval(c); !errors.Is(err, context.Canceled) {
		t.Fatal("lexize cancellation", err)
	}
	definition, ok := Lookup("ts_lexize")
	if !ok || definition.Bind == nil || !definition.ExactSpelling || !definition.SharedGates {
		t.Fatal("lexize registry")
	}
}
