package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

// Native identity is independent of the release allocator.
const infoForeignServersOID ids.OID = 16246

type serversFixture struct {
	f                       defaultUnitFixture
	state                   *catalog.State
	wrapper, server, second *catalog.ForeignObject
}

func newServersFixture(t *testing.T) serversFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	wrapper := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignWrapperClassID, ObjectID: 22600}, ObjectName: "server_wrapper", ObjectRevision: 1, Owner: f.reader, Options: []catalog.ForeignOption{{Name: "password", Value: "wrapper-secret"}}}
	kind, version := "é=🙂", ""
	server := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignServerClassID, ObjectID: 22601}, ObjectName: "view_server", ObjectRevision: 1, Owner: f.owner, Wrapper: wrapper.Address(), ServerType: &kind, Version: &version, ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.UsagePrivilege, 0)}}, Options: []catalog.ForeignOption{{Name: "password", Value: "server-secret"}, {Name: "empty", Value: ""}}}
	second := *server
	second.ObjectAddress.ObjectID++
	second.ObjectName = "null_server"
	second.ServerType, second.Version, second.Options = nil, nil, nil
	mapping := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignMappingClassID, ObjectID: 22603}, ObjectName: catalog.ForeignMappingName(server.Address(), f.reader), ObjectRevision: 1, Owner: f.owner, Server: server.Address(), MappedRole: f.reader, Options: []catalog.ForeignOption{{Name: "password", Value: "mapping-secret"}}}
	table := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 22604}, ObjectNamespace: f.schema, ObjectName: "foreign_decoy", ObjectRevision: 1, Owner: f.owner, Foreign: &catalog.ForeignTableDefinition{Server: server.Address(), Options: []catalog.ForeignOption{{Name: "password", Value: "table-secret"}}}}
	for _, d := range []catalog.Descriptor{wrapper, server, &second, mapping, table} {
		state.SetDescriptor(d.Address(), d)
	}
	return serversFixture{f: f, state: state, wrapper: wrapper, server: server, second: &second}
}

func (f serversFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: infoForeignServersOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}}
}

func serversRows(t *testing.T, in pgcatalog.Input) ([][]sqltype.Value, map[[7]any]int) {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(infoForeignServersOID)
	if !ok {
		t.Fatal("missing server producer")
	}
	source, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if source.Ref != in.Ref || source.View != nil || source.Anonymous == nil {
		t.Fatal("anonymous source authority", source)
	}
	bag := map[[7]any]int{}
	for _, row := range source.Anonymous {
		if len(row) != 7 {
			t.Fatal("row width", row)
		}
		var values [7]any
		for i, value := range row {
			column := in.Source.Columns[i]
			if value.Type() != column.Usage.Type || !value.ValidResult(column.Usage) {
				t.Fatal("source-owned carrier", i, value)
			}
			if value.IsNull() {
				if i != 4 && i != 5 {
					t.Fatal("unexpected NULL", i)
				}
				continue
			}
			base, err := value.BaseValue()
			baseID := sqltype.Name
			if i == 4 || i == 5 {
				baseID = sqltype.VarChar
			}
			if err != nil || base.Type().ID() != baseID {
				t.Fatal("base authority", base, err)
			}
			text, err := base.Text()
			if err != nil {
				t.Fatal(err)
			}
			values[i] = text
		}
		bag[values]++
	}
	return source.Anonymous, bag
}

func serversExpected(owner string) map[[7]any]int {
	return map[[7]any]int{
		{"captured_database", "view_server", "captured_database", "server_wrapper", "é=🙂", "", owner}: 1,
		{"captured_database", "null_server", "captured_database", "server_wrapper", nil, nil, owner}:  1,
	}
}

