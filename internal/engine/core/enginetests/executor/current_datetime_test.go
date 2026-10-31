package executor_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestCurrentDateTimeBindingAndSymbolicConsumers(t *testing.T) {
	ctx := context.Background()
	e := engine.New()
	b := enginetest.ExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	for _, kind := range []parser.CurrentDateTimeKind{parser.CurrentDate, parser.CurrentTime, parser.CurrentTimestamp, parser.LocalTime, parser.LocalTimestamp} {
		raw := parser.CurrentDateTimeExpr{Kind: kind, Pos: parser.Position{Character: 8, Offset: 7}}
		if kind != parser.CurrentDate {
			raw.HasPrecision = true
			raw.Precision = 3
		}
		bound, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
		if err != nil {
			t.Fatal(raw, err)
		}
		node, ok := bound.(functions.CurrentDateTime)
		if !ok || node.Properties() != (expr.Properties{Volatility: expr.Stable}) || node.ResultUsage().Type.ID() != functions.CurrentDateTimeType(kind) || exec.PlannedVolatility(node) != expr.Stable || plan.GenerationImmutable(node) {
			t.Fatal(raw, node)
		}
		spec, err := node.Render()
		if err != nil {
			t.Fatal(err)
		}
		sql := spec.Replay.Text
		rendered, err := (stored.Deparser{View: e.CatalogRead()}).SQL(ctx, node, stored.DeparseParent{})
		if err != nil || rendered != strings.ToUpper(sql) {
			t.Fatal(rendered, sql, err)
		}
		replay, err := (stored.ScalarRenderer{}).SQL(ctx, e.CatalogRead(), node)
		if err != nil || replay != sql {
			t.Fatal(replay, sql, err)
		}
		tree, err := (plan.BoundTree{Expression: node}).StructuralOutput(ctx)
		if err != nil || !strings.Contains(tree, "CurrentDateTime") || strings.Contains(tree, "Const") {
			t.Fatal(tree, err)
		}
		target := engine.New()
		retagged, changed, err := stored.RetagTypes(target.CatalogRead(), node, func(d *sqltype.Descriptor) *sqltype.Descriptor {
			next, _ := target.CatalogRead().LookupType(d.ID())
			return next
		})
		if err != nil || !changed {
			t.Fatal(changed, err)
		}
		remapped, err := stored.RemapExpression(retagged, func(ref ids.ObjectRef) ids.ObjectRef {
			t.Fatal("current leaf invented relation reference", ref)
			return ref
		}, nil)
		if err != nil {
			t.Fatal(err)
		}
		copy := remapped.(functions.CurrentDateTime)
		if copy.Kind != node.Kind || copy.Precision != node.Precision || copy.HasPrecision != node.HasPrecision || copy.Pos != node.Pos || copy.Usage.Type == node.Usage.Type || copy.Validate(expr.PropertiesMatch(copy.Property, functions.CurrentDateTimeIntrinsicRule())) != nil {
			t.Fatal(node, copy)
		}
		if sides, err := plan.JoinExpressionSides(copy, 0, 0); err != nil || sides != 0 {
			t.Fatal(sides, err)
		}
		if !b.SameGroupExpression(node, node) {
			t.Fatal("current leaf not semantically equal to itself")
		}
		other := node
		other.Kind = parser.CurrentDate
		if kind != parser.CurrentDate && b.SameGroupExpression(node, other) {
			t.Fatal("different current kinds equated")
		}
	}
	for _, raw := range []parser.CurrentDateTimeExpr{{Kind: 255}, {Kind: parser.CurrentDate, HasPrecision: true, Precision: 1}, {Kind: parser.CurrentTime, HasPrecision: true, Precision: -1}} {
		if _, _, err := functions.BindCurrentDateTime(b, raw); err == nil {
			t.Fatal("invalid current AST accepted", raw)
		}
	}
}
