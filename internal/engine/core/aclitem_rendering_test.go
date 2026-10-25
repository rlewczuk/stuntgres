package core_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestACLItemRenderingAuthority(t *testing.T) {
	e := engine.New()
	aclUnitRoles(e, "acl_render_a", "acl_render_b")
	value := aclUnitValue(t, e, "acl_render_a=r*/acl_render_b")
	view := e.CatalogRead()
	literal := plan.LiteralExpr{Info: expr.Info{Usage: value.Type().Usage()}, Value: value}
	captured := regobj.ACLItemOutput{Owner: &e.Catalog.Owner, Roles: e.Roles()}
	text, err := (stored.ScalarRenderer{ACLOutput: captured}).SQL(context.Background(), view, literal)
	if err != nil || text != "CAST('acl_render_a=r*/acl_render_b' AS ACLITEM)" {
		t.Fatal("explicit construction authority", text, err)
	}
	if _, err := stored.DefaultSQL(context.Background(), view, literal); err == nil {
		t.Fatal("real SQL presentation accepted missing authority")
	}
	// Build the actual persisted CHECK provider and its dependency manifest
	// without issuing SQL or asserting only a standalone formatting helper.
	boolean := aclUnitType(t, e, sqltype.Bool)
	ordinaryBoolean := plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}
	if _, err := stored.DefaultSQL(context.Background(), view, ordinaryBoolean); err != nil {
		t.Fatal("non-ACL presentation acquired authority requirement", err)
	}
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, value.Type(), value.Type(), boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	comparison := plan.ComparisonExpr{Info: expr.Info{Usage: boolean.Usage()}, Left: literal, Right: literal, Predicate: predicate, Operator: "="}
	relation := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 45000}, Attributes: []int32{1}}
	var refs []ids.ObjectRef
	if _, err := (stored.ScalarRenderer{ACLOutput: captured, Owner: relation, Dependencies: &refs}).SQL(context.Background(), view, comparison); err != nil {
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
	if err := table.ValidateChecks(view, relation, make(map[string]bool)); err != nil {
		t.Fatal("live CHECK provider validation", err)
	}
	role, _ := e.Roles().Lookup("acl_render_a")
	e.Mu.Lock()
	next := cloneRoleFixture(e.RoleStore.Published)
	retireFixtureRole(next, role.Ref)
	e.RoleStore.PublishLocked(next)
	e.Mu.Unlock()
	if err := table.ValidateChecks(view, relation, make(map[string]bool)); err != nil {
		t.Fatal("DROP invalidated stored CHECK datum", err)
	}
	validation := stored.ScalarRenderer{Validation: true}
	if _, err := validation.SQL(context.Background(), view, literal); err != nil {
		t.Fatal("stale structural validation", err)
	}
	ordinary := regobj.ACLItemOutput{Owner: &e.Catalog.Owner, Roles: e.Roles()}
	text, err = (stored.ScalarRenderer{ACLOutput: ordinary}).SQL(context.Background(), view, literal)
	if err != nil || !strings.Contains(text, "20000=r*/acl_render_b") {
		t.Fatal("ordinary stale presentation", text, err)
	}
	foreign := engine.New()
	foreignValue, err := sqltype.ACLItemValue(value.Type(), &foreign.RoleStore.ACLAuthority, 20000, 20001, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	foreignLiteral := literal
	foreignLiteral.Value = foreignValue
	if _, err := validation.SQL(context.Background(), view, foreignLiteral); err == nil {
		t.Fatal("foreign token validated through coincident descriptor/IDs")
	}
	if _, err := stored.DefaultSQL(context.Background(), view, literal); err == nil {
		t.Fatal("validation mode leaked into real SQL presentation")
	}
	t.Run("view owning cast replay", func(t *testing.T) {
		array := aclUnitType(t, e, sqltype.ACLItemArray)
		arrayValue, err := sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{value})
		if err != nil {
			t.Fatal(err)
		}
		render := func(value sqltype.Value, target sqltype.Usage) (string, error) {
			leafPath := analyze.ViewLiteralPath{Node: 1}
			renderer := stored.QueryRenderer{View: stored.NewView(stored.ViewParts{
				Literals:   map[analyze.ViewLiteralPath]plan.LiteralExpr{leafPath: {Info: expr.Info{Usage: value.Type().Usage()}, Value: value}},
				CastUsages: map[analyze.ViewLiteralPath]sqltype.Usage{{}: target},
			})}
			ordinal := 0
			kind := parser.StringValue
			if value.IsNull() {
				kind = parser.NullValue
			}
			raw := parser.CastExpr{Child: parser.LiteralExpr{Value: parser.Value{Kind: kind}}}
			return renderer.ExpressionSQLAt(regobj.ACLItemOutputContext(context.Background(), captured), view, raw, analyze.ViewLiteralPath{}, &ordinal, stored.DeparseParent{})
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
		if _, err := (stored.Deparser{View: view}).Literal(context.Background(), literal); err == nil {
			t.Fatal("pg presentation accepted missing captured authority")
		}
	})
}
