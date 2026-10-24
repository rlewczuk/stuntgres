package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
	"testing"
)

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
	if err := (catalog.CollationProvider{}).Validate(view, &bad); err == nil {
		t.Fatal("invalid fixed collation accepted")
	}
	conversion := *view.MustConversion(ids.ObjectAddress{ClassID: ids.ConversionClassID, ObjectID: 4402})
	conversion.ObjectName = "made_up"
	if err := (catalog.ConversionProvider{}).Validate(view, &conversion); err == nil {
		t.Fatal("invalid conversion accepted")
	}
	sessionSQL(t, s, "CREATE TABLE t(id OID CHECK(pg_type_is_visible(id) IS NOT NULL))")
	view = e.CatalogRead()
	relation := view.MustRelation(catalogRef(t, e, view, "t").Address)
	if !relation.Constraints.Check[0].VisibilitySnapshot {
		t.Fatal("missing CHECK capture manifest")
	}
	inquiry := relation.Constraints.Check[0].Expression.(plan.NullTestExpr).Child.(functions.VisibilityInquiry)
	renderer := stored.ScalarRenderer{Owner: relation}
	intType, _ := view.LookupType(sqltype.Int4)
	for _, tc := range []struct {
		name   string
		change func(*functions.VisibilityInquiry)
	}{
		{"kind", func(n *functions.VisibilityInquiry) { n.Kind = 255 }},
		{"child", func(n *functions.VisibilityInquiry) { n.Arg = nil }},
		{"result", func(n *functions.VisibilityInquiry) { n.Usage.Type = intType }},
		{"argument", func(n *functions.VisibilityInquiry) {
			c := n.Arg.(plan.ColumnExpr)
			c.Usage = intType.Usage()
			n.Arg = c
		}},
		{"properties", func(n *functions.VisibilityInquiry) { n.Property.RowDependent = false }},
		{"origin", func(n *functions.VisibilityInquiry) { c := n.Arg.(plan.ColumnExpr); c.Origin.Epoch++; n.Arg = c }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := inquiry
			tc.change(&n)
			if _, err := renderer.SQL(context.Background(), view, n); err == nil {
				t.Fatal("malformed stored inquiry accepted")
			}
			if tc.name != "origin" {
				// Exercise both newly integrated stored-tree consumers with the
				// independently malformed signature/property contract.
				if _, err := (stored.Deparser{View: view, Relation: relation}).SQL(context.Background(), n, stored.DeparseParent{}); err == nil {
					t.Fatal("PostgreSQL renderer accepted malformed inquiry")
				}
				if _, err := (plan.BoundTree{Expression: n}).StructuralOutput(context.Background()); err == nil {
					t.Fatal("structural serializer accepted malformed inquiry")
				}
			}
		})
	}
	broken := *relation
	broken.Constraints.Check = append([]catalog.CheckConstraint(nil), relation.Constraints.Check...)
	broken.Constraints.Check[0].VisibilitySnapshot = false
	if err := table.ValidateChecks(view, &broken, map[string]bool{}); err == nil {
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
	catalog := scope.inquiry.visibilityCatalog
	sessionSQL(t, peer, "DROP TABLE anchor")
	e.mu.Lock()
	source, err := e.captureQuerySourcesLocked(scope, nil, true)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if scope.inquiry.visibilityCatalog != catalog {
		t.Fatal("later provider replaced captured catalog")
	}
	oid, _ := catalog.LookupType(sqltype.Oid)
	boolean, _ := catalog.LookupType(sqltype.Bool)
	value, err := sqltype.OIDValue(oid, uint32(anchor.Address.ObjectID))
	if err != nil {
		t.Fatal(err)
	}
	n := functions.VisibilityInquiry{Info: expr.Info{Usage: boolean.Usage()}, Kind: functions.TableVisibility, Arg: plan.LiteralExpr{Info: expr.Info{Usage: oid.Usage()}, Value: value}}
	got, err := n.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Host: hostOf(scope)}})
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
		if err := e.SetObjectACL(context.Background(), ns, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
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
	for _, kind := range []analyze.LookupKind{analyze.CollationLookup, analyze.ConversionLookup} {
		path, err := lookupPath(auth, kind)
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
