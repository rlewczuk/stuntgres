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

const expectedForeignServerOptionsOID ids.OID = 16245

type serverOptionsFixture struct {
	f      defaultUnitFixture
	state  *catalog.State
	server *catalog.ForeignObject
}

func newServerOptionsFixture(t *testing.T) serverOptionsFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	wrapper := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignWrapperClassID, ObjectID: 22510}, ObjectName: "decoy_wrapper", ObjectRevision: 1, Owner: f.reader, Options: []catalog.ForeignOption{{Name: "wrapper-only", Value: "excluded"}}}
	state.SetDescriptor(wrapper.Address(), wrapper)
	server := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignServerClassID, ObjectID: 22500}, Wrapper: wrapper.Address(), ObjectName: "options_server", ObjectRevision: 1, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.UsagePrivilege, 0)}}, Options: []catalog.ForeignOption{{Name: "password", Value: "private=é🙂"}, {Name: "empty", Value: ""}}}
	state.SetDescriptor(server.Address(), server)
	mapping := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignMappingClassID, ObjectID: 22501}, Server: server.Address(), ObjectName: catalog.ForeignMappingName(server.Address(), f.reader), ObjectRevision: 1, Owner: f.reader, MappedRole: f.reader, Options: []catalog.ForeignOption{{Name: "mapping-only", Value: "excluded"}}}
	state.SetDescriptor(mapping.Address(), mapping)
	decoy := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 22503}, ObjectNamespace: f.schema, ObjectName: "decoy_table", ObjectRevision: 1, Owner: f.owner, Foreign: &catalog.ForeignTableDefinition{Options: []catalog.ForeignOption{{Name: "table-only", Value: "excluded"}}, Columns: map[int32][]catalog.ForeignOption{1: {{Name: "column-only", Value: "excluded"}}}}}
	state.SetDescriptor(decoy.Address(), decoy)
	return serverOptionsFixture{f: f, state: state, server: server}
}

func (f serverOptionsFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	if _, ok := pgcatalog.ProducerFor(expectedForeignServerOptionsOID); !ok {
		t.Fatal("missing foreign_server_options producer")
	}
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: expectedForeignServerOptionsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}}
}

