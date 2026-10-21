package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func TestACLItemPrincipalRemap(t *testing.T) {
	source, target := New(), New()
	aclUnitRoles(source, "acl_remap_a", "acl_remap_b")
	aclUnitRoles(target, "acl_remap_spacer", "acl_remap_spacer2", "acl_remap_a", "acl_remap_b")
	value := aclUnitValue(t, source, "acl_remap_a=a*r*w*d*D*x*t*X*U*C*T*c*s*A*m*/acl_remap_b")
	public := aclUnitValue(t, source, "=r*/acl_remap_b")
	d, td := aclUnitType(t, source, sqltype.ACLItem), aclUnitType(t, target, sqltype.ACLItem)
	array, ta := aclUnitType(t, source, sqltype.ACLItemArray), aclUnitType(t, target, sqltype.ACLItemArray)
	fromDomain, _ := sqltype.Domain(45000, "acl_domain", "acl_domain", array.Usage())
	toDomain, _ := sqltype.Domain(45000, "acl_domain", "acl_domain", ta.Usage())
	av, err := sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Lower: -1, Length: 2}, {Lower: 3, Length: 2}}, []sqltype.Value{value, sqltype.Null(d), public, value})
	if err != nil {
		t.Fatal(err)
	}
	dv, err := fromDomain.Wrap(av)
	if err != nil {
		t.Fatal(err)
	}
	fromComposite, _ := sqltype.Composite(45001, "acl_composite", "acl_composite", 0, []sqltype.CompositeMember{{Attribute: 1, Name: "scalar", Usage: d.Usage()}, {Attribute: 2, Name: "nested", Usage: fromDomain.Usage()}})
	toComposite, _ := sqltype.Composite(45001, "acl_composite", "acl_composite", 0, []sqltype.CompositeMember{{Attribute: 1, Name: "scalar", Usage: td.Usage()}, {Attribute: 2, Name: "nested", Usage: toDomain.Usage()}})
	cv, err := sqltype.OwnedCompositeValue(fromComposite, []sqltype.Value{value, dv})
	if err != nil {
		t.Fatal(err)
	}
	importer := target.aclItemImporter(source, source.Roles(), target.Roles(), nil)
	mapped, err := typedef.ReownDomainDatum(cv, toComposite, nil, nil, importer)
	if err != nil {
		t.Fatal(err)
	}
	_, g, grantor, p, o, err := mapped.CompositeField(0).ACLItem()
	granteeRole, _ := target.Roles().Lookup("acl_remap_a")
	grantorRole, _ := target.Roles().Lookup("acl_remap_b")
	if err != nil || g != uint32(granteeRole.Ref.ID()) || grantor != uint32(grantorRole.Ref.ID()) || p != 32767 || o != 32767 || mapped.Type() != toComposite {
		t.Fatal("both identities/options", g, grantor, p, o, err)
	}
	nested, err := mapped.CompositeField(1).BaseValue()
	if err != nil {
		t.Fatal(err)
	}
	if nested.Type() != ta || !nested.ArrayElement(1).IsNull() {
		t.Fatal("recursive array/domain descriptors/NULL")
	}
	_, g, grantor, p, o, err = nested.ArrayElement(2).ACLItem()
	if err != nil || g != 0 || grantor != uint32(grantorRole.Ref.ID()) || p != 2 || o != 2 {
		t.Fatal("PUBLIC/options remap", g, grantor, p, o, err)
	}
	if !sqltype.SameDatum(cv.CompositeField(0), value) || cv.CompositeField(1).Type() != fromDomain || av.ArrayElement(0) != value {
		t.Fatal("source payload changed")
	}
	// A stable descriptor does not imply stable principal authority.
	sameDescriptor, err := typedef.ReownDomainDatum(value, d, nil, nil, importer)
	if err != nil || sameDescriptor == value || sameDescriptor.Type() != d {
		t.Fatal("same-descriptor principal import skipped", err)
	}
	unchanged, err := typedef.ReownDomainDatum(mapped, toComposite, nil, nil, importer)
	if err != nil || unchanged != mapped {
		t.Fatal("destination-owned data copied/reimported", err)
	}
	foreign := New()
	aclUnitRoles(foreign, "acl_remap_a", "acl_remap_b")
	coincident := aclUnitValue(t, foreign, "acl_remap_a=r/acl_remap_b")
	if _, err := typedef.ReownDomainDatum(coincident, td, nil, nil, importer); err == nil {
		t.Fatal("foreign coincident IDs imported")
	}
	// The literal retagger must apply ACL import even without type changes.
	next := catalog.CloneState(source.catalog.state)
	next.SetACLItemImport(importer)
	literal := plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: value}
	rebound, changed, err := stored.RetagTypes(target.catalog.read(next), literal, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t })
	if err != nil || !changed || rebound.(plan.LiteralExpr).Value == value {
		t.Fatal("constant principal import skipped", changed, err)
	}
	// Carrier-only changes must also reach enclosing CHECK/index expressions;
	// otherwise their parent copy-on-change gate republishes source literals.
	boolean := aclUnitType(t, source, sqltype.Bool)
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, d, d, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	comparison := plan.ComparisonExpr{Info: expr.Info{Usage: boolean.Usage()}, Left: literal, Right: literal, Predicate: predicate, Operator: "="}
	rebound, changed, err = stored.RetagTypes(target.catalog.read(next), comparison, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t })
	if err != nil || !changed {
		t.Fatal("enclosing principal import skipped", changed, err)
	}
	imported := rebound.(plan.ComparisonExpr)
	if imported.Left.(plan.LiteralExpr).Value != sameDescriptor || imported.Right.(plan.LiteralExpr).Value != sameDescriptor || comparison.Left.(plan.LiteralExpr).Value != value {
		t.Fatal("enclosing import lost mapped carriers or mutated source")
	}
	if _, err := (scalarRenderer{Validation: true}).SQL(context.Background(), target.catalog.read(next), imported); err != nil {
		t.Fatal("target validation ran with source CHECK carriers", err)
	}
	retained, changed, err := stored.RetagTypes(target.catalog.read(next), imported, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t })
	if err != nil || changed || retained.(plan.ComparisonExpr).Left.(plan.LiteralExpr).Value != sameDescriptor {
		t.Fatal("destination-owned enclosing expression copied/reimported", changed, err)
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 45002}
	next.SetDescriptor(address, &catalog.Relation{ObjectAddress: address, Columns: []catalog.Column{{Name: "a", Usage: d.Usage()}}})
	original := &storage.Relation{Versions: []storage.RowVersion{{Values: []sqltype.Value{value}}}}
	stores := map[ids.ObjectAddress]*storage.Relation{address: original}
	if err := reownCatalogScalarTypes(next, stores, nil); err != nil {
		t.Fatal(err)
	}
	if stores[address] == original || original.Versions[0].Values[0] != value || stores[address].Versions[0].Values[0] == value {
		t.Fatal("same-descriptor row import/copy-on-change")
	}
}

