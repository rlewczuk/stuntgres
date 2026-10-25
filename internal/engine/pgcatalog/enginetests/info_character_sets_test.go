package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func characterSetsUnitInput(t *testing.T, f defaultUnitFixture, view catalog.Read, database string) pgcatalog.Input {
	t.Helper()
	auth := dtpUnitAuthority(t, f, view)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoCharacterSetsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: database}}
}

func characterSetsUnitRows(t *testing.T, in pgcatalog.Input, schema, name *string) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoCharacterSetsOID)
	if !ok {
		t.Fatal("missing character_sets producer")
	}
	captured, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || len(captured.Anonymous) != 1 || len(captured.Anonymous[0]) != 8 {
		t.Fatal("singleton anonymous capture", captured)
	}
	rows := captured.Anonymous
	want := [8]*string{nil, nil, catalog.CollationText("UTF8"), catalog.CollationText("UCS"), catalog.CollationText("UTF8"), catalog.CollationText(in.Runtime.DatabaseName()), schema, name}
	for i, value := range rows[0] {
		column := in.Source.Columns[i]
		if value.Type() != column.Usage.Type || value.Type().ID() != 13248 || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.InfoCharacterSetsOID) || column.OriginAttribute != int16(i+1) {
			t.Fatal("domain carrier/provenance", i, value, column)
		}
		if value.IsNull() != (want[i] == nil) {
			t.Fatal("typed NULL", i, value, want[i])
		}
		if want[i] != nil && infoUserMappingsUnitText(t, value) != *want[i] {
			t.Fatal("captured identity", i, value, *want[i])
		}
	}
	return rows
}

func TestInfoCharacterSetsProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := characterSetsUnitInput(t, f, view, "MiXeD db")
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoCharacterSetsOID)
	original := characterSetsUnitRows(t, in, catalog.CollationText("pg_catalog"), catalog.CollationText("C"))
	characterSetsUnitRows(t, characterSetsUnitInput(t, f, view, ""), catalog.CollationText("pg_catalog"), catalog.CollationText("C"))
	source := in.Source
	if !source.VirtualView || source.RelationKind != catalog.CatalogRelation || source.RowType != 13274 {
		t.Fatal("view identity", source)
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	row, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)})
	if !ok || row.OwningRelation != source.Address() || row.Array != 13273 {
		t.Fatal("owned row type", row)
	}
	array, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
	if !ok || array.OwningRelation != source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("owned array type", array)
	}
	// New immutable roots change identifiers without changing retained values.
	changed := catalog.CloneState(view.State())
	ns := *view.MustNamespace(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID})
	ns.ObjectName = "captured_namespace"
	changed.SetDescriptor(ns.Address(), &ns)
	collation := *view.Get(ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: 950}).(*catalog.Collation)
	collation.ObjectName = "captured_collation_long_name"
	changed.SetDescriptor(collation.Address(), &collation)
	changedView := f.e.Catalog.Read(changed)
	characterSetsUnitRows(t, characterSetsUnitInput(t, f, changedView, "MiXeD db"), catalog.CollationText(ns.Name()), catalog.CollationText(collation.Name()))
	if infoUserMappingsUnitText(t, original[0][6]) != "pg_catalog" || infoUserMappingsUnitText(t, original[0][7]) != "C" {
		t.Fatal("retained identifiers changed", original)
	}
	for _, remaining := range []int{1, 4} {
		canceled := in
		canceled.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(canceled); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation exposed rows", rows, err)
		}
	}
	foreign := engine.New()
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &foreign.Catalog.Owner },
		func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(core.EngineCatalogRuntime)
			runtime.Auth.RoleRecord = nil
			in.Runtime = runtime
		},
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(core.EngineCatalogRuntime)
			runtime.Auth.State.CurrentUser = foreign.Roles().LookupRef("postgres")
			in.Runtime = runtime
		},
	} {
		bad := in
		mutate(&bad)
		if rows, err := produce(bad); rows != nil || err == nil {
			t.Fatal("invalid capture accepted", rows, err)
		}
	}
	for _, malformed := range []string{"width", "base", "foreign_domain", "database"} {
		bad := catalog.CloneState(view.State())
		if malformed == "database" {
			bad.RemoveDescriptor(catalog.CurrentDatabaseAddress)
		} else {
			copy := *source
			copy.Columns = append([]catalog.Column(nil), source.Columns...)
			switch malformed {
			case "width":
				copy.Columns = copy.Columns[:7]
			case "base":
				copy.Columns[0].Usage.Type, _ = view.LookupType(sqltype.Name)
			case "foreign_domain":
				copy.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(13248)
			}
			bad.SetDescriptor(copy.Address(), &copy)
		}
		if rows, err := produce(characterSetsUnitInput(t, f, f.e.Catalog.Read(bad), "MiXeD db")); rows != nil || err == nil {
			t.Fatal("malformed metadata accepted", malformed, rows, err)
		}
	}
}