func serverOptionsRows(t *testing.T, in pgcatalog.Input) ([][]sqltype.Value, map[[4]any]int) {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(expectedForeignServerOptionsOID)
	if !ok {
		t.Fatal("missing producer")
	}
	source, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if source.Ref != in.Ref || source.View != nil || source.Anonymous == nil {
		t.Fatal("anonymous source authority", source)
	}
	bag := map[[4]any]int{}
	for _, row := range source.Anonymous {
		if len(row) != 4 {
			t.Fatal("width", row)
		}
		var values [4]any
		for i, value := range row {
			column := in.Source.Columns[i]
			if value.Type() != column.Usage.Type || !value.ValidResult(column.Usage) {
				t.Fatal("domain carrier", i, value)
			}
			if value.IsNull() {
				t.Fatal("NULL configured option", i)
			}
			base, err := value.BaseValue()
			want := sqltype.Name
			if i == 3 {
				want = sqltype.VarChar
			}
			if err != nil || base.Type().ID() != want {
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

func TestInfoForeignServerOptionsProducer(t *testing.T) {
	f := newServerOptionsFixture(t)
	in := f.input(t, f.state, f.f.owner)
	if in.Source.Name() != "foreign_server_options" || in.Source.Namespace().ObjectID != 13229 || in.Source.Address().ObjectID != 16245 || !in.Source.VirtualView {
		t.Fatal("source identity", in.Source)
	}
	row := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(in.Source.RowType)})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
	if in.Source.RowType != 13546 || row.Array != 13545 || row.OwningRelation != in.Source.Address() || array.OwningRelation != in.Source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("row/array ownership")
	}
	names := []string{"foreign_server_catalog", "foreign_server_name", "option_name", "option_value"}
	if len(in.Source.Columns) != 4 || len(in.Source.Attributes) != 4 {
		t.Fatal("exact source width")
	}
	currentRow, rowOK := in.Catalog.LookupType(sqltype.ID(in.Source.RowType))
	currentArray, arrayOK := in.Catalog.LookupType(sqltype.ID(row.Array))
	if !rowOK || !arrayOK || currentRow != row.SQLType || currentArray != array.SQLType {
		t.Fatal("row/array generation")
	}
	for i, column := range in.Source.Columns {
		domainID := sqltype.ID(13248)
		baseID := sqltype.Name
		if i == 3 {
			domainID, baseID = 13246, sqltype.VarChar
		}
		domain, _ := in.Catalog.LookupType(domainID)
		base, _ := in.Catalog.LookupType(baseID)
		if column.Name != names[i] || column.Usage != domain.Usage() || domain.BaseType() != base || !column.Nullable || !column.NullabilityKnown || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.DeclaredDimensions != 0 || column.OriginTable != 16245 || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			t.Fatal("source metadata", i, column)
		}
	}
	admin := in.Runtime.(core.EngineCatalogRuntime).Auth.RoleRecord.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	want := map[[4]any]int{{"captured_database", "options_server", "password", "private=é🙂"}: 1, {"captured_database", "options_server", "empty", ""}: 1}
	for _, actor := range []authorization.PrincipalRef{admin, f.f.owner, f.f.member, f.f.reader} {
		_, got := serverOptionsRows(t, f.input(t, f.state, actor))
		if !reflect.DeepEqual(got, want) {
			t.Fatal("authorized full values", actor, got)
		}
	}
	t.Run("distinct-servers", func(t *testing.T) {
		state := catalog.CloneState(f.state)
		second := *f.server
		second.ObjectAddress.ObjectID = 22504
		second.ObjectName = "second_server"
		state.SetDescriptor(second.Address(), &second)
		expected := map[[4]any]int{}
		for row, count := range want {
			expected[row] = count
			row[1] = "second_server"
			expected[row] = count
		}
		_, got := serverOptionsRows(t, f.input(t, state, f.f.reader))
		if !reflect.DeepEqual(got, expected) {
			t.Fatal("distinct identities", got)
		}
	})
	if !in.Source.Security.ACL.Explicit {
		t.Fatal("source must carry explicit PUBLIC SELECT")
	}
	for _, kind := range []string{"empty-catalog", "optionless", "empty-options", "denied"} {
		state := catalog.CloneState(f.f.e.Catalog.State)
		state.SetDescriptor(f.state.Get(f.server.Wrapper).Address(), f.state.Get(f.server.Wrapper))
		if kind != "empty-catalog" {
			server := *f.server
			server.ACL = authorization.ACLValue{Explicit: true}
			if kind == "optionless" {
				server.Options = nil
			}
			if kind == "empty-options" {
				server.Options = []catalog.ForeignOption{}
			}
			state.SetDescriptor(server.Address(), &server)
		}
		actor := f.f.owner
		if kind == "denied" {
			actor = f.f.reader
		}
		rows, got := serverOptionsRows(t, f.input(t, state, actor))
		if rows == nil || len(got) != 0 {
			t.Fatal("fabricated rows", kind, got)
		}
	}
	for _, tc := range []struct {
		name    string
		owner   authorization.PrincipalRef
		grantee authorization.ACLGrantee
		rights  authorization.Privileges
		actor   authorization.PrincipalRef
		visible bool
	}{
		{"public", f.f.owner, authorization.ACLGrantee{Public: true}, authorization.UsagePrivilege, f.f.reader, true},
		{"inherited-usage", f.f.reader, authorization.ACLGrantee{Role: f.f.owner}, authorization.UsagePrivilege, f.f.member, true},
		{"irrelevant-privilege", f.f.owner, authorization.ACLGrantee{Role: f.f.reader}, authorization.SelectPrivilege, f.f.reader, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			server := *f.server
			server.Owner = tc.owner
			server.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: tc.owner, Grantee: tc.grantee, Privileges: tc.rights}}}
			state.SetDescriptor(server.Address(), &server)
			_, got := serverOptionsRows(t, f.input(t, state, tc.actor))
			if tc.visible && !reflect.DeepEqual(got, want) || !tc.visible && len(got) != 0 {
				t.Fatal("ACL traversal", got)
			}
		})
	}
	// SET-only membership must not become immediately inherited ownership.
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
	setOnly.Roles = auth.Roles
	setOnly.Runtime = core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}
	_, setRows := serverOptionsRows(t, setOnly)
	if len(setRows) != 0 {
		t.Fatal("SET-only ownership", setRows)
	}
	rights, err := in.Runtime.Effective(in.Ref, false)
	if err != nil || rights&authorization.SelectPrivilege == 0 {
		t.Fatal("PUBLIC SELECT", rights, err)
	}
	// Removing source read access does not alter represented-object admission.
	state := catalog.CloneState(f.state)
	source := *in.Source
	source.Security.ACL = authorization.ACLValue{Explicit: true}
	state.SetDescriptor(source.Address(), &source)
	denied := f.input(t, state, f.f.reader)
	rights, err = denied.Runtime.Effective(denied.Ref, false)
	if err != nil || rights&authorization.SelectPrivilege != 0 {
		t.Fatal("source ACL", rights, err)
	}
}

