package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestPartitionInfoBinding(t *testing.T) {
	e := engine.New()
	for _, name := range []string{"pg_partition_tree", "pg_partition_ancestors", "pg_partition_root"} {
		node := catalogRelationsUnitBind(t, e, name, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}})
		child := node.(expr.Composite).Children()[0]
		if child.ResultUsage().Type.ID() != sqltype.Regclass {
			t.Fatal("formal type", name)
		}
		if name == "pg_partition_tree" {
			shape := node.ResultUsage().Type
			if shape.ID() != 2249 || shape.CompositeLen() != 4 {
				t.Fatal("tree shape", shape)
			}
			for i, want := range []struct {
				name string
				id   sqltype.ID
			}{{"relid", sqltype.Regclass}, {"parentrelid", sqltype.Regclass}, {"isleaf", sqltype.Bool}, {"level", sqltype.Int4}} {
				member := shape.CompositeMember(i)
				if member.Name != want.name || member.Attribute != int32(i+1) || member.Usage.Type.ID() != want.id {
					t.Fatal("tree field", i, member)
				}
			}
		} else if node.ResultUsage().Type.ID() != sqltype.Regclass {
			t.Fatal("scalar element type", name)
		}
		if expr.IsSetSource(node) != (name != "pg_partition_root") {
			t.Fatal("source cardinality", name)
		}
		if name == "pg_partition_ancestors" && expr.SourceOutName(node) != "relid" {
			t.Fatal("OUT name")
		}
		binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		binder.Clause.AggregateClause = "SELECT"
		raw := parser.FunctionExpr{
			Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}},
			Args: []parser.Expr{parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "pg_class"}}, TypeName: parser.TypeName{Name: "text"}}},
		}
		typed, err := binder.BindFunctionProvider(raw)
		if err != nil {
			t.Fatal("typed text formal", name, err)
		}
		coercion := typed.(expr.Composite).Children()[0]
		evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Input: binder.Input}}
		value, err := coercion.Eval(evaluation)
		if err != nil {
			t.Fatal("formal input authority", name, err)
		}
		identity, err := value.Text()
		if err != nil || identity != "1259" || value.Type().ID() != sqltype.Regclass {
			t.Fatal("resolved formal identity", name, identity, err)
		}
	}
}

func TestPartitionInfoRoutineIdentity(t *testing.T) {
	e := engine.New()
	for _, tc := range []struct {
		name       string
		oid        uint32
		volatility expr.Volatility
		set        bool
	}{
		{"pg_partition_tree", 3423, expr.Volatile, true}, {"pg_partition_ancestors", 3425, expr.Volatile, true}, {"pg_partition_root", 3424, expr.Immutable, false},
	} {
		node := catalogRelationsUnitBind(t, e, tc.name, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}})
		ref := expr.InfoOf(node).RoutineReference
		if ref == nil || ref.RoutineOID() != tc.oid || exec.PlannedVolatility(node) != tc.volatility {
			t.Fatal("routine identity", tc.name, ref)
		}
		found := false
		for _, decl := range routine.BuiltinRoutineDeclarations {
			if uint32(decl.Identity.ID) == tc.oid {
				found = true
				if !decl.Strict || decl.Volatility != tc.volatility || decl.ReturnsSet != tc.set || decl.Parallel != 's' || decl.Providers != functions.CatalogRoutineProvider {
					t.Fatal("released declaration", decl)
				}
			}
		}
		if !found {
			t.Fatal("missing executable declaration", tc.name)
		}
	}
}

func TestPartitionInfoProtocol(t *testing.T) {
	e := engine.New()
	for _, name := range []string{"pg_partition_tree", "pg_partition_ancestors", "pg_partition_root"} {
		node := catalogRelationsUnitBind(t, e, name, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}})
		composite := node.(expr.Composite)
		mapped, err := expr.Substitute(composite, expr.InfoOf(node), composite.Children())
		if err != nil || !analyze.SameGroupExpression(node, mapped) {
			t.Fatal("reconstruction", name, err)
		}
		if _, err := expr.Substitute(composite, expr.InfoOf(node), nil); err == nil {
			t.Fatal("missing child admitted")
		}
		if _, err := expr.Substitute(composite, expr.InfoOf(node), []expr.Expr{nil}); err == nil {
			t.Fatal("nil child admitted")
		}
		rendering, err := node.(interface {
			Render() (expr.Rendering, error)
		}).Render()
		if err != nil || rendering.Name != name || !rendering.Replay.CatalogSnapshot || len(rendering.Args) != 1 {
			t.Fatal("symbolic inquiry", rendering, err)
		}
		capture, ok, err := node.(interface {
			Capture(expr.CaptureKind) (expr.CaptureOperands, bool, error)
		}).Capture(expr.CaptureFunction)
		if err != nil || !ok || capture.Count != 1 || !analyze.SameGroupExpression(capture.Inline[0], composite.Children()[0]) {
			t.Fatal("capture", capture, err)
		}
		if name == "pg_partition_root" {
			if !exec.StrictScalarFunctionSource(node) {
				t.Fatal("root lost shared scalar-source NULL pruning")
			}
			evaluation := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
			folded, _, err := exec.SimplifyConditional(evaluation, node, false)
			literal, ok := folded.(plan.LiteralExpr)
			if err != nil || !ok || !literal.Value.IsNull() || expr.InfoOf(folded).RoutineReference != nil {
				t.Fatal("strict root NULL did not remove invocation", err)
			}
			regclass := node.ResultUsage().Type
			nonnull, err := expr.Substitute(composite, expr.InfoOf(node), []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage()}, Value: sqltype.String(regclass, "41012")}})
			if err != nil {
				t.Fatal(err)
			}
			symbolic, _, err := exec.SimplifyConditional(evaluation, nonnull, false)
			if _, ok := symbolic.(functions.PartitionRoot); err != nil || !ok {
				t.Fatal("topology folded into reusable plan", err)
			}
		}
		if expr.IsSetSource(node) {
			if !analyze.SameQuerySetOccurrence(node, mapped) {
				t.Fatal("source occurrence lost")
			}
			binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			requests := []expr.Expr{node}
			root, err := binder.LowerQuerySets(&plan.SingletonNode{}, requests, nil, false)
			if err != nil {
				t.Fatal(err)
			}
			stage, ok := root.(*plan.SetProjectNode)
			if !ok || len(stage.Calls) != 1 {
				t.Fatal("missing set stage")
			}
			if err := exec.ValidateQuerySetSteps(stage.Steps, len(stage.Calls), len(stage.Scalars)); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func catalogRelationsUnitBind(t *testing.T, e *engine.Engine, name string, args ...parser.Expr) expr.Expr {
	t.Helper()
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	binder.Clause.AggregateClause = "SELECT"
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args}
	node, err := binder.BindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	return node
}
