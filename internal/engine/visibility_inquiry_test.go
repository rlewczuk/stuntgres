package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestVisibilityInquiryCollationSelection(t *testing.T) {
	address := func(id ids.OID) ids.ObjectAddress {
		return ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: id}
	}
	for _, tc := range []struct {
		name       string
		candidates []collationCandidate
		want       ids.ObjectAddress
	}{
		{"exact-first", []collationCandidate{{address(1), 6}, {address(2), -1}, {address(3), 8}}, address(1)},
		{"fallback-first", []collationCandidate{{address(2), -1}, {address(3), 8}, {address(1), 6}}, address(1)},
		{"fallback-only", []collationCandidate{{address(2), -1}}, address(2)},
		{"wrong-only", []collationCandidate{{address(3), 8}}, ids.ObjectAddress{}},
		{"wrong-and-fallback", []collationCandidate{{address(3), 8}, {address(2), -1}}, address(2)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := selectCollation(tc.candidates, 6); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
	// Exercise the actual namespace-first inquiry path with independently built
	// name candidates, without admitting unsupported native collation DDL.
	a := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20001}
	b := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20002}
	first := &collationDescriptor{OID: 1, ObjectName: "same", Encoding: -1}
	second := &collationDescriptor{OID: 2, ObjectName: "same", Encoding: 6}
	state := catalogStateFixture(0, map[ids.ObjectAddress]Descriptor{first.Address(): first, second.Address(): second}, map[ids.NameKey]ids.ObjectAddress{
		{ClassID: ids.CollationClassID, Namespace: a, Name: "same"}: first.Address(),
		{ClassID: ids.CollationClassID, Namespace: b, Name: "same"}: second.Address(),
	})
	view := catalog.NewRead(nil, state)
	if !visibleCollation(view, []ids.ObjectAddress{a, b}, first) || visibleCollation(view, []ids.ObjectAddress{a, b}, second) {
		t.Fatal("later exact beat earlier fallback")
	}
	first.Encoding = 8
	if visibleCollation(view, []ids.ObjectAddress{a, b}, first) || !visibleCollation(view, []ids.ObjectAddress{a, b}, second) {
		t.Fatal("wrong encoding shadowed later usable candidate")
	}
	if visibleCollation(view, []ids.ObjectAddress{a}, first) {
		t.Fatal("ineligible subject became visible")
	}
}

func TestVisibilityInquiryProvidersAndStoredValidation(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	view := e.CatalogRead()
	if len(catalog.BuiltinConversions) != 128 || len(catalog.BuiltinCollations) != 7 {
		t.Fatal("fixed inventory cardinality")
	}
	for _, d := range view.Descriptors() {
		if d.Kind() == ids.CollationObject || d.Kind() == ids.ConversionObject {
			if err := e.catalog.Providers[d.Address().ClassID].Validate(view, d); err != nil {
				t.Fatal(err)
			}
		}
	}
	bad := *view.MustCollation(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: 950})
	bad.Encoding = 8
	if err := (collationProvider{}).Validate(view, &bad); err == nil {
		t.Fatal("invalid fixed collation accepted")
	}
	conversion := *view.MustConversion(ids.ObjectAddress{ClassID: ids.ConversionClassID, ObjectID: 4402})
	conversion.ObjectName = "made_up"
	if err := (conversionProvider{}).Validate(view, &conversion); err == nil {
		t.Fatal("invalid conversion accepted")
	}
	sessionSQL(t, s, "CREATE TABLE t(id OID CHECK(pg_type_is_visible(id) IS NOT NULL))")
	view = e.CatalogRead()
	relation := view.MustRelation(catalogRef(t, e, view, "t").Address)
	if !relation.CheckConstraints[0].VisibilitySnapshot {
		t.Fatal("missing CHECK capture manifest")
	}
	inquiry := relation.CheckConstraints[0].Expression.(nullTestExpression).child.(visibilityInquiryExpression)
	renderer := scalarRenderer{owner: relation}
	intType, _ := view.LookupType(sqltype.Int4)
	for _, tc := range []struct {
		name   string
		change func(*visibilityInquiryExpression)
	}{
		{"kind", func(n *visibilityInquiryExpression) { n.kind = 255 }},
		{"child", func(n *visibilityInquiryExpression) { n.arg = nil }},
		{"result", func(n *visibilityInquiryExpression) { n.Usage.Type = intType }},
		{"argument", func(n *visibilityInquiryExpression) {
			c := n.arg.(columnExpression)
			c.Usage = intType.Usage()
			n.arg = c
		}},
		{"properties", func(n *visibilityInquiryExpression) { n.Property.RowDependent = false }},
		{"origin", func(n *visibilityInquiryExpression) { c := n.arg.(columnExpression); c.origin.Epoch++; n.arg = c }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := inquiry
			tc.change(&n)
			if _, err := renderer.sql(context.Background(), view, n); err == nil {
				t.Fatal("malformed stored inquiry accepted")
			}
			if tc.name != "origin" {
				// Exercise both newly integrated stored-tree consumers with the
				// independently malformed signature/property contract.
				if _, err := (pgRenderer{view: view, relation: relation}).sql(context.Background(), n, pgRenderParent{}); err == nil {
					t.Fatal("PostgreSQL renderer accepted malformed inquiry")
				}
				if _, err := (boundTree{expression: n}).StructuralOutput(context.Background()); err == nil {
					t.Fatal("structural serializer accepted malformed inquiry")
				}
			}
		})
	}
	broken := *relation
	broken.CheckConstraints = append([]catalog.CheckConstraint(nil), relation.CheckConstraints...)
	broken.CheckConstraints[0].VisibilitySnapshot = false
	if err := validateChecks(view, &broken, map[string]bool{}); err == nil {
		t.Fatal("invalid capture manifest accepted")
	}
}

