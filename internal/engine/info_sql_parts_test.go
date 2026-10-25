package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func sqlPartsUnitInput(t *testing.T, f defaultUnitFixture, view catalog.Read) pgcatalog.Input {
	t.Helper()
	return sqlPartsAuthorityInput(t, dtpUnitAuthority(t, f, view))
}

func sqlPartsAuthorityInput(t *testing.T, auth AuthorizationView) pgcatalog.Input {
	t.Helper()
	view := auth.Catalog
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoSQLPartsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: EngineCatalogRuntime{Auth: auth, Database: "unit_database"}}
}

func sqlPartsUnitRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoSQLPartsOID)
	if !ok {
		t.Fatal("missing sql_parts producer")
	}
	captured, err := CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || len(captured.Anonymous) != 11 {
		t.Fatal("full anonymous capture", captured)
	}
	for _, row := range captured.Anonymous {
		if len(row) != 5 {
			t.Fatal("row width", len(row))
		}
		for i, value := range row {
			column := in.Source.Columns[i]
			id := sqltype.ID(13246)
			if i == 2 {
				id = 13256
			}
			base, _ := in.Catalog.LookupType(sqltype.VarChar)
			if value.Type() != column.Usage.Type || value.Type().ID() != id || value.Type().BaseType() != base || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.OriginTable != uint32(pgcatalog.InfoSQLPartsOID) || column.OriginAttribute != int16(i+1) {
				t.Fatal("domain/provenance", i, value, column)
			}
			if value.IsNull() != (i == 3) || i == 4 && sqlFeaturesUnitText(t, value) != "" {
				t.Fatal("typed NULL/empty boundary", i, value)
			}
		}
	}
	return captured.Anonymous
}

func sqlPartsUnitBag(t *testing.T, rows [][]sqltype.Value) map[[5]any]int {
	t.Helper()
	bag := make(map[[5]any]int)
	for _, row := range rows {
		var key [5]any
		for i, value := range row {
			if !value.IsNull() {
				key[i] = sqlFeaturesUnitText(t, value)
			}
		}
		bag[key]++
	}
	return bag
}

func TestInfoSQLPartsProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := sqlPartsUnitInput(t, f, view)
	rows := sqlPartsUnitRows(t, in)
	want := map[[5]any]int{
		{"1", "Framework (SQL/Framework)", "NO", nil, ""}:                                         1,
		{"2", "Foundation (SQL/Foundation)", "NO", nil, ""}:                                       1,
		{"3", "Call-Level Interface (SQL/CLI)", "NO", nil, ""}:                                    1,
		{"4", "Persistent Stored Modules (SQL/PSM)", "NO", nil, ""}:                               1,
		{"9", "Management of External Data (SQL/MED)", "NO", nil, ""}:                             1,
		{"10", "Object Language Bindings (SQL/OLB)", "NO", nil, ""}:                               1,
		{"11", "Information and Definition Schema (SQL/Schemata)", "NO", nil, ""}:                 1,
		{"13", "Routines and Types Using the Java Programming Language (SQL/JRT)", "NO", nil, ""}: 1,
		{"14", "XML-Related Specifications (SQL/XML)", "NO", nil, ""}:                             1,
		{"15", "Multi-Dimensional Arrays (SQL/MDA)", "NO", nil, ""}:                               1,
		{"16", "Property Graph Queries (SQL/PGQ)", "NO", nil, ""}:                                 1,
	}
	if got := sqlPartsUnitBag(t, rows); !reflect.DeepEqual(got, want) {
		t.Fatal("complete matrix", got)
	}
	source := in.Source
	if source.VirtualView || source.RelationKind != catalog.CatalogRelation || source.History.HasIndexes || source.RowType != 13424 || source.Owner != view.Owner().Principals.Principal(10) {
		t.Fatal("physical table identity/owner", source)
	}
	if acl := source.Security.ACL; acl.Explicit || len(acl.Entries) != 0 {
		t.Fatal("default absent ACL", acl)
	}
	rowtype, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13424})
	if !ok || rowtype.OwningRelation != source.Address() || rowtype.Array != 13423 {
		t.Fatal("owned rowtype", rowtype)
	}
	array, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13423})
	if !ok || array.OwningRelation != source.Address() || array.SQLType.Element() != rowtype.SQLType {
		t.Fatal("reciprocal array", array)
	}
	if len(source.ColumnMeta.CatalogAttributes) != 5 || len(source.ColumnMeta.Defaults) != 0 {
		t.Fatal("attribute width")
	}
	for _, attribute := range source.ColumnMeta.CatalogAttributes {
		if attribute.NotNull || attribute.Collation != 950 || attribute.Dimensions != 0 {
			t.Fatal("physical nullable attribute", attribute)
		}
	}
	second := sqlPartsUnitRows(t, in)
	other := newDefaultUnitFixture(t)
	if !reflect.DeepEqual(sqlPartsUnitBag(t, second), want) || !reflect.DeepEqual(sqlPartsUnitBag(t, sqlPartsUnitRows(t, sqlPartsUnitInput(t, other, other.e.CatalogRead()))), want) {
		t.Fatal("capture/engine independence")
	}
	rows[0][0] = sqltype.Null(rows[0][0].Type())
	if second[0][0].IsNull() || !reflect.DeepEqual(sqlPartsUnitBag(t, sqlPartsUnitRows(t, in)), want) {
		t.Fatal("mutable capture buffers shared")
	}
}

func TestInfoSQLPartsCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := sqlPartsUnitInput(t, f, view)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoSQLPartsOID)
	foreign := New()
	assertInvalid := func(label string, bad pgcatalog.Input) {
		t.Helper()
		rows, err := produce(bad)
		var state interface{ SQLState() string }
		if rows != nil || err == nil || errors.As(err, &state) {
			t.Fatal("invalid capture must return ordinary error and no rows", label, rows, err)
		}
	}
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &foreign.Catalog.Owner },
		func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { in.Catalog = catalog.Read{} },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { copied := *in.Source; in.Source = &copied },
		func(in *pgcatalog.Input) {
			r := in.Runtime.(EngineCatalogRuntime)
			r.Auth.RoleRecord = nil
			in.Runtime = r
		},
		func(in *pgcatalog.Input) {
			r := in.Runtime.(EngineCatalogRuntime)
			r.Auth.State.CurrentUser = authorization.PrincipalRef{}
			in.Runtime = r
		},
		func(in *pgcatalog.Input) {
			r := in.Runtime.(EngineCatalogRuntime)
			r.Auth.State.CurrentUser = foreign.Roles().LookupRef("postgres")
			in.Runtime = r
		},
	} {
		bad := in
		mutate(&bad)
		assertInvalid("authority", bad)
	}
	for _, malformed := range []string{"width", "base_column", "wrong_domain", "foreign_character", "foreign_support", "missing_character", "missing_support", "missing_varchar", "foreign_varchar", "catalog_foreign_character", "catalog_foreign_support"} {
		bad := catalog.CloneState(view.State())
		copy := *in.Source
		copy.Columns = append([]catalog.Column(nil), in.Source.Columns...)
		switch malformed {
		case "width":
			copy.Columns = copy.Columns[:4]
		case "base_column":
			copy.Columns[0].Usage.Type, _ = view.LookupType(sqltype.VarChar)
		case "wrong_domain":
			copy.Columns[2].Usage.Type, _ = view.LookupType(13246)
		case "foreign_character":
			copy.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(13246)
		case "foreign_support":
			copy.Columns[2].Usage.Type, _ = foreign.CatalogRead().LookupType(13256)
		default:
			id := ids.OID(13246)
			if malformed == "missing_support" || malformed == "catalog_foreign_support" {
				id = 13256
			} else if malformed == "missing_varchar" || malformed == "foreign_varchar" {
				id = ids.OID(sqltype.VarChar)
			}
			address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id}
			if malformed == "foreign_varchar" || malformed == "catalog_foreign_character" || malformed == "catalog_foreign_support" {
				descriptor := *view.Get(address).(*catalog.Type)
				descriptor.SQLType, _ = foreign.CatalogRead().LookupType(sqltype.ID(id))
				bad.SetDescriptor(address, &descriptor)
			} else {
				bad.RemoveDescriptor(address)
			}
		}
		bad.SetDescriptor(copy.Address(), &copy)
		assertInvalid(malformed, sqlPartsUnitInput(t, f, f.e.Catalog.Read(bad)))
	}
	sqlPartsUnitRows(t, in)
}

func TestInfoSQLPartsCancellation(t *testing.T) {
	f := newDefaultUnitFixture(t)
	in := sqlPartsUnitInput(t, f, f.e.CatalogRead())
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoSQLPartsOID)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := in
	canceled.Context = ctx
	if rows, err := produce(canceled); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancellation exposed rows", rows, err)
	}
	// Entry + eleven rows + final publication: no prefix may escape.
	for _, remaining := range []int{2, 7, 13} {
		canceled.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(canceled); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation exposed prefix", remaining, rows, err)
		}
	}
	sqlPartsUnitRows(t, in)
}

