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

func implementationInfoUnitInput(t *testing.T, f defaultUnitFixture, view catalog.Read) pgcatalog.Input {
	t.Helper()
	auth := dtpUnitAuthority(t, f, view)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoSQLImplementationInfoOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: EngineCatalogRuntime{Auth: auth, Database: "unit_database"}}
}

func implementationInfoUnitRows(t *testing.T, in pgcatalog.Input) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoSQLImplementationInfoOID)
	if !ok {
		t.Fatal("missing implementation info producer")
	}
	captured, err := CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || len(captured.Anonymous) != 12 {
		t.Fatal("full anonymous capture", captured)
	}
	for _, row := range captured.Anonymous {
		if len(row) != 5 {
			t.Fatal("row width", len(row))
		}
		for i, value := range row {
			column := in.Source.Columns[i]
			id, baseID := sqltype.ID(13246), sqltype.VarChar
			if i == 2 {
				id, baseID = 13243, sqltype.Int4
			}
			base, _ := in.Catalog.LookupType(baseID)
			if value.Type() != column.Usage.Type || value.Type().ID() != id || value.Type().BaseType() != base || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.Usage.Typmod != -1 || column.OriginTable != uint32(pgcatalog.InfoSQLImplementationInfoOID) || column.OriginAttribute != int16(i+1) {
				t.Fatal("domain/provenance", i, value, column)
			}
		}
	}
	return captured.Anonymous
}

func implementationInfoUnitBag(t *testing.T, rows [][]sqltype.Value) map[[5]any]int {
	t.Helper()
	bag := make(map[[5]any]int)
	for _, row := range rows {
		var key [5]any
		for i, value := range row {
			if value.IsNull() {
				continue
			}
			base, err := value.BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			if i == 2 {
				n, err := base.Integer()
				if err != nil {
					t.Fatal(err)
				}
				key[i] = n
			} else {
				key[i] = sqlFeaturesUnitText(t, value)
			}
		}
		bag[key]++
	}
	return bag
}

func TestInfoSQLImplementationInfoProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := implementationInfoUnitInput(t, f, view)
	rows := implementationInfoUnitRows(t, in)
	want := map[[5]any]int{
		{"10003", "CATALOG NAME", nil, "Y", nil}:                                                        1,
		{"10004", "COLLATING SEQUENCE", nil, "C", nil}:                                                  1,
		{"23", "CURSOR COMMIT BEHAVIOR", int64(1), nil, "close cursors and retain prepared statements"}: 1,
		{"2", "DATA SOURCE NAME", nil, "", nil}:                                                         1,
		{"17", "DBMS NAME", nil, "stuntgres", nil}:                                                      1,
		{"18", "DBMS VERSION", nil, "PostgreSQL 18.0 compatible; embedded stuntgres", nil}:              1,
		{"26", "DEFAULT TRANSACTION ISOLATION", int64(2), nil, "READ COMMITTED; user-settable"}:         1,
		{"28", "IDENTIFIER CASE", int64(3), nil, "stored in mixed case - case sensitive"}:               1,
		{"85", "NULL COLLATION", int64(0), nil, "nulls higher than non-nulls"}:                          1,
		{"13", "SERVER NAME", nil, "", nil}:                                                             1,
		{"94", "SPECIAL CHARACTERS", nil, "", "all non-ASCII characters allowed"}:                       1,
		{"46", "TRANSACTION CAPABLE", int64(2), nil, "both DML and DDL"}:                                1,
	}
	if got := implementationInfoUnitBag(t, rows); !reflect.DeepEqual(got, want) {
		t.Fatal("complete native bag", got)
	}
	source := in.Source
	if source.VirtualView || source.RelationKind != catalog.CatalogRelation || source.History.HasIndexes || source.RowType != 13419 {
		t.Fatal("physical table identity", source)
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	rowtype, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13419})
	if !ok || rowtype.OwningRelation != source.Address() || rowtype.Array != 13418 {
		t.Fatal("owned rowtype", rowtype)
	}
	array, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13418})
	if !ok || array.OwningRelation != source.Address() || array.SQLType.Element() != rowtype.SQLType {
		t.Fatal("reciprocal array", array)
	}
	if len(source.ColumnMeta.CatalogAttributes) != 5 {
		t.Fatal("attribute width")
	}
	for i, attribute := range source.ColumnMeta.CatalogAttributes {
		collation := uint32(950)
		if i == 2 {
			collation = 0
		}
		if attribute.NotNull || attribute.Collation != collation || attribute.Dimensions != 0 {
			t.Fatal("physical nullable attribute", attribute)
		}
	}
	second := implementationInfoUnitRows(t, in)
	other := newDefaultUnitFixture(t)
	if !reflect.DeepEqual(implementationInfoUnitBag(t, second), want) || !reflect.DeepEqual(implementationInfoUnitBag(t, implementationInfoUnitRows(t, implementationInfoUnitInput(t, other, other.e.CatalogRead()))), want) {
		t.Fatal("capture/engine independence")
	}
	rows[0][0] = sqltype.Null(rows[0][0].Type())
	if second[0][0].IsNull() || !reflect.DeepEqual(implementationInfoUnitBag(t, implementationInfoUnitRows(t, in)), want) {
		t.Fatal("mutable capture buffers shared")
	}
}