func TestInfoForeignServersProducer(t *testing.T) {
	f := newServersFixture(t)
	in := f.input(t, f.state, f.f.owner)
	if in.Source.Name() != "foreign_servers" || in.Source.Namespace().ObjectID != 13229 || in.Source.Address().ObjectID != 16246 || !in.Source.VirtualView {
		t.Fatal("source identity", in.Source)
	}
	row := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(in.Source.RowType)})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
	if in.Source.RowType != 13550 || row.Array != 13549 || row.OwningRelation != in.Source.Address() || array.OwningRelation != in.Source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("row/array identity")
	}
	names := []string{"foreign_server_catalog", "foreign_server_name", "foreign_data_wrapper_catalog", "foreign_data_wrapper_name", "foreign_server_type", "foreign_server_version", "authorization_identifier"}
	if len(in.Source.Columns) != 7 || len(in.Source.Attributes) != 7 {
		t.Fatal("source width")
	}
	for i, column := range in.Source.Columns {
		domainID, baseID := sqltype.ID(13248), sqltype.Name
		if i == 4 || i == 5 {
			domainID, baseID = 13246, sqltype.VarChar
		}
		domain, _ := in.Catalog.LookupType(domainID)
		base, _ := in.Catalog.LookupType(baseID)
		if column.Name != names[i] || column.Usage != domain.Usage() || domain.BaseType() != base || !column.Nullable || !column.NullabilityKnown || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.DeclaredDimensions != 0 || column.OriginTable != 16246 || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			t.Fatal("source metadata", i, column)
		}
	}
	owner, _ := in.Roles.Find(f.f.owner)
	_, got := serversRows(t, in)
	if !reflect.DeepEqual(got, serversExpected(owner.Name)) {
		t.Fatal("server projection", got)
	}
	if !in.Source.Security.ACL.Explicit {
		t.Fatal("PUBLIC SELECT source ACL")
	}
	reader := f.input(t, f.state, f.f.reader)
	rights, err := reader.Runtime.Effective(reader.Ref, false)
	if err != nil || rights&authorization.SelectPrivilege == 0 {
		t.Fatal("PUBLIC SELECT", rights, err)
	}
	_, empty := serversRows(t, f.input(t, catalog.CloneState(f.f.e.Catalog.State), f.f.owner))
	if len(empty) != 0 {
		t.Fatal("synthetic servers", empty)
	}
	state := catalog.CloneState(f.f.e.Catalog.State)
	state.SetDescriptor(f.wrapper.Address(), f.wrapper)
	_, empty = serversRows(t, f.input(t, state, f.f.owner))
	if len(empty) != 0 {
		t.Fatal("wrapper synthesized server", empty)
	}
}

func TestInfoForeignServersVisibility(t *testing.T) {
	f := newServersFixture(t)
	in := f.input(t, f.state, f.f.owner)
	owner, _ := in.Roles.Find(f.f.owner)
	admin := in.Runtime.(core.EngineCatalogRuntime).Auth.RoleRecord.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	for _, actor := range []authorization.PrincipalRef{admin, f.f.owner, f.f.member, f.f.reader} {
		_, got := serversRows(t, f.input(t, f.state, actor))
		if !reflect.DeepEqual(got, serversExpected(owner.Name)) {
			t.Fatal("admitted server", actor, got)
		}
	}
	for _, tc := range []struct {
		name     string
		grantee  authorization.ACLGrantee
		rights   authorization.Privileges
		actor    authorization.PrincipalRef
		admitted bool
	}{
		{"wrapper-owner-and-mapping-only", authorization.ACLGrantee{Role: f.f.owner}, authorization.UsagePrivilege, f.f.reader, false},
		{"public", authorization.ACLGrantee{Public: true}, authorization.UsagePrivilege, f.f.reader, true},
		{"inherited-usage", authorization.ACLGrantee{Role: f.f.owner}, authorization.UsagePrivilege, f.f.member, true},
		{"wrong-privilege", authorization.ACLGrantee{Role: f.f.reader}, authorization.SelectPrivilege, f.f.reader, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			for _, original := range []*catalog.ForeignObject{f.server, f.second} {
				server := *original
				server.Owner = f.f.reader
				if tc.name != "inherited-usage" {
					server.Owner = f.f.owner
				}
				server.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: server.Owner, Grantee: tc.grantee, Privileges: tc.rights}}}
				state.SetDescriptor(server.Address(), &server)
			}
			_, got := serversRows(t, f.input(t, state, tc.actor))
			expectedOwner := owner.Name
			if tc.name == "inherited-usage" {
				role, _ := in.Roles.Find(f.f.reader)
				expectedOwner = role.Name
			}
			if tc.admitted && !reflect.DeepEqual(got, serversExpected(expectedOwner)) || !tc.admitted && len(got) != 0 {
				t.Fatal("ACL traversal", got)
			}
		})
	}
	setOnly := f.input(t, f.state, f.f.member)
	auth := setOnly.Runtime.(core.EngineCatalogRuntime).Auth
	roles := cloneRoleFixture(auth.RoleRecord)
	edges := append([]authorization.Membership(nil), fixtureEdges(roles)...)
	for i := range edges {
		edges[i].Inherit = false
		edges[i].Set = true
	}
	setFixtureEdges(roles, edges)
	auth.RoleRecord = roles
	auth.Recapture(setOnly.Catalog)
	setOnly.Roles, setOnly.Runtime = auth.Roles, core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}
	_, got := serversRows(t, setOnly)
	if len(got) != 0 {
		t.Fatal("SET-only ownership admitted", got)
	}
}