func TestVisibilityInquiryCapturedFrontier(t *testing.T) {
	ctx := context.Background()
	e := New()
	s := sessionStart(t, e, nil)
	peer := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE anchor(id INT)")
	sessionSQL(t, s, "CREATE TABLE checks(id OID CHECK(pg_table_is_visible(id) IS NOT NULL))")
	view := e.CatalogRead()
	anchor := catalogRef(t, e, view, "anchor")
	op, err := s.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	e.mu.Lock()
	err = e.captureVisibilitySnapshotLocked(scope)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	catalog := scope.visibilityCatalog
	sessionSQL(t, peer, "DROP TABLE anchor")
	e.mu.Lock()
	source, err := e.captureQuerySourcesLocked(scope, nil, true)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer source.close()
	if scope.visibilityCatalog != catalog {
		t.Fatal("later provider replaced captured catalog")
	}
	oid, _ := catalog.LookupType(sqltype.Oid)
	boolean, _ := catalog.LookupType(sqltype.Bool)
	value, err := sqltype.OIDValue(oid, uint32(anchor.Address.ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	n := visibilityInquiryExpression{Info: expr.Info{Usage: boolean.Usage()}, kind: tableVisibility, arg: literalExpression{Info: expr.Info{Usage: oid.Usage()}, value: value}}
	got, err := n.Eval(&EvalContext{Context: ctx, statementScope: scope})
	if err != nil {
		t.Fatal(err)
	}
	visible, err := got.Boolean()
	if err != nil || !visible {
		t.Fatal("captured identity changed", got, err)
	}
}

func TestVisibilityInquiryAuthorizationAndPathKinds(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER visibility_reader", "CREATE SCHEMA visible", "CREATE TABLE visible.t(id INT)", "SET search_path=visible"} {
		roleExecute(t, e, op, q)
	}
	reader, _ := e.Roles().Lookup("visibility_reader")
	ns := schemaRef(t, e, "visible")
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns.Address, Name: "t"})
	query := fmt.Sprintf("SELECT pg_table_is_visible(%d::oid)", address.ObjectID)
	for _, rights := range []authorization.Privileges{authorization.UsagePrivilege, 0, authorization.UsagePrivilege} {
		if err := e.SetObjectACL(context.Background(), ns, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
			t.Fatal(err)
		}
		roleExecute(t, e, op, "SET SESSION AUTHORIZATION visibility_reader")
		got := tuples(roleExecute(t, e, op, query))
		if len(got) != 1 || got[0][0] != (rights == authorization.UsagePrivilege) {
			t.Fatal("schema authority/subject privilege", got, rights)
		}
		roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	}
	description, err := e.Describe(op, statement(t, "SELECT pg_type_is_visible(NULL)"))
	if err != nil {
		t.Fatal(err)
	}
	column := description.Results[0]
	if column.Usage.Type.ID() != sqltype.Bool || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 0 || column.OriginTable != 0 || column.OriginAttribute != 0 {
		t.Fatal(column)
	}
	for _, q := range []string{"CREATE TEMP SEQUENCE own_temp", "SET search_path=pg_temp,visible,pg_catalog"} {
		roleExecute(t, e, op, q)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	temp := temporaryNamespace(auth)
	for _, kind := range []lookupKind{collationLookup, conversionLookup} {
		path, err := e.lookupPath(auth, kind)
		if err != nil {
			e.mu.Unlock()
			t.Fatal(err)
		}
		for _, a := range path {
			if a == temp {
				e.mu.Unlock()
				t.Fatal("TEMP entered inquiry path", kind)
			}
		}
	}
	e.mu.Unlock()
	roleExecute(t, e, op, "ALTER SCHEMA pg_catalog RENAME TO renamed_catalog")
	if got := tuples(roleExecute(t, e, op, "SELECT pg_type_is_visible(23),pg_collation_is_visible(950),pg_conversion_is_visible(4402)")); len(got) != 1 || got[0][0] != true || got[0][1] != true || got[0][2] != true {
		t.Fatal(got)
	}
	roleExecute(t, e, op, `CREATE TABLE visible.fixed_collation(x TEXT COLLATE renamed_catalog."C")`)
}
