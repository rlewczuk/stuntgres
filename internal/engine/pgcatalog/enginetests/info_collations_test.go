package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func collationsUnitInput(t *testing.T, f defaultUnitFixture, view catalog.Read, database string) pgcatalog.Input {
	t.Helper()
	auth := dtpUnitAuthority(t, f, view)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoCollationsOID})
	return pgcatalog.Input{Context:t.Context(), Catalog:view, Roles:auth.Roles, Owner:view.Owner(), Ref:view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source:source, Runtime:core.EngineCatalogRuntime{Auth:auth, Database:database}}
}

func collationsUnitRows(t *testing.T, in pgcatalog.Input, want [][]string) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoCollationsOID)
	if !ok { t.Fatal("missing collations producer") }
	captured, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil { t.Fatal(err) }
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil { t.Fatal("anonymous capture", captured) }
	got := make([][]string, 0, len(captured.Anonymous))
	for _, row := range captured.Anonymous {
		if len(row) != 4 { t.Fatal("row width", len(row)) }
		text := make([]string, 4)
		for i, value := range row {
			column := in.Source.Columns[i]
			if value.IsNull() || value.Type() != column.Usage.Type || !value.ValidResult(column.Usage) { t.Fatal("owned nonnull domain", i, value, column) }
			if i == 3 {
				text[i] = sqlFeaturesUnitText(t, value)
			} else {
				text[i] = infoUserMappingsUnitText(t, value)
			}
		}
		got = append(got, text)
	}
	sort.Slice(got, func(i,j int) bool { if got[i][1] != got[j][1] { return got[i][1] < got[j][1] }; return got[i][2] < got[j][2] })
	if !reflect.DeepEqual(got, want) { t.Fatalf("rows=%#v want=%#v", got, want) }
	return captured.Anonymous
}

func collationsUnitSeeds(database, schema string) [][]string {
	rows := make([][]string, 0, 7)
	for _, name := range []string{"C","POSIX","default","pg_c_utf8","pg_unicode_fast","ucs_basic","unicode"} { rows = append(rows, []string{database,schema,name,"NO PAD"}) }
	return rows
}

func TestInfoCollationsProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := collationsUnitInput(t, f, view, "MiXeD db")
	if pgcatalog.InfoCollationsOID != 16229 || in.Source.Name() != "collations" || in.Source.Namespace().ObjectID != 13229 || !in.Source.VirtualView || in.Source.RelationKind != catalog.CatalogRelation || in.Source.RowType != 13289 { t.Fatal("source identity", in.Source) }
	acl := in.Source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege { t.Fatal("PUBLIC SELECT", acl) }
	row, ok := view.Type(ids.ObjectAddress{ClassID:ids.TypeClassID, ObjectID:13289})
	if !ok || row.OwningRelation != in.Source.Address() || row.Array != 13288 { t.Fatal("row identity", row) }
	array, ok := view.Type(ids.ObjectAddress{ClassID:ids.TypeClassID, ObjectID:13288})
	if !ok || array.OwningRelation != in.Source.Address() || array.SQLType.Element() != row.SQLType { t.Fatal("array identity", array) }
	names := []string{"collation_catalog","collation_schema","collation_name","pad_attribute"}
	if len(in.Source.Columns) != 4 { t.Fatal("columns", in.Source.Columns) }
	for i, column := range in.Source.Columns {
		domainID, baseID := sqltype.ID(13248), sqltype.Name
		if i == 3 { domainID, baseID = 13246, sqltype.VarChar }
		domain, domainOK := view.LookupType(domainID)
		base, baseOK := view.LookupType(baseID)
		if !domainOK || !baseOK || column.Name != names[i] || column.Usage.Type != domain || !domain.IsDomain() || domain.BaseType() != base || !column.Nullable || !column.NullabilityKnown || column.OriginTable != 16229 || column.OriginAttribute != int16(i+1) || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.DeclaredDimensions != 0 { t.Fatal("column domain/origin", i, column) }
	}
	original := collationsUnitRows(t, in, collationsUnitSeeds("MiXeD db","pg_catalog"))
	collationsUnitRows(t, collationsUnitInput(t,f,view,""), collationsUnitSeeds("","pg_catalog"))
	changed := catalog.CloneState(view.State())
	ns := *view.MustNamespace(ids.ObjectAddress{ClassID:ids.NamespaceClassID, ObjectID:ids.PGCatalogNamespaceOID})
	ns.ObjectName = "captured_namespace"
	changed.SetDescriptor(ns.Address(), &ns)
	collation := *view.Get(ids.ObjectAddress{ClassID:ids.CollationClassID, ObjectID:950}).(*catalog.Collation)
	collation.ObjectName = "renamed_C"
	changed.SetDescriptor(collation.Address(), &collation)
	want := [][]string{{"MiXeD db",ns.Name(),"POSIX","NO PAD"},{"MiXeD db",ns.Name(),"default","NO PAD"},{"MiXeD db",ns.Name(),"pg_c_utf8","NO PAD"},{"MiXeD db",ns.Name(),"pg_unicode_fast","NO PAD"},{"MiXeD db",ns.Name(),"renamed_C","NO PAD"},{"MiXeD db",ns.Name(),"ucs_basic","NO PAD"},{"MiXeD db",ns.Name(),"unicode","NO PAD"}}
	collationsUnitRows(t, collationsUnitInput(t,f,f.e.Catalog.Read(changed),"MiXeD db"), want)
	for _, values := range original {
		if infoUserMappingsUnitText(t,values[1]) != "pg_catalog" || infoUserMappingsUnitText(t,values[2]) == "renamed_C" { t.Fatal("retained capture changed", values) }
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoCollationsOID)
	for _, remaining := range []int{1,4} {
		canceled := in
		canceled.Context = &dtpCancelContext{Context:context.Background(), remaining:remaining}
		if rows, err := produce(canceled); rows != nil || !errors.Is(err,context.Canceled) { t.Fatal("cancellation exposed rows", rows, err) }
	}
	foreign := engine.New()
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &foreign.Catalog.Owner },
		func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) { runtime := in.Runtime.(core.EngineCatalogRuntime); runtime.Auth.RoleRecord = nil; in.Runtime = runtime },
		func(in *pgcatalog.Input) { runtime := in.Runtime.(core.EngineCatalogRuntime); runtime.Auth.State.CurrentUser = foreign.Roles().LookupRef("postgres"); in.Runtime = runtime },
	} {
		bad := in
		mutate(&bad)
		if rows, err := produce(bad); rows != nil || err == nil { t.Fatal("invalid authority accepted", rows, err) }
	}
	for _, malformed := range []string{"width","database","primitive","foreign_domain"} {
		for column := range 4 {
			bad := catalog.CloneState(view.State())
			if malformed == "database" { bad.RemoveDescriptor(catalog.CurrentDatabaseAddress) } else {
				copy := *in.Source
				copy.Columns = append([]catalog.Column(nil), in.Source.Columns...)
				switch malformed {
				case "width": copy.Columns = copy.Columns[:3]
				case "primitive": copy.Columns[column].Usage.Type = copy.Columns[column].Usage.Type.BaseType()
				case "foreign_domain": copy.Columns[column].Usage.Type, _ = foreign.CatalogRead().LookupType(copy.Columns[column].Usage.Type.ID())
				}
				bad.SetDescriptor(copy.Address(), &copy)
			}
			if rows, err := produce(collationsUnitInput(t,f,f.e.Catalog.Read(bad),"MiXeD db")); rows != nil || err == nil { t.Fatal("malformed source accepted", malformed, column, rows, err) }
			if malformed == "width" || malformed == "database" { break }
		}
	}
}

func TestInfoCollationsEncodingApplicability(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	without := catalog.CloneState(view.State())
	for address, descriptor := range view.Descriptors() { if _, ok := descriptor.(*catalog.Collation); ok { without.RemoveDescriptor(address) } }
	collationsUnitRows(t, collationsUnitInput(t,f,f.e.Catalog.Read(without),"unit_database"), [][]string{})
	for i, candidate := range []struct{ name string; encoding int32; missingNamespace bool }{{"all_encodings",-1,false},{"incompatible",8,false},{"missing_namespace",6,true},{"utf8",6,false}} {
		copy := *view.Get(ids.ObjectAddress{ClassID:ids.CollationClassID, ObjectID:950}).(*catalog.Collation)
		copy.OID, copy.ObjectName, copy.Encoding = ids.OID(22400+i), candidate.name, candidate.encoding
		if candidate.missingNamespace { copy.ObjectNamespace = ids.ObjectAddress{ClassID:ids.NamespaceClassID, ObjectID:999999} }
		without.SetDescriptor(copy.Address(), &copy)
	}
	collationsUnitRows(t, collationsUnitInput(t,f,f.e.Catalog.Read(without),"unit_database"), [][]string{{"unit_database","pg_catalog","all_encodings","NO PAD"},{"unit_database","pg_catalog","utf8","NO PAD"}})
	// Inquiry-only execution rejection is independent of metadata eligibility.
	for _, oid := range []ids.OID{962,963} {
		descriptor := view.Get(ids.ObjectAddress{ClassID:ids.CollationClassID,ObjectID:oid}).(*catalog.Collation)
		if _, err := descriptor.Usage(); err == nil { t.Fatal("inquiry-only execution unexpectedly admitted", oid) }
	}
	collationsUnitRows(t, collationsUnitInput(t,f,view,"unit_database"), collationsUnitSeeds("unit_database","pg_catalog"))
}
