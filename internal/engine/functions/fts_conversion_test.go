package functions

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/sqltype"
	"testing"
)

// Synthetic lexical authority witnesses metadata that stock dictionaries do not
// emit. It is private, not a SQL dictionary or a synonym implementation.
type convertRichDictionary struct {
	prefix   bool
	variants bool
}

func (d convertRichDictionary) Filtering() bool { return false }
func (d convertRichDictionary) Lexize(ctx context.Context, input string) (fts.LexicalResult, error) {
	if err := ctx.Err(); err != nil {
		return fts.LexicalResult{}, err
	}
	words := []fts.Lexeme{{Text: "index", Prefix: d.prefix}}
	if d.variants {
		words = []fts.Lexeme{{Text: "first", Variant: 1}, {Text: "second", Variant: 1}, {Text: "third", Variant: 2}}
	}
	return fts.LexicalResult{Recognized: true, Lexemes: words}, nil
}
func TestFTSConvertLexemes(t *testing.T) {
	types := inspectCarrierTypes()
	for _, dictionaryPrefix := range []bool{false, true} {
		runtime := convertRichDictionary{prefix: dictionaryPrefix}
		normalize := func(input string) ([]fts.LexicalPosition, error) {
			lexical, err := runtime.Lexize(t.Context(), input)
			return []fts.LexicalPosition{{Position: 1, Lexemes: lexical.Lexemes}}, err
		}
		lexical, err := runtime.Lexize(t.Context(), "indices")
		if err != nil {
			t.Fatal(err)
		}
		projection, err := sqltype.LexicalArray(dictionaryArrayType(t), lexical)
		if err != nil || projection.ArrayLen() != 1 {
			t.Fatal(projection, err)
		}
		if text, err := projection.ArrayElement(0).Text(); err != nil || text != "index" {
			t.Fatal(text, err)
		}
		positions, err := normalize("indices")
		if err != nil {
			t.Fatal(err)
		}
		var builder fts.VectorBuilder
		for _, p := range positions {
			for _, word := range p.Lexemes {
				builder.Add(word.Text, p.Position)
			}
		}
		vector, err := builder.Finish(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		value, err := sqltype.TSVectorValue(types[sqltype.TSVector], vector)
		if err != nil {
			t.Fatal(err)
		}
		if text, err := value.Type().Output(t.Context(), value); err != nil || text != "'index':1" {
			t.Fatal(text, err)
		}
		for _, source := range []struct{ input, want string }{{"indices", "'index'"}, {"indices:AB", "'index':AB"}, {"indices:*AB", "'index':*AB"}} {
			want := source.want
			if dictionaryPrefix {
				want = "'index':*"
				if source.input != "indices" {
					want += "AB"
				}
			}
			query, _, err := fts.ConvertQuery(t.Context(), fts.StrictConversion, source.input, normalize)
			if err != nil {
				t.Fatal(err)
			}
			typed, err := sqltype.TSQueryValue(types[sqltype.TSQuery], query)
			if err != nil {
				t.Fatal(err)
			}
			if text, err := typed.Type().Output(t.Context(), typed); err != nil || text != want {
				t.Fatal(text, want, err)
			}
		}
	}
	variantRuntime := convertRichDictionary{variants: true}
	q, _, err := fts.ConvertQuery(t.Context(), fts.StrictConversion, "token", func(input string) ([]fts.LexicalPosition, error) {
		lexical, err := variantRuntime.Lexize(t.Context(), input)
		return []fts.LexicalPosition{{Position: 1, Lexemes: lexical.Lexemes}}, err
	})
	if err != nil || q.String() != "'first' & 'second' | 'third'" {
		t.Fatal(q, err)
	}
}

func TestFTSConvertLifecycle(t *testing.T) {
	types := inspectCarrierTypes()
	for kind := fts.VectorConversion; kind <= fts.WebConversion; kind++ {
		for _, explicit := range []bool{false, true} {
			var order []int
			result := sqltype.TSQuery
			if kind == fts.VectorConversion {
				result = sqltype.TSVector
			}
			node := FTSConversion{Info: expr.Info{Usage: types[result].Usage()}, Kind: kind, Explicit: explicit}
			for i := range node.arity() {
				typ := types[sqltype.Text]
				if explicit && i == 0 {
					typ = types[sqltype.Regconfig]
				}
				// NULL configuration needs no input resolver and still exercises checked type.
				node.Args[i] = stringUnitArgument{Info: expr.Info{Usage: typ.Usage()}, value: sqltype.Null(typ), order: &order, index: i}
			}
			wantVolatility := expr.Stable
			if explicit {
				wantVolatility = expr.Immutable
			}
			if err := node.Validate(); err != nil || node.IntrinsicVolatility() != wantVolatility || node.PreflightFold() != expr.PreflightNullOrEvaluate {
				t.Fatal(node, err)
			}
			c := &stringUnitContext{Context: context.Background()}
			v, err := node.Eval(c)
			if err != nil || !v.IsNull() || v.Type() != types[result] || len(order) != node.arity() {
				t.Fatal(v, order, err)
			}
			rebuilt, err := node.Rebuild(node.Info, node.Children())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := node.Substitute(node.Info, node.Children()); err != nil {
				t.Fatal(err)
			}
			if !node.SameGroup(rebuilt, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, true) {
				t.Fatal("rebuild identity")
			}
			other := node
			other.Kind = (kind + 1) % 5
			if node.SameGroup(other, func(a, b expr.Expr) bool { return true }, false) {
				t.Fatal("different kinds grouped")
			}
			if _, err := node.Rebuild(node.Info, nil); err == nil {
				t.Fatal("missing children")
			}
			bad := node
			bad.Args[0] = nil
			if bad.Validate() == nil {
				t.Fatal("missing operand")
			}
			bad = node
			bad.Usage = types[sqltype.Text].Usage()
			if bad.Validate() == nil {
				t.Fatal("wrong result")
			}
			spec, err := node.Render()
			if err != nil || spec.Namespace != "pg_catalog" {
				t.Fatal(spec, err)
			}
			capture, ok, err := node.Capture(expr.CaptureFunction)
			if err != nil || !ok || capture.Count != node.arity() {
				t.Fatal(capture, ok, err)
			}
			for i := range node.arity() {
				if arg, err := node.CaptureOperand(i); err != nil || arg != node.Args[i] {
					t.Fatal(arg, err)
				}
			}
			if _, err := node.CaptureOperand(node.arity()); err == nil {
				t.Fatal("invalid capture")
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			c.Context = canceled
			if _, err := node.Eval(c); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			c.Context = context.Background()
			first := node.Args[0].(stringUnitArgument)
			first.literal = true
			node.Args[0] = first
			order = nil
			if v, err := node.Eval(c); err != nil || !v.IsNull() || len(order) != 0 {
				t.Fatal(v, order, err)
			}
			if explicit {
				first.literal = false
				node.Args[0] = first
				sibling := node.Args[1].(stringUnitArgument)
				failure := errors.New("demanded child")
				sibling.err = failure
				node.Args[1] = sibling
				if _, err := node.Eval(c); !errors.Is(err, failure) {
					t.Fatal("NULL hid child failure", err)
				}
			}
			node.Args[node.arity()-1] = stringUnitArgument{Info: expr.Info{Usage: types[sqltype.Text].Usage()}, value: sqltype.String(types[sqltype.Text], "word"), order: &order}
			if !explicit {
				if _, err := node.Eval(c); err == nil {
					t.Fatal("missing runtime authority")
				}
			}
		}
	}
}