func TestACLItemRestoreFence(t *testing.T) {
	source, target := New(), New()
	aclUnitRoles(source, "acl_fence_a", "acl_fence_b")
	aclUnitRoles(target, "acl_fence_a", "acl_fence_b")
	value := aclUnitValue(t, source, "acl_fence_a=r*/acl_fence_b")
	captured := target.Roles()
	old, _ := captured.Lookup("acl_fence_a")
	importer := target.aclItemImporter(source, source.Roles(), captured, nil)
	target.mu.Lock()
	next := cloneRoleFixture(target.roles.published)
	retireFixtureRole(next, old.Ref)
	old.Ref = target.roles.domain.Principal(30000)
	putFixtureRole(next, old)
	target.roles.published = next
	target.mu.Unlock()
	if _, err := typedef.ReownDomainDatum(value, aclUnitType(t, target, sqltype.ACLItem), nil, nil, importer); err == nil {
		t.Fatal("drop/recreate crossed captured target fence")
	}
	// Expected importTargets rejects a different captured incarnation as well.
	original, _ := source.Roles().Lookup("acl_fence_a")
	expected := map[authorization.PrincipalRef]authorization.PrincipalRef{original.Ref: target.roles.domain.Principal(20000)}
	importer = target.aclItemImporter(source, source.Roles(), target.Roles(), expected)
	if _, err := typedef.ReownDomainDatum(value, aclUnitType(t, target, sqltype.ACLItem), nil, nil, importer); err == nil {
		t.Fatal("expected target incarnation ignored")
	}
	freshSource, freshTarget := New(), New()
	if _, err := freshTarget.replaceCatalog(context.Background(), freshSource, freshSource.CatalogRead(), nil); err != nil {
		t.Fatal(err)
	}
	if freshTarget.catalog.state.ACLItemImport() != nil || freshSource.catalog.state.ACLItemImport() != nil {
		t.Fatal("import callback published")
	}
	aclUnitRoles(freshSource, "acl_missing_target")
	freshSource.mu.Lock()
	public := freshSource.catalog.state.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	copy := *public
	role, _ := freshSource.roles.read(freshSource.catalog.owner(), freshSource.catalog.read(freshSource.catalog.state), freshSource.roles.published).Lookup("acl_missing_target")
	copy.Owner = role.Ref
	freshSource.catalog.state = catalog.CloneState(freshSource.catalog.state)
	freshSource.catalog.state.SetDescriptor(copy.Address(), &copy)
	freshSource.mu.Unlock()
	before := freshTarget.catalog.state
	if _, err := freshTarget.replaceCatalog(context.Background(), freshSource, freshSource.CatalogRead(), nil); err == nil {
		t.Fatal("missing target principal admitted")
	}
	if freshTarget.catalog.state != before || freshTarget.catalog.state.ACLItemImport() != nil || freshSource.catalog.state.ACLItemImport() != nil {
		t.Fatal("failure published state/import callback")
	}
}

