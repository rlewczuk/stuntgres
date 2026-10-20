package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRegclassNumericClassification(t *testing.T) {
	typ, _ := New().CatalogRead().LookupType(sqltype.Oid)
	for _, tc := range []struct {
		input   string
		numeric bool
		oid     uint32
		code    string
	}{
		{"-", true, 0, ""}, {"0", true, 0, ""}, {"010", true, 8, ""}, {"4294967295", true, 4294967295, ""},
		{"08", true, 0, "22P02"}, {"4294967296", true, 0, "22003"}, {"", false, 0, ""}, {"+1", false, 0, ""}, {"-1", false, 0, ""}, {" 42 ", false, 0, ""}, {"a.b", false, 0, ""},
	} {
		t.Run(tc.input, func(t *testing.T) {
			oid, numeric, err := regclassNumeric(tc.input, typ)
			if numeric != tc.numeric || oid != tc.oid {
				t.Fatal(oid, numeric, err)
			}
			var d *sqlstate.Error
			if tc.code != "" {
				if !errors.As(err, &d) || d.Code != tc.code {
					t.Fatal(err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRegclassScalarDependencyAndReplaySeparation(t *testing.T) {
	e := New()
	view := e.CatalogRead()
	typ, _ := view.LookupType(sqltype.Regclass)
	arrayTyp, _ := view.LookupType(typ.AssociatedArray())
	value := sqltype.String(typ, "1259")
	var err error
	literal := plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: value}
	var deps []ids.ObjectRef
	if _, err = (scalarRenderer{dependencies: &deps}).sql(context.Background(), view, literal); err != nil || len(deps) != 2 || deps[0].Address != (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Regclass)}) || deps[1].Address != (ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 1259}) {
		t.Fatal(deps, err)
	}
	array, err := sqltype.OwnedArrayValue(arrayTyp, []sqltype.ArrayDimension{{Length: 1, Lower: 1}}, []sqltype.Value{value})
	if err != nil {
		t.Fatal(err)
	}
	opaque := plan.LiteralExpr{Info: expr.Info{Usage: arrayTyp.Usage()}, Value: array}
	deps = nil
	if _, err = (scalarRenderer{dependencies: &deps}).sql(context.Background(), view, opaque); err != nil {
		t.Fatal(err)
	}
	for _, dep := range deps {
		if dep.Address.ClassID == ids.RelationClassID {
			t.Fatal("opaque array acquired DROP dependency", dep)
		}
	}
	var replay []ids.ObjectAddress
	if err = regclassReplayReferences(view, opaque, func(a ids.ObjectAddress) error { replay = append(replay, a); return nil }); err != nil || !reflect.DeepEqual(replay, []ids.ObjectAddress{{ClassID: ids.RelationClassID, ObjectID: 1259}}) {
		t.Fatal(replay, err)
	}
	sentinel := errors.New("replay sentinel")
	if err = regclassReplayReferences(view, opaque, func(ids.ObjectAddress) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	intTyp, _ := view.LookupType(sqltype.Int4)
	integer, err := sqltype.Integer(intTyp, 1259)
	if err != nil {
		t.Fatal(err)
	}
	coercionPlan, err := sqltype.PlanCoercion(intTyp.Usage(), typ.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	numeric := plan.CoercionExpr{Info: expr.Info{Usage: typ.Usage()}, Child: plan.LiteralExpr{Info: expr.Info{Usage: intTyp.Usage()}, Value: integer}, Plan: coercionPlan, Mode: sqltype.Explicit}
	replay = nil
	if err = regclassReplayReferences(view, numeric, func(a ids.ObjectAddress) error { replay = append(replay, a); return nil }); err != nil || len(replay) != 0 {
		t.Fatal(replay, err)
	}
	if !defaultReplayDeferrable(catalog.ColumnDefault{Expression: numeric}) {
		t.Fatal("closed numeric regclass default not deferred")
	}
}

func TestRegclassConstructorShapeOwnershipAndWalkers(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := parser.ArrayExpr{Elements: []parser.Expr{parser.RegclassExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "pg_catalog.pg_class"}}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}}
	node, err := b.bindArray(raw, sqltype.Usage{})
	if err != nil {
		t.Fatal(err)
	}
	value, err := node.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
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
	if PlannedVolatility(node) != expr.Immutable || !generationImmutable(node) || !defaultReplayDeferrable(catalog.ColumnDefault{Expression: node}) || !b.sameGroupExpression(node, node) {
		t.Fatal("constructor properties/walkers")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = node.Eval(&EvalContext{evalStatement: evalStatement{Context: ctx}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	empty, err := b.bindArray(parser.ArrayExpr{}, node.ResultUsage())
	if err != nil {
		t.Fatal(err)
	}
	emptyValue, err := empty.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
	if err != nil || emptyValue.IsNull() || emptyValue.ArrayLen() != 0 || len(emptyValue.ArrayDimensions()) != 0 {
		t.Fatal(emptyValue, err)
	}
}

func TestRegclassDeferredDefinitionSelection(t *testing.T) {
	e := New()
	ownershipUnitExec(t, e, "CREATE TABLE t(v TEXT,r REGCLASS)")
	view := e.CatalogRead()
	descriptor := retiredUnitTable(t, e, "t")
	regclass, _ := view.LookupType(sqltype.Regclass)
	text, _ := view.LookupType(sqltype.Text)
	scalar := plan.LiteralExpr{Info: expr.Info{Usage: regclass.Usage()}, Value: sqltype.String(regclass, "1259")}
	inline := plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "inline")}
	if !defaultReplayDeferrable(catalog.ColumnDefault{Expression: scalar}) || defaultReplayDeferrable(catalog.ColumnDefault{Expression: inline}) {
		t.Fatal("selected result eligibility or primitive inline definition changed")
	}
	table := dumpRelation{
		schema: "public", name: "t", attributes: descriptor.Attributes,
		columns: descriptor.Columns, descriptor: descriptor,
		defaults:            map[int32]catalog.ColumnDefault{1: {Expression: inline}, 2: {Expression: scalar}},
		deferredDefinitions: map[int32]bool{2: true},
	}
	var emitted []string
	err := table.emitDeferredDefaults(context.Background(), view, func(statement string) error {
		emitted = append(emitted, statement)
		return nil
	})
	if err != nil || len(emitted) != 1 || !strings.HasPrefix(emitted[0], `ALTER TABLE ONLY "public"."t" ALTER COLUMN "r" SET DEFAULT `) || !strings.Contains(emitted[0], "pg_class") {
		t.Fatal(emitted, err)
	}
	sentinel := errors.New("deferred emission")
	if err := table.emitDeferredDefaults(context.Background(), view, func(string) error { return sentinel }); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
}

func TestRegclassConstructorParameterOccurrences(t *testing.T) {
	for _, contextual := range []bool{false, true} {
		for _, repeated := range []bool{false, true} {
			e := New()
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			typ := b.typ(sqltype.Regclass)
			raw := parser.ArrayExpr{Elements: []parser.Expr{parser.ParameterExpr{Number: 1}}}
			expected := sqltype.Usage{}
			if contextual {
				expected = b.typ(typ.AssociatedArray()).Usage()
			} else {
				raw.Elements = append(raw.Elements, parser.RegclassExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "pg_catalog.pg_class"}}})
			}
			if repeated {
				raw.Elements = append(raw.Elements, parser.ParameterExpr{Number: 1})
			}
			node, err := b.bindArray(raw, expected)
			if err != nil {
				t.Fatal(contextual, repeated, err)
			}
			parameters, err := b.finish()
			if err != nil || parameters.Count != 1 || parameters.Types[0].Type != typ || !node.Properties().ParameterDependent {
				t.Fatal(parameters, node.Properties(), err)
			}
			for _, parameter := range []sqltype.Value{sqltype.String(typ, "0"), sqltype.Null(typ), sqltype.String(typ, "1259")} {
				value, err := node.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background(), Parameters: []sqltype.Value{parameter}}})
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
			e := New()
			op, done := roleOperation(t, e, "postgres")
			defer done()
			for _, query := range []string{
				"CREATE TABLE rr_opaque_owner(v BOOL)",
				"CREATE TABLE rr_opaque_target(n INT)",
				"ALTER TABLE rr_opaque_owner ALTER COLUMN v SET DEFAULT ('{rr_opaque_target}'::regclass[] IS NOT NULL)",
			} {
				roleExecute(t, e, op, query)
			}
			if mixed {
				// This reverse opaque edge belongs only to an omitted eligible
				// definition, not CREATE. Making it mandatory would add a cycle.
				roleExecute(t, e, op, "ALTER TABLE rr_opaque_target ADD COLUMN a regclass[] DEFAULT '{rr_opaque_owner}'")
			}
			view := e.CatalogRead()
			ownerRef, targetRef := roleTableRef(t, e, "rr_opaque_owner"), roleTableRef(t, e, "rr_opaque_target")
			descriptor, _ := view.Descriptor(ownerRef.Address)
			owner := descriptor.(*catalog.Relation)
			definition := owner.ColumnMeta.Defaults[owner.Attributes[owner.Index["v"]]]
			if defaultReplayDeferrable(definition) || definition.Expression.ResultUsage().Type.ID() != sqltype.Bool {
				t.Fatal("opaque BOOL default changed eligibility")
			}
			for _, dependency := range definition.Dependencies {
				if dependency.Address.ClassID == ids.RelationClassID {
					t.Fatal("opaque payload gained DROP dependency", dependency)
				}
			}
			var refs []ids.ObjectAddress
			if err := regclassReplayReferences(view, definition.Expression, func(address ids.ObjectAddress) error { refs = append(refs, address); return nil }); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(refs, []ids.ObjectAddress{targetRef.Address}) {
				t.Fatal("live replay-only prerequisite missing", refs)
			}
		})
	}
}
