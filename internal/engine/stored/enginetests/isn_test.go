package enginetests_test

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestIsnExpressionTransport(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	if _, err := e.Execute(op, &parser.CreateExtension{Name: parser.Identifier{Text: "isn"}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	installation := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "isn"}))
	cast := func(text, role string) parser.Expr {
		return parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: text}}, TypeName: parser.TypeName{Name: role}, Syntax: parser.PostfixCast}
	}
	identity := func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }
	for i, name := range []string{"isneq", "isnne", "isnlt", "isnle", "isngt", "isnge", "isbn"} {
		t.Run(name, func(t *testing.T) {
			b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			args := []parser.Expr{cast("9781234567897!", "ean13"), cast("123456789X", "isbn")}
			slot := extension.IsnComparisonStart + 8*6 + i
			if i == 6 {
				args = args[:1]
				slot = extension.IsnConversionStart
			}
			node, err := b.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args})
			if err != nil {
				t.Fatal(err)
			}
			selected := view.MustProcedure(installation.Installation.Members[slot])
			if expr.InfoOf(node).RoutineReference != selected {
				t.Fatal("missing actual selected procedure")
			}
			got, dependencyErr := stored.ExpressionDependRefs(context.Background(), view, node)
			want := []ids.ObjectAddress{selected.Address(), installation.Installation.Members[0]}
			if i < 6 {
				want = append(want, installation.Installation.Members[3])
			}
			selectedDependencySet(t, got, dependencyErr, want...)
			if next, changed, err := stored.RetagTypes(view, node, identity); err != nil || changed || !reflect.DeepEqual(next, node) {
				t.Fatal("identity retag replaced original", err)
			}
			if next, err := stored.RemapExpression(node, func(r ids.ObjectRef) ids.ObjectRef { return r }, nil); err != nil || !reflect.DeepEqual(next, node) {
				t.Fatal("identity reference remap", err)
			}
			capturable, ok := node.(expr.Capturable)
			if !ok {
				t.Fatal("missing callable capture")
			}
			operands, ok, err := capturable.Capture(expr.CaptureFunction)
			if err != nil || !ok || operands.Count != len(args) {
				t.Fatal("ordered callable operands", err)
			}
			for _, change := range []func(*catalog.Procedure){
				func(p *catalog.Procedure) { p.Display.Source = "foreign" },
				func(p *catalog.Procedure) { p.ObjectRevision = selected.Revision() + 1 },
				func(p *catalog.Procedure) { p.Implementation = extension.StockCube },
				func(p *catalog.Procedure) { p.Signature = "25" },
				func(p *catalog.Procedure) { p.Origin = nil },
			} {
				bad := *selected
				change(&bad)
				var forged expr.Expr
				switch n := node.(type) {
				case plan.ComparisonExpr:
					n.RoutineReference = &bad
					forged = n
				case functions.CastRoutine:
					n.RoutineReference = &bad
					forged = n
				default:
					t.Fatalf("unexpected provider %T", node)
				}
				if _, _, err := stored.RetagTypes(view, forged, identity); err == nil {
					t.Fatal("forged original accepted")
				}
				if expr.InfoOf(node).RoutineReference != selected {
					t.Fatal("rejection mutated source")
				}
			}
			for _, change := range []func(*catalog.Procedure){func(p *catalog.Procedure) { p.Strict = false }, func(p *catalog.Procedure) { p.Result = 25 }, func(p *catalog.Procedure) { p.Origin = nil }} {
				state := catalog.CloneState(view.State())
				bad := *selected
				change(&bad)
				state.SetDescriptor(bad.Address(), &bad)
				if _, _, err := stored.RetagTypes(view.WithState(state), node, identity); err == nil {
					t.Fatal("forged destination accepted")
				}
			}
			if i < 6 {
				bad := node.(plan.ComparisonExpr)
				bad.RoutineRef.Epoch++
				if _, _, err := stored.RetagTypes(view, bad, identity); err == nil {
					t.Fatal("foreign retained epoch")
				}
			}
		})
	}
}

func TestIsnLiteralRenderingIdempotence(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	if _, err := e.Execute(op, &parser.CreateExtension{Name: parser.Identifier{Text: "isn"}}, 0, nil); err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	for _, tc := range []struct{ role, value string }{
		{"ean13", "9781234567897!"}, {"isbn", "123456789X!"},
		{"isbn13", "9781234567897!"}, {"ismn", "M123456785!"},
		{"ismn13", "9790123456785!"}, {"issn", "9771234567898!"},
		{"issn13", "9771234567898!"}, {"upc", "123456789012!"},
	} {
		t.Run(tc.role, func(t *testing.T) {
			typ := view.MustType(view.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: tc.role}))
			value, err := typ.SQLType.Input(context.Background(), tc.value, -1)
			if err != nil {
				t.Fatal(err)
			}
			usage := typ.SQLType.Usage()
			var node expr.Expr = plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: value}
			want, err := stored.DefaultSQL(context.Background(), view, node)
			if err != nil {
				t.Fatal(err)
			}
			coercion, err := sqltype.PlanCoercion(usage, usage, sqltype.Explicit)
			if err != nil {
				t.Fatal(err)
			}
			for round := range 3 {
				node = plan.CoercionExpr{Info: expr.Info{Usage: usage}, Child: node, Plan: coercion, Mode: sqltype.Explicit}
				got, err := stored.DefaultSQL(context.Background(), view, node)
				if err != nil || got != want {
					t.Fatalf("identity annotation round %d: got %q, want %q, error %v", round, got, want, err)
				}
			}
		})
	}
}
