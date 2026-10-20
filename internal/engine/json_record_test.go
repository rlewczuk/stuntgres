package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func jsonRecordUnitCall(t *testing.T, b *analyze.Binder, name string, defined bool) functions.JSONRecord {
	t.Helper()
	b.Clause.AggregateClause = "FROM"
	b.Query.Namespace = &analyze.QueryNamespace{}
	raw := nullCountRaw(name, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: `{"a":2}`}})
	node, err := b.BindFunctionSource(raw)
	if err != nil {
		t.Fatal(err)
	}
	if defined {
		node, err = b.CompleteFunctionDefinitions(raw, node, []parser.FunctionColumnDefinition{{Name: parser.Identifier{Text: "a"}, Type: parser.TypeName{Name: "integer"}}, {Name: parser.Identifier{Text: "b"}, Type: parser.TypeName{Name: "text"}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	return node.(functions.JSONRecord)
}
func TestJSONRecordBinding(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	scalar := jsonRecordUnitCall(t, b, "json_to_record", false)
	if exec.SourceReturnsSet(scalar) || !exec.SourceAdmitted(scalar) || scalar.ResultUsage().Type.IsComposite() || !scalar.ResultUsage().Type.IsDeferredRecord() || exec.PlannedVolatility(scalar) != expr.Stable {
		t.Fatal("deferred scalar/set identity", scalar)
	}
	if _, err := scalar.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}); err == nil {
		t.Fatal("fabricated runtime shape")
	}
	bound := jsonRecordUnitCall(t, b, "json_to_record", true)
	if !bound.Defined || bound.ResultUsage().Type.CompositeLen() != 2 || !validExpressionUsage(e.CatalogRead(), bound.ResultUsage()) {
		t.Fatal("contextual source authority", bound)
	}
	v, err := bound.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || v.Type() != bound.ResultUsage().Type || !v.CompositeField(1).IsNull() {
		t.Fatal(v, err)
	}
	set := jsonRecordUnitCall(t, b, "json_to_recordset", true)
	if !exec.SourceReturnsSet(set) || !set.Properties().StageDependent || exec.PlannedVolatility(set) != expr.Stable {
		t.Fatal("set-stage classification", set)
	}
}
func TestJSONRecordRemap(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	original := jsonRecordUnitCall(t, b, "json_to_record", true)
	fresh := jsonObjectUnitTypes()
	mapped, changed, err := retagBoundTypes(view, original, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
	if err != nil || !changed {
		t.Fatal(mapped, changed, err)
	}
	next := mapped.(functions.JSONRecord)
	if next.ResultUsage().Type == original.ResultUsage().Type || next.Plan.ResultUsage() != next.ResultUsage() || next.Args[0].ResultUsage().Type != fresh[sqltype.JSON] || next.ResultUsage().Type.CompositeMember(0).Usage.Type != fresh[sqltype.Int4] || !next.Defined {
		t.Fatal("foreign plan/result authority", next)
	}
	v, err := next.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || v.Type() != next.ResultUsage().Type || v.CompositeField(0).Type() != fresh[sqltype.Int4] {
		t.Fatal(v, err)
	}
	walked := 0
	plan.WalkExpression(original, func(n expr.Expr) {
		if n == original.Args[0] {
			walked++
		}
	})
	if walked != 1 || !analyze.SameGroupExpression(original, original) {
		t.Fatal("closed child traversal", walked)
	}
	copy, err := remapBoundExpression(original, func(r ids.ObjectRef) ids.ObjectRef { return r }, nil)
	if err != nil || !analyze.SameGroupExpression(original, copy) {
		t.Fatal(copy, err)
	}
	text, err := (pgRenderer{view: view}).sql(context.Background(), original, pgRenderParent{})
	if err != nil || !strings.Contains(text, "json_to_record(") || !strings.Contains(text, "::json") {
		t.Fatal(text, err)
	}
	folded, _, err := simplifyConditional(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, original, false)
	if err != nil || folded.ResultUsage() != original.ResultUsage() || exec.PlannedVolatility(folded) != expr.Stable {
		t.Fatal("folded STABLE call", folded, err)
	}
}
