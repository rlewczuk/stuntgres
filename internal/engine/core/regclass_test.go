package core_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestRegclassConstructorShapeOwnershipAndWalkers(t *testing.T) {
	e := engine.New()
	b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.ArrayExpr{Elements: []parser.Expr{parser.RegclassExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "pg_catalog.pg_class"}}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}}
	node, err := b.BindArray(raw, sqltype.Usage{})
	if err != nil {
		t.Fatal(err)
	}
	value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil {
		t.Fatal(err)
	}
	if value.IsNull() || value.ArrayLen() != 2 || !value.ArrayElement(1).IsNull() || !reflect.DeepEqual(value.ArrayDimensions(), []sqltype.ArrayDimension{{Length: 2, Lower: 1}}) {
		t.Fatal(value, err)
	}
	dimensions := value.ArrayDimensions()
	dimensions[0].Lower = 99
	if value.ArrayDimensions()[0].Lower != 1 {
		t.Fatal("dimension accessor mutates owned datum")
	}
	count := 0
	plan.ExpressionChildren(node, func(expr.Expr) { count++ })
	if count != 2 {
		t.Fatal(count)
	}
	if exec.PlannedVolatility(node) != expr.Immutable || !plan.GenerationImmutable(node) || !table.DefaultReplayDeferrable(catalog.ColumnDefault{Expression: node}) || !b.SameGroupExpression(node, node) {
		t.Fatal("constructor properties/walkers")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	empty, err := b.BindArray(parser.ArrayExpr{}, node.ResultUsage())
	if err != nil {
		t.Fatal(err)
	}
	emptyValue, err := empty.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
	if err != nil || emptyValue.IsNull() || emptyValue.ArrayLen() != 0 || len(emptyValue.ArrayDimensions()) != 0 {
		t.Fatal(emptyValue, err)
	}
}

func TestRegclassDeferredDefinitionSelection(t *testing.T) {
	e := engine.New()
	ownershipUnitExec(t, e, "CREATE TABLE t(v TEXT,r REGCLASS)")
	view := e.CatalogRead()
	descriptor := retiredUnitTable(t, e, "t")
	regclass, _ := view.LookupType(sqltype.Regclass)
	text, _ := view.LookupType(sqltype.Text)
	scalar := plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage()}, Value: sqltype.String(regclass, "1259")}
	inline := plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "inline")}
	if !table.DefaultReplayDeferrable(catalog.ColumnDefault{Expression: scalar}) || table.DefaultReplayDeferrable(catalog.ColumnDefault{Expression: inline}) {
		t.Fatal("selected result eligibility or primitive inline definition changed")
	}
	table := table.DumpRelation{
		Schema: "public", Name: "t", Attributes: descriptor.Attributes,
		Columns: descriptor.Columns, Descriptor: descriptor,
		Defaults:            map[int32]catalog.ColumnDefault{1: {Expression: inline}, 2: {Expression: scalar}},
		DeferredDefinitions: map[int32]bool{2: true},
	}
	var emitted []string
	err := table.EmitDeferredDefaults(context.Background(), view, func(statement string) error {
		emitted = append(emitted, statement)
		return nil
	})
	if err != nil || len(emitted) != 1 || !strings.HasPrefix(emitted[0], `ALTER TABLE ONLY "public"."t" ALTER COLUMN "r" SET DEFAULT `) || !strings.Contains(emitted[0], "pg_class") {
		t.Fatal(emitted, err)
	}
	sentinel := errors.New("deferred emission")
	if err := table.EmitDeferredDefaults(context.Background(), view, func(string) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
}