func TestInfoSQLImplementationInfoCapture(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := implementationInfoUnitInput(t, f, view)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoSQLImplementationInfoOID)
	foreign := New()
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
		if rows, err := produce(bad); rows != nil || err == nil {
			t.Fatal("invalid authority accepted", rows, err)
		}
	}
	for _, malformed := range []string{"width", "base_column", "wrong_domain", "foreign_character", "foreign_cardinal", "missing_character", "missing_cardinal", "missing_varchar", "missing_int4", "foreign_varchar", "foreign_int4"} {
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
		case "foreign_cardinal":
			copy.Columns[2].Usage.Type, _ = foreign.CatalogRead().LookupType(13243)
		default:
			id := ids.OID(13246)
			switch malformed {
			case "missing_cardinal":
				id = 13243
			case "missing_varchar", "foreign_varchar":
				id = ids.OID(sqltype.VarChar)
			case "missing_int4", "foreign_int4":
				id = ids.OID(sqltype.Int4)
			}
			address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id}
			if malformed == "foreign_varchar" || malformed == "foreign_int4" {
				descriptor := *view.Get(address).(*catalog.Type)
				descriptor.SQLType, _ = foreign.CatalogRead().LookupType(sqltype.ID(id))
				bad.SetDescriptor(address, &descriptor)
			} else {
				bad.RemoveDescriptor(address)
			}
		}
		bad.SetDescriptor(copy.Address(), &copy)
		if rows, err := produce(implementationInfoUnitInput(t, f, f.e.Catalog.Read(bad))); rows != nil || err == nil {
			t.Fatal("malformed metadata accepted", malformed, rows, err)
		}
	}
}

func TestInfoSQLImplementationInfoCancellation(t *testing.T) {
	f := newDefaultUnitFixture(t)
	in := implementationInfoUnitInput(t, f, f.e.CatalogRead())
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoSQLImplementationInfoOID)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled := in
	canceled.Context = ctx
	if rows, err := produce(canceled); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancellation exposed rows", rows, err)
	}
	// Entry + twelve rows + final publication: no prefix may escape any edge.
	for _, remaining := range []int{2, 7, 14} {
		canceled.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(canceled); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation exposed prefix", remaining, rows, err)
		}
	}
	implementationInfoUnitRows(t, in)
}

func TestInfoSQLImplementationInfoCatalogACLRecheck(t *testing.T) {
	f := newDefaultUnitFixture(t)
	e := f.e
	d := e.CatalogRead().MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoSQLImplementationInfoOID})
	ref := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), d.Address())
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	role := roleAttributeCreate(t, e, admin, "implementation_info_reader", true, parser.RoleOptions{})
	reader, readerDone := roleOperation(t, e, role.Name)
	defer readerDone()
	requirement := access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege}
	retained, err := OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := retained.Check(requirement); err != nil {
		t.Fatal("PUBLIC SELECT", err)
	}
	if err := e.SetObjectACL(t.Context(), ref, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	denied, err := OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	domainUnitDiagnostic(t, denied.Check(requirement), "42501", "permission denied for table sql_implementation_info")
	if err := retained.Check(requirement); err != nil {
		t.Fatal("retained ACL mutated", err)
	}
	current := e.CatalogRead().MustRelation(d.Address())
	if current == d || !current.Security.ACL.Explicit || len(current.Security.ACL.Entries) != 0 || len(d.Security.ACL.Entries) != 1 {
		t.Fatal("ACL roots not isolated")
	}
	if err := e.SetObjectACL(t.Context(), ref, d.Security.ACL); err != nil {
		t.Fatal(err)
	}
	restored, err := OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Check(requirement); err != nil {
		t.Fatal("restored SELECT", err)
	}
	domainUnitDiagnostic(t, denied.Check(requirement), "42501", "permission denied for table sql_implementation_info")
	if err := retained.Check(requirement); err != nil {
		t.Fatal("restoration mutated retained ACL", err)
	}
	namespace := e.CatalogRead().MustNamespace(d.Namespace())
	nsRef := e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), namespace.Address())
	if err := restored.CanUse(nsRef); err != nil {
		t.Fatal("PUBLIC schema USAGE", err)
	}
	if err := e.SetObjectACL(t.Context(), nsRef, authorization.ACLValue{Explicit: true}); err != nil {
		t.Fatal(err)
	}
	noUsage, err := OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	domainUnitDiagnostic(t, noUsage.CanUse(nsRef), "42501", "permission denied for schema information_schema")
	if err := restored.CanUse(nsRef); err != nil {
		t.Fatal("retained schema ACL mutated", err)
	}
	if err := e.SetObjectACL(t.Context(), nsRef, namespace.ACL); err != nil {
		t.Fatal(err)
	}
	withUsage, err := OperationAuthorization(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := withUsage.CanUse(nsRef); err != nil {
		t.Fatal("restored schema USAGE", err)
	}
	domainUnitDiagnostic(t, noUsage.CanUse(nsRef), "42501", "permission denied for schema information_schema")
}
