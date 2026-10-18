package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestStringIdentBinding(t *testing.T) {
	for _, query := range []string{"SELECT parse_ident($1,$2)", "SELECT parse_ident(strict=>$2,str=>$1)"} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		raw := statement(t, query).(*parser.Select).Targets[0]
		bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(err)
		}
		n := bound.(functions.String)
		reversed := !n.Textual[0]
		if len(n.Args) != 2 || !n.ResultUsage().Type.IsArray() || n.ResultUsage().Type.Element() != b.typ(sqltype.Text) || !n.Properties().ParameterDependent {
			t.Fatal(n)
		}
		if b.parameters[1].Type != b.typ(sqltype.Text) || b.parameters[2].Type != b.typ(sqltype.Bool) {
			t.Fatal(b.parameters)
		}
		// These native effect/error units protect written evaluation order,
		// not an optimizer-wide PostgreSQL order guarantee.
		for _, failing := range []int{-1, 0, 1} {
			var observed []int
			failure := errors.New("argument failed")
			copy := n
			copy.Args = append([]expr.Expr(nil), n.Args...)
			for i, arg := range n.Args {
				v := sqltype.String(b.typ(sqltype.Text), "A.B[]")
				if n.Textual[i] == false {
					v = sqltype.Boolean(b.typ(sqltype.Bool), false)
				}
				var childError error
				if i == failing {
					childError = failure
				}
				copy.Args[i] = mathObservedArgument{Info: expr.InfoOf(arg), value: v, order: &observed, index: i, err: childError}
			}
			v, err := copy.Eval(&EvalContext{Context: context.Background()})
			want := []int{0, 1}
			if failing == 0 {
				want = []int{0}
			}
			if !reflect.DeepEqual(observed, want) {
				t.Fatal(reversed, failing, observed)
			}
			if failing >= 0 {
				if !errors.Is(err, failure) {
					t.Fatal(err)
				}
			} else if err != nil || v.ArrayLen() != 2 {
				t.Fatal(v, err)
			}
		}
	}
}

func TestStringIdentReconstruction(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	raw := statement(t, `SELECT parse_ident(strict=>false,str=>'A.B[]')`).(*parser.Select).Targets[0]
	bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	n := bound.(functions.String)
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	// Frozen views use their retained AST renderer rather than either bound
	// scalar renderer. Both presentation and replay must retain named actuals.
	if err := b.captureViewLiterals(raw, n, viewTargetLiteral, 0); err != nil {
		t.Fatal(err)
	}
	for _, deparse := range []bool{false, true} {
		renderer := viewRenderer{viewDefinition: &viewDefinition{literals: b.viewLiterals, castUsages: b.viewCastUsages}, frame: b.frameID(), presentation: viewPresentation{deparse: deparse, pretty: true}}
		sql, err := renderer.expressionSQL(context.Background(), b.bindingCatalog(), raw, viewLiteralPath{clause: viewTargetLiteral})
		if err != nil {
			t.Fatal(err)
		}
		replayed, err := b.bind(statement(t, "SELECT "+sql).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal("frozen view named replay", sql, err)
		}
		value, err := replayed.Eval(&EvalContext{Context: context.Background()})
		if err != nil || value.ArrayLen() != 2 {
			t.Fatal("frozen view replay result", sql, value, err)
		}
	}
	target := New()
	targetBinder := newTestExpressionBinder(t, context.Background(), target, sqltype.InputEnvironment{}, nil)
	rebound, changed, err := retagBoundTypes(target.CatalogRead(), n, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		next, _ := target.CatalogRead().LookupType(d.ID())
		return next
	})
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	copy := rebound.(functions.String)
	if copy.Textual != n.Textual || copy.Textual[0] || !copy.Textual[1] {
		t.Fatal(copy.Textual)
	}
	if err := copy.Validate(); err != nil {
		t.Fatal(err)
	}
	for i, node := range []functions.String{n, copy} {
		owner := b
		if i == 1 {
			owner = targetBinder
		}
		// Bound presentation is a third renderer, independent of canonical
		// scalar SQL and the frozen view AST. Execute both pretty modes with
		// each node's actual catalog owner to protect reversed-role labels.
		for _, pretty := range []bool{false, true} {
			renderer := pgRenderer{view: owner.bindingCatalog(), pretty: pretty}
			rendered, err := renderer.sql(context.Background(), node, pgRenderParent{})
			if err != nil {
				t.Fatal("bound presentation", i, pretty, err)
			}
			replayed, err := owner.bind(statement(t, "SELECT "+rendered).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal("bound presentation replay", i, pretty, rendered, err)
			}
			value, err := replayed.Eval(&EvalContext{Context: context.Background()})
			if err != nil {
				t.Fatal("bound presentation evaluation", i, pretty, rendered, err)
			}
			text, err := value.Type().Output(context.Background(), value)
			if err != nil || text != "{a,b}" {
				t.Fatal("bound presentation result", i, pretty, rendered, text, err)
			}
		}
		// Canonical stored SQL has a separate renderer from pg_get_viewdef's
		// presentation route. Replay must preserve the reversed formal roles.
		sql, err := defaultSQL(context.Background(), owner.bindingCatalog(), node)
		if err != nil {
			t.Fatal(err)
		}
		replayed, err := owner.bind(statement(t, "SELECT "+sql).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal("canonical named replay", sql, err)
		}
		replayedValue, err := replayed.Eval(&EvalContext{Context: context.Background()})
		if err != nil || replayedValue.ArrayLen() != 2 {
			t.Fatal("canonical named replay result", sql, replayedValue, err)
		}
		v, err := node.Eval(&EvalContext{Context: context.Background()})
		if err != nil || v.Type() != node.ResultUsage().Type || v.Type().Element() != node.Plan.TextUsage().Type || v.ArrayLen() != 2 {
			t.Fatal(v, err)
		}
		text, err := v.Type().Output(context.Background(), v)
		if err != nil || text != "{a,b}" {
			t.Fatal(text, err)
		}
	}
	if n.ResultUsage().Type == copy.ResultUsage().Type || n.Plan.TextUsage().Type == copy.Plan.TextUsage().Type {
		t.Fatal("generation ownership was not replaced")
	}
	broken := copy
	broken.Args = append([]expr.Expr(nil), copy.Args...)
	broken.Args[1] = n.Args[1]
	if err := broken.Validate(); err == nil {
		t.Fatal("foreign input owner accepted")
	}
}