func TestRegclassConstructorParameterOccurrences(t *testing.T) {
	for _, contextual := range []bool{false, true} {
		for _, repeated := range []bool{false, true} {
			e := engine.New()
			b := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			typ := b.Type(sqltype.Regclass)
			raw := parser.ArrayExpr{Elements: []parser.Expr{parser.ParameterExpr{Number: 1}}}
			expected := sqltype.Usage{}
			if contextual {
				expected = b.Type(typ.AssociatedArray()).Usage()
			} else {
				raw.Elements = append(raw.Elements, parser.RegclassExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "pg_catalog.pg_class"}}})
			}
			if repeated {
				raw.Elements = append(raw.Elements, parser.ParameterExpr{Number: 1})
			}
			node, err := b.BindArray(raw, expected)
			if err != nil {
				t.Fatal(contextual, repeated, err)
			}
			parameters, err := b.Finish()
			if err != nil || parameters.Count != 1 || parameters.Types[0].Type != typ || !node.Properties().ParameterDependent {
				t.Fatal(parameters, node.Properties(), err)
			}
			for _, parameter := range []sqltype.Value{sqltype.String(typ, "0"), sqltype.Null(typ), sqltype.String(typ, "1259")} {
				value, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Parameters: []sqltype.Value{parameter}}})
				if err != nil || value.ArrayLen() != len(raw.Elements) {
					t.Fatal(value, err)
				}
				first := value.ArrayElement(0)
				if first.Type() != typ || first.IsNull() != parameter.IsNull() {
					t.Fatal(first, parameter)
				}
				if repeated {
					last := value.ArrayElement(value.ArrayLen() - 1)
					if !reflect.DeepEqual(first, last) {
						t.Fatal("shared slot occurrences changed", first, last)
					}
				}
			}
		}
	}
}

func TestRegclassOpaqueInlineDefaultReplayPrerequisites(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		t.Run(fmt.Sprint(mixed), func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			for _, query := range []string{
				"CREATE TABLE rr_opaque_owner(v BOOL)",
				"CREATE TABLE rr_opaque_target(n INT)",
				"ALTER TABLE rr_opaque_owner ALTER COLUMN v SET DEFAULT ('{rr_opaque_target}'::regclass[] IS NOT NULL)",
			} {
				enginetest.RoleExecute(t, e, op, query)
			}
			if mixed {
				// This reverse opaque edge belongs only to an omitted eligible
				// definition, not CREATE. Making it mandatory would add a cycle.
				enginetest.RoleExecute(t, e, op, "ALTER TABLE rr_opaque_target ADD COLUMN a regclass[] DEFAULT '{rr_opaque_owner}'")
			}
			view := e.CatalogRead()
			ownerRef, targetRef := enginetest.RoleTableRef(t, e, "rr_opaque_owner"), enginetest.RoleTableRef(t, e, "rr_opaque_target")
			descriptor, _ := view.Descriptor(ownerRef.Address)
			owner := descriptor.(*catalog.Relation)
			definition := owner.ColumnMeta.Defaults[owner.Attributes[owner.Index["v"]]]
			if table.DefaultReplayDeferrable(definition) || definition.Expression.ResultUsage().Type.ID() != sqltype.Bool {
				t.Fatal("opaque BOOL default changed eligibility")
			}
			for _, dependency := range definition.Dependencies {
				if dependency.Address.ClassID == ids.RelationClassID {
					t.Fatal("opaque payload gained DROP dependency", dependency)
				}
			}
			var refs []ids.ObjectAddress
			if err := regobj.ClassReplayReferences(view, definition.Expression, func(address ids.ObjectAddress) error { refs = append(refs, address); return nil }); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(refs, []ids.ObjectAddress{targetRef.Address}) {
				t.Fatal("live replay-only prerequisite missing", refs)
			}
		})
	}
}

func ownershipUnitExec(t *testing.T, e *core.Engine, q string) {
	t.Helper()
	if _, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, q), nil); err != nil {
		t.Fatal(q, err)
	}
}

// These are package fixtures: SQL constructs physical descriptors through the
// real providers; assertions exercise the issuer and catalog validator directly.
func retiredUnitTable(t *testing.T, e *core.Engine, name string) *catalog.Relation {
	t.Helper()
	for _, d := range e.CatalogRead().Descriptors() {
		if r, ok := d.(*catalog.Relation); ok && r.Name() == name {
			return r
		}
	}
	t.Fatal("missing physical relation", name)
	return nil
}
