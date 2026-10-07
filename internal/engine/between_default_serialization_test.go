package engine

import (
	"context"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestBetweenGroupedDefaultSerialization(t *testing.T) {
	for _, tc := range []struct {
		expression string
		want       any
	}{
		{"TRUE BETWEEN (1<2) AND (2<3)", true},
		{"(TRUE OR FALSE) BETWEEN FALSE AND TRUE", true},
		{"TRUE BETWEEN SYMMETRIC TRUE AND (2 BETWEEN 1 AND 3)", true},
		{"TRUE NOT BETWEEN SYMMETRIC (FALSE OR TRUE) AND (1 BETWEEN 0 AND 2)", false},
		{"TRUE BETWEEN (NULL=TRUE) AND TRUE", nil},
		{"(NULL IS NULL) BETWEEN FALSE AND TRUE", true},
		{"(TRUE IS TRUE) BETWEEN FALSE AND TRUE", true},
		{"(1 IS DISTINCT FROM 2) BETWEEN FALSE AND TRUE", true},
		{"TRUE BETWEEN (1/0=0) AND TRUE", "22012"},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			e := New()
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			column := ColumnDescriptor{Name: "b", Usage: b.typ(sqltype.Bool).Usage()}
			parseDefault := func(expression string) parser.Expr {
				t.Helper()
				statements, err := parser.Parse("CREATE TABLE d(b BOOL DEFAULT (" + expression + "))")
				if err != nil {
					t.Fatal(err)
				}
				return statements[0].(*parser.CreateTable).Elements[0].Column.Constraints[0].Default
			}
			original, err := b.bindDefault(parseDefault(tc.expression), column)
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := defaultSQL(context.Background(), e.CatalogRead(), original)
			if err != nil {
				t.Fatal("serialization evaluated/rejected valid default", err)
			}
			// Replay through the maintained public grammar and binder, not a permissive
			// parser mode or the original bound tree.
			replayed, err := b.bindDefault(parseDefault(canonical), column)
			if err != nil {
				t.Fatal("serialized default cannot rebind", err)
			}
			for _, node := range []BoundExpr{original, replayed} {
				value, err := node.Eval(&EvalContext{Context: context.Background()})
				if code, ok := tc.want.(string); ok {
					state(t, err, code)
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				if tc.want == nil {
					if !value.IsNull() {
						t.Fatal("NULL default became non-NULL", value)
					}
					continue
				}
				got, err := value.Boolean()
				if err != nil || got != tc.want.(bool) {
					t.Fatal(got, err, tc.want)
				}
			}
		})
	}
}