func TestInfoSQLPartsCatalogACLRecheck(t *testing.T) {
	f := newDefaultUnitFixture(t)
	e := f.e
	d := e.CatalogRead().MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoSQLPartsOID})
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	role := roleAttributeCreate(t, e, admin, "sql_parts_reader", true, parser.RoleOptions{})
	reader, readerDone := roleOperation(t, e, role.Name)
	defer readerDone()
	requirement := access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege}
	authority := func() AuthorizationView {
		t.Helper()
		auth, err := OperationAuthorization(reader)
		if err != nil {
			t.Fatal(err)
		}
		return auth
	}
	denied := authority()
	domainUnitDiagnostic(t, denied.Check(requirement), "42501", "permission denied for table sql_parts")
	adminAuth, err := OperationAuthorization(admin)
	if err != nil {
		t.Fatal(err)
	}
	if err := adminAuth.Check(requirement); err != nil {
		t.Fatal("owner/superuser SELECT", err)
	}
	sqlPartsUnitRows(t, sqlPartsAuthorityInput(t, adminAuth))
	namespace := denied.Catalog.MustNamespace(d.Namespace())
	nsRef := e.Catalog.Domain.Ref(denied.Catalog.Epoch(), namespace.Address())
	if err := denied.CanUse(nsRef); err != nil {
		t.Fatal("independent PUBLIC schema USAGE", err)
	}
	grant := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: d.Owner, Grantee: authorization.ACLGrantee{Role: role.Ref}, Privileges: authorization.SelectPrivilege}}}
	if err := e.SetObjectACL(t.Context(), ref, grant); err != nil {
		t.Fatal(err)
	}
	allowed := authority()
	if err := allowed.Check(requirement); err != nil {
		t.Fatal("explicit reader SELECT", err)
	}
	matrix := sqlPartsUnitBag(t, sqlPartsUnitRows(t, sqlPartsAuthorityInput(t, allowed)))
	domainUnitDiagnostic(t, denied.Check(requirement), "42501", "permission denied for table sql_parts")
	if err := e.SetObjectACL(t.Context(), ref, d.Security.ACL); err != nil {
		t.Fatal(err)
	}
	revoked := authority()
	domainUnitDiagnostic(t, revoked.Check(requirement), "42501", "permission denied for table sql_parts")
	if err := allowed.Check(requirement); err != nil {
		t.Fatal("retained grant mutated", err)
	}
	if !reflect.DeepEqual(matrix, sqlPartsUnitBag(t, sqlPartsUnitRows(t, sqlPartsAuthorityInput(t, allowed)))) {
		t.Fatal("retained provider root changed")
	}
	if allowed.Catalog.MustRelation(d.Address()) == revoked.Catalog.MustRelation(d.Address()) || denied.Catalog.MustRelation(d.Address()).Security.ACL.Explicit || revoked.Catalog.MustRelation(d.Address()).Security.ACL.Explicit {
		t.Fatal("ACL roots not isolated/default restored")
	}
	// Regrant SELECT so schema USAGE is the only missing admission condition.
	if err := e.SetObjectACL(t.Context(), ref, grant); err != nil {
		t.Fatal(err)
	}
	withUsage := authority()
	if err := withUsage.CanUse(nsRef); err != nil {
		t.Fatal(err)
	}
	if err := e.SetObjectACL(t.Context(), nsRef, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	noUsage := authority()
	domainUnitDiagnostic(t, noUsage.CanUse(nsRef), "42501", "permission denied for schema information_schema")
	if err := withUsage.CanUse(nsRef); err != nil {
		t.Fatal("retained schema ACL mutated", err)
	}
	if err := noUsage.Check(requirement); err != nil {
		t.Fatal("table grant independent of schema ACL", err)
	}
	if err := e.SetObjectACL(t.Context(), nsRef, namespace.ACL); err != nil {
		t.Fatal(err)
	}
	restored := authority()
	if err := restored.CanUse(nsRef); err != nil {
		t.Fatal("restored schema USAGE", err)
	}
	if err := restored.Check(requirement); err != nil {
		t.Fatal(err)
	}
	domainUnitDiagnostic(t, noUsage.CanUse(nsRef), "42501", "permission denied for schema information_schema")
	domainUnitDiagnostic(t, denied.Check(requirement), "42501", "permission denied for table sql_parts")
	for _, retained := range []AuthorizationView{allowed, withUsage, restored} {
		if !reflect.DeepEqual(matrix, sqlPartsUnitBag(t, sqlPartsUnitRows(t, sqlPartsAuthorityInput(t, retained)))) {
			t.Fatal("captured matrix changed across ACL publication")
		}
	}
}