func TestInfoForeignServersAuthority(t *testing.T) {
	f := newServersFixture(t)
	old := f.input(t, f.state, f.f.reader)
	produce, _ := pgcatalog.ProducerFor(infoForeignServersOID)
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &engine.New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = engine.New().CatalogRead() },
		func(in *pgcatalog.Input) { in.Roles = engine.New().Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) { source := *in.Source; in.Source = &source },
		func(in *pgcatalog.Input) { in.Runtime = nil },
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(core.EngineCatalogRuntime)
			runtime.Auth.RoleRecord = nil
			in.Runtime = runtime
		},
	} {
		in := old
		mutate(&in)
		rows, err := produce(in)
		if rows != nil || err == nil {
			t.Fatal("invalid capture accepted", rows, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("invariant acquired SQLSTATE", err)
		}
	}
	for _, kind := range []string{"width", "relation-name", "namespace", "name", "domain", "generation", "base-generation", "typmod", "collation", "nullable", "nullability-known", "dimensions", "origin", "origin-table", "virtual", "attributes", "attribute-width", "missing-owner", "missing-wrapper", "wrong-wrapper-class"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			source := *old.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			source.Attributes = append([]int32(nil), source.Attributes...)
			switch kind {
			case "width":
				source.Columns = source.Columns[:3]
			case "relation-name":
				source.ObjectName = "wrong"
			case "namespace":
				source.ObjectNamespace.ObjectID++
			case "name":
				source.Columns[0].Name = "wrong"
			case "domain":
				source.Columns[0].Usage = source.Columns[4].Usage
			case "generation":
				source.Columns[0].Usage.Type, _ = engine.New().CatalogRead().LookupType(13248)
			case "base-generation":
				typ := *old.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Name)})
				typ.SQLType, _ = engine.New().CatalogRead().LookupType(sqltype.Name)
				state.SetDescriptor(typ.Address(), &typ)
			case "typmod":
				source.Columns[0].Usage.Typmod = 0
			case "collation":
				source.Columns[0].Usage.Collation = sqltype.CollationRef{}
			case "nullable":
				source.Columns[0].Nullable = false
			case "nullability-known":
				source.Columns[0].NullabilityKnown = false
			case "dimensions":
				source.Columns[0].DeclaredDimensions = 1
			case "origin":
				source.Columns[0].OriginAttribute = 2
			case "origin-table":
				source.Columns[0].OriginTable++
			case "virtual":
				source.VirtualView = false
			case "attributes":
				source.Attributes[0] = 2
			case "attribute-width":
				source.Attributes = source.Attributes[:3]
			case "missing-owner", "missing-wrapper", "wrong-wrapper-class":
				server := *f.server
				switch kind {
				case "missing-owner":
					server.Owner = f.f.e.RoleStore.Domain.Principal(99999)
				case "missing-wrapper":
					server.Wrapper.ObjectID = 99999
				case "wrong-wrapper-class":
					server.Wrapper = f.second.Address()
				}
				state.SetDescriptor(server.Address(), &server)
			}
			state.SetDescriptor(source.Address(), &source)
			rows, err := produce(f.input(t, state, f.f.reader))
			if rows != nil || err == nil {
				t.Fatal("malformed source accepted", rows, err)
			}
			var sqlState interface{ SQLState() string }
			if errors.As(err, &sqlState) {
				t.Fatal("invariant acquired SQLSTATE", err)
			}
		})
	}
}

