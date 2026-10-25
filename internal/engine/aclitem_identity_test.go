package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func aclUnitRoles(e *Engine, names ...string) {
	e.Mu.Lock()
	defer e.Mu.Unlock()
	next := cloneRoleFixture(e.RoleStore.Published)
	for i, name := range names {
		putFixtureRole(next, authorization.RoleDescriptor{Ref: e.RoleStore.Domain.Principal(uint64(20000 + i)), Name: name, Inherit: true, ConnectionLimit: -1})
	}
	e.RoleStore.PublishLocked(next)
	if nextID := uint64(20000 + len(names)); e.RoleStore.NextPrincipal() < nextID {
		e.RoleStore.InstallPrincipalFrontierLocked(nextID)
	}
}
func aclUnitType(t *testing.T, e *Engine, id sqltype.ID) *sqltype.Descriptor {
	t.Helper()
	d, ok := e.CatalogRead().LookupType(id)
	if !ok {
		t.Fatal("missing type", id)
	}
	return d
}
func aclUnitValue(t *testing.T, e *Engine, text string) sqltype.Value {
	t.Helper()
	d := aclUnitType(t, e, sqltype.ACLItem)
	v, err := d.InputWithEnvironment(context.Background(), text, -1, sqltype.InputEnvironment{ACLItem: e.CaptureACLItemInput(AuthorizationView{View: access.View{Roles: e.Roles()}}, nil)})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestACLItemRetainedIdentity(t *testing.T) {
	e := New()
	aclUnitRoles(e, "acl_unit_a", "acl_unit_b")
	d := aclUnitType(t, e, sqltype.ACLItem)
	array := aclUnitType(t, e, sqltype.ACLItemArray)
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	position := parser.Position{Offset: 7, Character: 8}
	value, err := b.InputConstant("acl_unit_a=rw*/acl_unit_b", d.Usage(), position)
	if err != nil {
		t.Fatal(err)
	}
	arrayValue, err := b.InputConstant("[0:1]={acl_unit_a=rw*/acl_unit_b,NULL}", array.Usage(), parser.Position{Offset: 50, Character: 51})
	if err != nil {
		t.Fatal(err)
	}
	oldRoles := e.Roles()
	rows := exec.NewFinalRows([]catalog.Column{{Name: "a", Usage: d.Usage()}, {Name: "aa", Usage: array.Usage()}}, 1)
	rows.Values = []sqltype.Value{value, arrayValue}
	defer rows.Close()
	output := regobj.ACLItemOutput{Owner: &e.Catalog.Owner, Roles: oldRoles}
	for _, v := range rows.Values {
		if err := regobj.CaptureACLItemOutputValue(rows, output, v); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := oldRoles.Lookup("acl_unit_a")
	e.Mu.Lock()
	next := cloneRoleFixture(e.RoleStore.Published)
	a.Name = "acl_unit_renamed"
	putFixtureRole(next, a)
	e.RoleStore.PublishLocked(next)
	e.Mu.Unlock()
	fresh := regobj.ACLItemOutput{Owner: &e.Catalog.Owner, Roles: e.Roles()}
	text, err := d.OutputWithEnvironment(context.Background(), value, sqltype.OutputEnvironment{ACLItem: fresh})
	if err != nil || text != "acl_unit_renamed=rw*/acl_unit_b" {
		t.Fatal("new spelling", text, err)
	}
	if original, ok := oldRoles.Lookup("acl_unit_a"); !ok || original.Ref != a.Ref {
		t.Fatal("rename mutated old role root")
	}
	expected := &StatementDescription{Temporals: append([]analyze.TemporalConstant(nil), b.Prepared.Temporals...), catalogOwner: &e.Catalog.Owner, catalogEpoch: e.CatalogRead().Epoch()}
	reused := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, expected)
	captured, err := reused.InputConstant("acl_unit_a=rw*/acl_unit_b", d.Usage(), position)
	if err != nil || !sqltype.SameDatum(value, captured) {
		t.Fatal("prepared literal re-resolved old spelling", err)
	}
	text, err = d.OutputWithEnvironment(context.Background(), captured, sqltype.OutputEnvironment{ACLItem: fresh})
	if err != nil || text != "acl_unit_renamed=rw*/acl_unit_b" {
		t.Fatal("prepared current output", text, err)
	}
	if _, err := d.InputWithEnvironment(context.Background(), "acl_unit_a=r/acl_unit_b", -1, sqltype.InputEnvironment{ACLItem: e.CaptureACLItemInput(AuthorizationView{View: access.View{Roles: e.Roles()}}, nil)}); err == nil {
		t.Fatal("fresh bind adopted renamed source spelling")
	}
	retained := make([]sqltype.Value, 2)
	if ok, err := rows.Next(context.Background(), retained); err != nil || !ok {
		t.Fatal(ok, err)
	}
	for i, v := range retained {
		text, err := v.Type().OutputWithEnvironment(context.Background(), v, rows.OutputEnvironment())
		want := "acl_unit_a=rw*/acl_unit_b"
		if i == 1 {
			want = "[0:1]={acl_unit_a=rw*/acl_unit_b,NULL}"
		}
		if err != nil || text != want {
			t.Fatal("held role snapshot", text, err, want)
		}
	}
	if rows.Schema()[0].Usage.Type != d || rows.Schema()[1].Usage.Type != array {
		t.Fatal("held descriptors changed")
	}
	t.Run("bootstrap_default_rename", func(t *testing.T) {
		bootstrapEngine := New()
		typeACL := aclUnitType(t, bootstrapEngine, sqltype.ACLItem)
		old := bootstrapEngine.Roles()
		value, err := typeACL.InputWithEnvironment(context.Background(), "=r", -1, sqltype.InputEnvironment{ACLItem: bootstrapEngine.CaptureACLItemInput(AuthorizationView{View: access.View{Roles: old}}, nil)})
		if err != nil {
			t.Fatal(err)
		}
		role, err := old.Descriptor(bootstrapEngine.RoleStore.Domain.Principal(10))
		if err != nil {
			t.Fatal(err)
		}
		bootstrapEngine.Mu.Lock()
		next := cloneRoleFixture(bootstrapEngine.RoleStore.Published)
		role.Name = "acl_unit_bootstrap"
		putFixtureRole(next, role)
		bootstrapEngine.RoleStore.PublishLocked(next)
		bootstrapEngine.Mu.Unlock()
		output := regobj.ACLItemOutput{Owner: &bootstrapEngine.Catalog.Owner, Roles: bootstrapEngine.Roles()}
		text, err := typeACL.OutputWithEnvironment(context.Background(), value, sqltype.OutputEnvironment{ACLItem: output})
		if err != nil || text != "=r/acl_unit_bootstrap" {
			t.Fatal("default grantor followed a name rather than identity10", text, err)
		}
		_, _, grantor, _, _, err := value.ACLItem()
		if err != nil || grantor != 10 {
			t.Fatal(grantor, err)
		}
	})
}

func TestACLItemEqualityAdmission(t *testing.T) {
	e := New()
	d := aclUnitType(t, e, sqltype.ACLItem)
	array := aclUnitType(t, e, sqltype.ACLItemArray)
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	for _, operator := range []string{"<>", "!=", "<", "<=", ">", ">="} {
		raw := parser.OperatorExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: operator}}}, Pos: parser.Position{Character: 29}}
		_, _, err := b.ResolveComparisonTypes(raw, d, nil)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42883" || diagnostic.Position != "29" {
			t.Fatal("operator-before-input boundary", operator, err)
		}
	}
	for _, typ := range []*sqltype.Descriptor{d, array} {
		comparison, err := sqltype.BindComparison(typ, typ, sqltype.CollationRef{}, false)
		if typ == d {
			if err != nil {
				t.Fatal("scalar ACL equality provider absent", err)
			}
			aclUnitRoles(e, "acl_compare_unit")
			value := aclUnitValue(t, e, "=r/acl_compare_unit")
			if equal, err := comparison.EqualValues(context.Background(), value, value, sqltype.InputEnvironment{}); err != nil || !equal {
				t.Fatal("scalar ACL equality", equal, err)
			}
			if _, err := comparison.Compare(context.Background(), value, value, sqltype.InputEnvironment{}); err == nil {
				t.Fatal("scalar ACL gained total ordering")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			empty, err := array.Input(context.Background(), "{}", -1)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := comparison.Compare(context.Background(), empty, empty, sqltype.InputEnvironment{}); err == nil {
				t.Fatal("ACL array gained demanded ordering")
			}
		}
		if _, err := sqltype.BindGrouping(typ.Usage()); err == nil {
			t.Fatal("ACL gained bounded native grouping")
		}
	}
	if _, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, d, d, aclUnitType(t, e, sqltype.Bool), sqltype.CollationRef{}, false); err != nil {
		t.Fatal("ACL equality absent", err)
	}
}