func TestInfoForeignServerOptionsCapture(t *testing.T) {
	f := newServerOptionsFixture(t)
	old := f.input(t, f.state, f.f.reader)
	oldOptions := f.server.Options
	oldOptionBacking := &oldOptions[0]
	oldRecord := old.Runtime.(core.EngineCatalogRuntime).Auth.RoleRecord
	oldDomain := old.Source.Columns[0].Usage.Type
	retained, original := serverOptionsRows(t, old)
	saved := make([][]sqltype.Value, len(retained))
	backing := make([]*sqltype.Value, len(retained))
	for i, row := range retained {
		saved[i] = append([]sqltype.Value(nil), row...)
		backing[i] = &row[0]
	}
	state := catalog.CloneState(f.state)
	server := *f.server
	server.ObjectRevision = f.server.Revision() + 1
	server.Owner = f.f.member
	server.ObjectName = "renamed_server"
	server.Options = []catalog.ForeignOption{{Name: "secret", Value: "replacement"}}
	server.ACL = authorization.ACLValue{Explicit: true}
	state.SetDescriptor(server.Address(), &server)
	deniedCapture := f.input(t, state, f.f.reader)
	_, got := serverOptionsRows(t, deniedCapture)
	if len(got) != 0 {
		t.Fatal("fresh revoke", got)
	}
	state = catalog.CloneState(state)
	admitted := server
	admitted.ObjectRevision++
	admitted.ACL.Entries = []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.UsagePrivilege, 0)}
	state.SetDescriptor(admitted.Address(), &admitted)
	fresh := f.input(t, state, f.f.reader)
	auth := fresh.Runtime.(core.EngineCatalogRuntime).Auth
	role, _ := auth.Roles.Find(f.f.reader)
	renamed := role
	renamed.Name = "Renamed Reader"
	renamed.AuthIDVersion = role.AuthIDVersion + 1
	root, err := auth.RoleRecord.Root.PutDescriptor(renamed)
	if err != nil {
		t.Fatal(err)
	}
	auth.RoleRecord = &access.RoleRecord{Root: root, Revision: auth.RoleRecord.Revision + 1}
	auth.Recapture(fresh.Catalog)
	fresh.Roles = auth.Roles
	fresh.Runtime = core.EngineCatalogRuntime{Auth: auth, Database: "fresh_database"}
	_, got = serverOptionsRows(t, fresh)
	if !reflect.DeepEqual(got, map[[4]any]int{{"fresh_database", "renamed_server", "secret", "replacement"}: 1}) {
		t.Fatal("fresh capture", got)
	}
	if old.Catalog.State() != f.state || fresh.Catalog.State() == old.Catalog.State() || fresh.Catalog.Get(f.server.Address()).Revision() <= f.server.Revision() || fresh.Roles == old.Roles || fresh.Runtime.(core.EngineCatalogRuntime).Auth.RoleRecord.Revision <= oldRecord.Revision {
		t.Fatal("replacement did not advance genuine catalog/role authorities")
	}
	if _, denied := serverOptionsRows(t, deniedCapture); len(denied) != 0 {
		t.Fatal("captured revoked authority mutated", denied)
	}
	if old.Catalog.Get(f.server.Address()) != f.server || f.server.Options[0].Value != "private=é🙂" || old.Source.Columns[0].Usage.Type != retained[0][0].Type() {
		t.Fatal("old descriptor/domain backing changed")
	}
	if oldRole, _ := old.Roles.Find(f.f.reader); oldRole != role {
		t.Fatal("old role root changed")
	}
	if &f.server.Options[0] != oldOptionBacking || !reflect.DeepEqual(oldOptions, f.server.Options) || old.Runtime.(core.EngineCatalogRuntime).Auth.RoleRecord != oldRecord || fresh.Runtime.(core.EngineCatalogRuntime).Auth.RoleRecord == oldRecord || retained[0][0].Type() != oldDomain {
		t.Fatal("retained option/role/domain authority changed")
	}
	if !reflect.DeepEqual(retained, saved) {
		t.Fatal("retained values changed")
	}
	for i, row := range retained {
		if &row[0] != backing[i] {
			t.Fatal("row backing replaced")
		}
	}
	_, oldBag := serverOptionsRows(t, old)
	if !reflect.DeepEqual(oldBag, original) {
		t.Fatal("old authority changed")
	}
	produce, _ := pgcatalog.ProducerFor(expectedForeignServerOptionsOID)
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = nil },
		func(in *pgcatalog.Input) { in.Source = nil },
		func(in *pgcatalog.Input) { in.Runtime = core.EngineCatalogRuntime{} },
		func(in *pgcatalog.Input) { in.Owner = &engine.New().Catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = engine.New().CatalogRead() },
		func(in *pgcatalog.Input) { in.Roles = engine.New().Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { in.Ref = new(ids.Domain).Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) { in.Runtime = nil },
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(core.EngineCatalogRuntime)
			runtime.Auth = f.input(t, f.state, f.f.owner).Runtime.(core.EngineCatalogRuntime).Auth
			runtime.Auth.Catalog = engine.New().CatalogRead()
			in.Runtime = runtime
		},
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
			t.Fatal("invalid authority accepted", rows, err)
		}
		var sqlState interface{ SQLState() string }
		if errors.As(err, &sqlState) {
			t.Fatal("internal invariant became SQLSTATE", err)
		}
	}
	for _, kind := range []string{"width", "relation-name", "namespace", "name", "domain", "generation", "base-generation", "typmod", "collation", "nullable", "nullability-known", "dimensions", "origin", "origin-table", "virtual", "attributes", "attribute-width", "missing-owner"} {
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
				source.Columns[0].Usage = source.Columns[3].Usage
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
			case "missing-owner":
				server := *f.server
				server.Owner = f.f.e.RoleStore.Domain.Principal(99999)
				state.SetDescriptor(server.Address(), &server)

			}
			state.SetDescriptor(source.Address(), &source)
			rows, err := produce(f.input(t, state, f.f.reader))
			if rows != nil || err == nil {
				t.Fatal("malformed metadata accepted", rows, err)
			}
			var sqlState interface{ SQLState() string }
			if errors.As(err, &sqlState) {
				t.Fatal("malformed authority became SQLSTATE", err)
			}
		})
	}
	// A counting context deterministically cancels at each capture boundary,
	// including after an option prefix and the final publication check.
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
			t.Fatal("canceled prefix publication", source, err)
		}
	}
	if completed < 4 {
		t.Fatal("capture did not traverse options")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in := old
	in.Context = ctx
	if source, err := core.CaptureCatalogSourceRows(in, produce); source != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled publication", source, err)
	}
	_, unchanged := serverOptionsRows(t, old)
	if !reflect.DeepEqual(unchanged, original) {
		t.Fatal("cancellation mutated catalog")
	}
	if &f.server.Options[0] != oldOptionBacking || old.Runtime.(core.EngineCatalogRuntime).Auth.RoleRecord != oldRecord || old.Source.Columns[0].Usage.Type != oldDomain || !reflect.DeepEqual(retained, saved) {
		t.Fatal("cancellation changed retained authority")
	}
	for i, row := range retained {
		if &row[0] != backing[i] {
			t.Fatal("cancellation replaced retained backing")
		}
	}
}
