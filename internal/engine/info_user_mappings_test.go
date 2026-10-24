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
	"stuntgres/internal/sqltype"
)

func TestInfoUserMappingsProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoUserMappingsOID)
	if !ok {
		t.Fatal("missing producer")
	}
	state := catalog.CloneState(f.e.catalog.state)
	server := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignServerClassID, ObjectID: 20200}, ObjectName: "Mixed Server", ObjectRevision: 1, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true}}
	state.SetDescriptor(server.Address(), server)
	for i, role := range []authorization.PrincipalRef{f.reader, {}} {
		mapping := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignMappingClassID, ObjectID: ids.OID(20201 + i)}, ObjectName: catalog.ForeignMappingName(server.Address(), role), ObjectRevision: 1, Server: server.Address(), MappedRole: role, MappedName: "obsolete diagnostic name"}
		state.SetDescriptor(mapping.Address(), mapping)
	}
	view := f.e.catalog.read(state)
	auth := dtpUnitAuthority(t, f, view)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoUserMappingsOID})
	input := func(v catalog.Read, a AuthorizationView) pgcatalog.Input {
		return pgcatalog.Input{Context: t.Context(), Catalog: v, Roles: a.Roles, Owner: v.Owner(), Ref: v.Owner().Domain.Ref(v.Epoch(), source.Address()), Source: v.MustRelation(source.Address()), Runtime: engineCatalogRuntime{auth: a, database: "captured_database"}}
	}
	checkRows := func(in pgcatalog.Input, want map[string]int) [][]sqltype.Value {
		t.Helper()
		captured, err := captureCatalogSourceRows(in, produce)
		if err != nil {
			t.Fatal(err)
		}
		defer captured.Close()
		if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil {
			t.Fatal("capture lost anonymous source identity", captured.Ref, in.Ref)
		}
		rows := captured.Anonymous
		got := map[string]int{}
		for _, row := range rows {
			if len(row) != 3 {
				t.Fatal("projection width", row)
			}
			for i, value := range row {
				column := source.Columns[i]
				if value.Type() != column.Usage.Type || value.IsNull() || !value.ValidResult(column.Usage) || !column.Nullable || !column.NullabilityKnown || column.OriginTable != uint32(pgcatalog.InfoUserMappingsOID) || column.OriginAttribute != int16(i+1) {
					t.Fatal("domain carrier/provenance", i, value, column)
				}
				base, err := value.BaseValue()
				if err != nil || base.Type().ID() != sqltype.Name {
					t.Fatal("NAME payload lost", base, err)
				}
			}
			name := infoUserMappingsUnitText(t, row[0])
			database := infoUserMappingsUnitText(t, row[1])
			serverName := infoUserMappingsUnitText(t, row[2])
			if database != "captured_database" || serverName != "Mixed Server" {
				t.Fatal("captured names", database, serverName)
			}
			got[name]++
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("identity bag", got, want)
		}
		return rows
	}
	want := map[string]int{"default_reader": 1, "PUBLIC": 1}
	for _, actor := range []authorization.PrincipalRef{auth.State.CurrentUser, f.owner, f.member} {
		auth.State.CurrentUser = actor
		checkRows(input(view, auth), want)
	}
	// Replace only the immutable role descriptor, keeping its PrincipalRef and
	// the exact catalog/mapping descriptors. Public role-rename SQL is separate.
	originalAuth := auth
	originalAuth.State.CurrentUser = f.owner
	originalRows := checkRows(input(view, originalAuth), want)
	originalRole, ok := originalAuth.Roles.Find(f.reader)
	if !ok || originalRole.Name != "default_reader" {
		t.Fatal("original mapped role", originalRole, ok)
	}
	mappingAddress := ids.ObjectAddress{ClassID: ids.ForeignMappingClassID, ObjectID: 20201}
	originalMapping := view.Get(mappingAddress).(*catalog.ForeignObject)
	renamedRole := originalRole
	renamedRole.Name = "IUM Renamed Role"
	renamedRoot, err := originalAuth.RoleRecord.Root.PutDescriptor(renamedRole)
	if err != nil {
		t.Fatal(err)
	}
	renamedAuth := originalAuth
	renamedAuth.RoleRecord = &access.RoleRecord{Root: renamedRoot, Revision: originalAuth.RoleRecord.Revision + 1}
	renamedAuth.Recapture(view)
	if _, ok := renamedAuth.Roles.Lookup(originalRole.Name); ok {
		t.Fatal("replacement retained old role-name index")
	}
	if role, ok := renamedAuth.Roles.Lookup(renamedRole.Name); !ok || role != renamedRole || role.Ref != f.reader {
		t.Fatal("replacement role-name index", role, ok)
	}
	if role, ok := originalAuth.Roles.Lookup(originalRole.Name); !ok || role != originalRole {
		t.Fatal("old root lost original role", role, ok)
	}
	if _, ok := originalAuth.Roles.Lookup(renamedRole.Name); ok {
		t.Fatal("old root acquired replacement name")
	}
	checkRows(input(view, renamedAuth), map[string]int{"IUM Renamed Role": 1, "PUBLIC": 1})
	checkRows(input(view, originalAuth), want)
	if mapping := renamedAuth.Catalog.Get(mappingAddress); mapping != originalMapping || originalMapping.MappedRole != f.reader || originalMapping.MappedName != "obsolete diagnostic name" {
		t.Fatal("role replacement changed mapping descriptor", mapping)
	}
	retainedNames := map[string]int{}
	for _, row := range originalRows {
		retainedNames[infoUserMappingsUnitText(t, row[0])]++
		if infoUserMappingsUnitText(t, row[1]) != "captured_database" || infoUserMappingsUnitText(t, row[2]) != "Mixed Server" {
			t.Fatal("role replacement changed retained row", row)
		}
	}
	if !reflect.DeepEqual(retainedNames, want) {
		t.Fatal("role replacement changed retained identities", retainedNames, want)
	}
	// Mapping ownership alone does not reveal even that mapping's identity.
	auth.State.CurrentUser = f.reader
	checkRows(input(view, auth), map[string]int{})
	granted := catalog.CloneState(state)
	allowed := *server
	allowed.ACL.Entries = []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.UsagePrivilege, 0)}
	granted.SetDescriptor(server.Address(), &allowed)
	grantedView := f.e.catalog.read(granted)
	grantedAuth := auth
	grantedAuth.Recapture(grantedView)
	held := checkRows(input(grantedView, grantedAuth), want)
	checkRows(input(view, auth), map[string]int{})
	if name := infoUserMappingsUnitText(t, held[0][1]); name != "captured_database" {
		t.Fatal("retained row mutated")
	}
	// A server without mappings must not manufacture a PUBLIC/default row.
	empty := catalog.CloneState(f.e.catalog.state)
	empty.SetDescriptor(server.Address(), server)
	emptyView := f.e.catalog.read(empty)
	emptyAuth := auth
	emptyAuth.Recapture(emptyView)
	checkRows(input(emptyView, emptyAuth), map[string]int{})
	for _, ctx := range []context.Context{&dtpCancelContext{Context: context.Background(), remaining: 1}, &dtpCancelContext{Context: context.Background(), remaining: 4}} {
		in := input(grantedView, grantedAuth)
		in.Context = ctx
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation exposed prefix", rows, err)
		}
	}
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = New().catalog.owner() },
		func(in *pgcatalog.Input) { in.Roles = New().Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			a := grantedAuth
			a.RoleRecord = nil
			in.Runtime = engineCatalogRuntime{auth: a}
		},
	} {
		in := input(grantedView, grantedAuth)
		mutate(&in)
		if rows, err := produce(in); rows != nil || err == nil {
			t.Fatal("invalid capture accepted", rows, err)
		}
	}
	for _, malformed := range []string{"width", "type", "server", "role"} {
		bad := catalog.CloneState(granted)
		if malformed == "width" || malformed == "type" {
			copy := *source
			copy.Columns = append([]catalog.Column(nil), source.Columns...)
			if malformed == "width" {
				copy.Columns = copy.Columns[:2]
			} else {
				copy.Columns[0].Usage.Type, _ = view.LookupType(sqltype.Name)
			}
			bad.SetDescriptor(copy.Address(), &copy)
		} else {
			mapping := *view.Get(ids.ObjectAddress{ClassID: ids.ForeignMappingClassID, ObjectID: 20201}).(*catalog.ForeignObject)
			if malformed == "server" {
				mapping.Server.ObjectID++
			} else {
				mapping.MappedRole = f.e.roles.domain.Principal(99999)
			}
			bad.SetDescriptor(mapping.Address(), &mapping)
		}
		badView := f.e.catalog.read(bad)
		badAuth := grantedAuth
		badAuth.Recapture(badView)
		if rows, err := produce(input(badView, badAuth)); rows != nil || err == nil {
			t.Fatal("malformed metadata accepted", malformed, rows, err)
		}
	}
}

func infoUserMappingsUnitText(t *testing.T, value sqltype.Value) string {
	t.Helper()
	base, err := value.BaseValue()
	if err != nil || base.Type().ID() != sqltype.Name {
		t.Fatal("NAME payload lost", base, err)
	}
	text, err := base.Text()
	if err != nil {
		t.Fatal("NAME text", err)
	}
	return text
}
