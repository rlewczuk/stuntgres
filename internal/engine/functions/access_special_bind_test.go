package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Unexpected binder calls fail through the embedded nil interface. In
// particular, no routine association is reachable in either failing signature.
type accessSpecialEligibilityBinder struct {
	Binder
	args       []Argument
	analyzed   int
	coerced    []CoercionIntent
	signatures [][]string
	rejection  error
}

func (b *accessSpecialEligibilityBinder) Type(id sqltype.ID) *sqltype.Descriptor {
	return aclTestTypes[id]
}
func (b *accessSpecialEligibilityBinder) AnalyzeArgument(parser.Expr, AnalysisRequest) (Argument, error) {
	arg := b.args[b.analyzed]
	b.analyzed++
	return arg, nil
}
func (b *accessSpecialEligibilityBinder) AnalyzeFilter(parser.Expr) error { return nil }
func (b *accessSpecialEligibilityBinder) DefaultConsumer() bool           { return false }
func (b *accessSpecialEligibilityBinder) BuiltinVisible(parser.ObjectName) (bool, error) {
	return true, nil
}
func (b *accessSpecialEligibilityBinder) SignatureError(_ parser.ObjectName, _ parser.Position, names []string) error {
	b.signatures = append(b.signatures, append([]string(nil), names...))
	return b.rejection
}
func (b *accessSpecialEligibilityBinder) CoerceArgument(arg Argument, request CoercionRequest) (expr.Expr, error) {
	b.coerced = append(b.coerced, request.Intent)
	if arg.Kind != DeferredStringArgument || request.Intent != FinalizeArgument {
		return nil, errors.New("unexpected argument finalization")
	}
	// Use the actual descriptor input codec, not a manufactured 22P02.
	value, err := request.Target.Type.Input(context.Background(), "x", -1)
	if err != nil {
		return nil, err
	}
	return aclTestLiteral(value), nil
}

func TestAccessSpecialBinderEligibility(t *testing.T) {
	raw := parser.FunctionExpr{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "has_largeobject_privilege"}}},
		Args: []parser.Expr{parser.LiteralExpr{}, parser.LiteralExpr{}, parser.LiteralExpr{}},
	}
	for _, first := range []ArgumentKind{DeferredStringArgument, UnresolvedParameterArgument} {
		b := &accessSpecialEligibilityBinder{
			args:      []Argument{{Kind: first}, {Kind: DeferredStringArgument}, {Usage: aclTestTypes[sqltype.Bool].Usage()}},
			rejection: errors.New("full signature rejected"),
		}
		_, err := bindAccessInquiry(b, raw)
		if !errors.Is(err, b.rejection) || b.analyzed != 3 || len(b.coerced) != 0 || !reflect.DeepEqual(b.signatures, [][]string{{"unknown", "unknown", "boolean"}}) {
			t.Fatal("late typed rejection must precede all coercion/finalization", first, err, b.analyzed, b.coerced, b.signatures)
		}
	}
	b := &accessSpecialEligibilityBinder{
		args: []Argument{{Kind: DeferredStringArgument}, {Kind: DeferredStringArgument}, {Usage: aclTestTypes[sqltype.Text].Usage()}},
	}
	_, err := bindAccessInquiry(b, raw)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22P02" || diagnostic.Message != `invalid input syntax for type oid: "x"` || b.analyzed != 3 || !reflect.DeepEqual(b.coerced, []CoercionIntent{FinalizeArgument, FinalizeArgument}) || len(b.signatures) != 0 {
		t.Fatal("eligible signature must reach real OID input failure", err, b.analyzed, b.coerced, b.signatures)
	}
}