func TestACLItemStaleDump(t *testing.T) {
	e := New()
	aclUnitRoles(e, "acl_dump_a", "acl_dump_b")
	value := aclUnitValue(t, e, "acl_dump_a=r*/acl_dump_b")
	captured := regobj.ACLItemOutput{Owner: e.catalog.owner(), Roles: e.Roles(), Lossless: true}
	ctx := regobj.ACLItemOutputContext(context.Background(), captured)
	literal := plan.LiteralExpr{Info: expr.Info{Usage: value.Type().Usage()}, Value: value}
	if text, err := regobj.Literal(ctx, e.CatalogRead(), literal); err != nil || text != "CAST('acl_dump_a=r*/acl_dump_b' AS ACLITEM)" {
		t.Fatal(text, err)
	}
	if _, err := regobj.Literal(context.Background(), e.CatalogRead(), literal); err == nil {
		t.Fatal("literal rendered without authority")
	}
	a, _ := e.Roles().Lookup("acl_dump_a")
	e.mu.Lock()
	next := cloneRoleFixture(e.roles.published)
	retireFixtureRole(next, a.Ref)
	e.roles.published = next
	e.mu.Unlock()
	ordinary := regobj.ACLItemOutput{Owner: e.catalog.owner(), Roles: e.Roles()}
	text, err := value.Type().OutputWithEnvironment(context.Background(), value, sqltype.OutputEnvironment{ACLItem: ordinary})
	if err != nil || text != "20000=r*/acl_dump_b" {
		t.Fatal("stale ordinary output", text, err)
	}
	if err := regobj.PreflightACLItemValue(context.Background(), regobj.ACLItemOutput{Owner: e.catalog.owner(), Roles: e.Roles(), Lossless: true}, value); err == nil {
		t.Fatal("stale dump leaf accepted")
	}
	if text, err := regobj.Literal(ctx, e.CatalogRead(), literal); err != nil || text != "CAST('acl_dump_a=r*/acl_dump_b' AS ACLITEM)" {
		t.Fatal("captured role snapshot consulted live roles", text, err)
	}
	if _, err := (pgRenderer{View: e.CatalogRead()}).Literal(context.Background(), literal); err == nil {
		t.Fatal("renderer accepted missing ACL authority")
	}
	if text, err := (pgRenderer{View: e.CatalogRead(), Output: sqltype.OutputEnvironment{ACLItem: ordinary}}).Literal(context.Background(), literal); err != nil || text == "" {
		t.Fatal("ordinary renderer inherited dump lossless policy", text, err)
	}
}