func TestInfoForeignServersRetentionCancellation(t *testing.T) {
	f := newServersFixture(t)
	old := f.input(t, f.state, f.f.reader)
	oldRecord := old.Runtime.(core.EngineCatalogRuntime).Auth.RoleRecord
	oldDomain := old.Source.Columns[0].Usage.Type
	oldCharacter := old.Source.Columns[4].Usage.Type
	oldKind, oldVersion := f.server.ServerType, f.server.Version
	retained, original := serversRows(t, old)
	saved := make([][]sqltype.Value, len(retained))
	backing := make([]*sqltype.Value, len(retained))
	for i, row := range retained {
		saved[i] = append([]sqltype.Value(nil), row...)
		backing[i] = &row[0]
	}
	state := catalog.CloneState(f.state)
	wrapper := *f.wrapper
	wrapper.ObjectName = "renamed_wrapper"
	wrapper.ObjectRevision++
	state.SetDescriptor(wrapper.Address(), &wrapper)
	server := *f.server
	server.ObjectName = "renamed_server"
	server.ObjectRevision++
	server.ACL = authorization.ACLValue{Explicit: true}
	kind, version := "replacement", "new-version"
	server.ServerType, server.Version = &kind, &version
	state.SetDescriptor(server.Address(), &server)
	f.f.e.Mu.Lock()
	f.f.e.Catalog.PublishLocked(state)
	f.f.e.Mu.Unlock()
	_, revoked := serversRows(t, f.input(t, state, f.f.reader))
	if len(revoked) != 1 {
		t.Fatal("fresh revoke must leave only second server", revoked)
	}
	ownedServer := server
	ownedServer.Owner = f.f.reader
	ownedServer.ObjectRevision++
	state = catalog.CloneState(state)
	state.SetDescriptor(ownedServer.Address(), &ownedServer)
	fresh := f.input(t, state, f.f.reader)
	auth := fresh.Runtime.(core.EngineCatalogRuntime).Auth
	role, _ := auth.Roles.Find(f.f.reader)
	renamed := role
	renamed.Name = "Renamed Server Owner"
	root, err := auth.RoleRecord.Root.PutDescriptor(renamed)
	if err != nil {
		t.Fatal(err)
	}
	auth.RoleRecord = &access.RoleRecord{Root: root, Revision: auth.RoleRecord.Revision + 1}
	auth.Recapture(fresh.Catalog)
	fresh.Roles, fresh.Runtime = auth.Roles, core.EngineCatalogRuntime{Auth: auth, Database: "fresh_database"}
	_, got := serversRows(t, fresh)
	owner, _ := old.Roles.Find(f.f.owner)
	expected := map[[7]any]int{
		{"fresh_database", "renamed_server", "fresh_database", "renamed_wrapper", "replacement", "new-version", "Renamed Server Owner"}: 1,
		{"fresh_database", "null_server", "fresh_database", "renamed_wrapper", nil, nil, owner.Name}:                                    1,
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatal("fresh captured roots", got)
	}
	if old.Catalog.Get(f.server.Address()) != f.server || old.Catalog.Get(f.wrapper.Address()) != f.wrapper || f.server.ServerType != oldKind || f.server.Version != oldVersion || *oldKind != "é=🙂" || *oldVersion != "" {
		t.Fatal("retained descriptors changed")
	}
	if prior, _ := old.Roles.Find(f.f.reader); prior != role {
		t.Fatal("old role changed")
	}
	if old.Runtime.(core.EngineCatalogRuntime).Auth.RoleRecord != oldRecord || auth.RoleRecord == oldRecord || retained[0][0].Type() != oldDomain || retained[0][4].Type() != oldCharacter || !reflect.DeepEqual(retained, saved) {
		t.Fatal("retained values/type/role authority changed")
	}
	for i, row := range retained {
		if &row[0] != backing[i] {
			t.Fatal("row backing replaced")
		}
	}
	_, unchanged := serversRows(t, old)
	if !reflect.DeepEqual(unchanged, original) {
		t.Fatal("old capture changed", unchanged)
	}
	produce, _ := pgcatalog.ProducerFor(infoForeignServersOID)
	completed := 0
	for remaining := 1; ; remaining++ {
		in := old
		in.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		source, err := core.CaptureCatalogSourceRows(in, produce)
		if err == nil {
			source.Close()
			completed = remaining
			break
		}
		if source != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled prefix published", source, err)
		}
	}
	if completed < 4 {
		t.Fatal("inventory not traversed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in := old
	in.Context = ctx
	if source, err := core.CaptureCatalogSourceRows(in, produce); source != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-canceled capture", source, err)
	}
	_, unchanged = serversRows(t, old)
	if !reflect.DeepEqual(unchanged, original) {
		t.Fatal("cancellation changed captured roots")
	}
}