func TestInfoCharacterSetsCollationChoice(t *testing.T) {
	f := newDefaultUnitFixture(t)
	base := f.e.CatalogRead()
	without := catalog.CloneState(base.State())
	for address, descriptor := range base.Descriptors() {
		if _, ok := descriptor.(*catalog.Collation); ok {
			without.RemoveDescriptor(address)
		}
	}
	// A missing match still returns the database identity, unlike the manual's
	// catalog-NULL sentence. Only released SQL's schema/name become NULL.
	characterSetsUnitRows(t, characterSetsUnitInput(t, f, f.e.Catalog.Read(without), "unit_database"), nil, nil)
	locale := catalog.CollationText("C")
	posix := catalog.CollationText("POSIX")
	// These synthetic edges exist only in unpublished immutable clones. They
	// test released selection semantics, not admission/publication validity.
	noncandidates := []catalog.Collation{
		{ObjectName: "long_no_fields", Provider: "d"},
		{ObjectName: "long_missing_ctype", Collate: locale},
		{ObjectName: "long_missing_collate", CType: locale},
		{ObjectName: "long_wrong_ctype", Collate: locale, CType: posix},
		{ObjectName: "long_wrong_collate", Collate: posix, CType: locale},
		{ObjectName: "long_posix", Collate: posix, CType: posix},
		{ObjectName: "long_locale_only", Provider: "b", Locale: locale},
		{ObjectName: "long_missing_namespace", Collate: locale, CType: locale, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 999999}},
	}
	for i, descriptor := range noncandidates {
		descriptor.OID = ids.OID(22000 + i)
		without.SetDescriptor(descriptor.Address(), &descriptor)
	}
	characterSetsUnitRows(t, characterSetsUnitInput(t, f, f.e.Catalog.Read(without), "unit_database"), nil, nil)
	for _, names := range [][]string{{"cs_rank_bb", "cs_rank_aa"}, {"cs_rank_aa", "cs_rank_bb"}, {"cs_ééééé", "cs_aaaaaaa"}, {"cs_aaaaaaa", "cs_ééééé"}, {"C", "cs_longer_name"}} {
		state := catalog.CloneState(without)
		for i, name := range names {
			// Matching fields, not provider/encoding, determine eligibility.
			d := &catalog.Collation{OID: ids.OID(22100 + i), ObjectName: name, Provider: "b", Encoding: 42, Collate: locale, CType: locale}
			state.SetDescriptor(d.Address(), d)
		}
		winner := "cs_rank_aa"
		if names[0] == "cs_ééééé" || names[1] == "cs_ééééé" {
			winner = "cs_aaaaaaa"
		} else if names[0] == "C" {
			winner = "cs_longer_name"
		}
		characterSetsUnitRows(t, characterSetsUnitInput(t, f, f.e.Catalog.Read(state), "unit_database"), catalog.CollationText("pg_catalog"), catalog.CollationText(winner))
	}
	// Released SQL has no namespace tie-breaker for identical candidate names.
	tied := catalog.CloneState(without)
	for i, schema := range []string{"cs_tie_one", "cs_tie_two"} {
		ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.OID(22200 + i)}, ObjectName: schema, ObjectRevision: 1, Owner: f.owner}
		tied.SetDescriptor(ns.Address(), ns)
		d := &catalog.Collation{OID: ids.OID(22300 + i), ObjectName: "cs_tied_name", ObjectNamespace: ns.Address(), ObjectRevision: 1, Owner: f.owner, Provider: "c", Encoding: -1, Collate: locale, CType: locale, Semantics: 950, Deterministic: true}
		tied.SetDescriptor(d.Address(), d)
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoCharacterSetsOID)
	rows, err := produce(characterSetsUnitInput(t, f, f.e.Catalog.Read(tied), "unit_database"))
	if err != nil || len(rows) != 1 || len(rows[0]) != 8 {
		t.Fatal("tied selection", rows, err)
	}
	schema := infoUserMappingsUnitText(t, rows[0][6])
	if (schema != "cs_tie_one" && schema != "cs_tie_two") || infoUserMappingsUnitText(t, rows[0][7]) != "cs_tied_name" {
		t.Fatal("winner outside tied candidate set", rows)
	}
}

func dtpUnitAuthority(t *testing.T, f defaultUnitFixture, catalog catalog.Read) core.AuthorizationView {
	t.Helper()
	f.e.Mu.Lock()
	auth, err := f.e.AuthorizationLocked(f.op)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	auth.Recapture(catalog)
	return auth
}

// A deterministic cancellation point inside enumeration, without timing races.
type dtpCancelContext struct {
	context.Context
	remaining int
}

func (c *dtpCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}
