package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestACLItemRenderingAuthority(t *testing.T) {
	e := New()
	aclUnitRoles(e, "acl_render_a", "acl_render_b")
	value := aclUnitValue(t, e, "acl_render_a=r*/acl_render_b")
	view := e.CatalogRead()
	literal := plan.LiteralExpr{Info: expr.Info{Usage: value.Type().Usage()}, Value: value}
	captured := capturedACLItemOutput{owner: e.catalog.owner(), roles: e.Roles()}
	text, err := (scalarRenderer{aclOutput: captured}).sql(context.Background(), view, literal)
	if err != nil || text != "CAST('acl_render_a=r*/acl_render_b' AS ACLITEM)" {
		t.Fatal("explicit construction authority", text, err)
	}
	if _, err := defaultSQL(context.Background(), view, literal); err == nil {
		t.Fatal("real SQL presentation accepted missing authority")
	}
	// Build the actual persisted CHECK provider and its dependency manifest
	// without issuing SQL or asserting only a standalone formatting helper.
	boolean := aclUnitType(t, e, sqltype.Bool)
	ordinaryBoolean := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}
	if _, err := defaultSQL(context.Background(), view, ordinaryBoolean); err != nil {
		t.Fatal("non-ACL presentation acquired authority requirement", err)
	}
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, value.Type(), value.Type(), boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	comparison := plan.ComparisonExpr{Info: expr.Info{Usage: boolean.Usage()}, Left: literal, Right: literal, Predicate: predicate, Operator: "="}
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 45000}, Attributes: []int32{1}}
	var refs []ids.ObjectRef
	if _, err := (scalarRenderer{aclOutput: captured, owner: relation, dependencies: &refs}).sql(context.Background(), view, comparison); err != nil {
		t.Fatal(err)
	}
	check := catalog.CheckConstraint{OID: 45001, Name: "acl_render_check", Expression: comparison}
	for _, ref := range refs {
		descriptor, ok := view.Descriptor(ref.Address)
		if !ok {
			t.Fatal("missing real provider dependency", ref.Address.ObjectID)
		}
		check.Dependencies = append(check.Dependencies, catalog.ExpressionDependency{Ref: ref, Revision: descriptor.Revision()})
	}
	relation.Constraints.Check = []catalog.CheckConstraint{check}
	if err := validateChecks(view, relation, make(map[string]bool)); err != nil {
		t.Fatal("live CHECK provider validation", err)
	}
	role, _ := e.Roles().Lookup("acl_render_a")
	e.mu.Lock()
	next := cloneRoleFixture(e.roles.published)
	retireFixtureRole(next, role.Ref)
	e.roles.published = next
	e.mu.Unlock()
	if err := validateChecks(view, relation, make(map[string]bool)); err != nil {
		t.Fatal("DROP invalidated stored CHECK datum", err)
	}
	validation := scalarRenderer{validation: true}
	if _, err := validation.sql(context.Background(), view, literal); err != nil {
		t.Fatal("stale structural validation", err)
	}
	ordinary := capturedACLItemOutput{owner: e.catalog.owner(), roles: e.Roles()}
	text, err = (scalarRenderer{aclOutput: ordinary}).sql(context.Background(), view, literal)
	if err != nil || !strings.Contains(text, "20000=r*/acl_render_b") {
		t.Fatal("ordinary stale presentation", text, err)
	}
	foreign := New()
	foreignValue, err := sqltype.ACLItemValue(value.Type(), &foreign.roles.aclAuthority, 20000, 20001, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	foreignLiteral := literal
	foreignLiteral.Value = foreignValue
	if _, err := validation.sql(context.Background(), view, foreignLiteral); err == nil {
		t.Fatal("foreign token validated through coincident descriptor/IDs")
	}
	if _, err := defaultSQL(context.Background(), view, literal); err == nil {
		t.Fatal("validation mode leaked into real SQL presentation")
	}
	t.Run("view owning cast replay", func(t *testing.T) {
		array := aclUnitType(t, e, sqltype.ACLItemArray)
		arrayValue, err := sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{value})
		if err != nil {
			t.Fatal(err)
		}
		render := func(value sqltype.Value, target sqltype.Usage) (string, error) {
			leafPath := viewLiteralPath{Node: 1}
			renderer := viewRenderer{viewDefinition: &viewDefinition{
				literals:   map[viewLiteralPath]plan.LiteralExpr{leafPath: {Info: expr.Info{Usage: value.Type().Usage()}, Value: value}},
				castUsages: map[viewLiteralPath]sqltype.Usage{{}: target},
			}}
			ordinal := 0
			kind := parser.StringValue
			if value.IsNull() {
				kind = parser.NullValue
			}
			raw := parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: kind}}}
			return renderer.expressionSQLAt(aclItemOutputContext(context.Background(), captured), view, raw, viewLiteralPath{}, &ordinal, pgRenderParent{})
		}
		for _, datum := range []sqltype.Value{value, sqltype.Null(value.Type()), arrayValue, sqltype.Null(array)} {
			first, err := render(datum, datum.Type().Usage())
			if err != nil || strings.Count(first, "CAST(") != 1 {
				t.Fatal("view replay duplicated or lost owning cast", first, err)
			}
			second, err := render(datum, datum.Type().Usage())
			if err != nil || second != first {
				t.Fatal("view replay changed captured rendering", first, second, err)
			}
			if datum.Type() == value.Type() && datum.IsNull() && first != "CAST(NULL AS ACLITEM)" {
				t.Fatal("scalar NULL lost source type", first)
			}
		}
		text, err := render(value, aclUnitType(t, e, sqltype.Text).Usage())
		if err != nil || strings.Count(text, "CAST(") != 2 {
			t.Fatal("different-usage source cast stripped", text, err)
		}
		if _, err := (pgRenderer{view: view}).literal(context.Background(), literal); err == nil {
			t.Fatal("pg presentation accepted missing captured authority")
		}
	})
}
