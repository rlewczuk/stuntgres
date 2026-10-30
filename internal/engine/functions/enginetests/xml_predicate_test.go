package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestXMLPredicateExpressionProtocols(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	for _, tc := range []struct {
		q      string
		kind   expr.CaptureKind
		count  int
		oid    uint32
		stable bool
	}{
		{`SELECT XMLPARSE(CONTENT '<r/>') IS DOCUMENT`, expr.CaptureXMLDocument, 1, 0, false},
		{`SELECT XMLEXISTS('/r' PASSING BY VALUE XMLPARSE(CONTENT '<r/>') BY REF)`, expr.CaptureFunction, 2, 2614, false},
		{`SELECT XMLEXISTS(('/r'::text) PASSING BY REF ('<r/>'::xml) BY VALUE)`, expr.CaptureFunction, 2, 2614, false},
		{`SELECT pg_catalog.xmlexists('/r','<r/>'::xml)`, expr.CaptureFunction, 2, 2614, false},
		{`SELECT xml_is_well_formed('<r/>')`, expr.CaptureFunction, 1, 3051, true},
		{`SELECT xml_is_well_formed_document('<r/>')`, expr.CaptureFunction, 1, 3052, false},
		{`SELECT xml_is_well_formed_content('<r/>')`, expr.CaptureFunction, 1, 3053, false},
	} {
		raw := enginetest.Statement(t, tc.q).(*parser.Select).Targets[0]
		node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(tc.q, err)
		}
		recipe, ok := node.(interface {
			Children() []expr.Expr
			Rebuild(expr.Info, []expr.Expr) (expr.Expr, error)
			Capture(expr.CaptureKind) (expr.CaptureOperands, bool, error)
			Render() (expr.Rendering, error)
			ValidateStoredProvider() error
			SameGroup(expr.Expr, func(expr.Expr, expr.Expr) bool, bool) bool
			PlanningFold() expr.Fold
			StrictNull() expr.Strict
		})
		if !ok {
			t.Fatalf("missing protocol %T", node)
		}
		children := recipe.Children()
		if len(children) != tc.count || node.ResultUsage().Type.ID() != sqltype.Bool || recipe.ValidateStoredProvider() != nil || recipe.PlanningFold() != expr.FoldNever || recipe.StrictNull() != expr.StrictAfter {
			t.Fatal(tc.q, node)
		}
		if tc.oid != 0 {
			reference := expr.InfoOf(node).RoutineReference
			if reference == nil || reference.RoutineOID() != tc.oid {
				t.Fatal("missing actual routine authority", tc.q, reference)
			}
		}
		if (node.Properties().Volatility == expr.Stable) != tc.stable {
			t.Fatal("volatility", tc.q, node.Properties())
		}
		rebuilt, err := recipe.Rebuild(expr.InfoOf(node), children)
		if err != nil || !reflect.DeepEqual(node, rebuilt) || !recipe.SameGroup(rebuilt, func(a, b expr.Expr) bool { return reflect.DeepEqual(a, b) }, true) {
			t.Fatal("rebuild changed authority", tc.q, err)
		}
		capture, ok, err := recipe.Capture(tc.kind)
		if err != nil || !ok || capture.Check(recipe.(expr.Capturable), tc.count) != nil {
			t.Fatal("capture", tc.q, capture, err)
		}
		if _, err := recipe.Rebuild(expr.InfoOf(node), nil); err == nil {
			t.Fatal("lost operands admitted", tc.q)
		}
		rendering, err := recipe.Render()
		if err != nil || len(rendering.Args) != tc.count {
			t.Fatal("render", tc.q, rendering, err)
		}
	}
}
func TestXMLPredicateDemandOrder(t *testing.T) {
	e := engine.New()
	usage := enginetest.Usage(e, sqltype.Bool)
	text := enginetest.Usage(e, sqltype.Text)
	xml := enginetest.Usage(e, sqltype.XML)
	order := []int{}
	cause := errors.New("demanded source conversion")
	first := ivUnitChild{Info: expr.Info{Usage: text}, value: sqltype.Null(text.Type), order: &order, ordinal: 1}
	second := ivUnitChild{Info: expr.Info{Usage: xml}, value: sqltype.Null(xml.Type), failure: cause, order: &order, ordinal: 2}
	node := functions.XMLExists{Info: expr.Info{Usage: usage}, Path: first, Context: second}
	if _, err := node.Eval(newIVUnitContext()); !errors.Is(err, cause) || !reflect.DeepEqual(order, []int{1, 2}) {
		t.Fatal(order, err)
	}
	second.failure = nil
	node.Context = second
	if value, err := node.Eval(newIVUnitContext()); err != nil || !value.IsNull() || value.Type() != usage.Type {
		t.Fatal(value, err)
	}
}

func TestXMLPredicateOperandTraversal(t *testing.T) {
	aggregate := parser.AggregateExpr{Name: "sum"}
	null := parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}
	for _, raw := range []parser.Expr{
		parser.XMLDocumentPredicateExpr{Child: aggregate},
		parser.FunctionExpr{Args: []parser.Expr{aggregate, null}},
		parser.FunctionExpr{Args: []parser.Expr{null, aggregate}},
	} {
		if !analyze.ContainsValuesAggregateAnalysis(raw) {
			t.Fatal("hidden aggregate escaped operand traversal", raw)
		}
	}
}
