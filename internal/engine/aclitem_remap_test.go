package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
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
	mapped, err := reownDomainDatum(cv, toComposite, nil, nil, importer)
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
	sameDescriptor, err := reownDomainDatum(value, d, nil, nil, importer)
	if err != nil || sameDescriptor == value || sameDescriptor.Type() != d {
		t.Fatal("same-descriptor principal import skipped", err)
	}
	unchanged, err := reownDomainDatum(mapped, toComposite, nil, nil, importer)
	if err != nil || unchanged != mapped {
		t.Fatal("destination-owned data copied/reimported", err)
	}
	foreign := New()
	aclUnitRoles(foreign, "acl_remap_a", "acl_remap_b")
	coincident := aclUnitValue(t, foreign, "acl_remap_a=r/acl_remap_b")
	if _, err := reownDomainDatum(coincident, td, nil, nil, importer); err == nil {
		t.Fatal("foreign coincident IDs imported")
	}
	// The literal retagger must apply ACL import even without type changes.
	next := cloneCatalog(source.catalog.state)
	next.aclItemImport = importer
	literal := literalExpression{expressionInfo: expressionInfo{Usage: d.Usage()}, value: value}
	rebound, changed, err := retagBoundTypes(target.catalog.read(next), literal, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t })
	if err != nil || !changed || rebound.(literalExpression).value == value {
		t.Fatal("constant principal import skipped", changed, err)
	}
	// Carrier-only changes must also reach enclosing CHECK/index expressions;
	// otherwise their parent copy-on-change gate republishes source literals.
	boolean := aclUnitType(t, source, sqltype.Bool)
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, d, d, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	comparison := comparisonExpression{expressionInfo: expressionInfo{Usage: boolean.Usage()}, left: literal, right: literal, predicate: predicate, operator: "="}
	rebound, changed, err = retagBoundTypes(target.catalog.read(next), comparison, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t })
	if err != nil || !changed {
		t.Fatal("enclosing principal import skipped", changed, err)
	}
	imported := rebound.(comparisonExpression)
	if imported.left.(literalExpression).value != sameDescriptor || imported.right.(literalExpression).value != sameDescriptor || comparison.left.(literalExpression).value != value {
		t.Fatal("enclosing import lost mapped carriers or mutated source")
	}
	if _, err := (scalarRenderer{validation: true}).sql(context.Background(), target.catalog.read(next), imported); err != nil {
		t.Fatal("target validation ran with source CHECK carriers", err)
	}
	retained, changed, err := retagBoundTypes(target.catalog.read(next), imported, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t })
	if err != nil || changed || retained.(comparisonExpression).left.(literalExpression).value != sameDescriptor {
		t.Fatal("destination-owned enclosing expression copied/reimported", changed, err)
	}
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 45002}
	next.setDescriptor(address, &relationDescriptor{address: address, columns: []ColumnDescriptor{{Name: "a", Usage: d.Usage()}}})
	original := &relationStorage{versions: []rowVersion{{values: []sqltype.Value{value}}}}
	stores := map[ids.ObjectAddress]*relationStorage{address: original}
	if err := reownCatalogScalarTypes(next, stores, nil); err != nil {
		t.Fatal(err)
	}
	if stores[address] == original || original.versions[0].values[0] != value || stores[address].versions[0].values[0] == value {
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
	next := cloneRoleFixture(target.roles)
	retireFixtureRole(next, old.Ref)
	old.Ref = target.authorizationDomain.Principal(30000)
	putFixtureRole(next, old)
	target.roles = next
	target.mu.Unlock()
	if _, err := reownDomainDatum(value, aclUnitType(t, target, sqltype.ACLItem), nil, nil, importer); err == nil {
		t.Fatal("drop/recreate crossed captured target fence")
	}
	// Expected importTargets rejects a different captured incarnation as well.
	original, _ := source.Roles().Lookup("acl_fence_a")
	expected := map[authorization.PrincipalRef]authorization.PrincipalRef{original.Ref: target.authorizationDomain.Principal(20000)}
	importer = target.aclItemImporter(source, source.Roles(), target.Roles(), expected)
	if _, err := reownDomainDatum(value, aclUnitType(t, target, sqltype.ACLItem), nil, nil, importer); err == nil {
		t.Fatal("expected target incarnation ignored")
	}
	freshSource, freshTarget := New(), New()
	if _, err := freshTarget.replaceCatalog(context.Background(), freshSource, freshSource.CatalogRead(), nil); err != nil {
		t.Fatal(err)
	}
	if freshTarget.catalog.state.aclItemImport != nil || freshSource.catalog.state.aclItemImport != nil {
		t.Fatal("import callback published")
	}
	aclUnitRoles(freshSource, "acl_missing_target")
	freshSource.mu.Lock()
	public := freshSource.catalog.state.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID})
	copy := *public
	role, _ := freshSource.roleRead(freshSource.catalog.read(freshSource.catalog.state), freshSource.roles).Lookup("acl_missing_target")
	copy.owner = role.Ref
	freshSource.catalog.state = cloneCatalog(freshSource.catalog.state)
	freshSource.catalog.state.setDescriptor(copy.address, &copy)
	freshSource.mu.Unlock()
	before := freshTarget.catalog.state
	if _, err := freshTarget.replaceCatalog(context.Background(), freshSource, freshSource.CatalogRead(), nil); err == nil {
		t.Fatal("missing target principal admitted")
	}
	if freshTarget.catalog.state != before || freshTarget.catalog.state.aclItemImport != nil || freshSource.catalog.state.aclItemImport != nil {
		t.Fatal("failure published state/import callback")
	}
}

