package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestExtremaCharConversionAndRemap(t *testing.T) {
	ctx := context.Background()
	old, next := map[sqltype.ID]*sqltype.Descriptor{}, map[sqltype.ID]*sqltype.Descriptor{}
	for _, d := range sqltype.Builtins() {
		old[d.ID()] = d
	}
	for _, d := range sqltype.Builtins() {
		next[d.ID()] = d
	}
	char, text := old[sqltype.Char], old[sqltype.Text]
	for _, tc := range []struct{ input, want string }{{"", ""}, {"r", "r"}, {`\303`, `\303`}} {
		value, err := char.Input(ctx, tc.input, -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, null := range []bool{false, true} {
			v := value
			if null {
				v = sqltype.Null(char)
			}
			child := literalExpression{Info: expr.Info{Usage: char.Usage()}, value: v}
			plan, err := sqltype.PlanCoercion(char.Usage(), text.Usage(), sqltype.Implicit)
			if err != nil {
				t.Fatal(err)
			}
			node := coercionExpression{expr.Info{Usage: text.Usage()}, child, plan, sqltype.Implicit, false}
			check := func(expression expr.Expr, owner *sqltype.Descriptor) {
				got, err := expression.Eval(&EvalContext{Context: ctx})
				if err != nil || got.Type() != owner || got.IsNull() != null {
					t.Fatal(got, err)
				}
				if !null {
					s, err := got.Text()
					if err != nil || s != tc.want {
						t.Fatal(s, tc.want, err)
					}
				}
				if expression.ResultUsage().Collation != owner.Usage().Collation {
					t.Fatal("default TEXT collation lost")
				}
			}
			check(node, text)
			remapped, changed, err := retagBoundTypes(New().CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return next[d.ID()] })
			if err != nil || !changed {
				t.Fatal(changed, err)
			}
			check(remapped, next[sqltype.Text])
			// Old bound plans and values retain their original generation.
			check(node, text)
		}
	}
}