func TestACLItemStaleDump(t *testing.T) {
	e := New()
	aclUnitRoles(e, "acl_dump_a", "acl_dump_b")
	value := aclUnitValue(t, e, "acl_dump_a=r*/acl_dump_b")
	captured := capturedACLItemOutput{owner: e, roles: e.Roles(), lossless: true}
	ctx := aclItemOutputContext(context.Background(), captured)
	literal := literalExpression{expressionInfo: expressionInfo{Usage: value.Type().Usage()}, value: value}
	if text, err := catalogLiteralSQL(ctx, e.CatalogRead(), literal); err != nil || text != "CAST('acl_dump_a=r*/acl_dump_b' AS ACLITEM)" {
		t.Fatal(text, err)
	}
	if _, err := catalogLiteralSQL(context.Background(), e.CatalogRead(), literal); err == nil {
		t.Fatal("literal rendered without authority")
	}
	a, _ := e.Roles().Lookup("acl_dump_a")
	e.mu.Lock()
	next := cloneRoleFixture(e.roles)
	retireFixtureRole(next, a.Ref)
	e.roles = next
	e.mu.Unlock()
	ordinary := capturedACLItemOutput{owner: e, roles: e.Roles()}
	text, err := value.Type().OutputWithEnvironment(context.Background(), value, sqltype.OutputEnvironment{ACLItem: ordinary})
	if err != nil || text != "20000=r*/acl_dump_b" {
		t.Fatal("stale ordinary output", text, err)
	}
	if err := preflightACLItemValue(context.Background(), capturedACLItemOutput{owner: e, roles: e.Roles(), lossless: true}, value); err == nil {
		t.Fatal("stale dump leaf accepted")
	}
	if text, err := catalogLiteralSQL(ctx, e.CatalogRead(), literal); err != nil || text != "CAST('acl_dump_a=r*/acl_dump_b' AS ACLITEM)" {
		t.Fatal("captured role snapshot consulted live roles", text, err)
	}
	if _, err := (pgRenderer{view: e.CatalogRead()}).literal(context.Background(), literal); err == nil {
		t.Fatal("renderer accepted missing ACL authority")
	}
	if text, err := (pgRenderer{view: e.CatalogRead(), output: sqltype.OutputEnvironment{ACLItem: ordinary}}).literal(context.Background(), literal); err != nil || text == "" {
		t.Fatal("ordinary renderer inherited dump lossless policy", text, err)
	}
}
