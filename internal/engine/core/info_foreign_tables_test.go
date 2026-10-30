package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

type foreignTablesFixture struct {
	f     defaultUnitFixture
	state *catalog.State
	table *catalog.Relation
}

func newForeignTablesFixture(t *testing.T) foreignTablesFixture {
	t.Helper()
	// Reuse the actual foreign relation fixture, including dropped-column decoys.
	base := newColumnOptionsFixture(t)
	table := *base.table
	table.ObjectName = "ift_unit"
	foreign := *table.Foreign
	foreign.Options = []catalog.ForeignOption{{Name: "empty", Value: ""}, {Name: "password", Value: "visible=value"}, {Name: "remote", Value: "é=🙂"}, {Name: "quoted", Value: "a'b\\c  "}}
	table.Foreign = &foreign
	base.state.SetDescriptor(table.Address(), &table)
	wrapper := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignWrapperClassID, ObjectID: 22600}, ObjectName: "ift_wrapper", ObjectRevision: 1, Owner: base.f.owner}
	server := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignServerClassID, ObjectID: 22601}, ObjectName: "ift_server", ObjectRevision: 1, Owner: base.f.owner, Wrapper: wrapper.Address()}
	base.state.SetDescriptor(wrapper.Address(), wrapper)
	base.state.SetDescriptor(server.Address(), server)
	foreign.Server = server.Address()

	return foreignTablesFixture{f: base.f, state: base.state, table: &table}
}

func (f foreignTablesFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoForeignTablesOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}}
}

func foreignTablesRows(t *testing.T, in pgcatalog.Input) ([][]sqltype.Value, map[[5]string]int) {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoForeignTablesOID)
	if !ok {
		t.Fatal("missing producer")
	}
	source, err := core.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if source.Ref != in.Ref || source.View != nil || source.Anonymous == nil {
		t.Fatal("anonymous capture authority", source)
	}
	bag := map[[5]string]int{}
	for _, row := range source.Anonymous {
		if len(row) != 5 {
			t.Fatal("width", row)
		}
		var values [5]string
		for i, value := range row {
			column := in.Source.Columns[i]
			if value.IsNull() || value.Type() != column.Usage.Type || !value.ValidResult(column.Usage) {
				t.Fatal("domain authority", i, value)
			}
			base, err := value.BaseValue()
			want, ok := in.Catalog.LookupType(sqltype.Name)
			if err != nil || !ok || base.Type() != want {
				t.Fatal("base authority", i, base, err)
			}
			values[i], err = base.Text()
			if err != nil {
				t.Fatal(err)
			}
		}
		if row[0] != row[3] {
			t.Fatal("catalog cells do not share the captured value")
		}
		bag[values]++
	}
	return source.Anonymous, bag
}

func foreignTablesExpected() map[[5]string]int {
	return map[[5]string]int{{"captured_database", "default_schema", "ift_unit", "captured_database", "ift_server"}: 1}
}

func TestInfoForeignTablesProducer(t *testing.T) {
	f := newForeignTablesFixture(t)
	in := f.input(t, f.state, f.f.owner)
	if in.Source.Name() != "foreign_tables" || in.Source.Namespace().ObjectID != 13229 || in.Source.Address().ObjectID != 16248 || !in.Source.VirtualView {
		t.Fatal("source identity", in.Source)
	}
	row := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(in.Source.RowType)})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
	if in.Source.RowType != 13563 || row.Array != 13562 || row.OwningRelation != in.Source.Address() || array.OwningRelation != in.Source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("row/array ownership")
	}
	names := []string{"foreign_table_catalog", "foreign_table_schema", "foreign_table_name", "foreign_server_catalog", "foreign_server_name"}
	if len(in.Source.Columns) != len(names) || len(in.Source.Attributes) != len(names) {
		t.Fatal("source width")
	}
	for i, column := range in.Source.Columns {
		domainID, baseID := sqltype.ID(13248), sqltype.Name
		domain, _ := in.Catalog.LookupType(domainID)
		base, _ := in.Catalog.LookupType(baseID)
		if column.Name != names[i] || column.Usage != domain.Usage() || domain.BaseType() != base || !column.Nullable || !column.NullabilityKnown || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.DeclaredDimensions != 0 || column.OriginTable != 16248 || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			t.Fatal("source metadata", i, column)
		}
	}
	admin := in.Runtime.(core.EngineCatalogRuntime).Auth.RoleRecord.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	for _, actor := range []authorization.PrincipalRef{admin, f.f.owner, f.f.member} {
		_, got := foreignTablesRows(t, f.input(t, f.state, actor))
		if !reflect.DeepEqual(got, foreignTablesExpected()) {
			t.Fatal("authorized full values", actor, got)
		}
	}
	rights, err := in.Runtime.Effective(in.Ref, false)
	if err != nil || rights&authorization.SelectPrivilege == 0 || !in.Source.Security.ACL.Explicit {
		t.Fatal("source PUBLIC SELECT", rights, err)
	}
	for _, column := range []bool{false, true} {
		for _, privilege := range []authorization.Privileges{0, authorization.SelectPrivilege, authorization.InsertPrivilege, authorization.UpdatePrivilege, authorization.DeletePrivilege, authorization.TruncatePrivilege, authorization.ReferencesPrivilege, authorization.TriggerPrivilege, authorization.MaintainPrivilege} {
			state := catalog.CloneState(f.state)
			table := *f.table
			acl := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, privilege, 0)}}
			mask := authorization.SelectPrivilege | authorization.InsertPrivilege | authorization.UpdatePrivilege | authorization.ReferencesPrivilege
			if column {
				table.Security.ColumnACL = map[int32]authorization.ACLValue{1: acl}
			} else {
				table.Security.ACL = acl
				mask |= authorization.DeletePrivilege | authorization.TruncatePrivilege | authorization.TriggerPrivilege
			}
			state.SetDescriptor(table.Address(), &table)
			_, got := foreignTablesRows(t, f.input(t, state, f.f.reader))
			want := map[[5]string]int{}
			if privilege&mask != 0 {
				want = foreignTablesExpected()
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("eligibility", column, privilege, got, want)
			}
		}
	}
	for _, kind := range []string{"empty-catalog", "ordinary", "unoptioned", "no-foreign-definition", "zero-column", "dropped-column-right", "nonpositive-column-right", "public", "combined"} {
		state := catalog.CloneState(f.state)
		table := *f.table
		foreign := *table.Foreign
		table.Foreign = &foreign
		actor := f.f.owner
		visible := false
		switch kind {
		case "empty-catalog":
			state = catalog.CloneState(f.f.e.Catalog.State)
		case "ordinary":
			table.RelationKind = catalog.TableRelation
		case "unoptioned":
			foreign.Options = nil
			visible = true
		case "no-foreign-definition":
			table.Foreign = nil
		case "zero-column":
			table.Columns, table.Attributes = nil, nil
			visible = true
		case "dropped-column-right":
			actor = f.f.reader
			table.ColumnMeta.Dropped = map[int32]catalog.AttributeMetadata{1: {}, 2: {}, 3: {}}
			table.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}}}
		case "nonpositive-column-right":
			actor = f.f.reader
			table.Attributes = []int32{-1, 0}
			table.Security.ColumnACL = map[int32]authorization.ACLValue{-1: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}}}
		case "public":
			actor, visible = f.f.reader, true
			table.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: f.f.owner, Grantee: authorization.ACLGrantee{Public: true}, Privileges: authorization.SelectPrivilege}}}
		case "combined":
			actor, visible = f.f.reader, true
			table.Security.ACL.Entries = []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.MaintainPrivilege, 0)}
			table.Security.ColumnACL = map[int32]authorization.ACLValue{3: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.UpdatePrivilege, 0)}}}
		}
		if kind != "empty-catalog" {
			state.SetDescriptor(table.Address(), &table)
		}
		rows, got := foreignTablesRows(t, f.input(t, state, actor))
		want := map[[5]string]int{}
		if visible {
			want = foreignTablesExpected()
		}
		if rows == nil || !reflect.DeepEqual(got, want) {
			t.Fatal("boundary", kind, got, want)
		}
	}
	t.Run("actual_server_associations", func(t *testing.T) {
		state := catalog.CloneState(f.state)
		original, ok := in.Catalog.Foreign(f.table.Foreign.Server)
		if !ok {
			t.Fatal("missing fixture server")
		}
		server := *original
		server.ObjectAddress.ObjectID = 22602
		server.ObjectName = "ift_second_server"
		state.SetDescriptor(server.Address(), &server)
		for i, address := range []ids.ObjectAddress{server.Address(), f.table.Foreign.Server} {
			table := *f.table
			table.ObjectAddress.ObjectID = ids.OID(22610 + i)
			table.ObjectName = []string{"ift_second", "ift_shared"}[i]
			foreign := *table.Foreign
			foreign.Server = address
			table.Foreign = &foreign
			state.SetDescriptor(table.Address(), &table)
		}
		_, got := foreignTablesRows(t, f.input(t, state, f.f.owner))
		want := foreignTablesExpected()
		want[[5]string{"captured_database", "default_schema", "ift_second", "captured_database", "ift_second_server"}] = 1
		want[[5]string{"captured_database", "default_schema", "ift_shared", "captured_database", "ift_server"}] = 1
		if !reflect.DeepEqual(got, want) {
			t.Fatal("table-server association or shared-server cardinality", got, want)
		}
	})
}

func TestInfoForeignTablesAuthority(t *testing.T) {
	f := newForeignTablesFixture(t)
	old := f.input(t, f.state, f.f.owner)
	oldOptions := f.table.Foreign.Options
	oldOptionBacking := &oldOptions[0]
	oldDomain := old.Source.Columns[0].Usage.Type
	oldRecord := old.Runtime.(core.EngineCatalogRuntime).Auth.RoleRecord
	retained, original := foreignTablesRows(t, old)
	saved := make([][]sqltype.Value, len(retained))
	backing := make([]*sqltype.Value, len(retained))
	for i, row := range retained {
		saved[i] = append([]sqltype.Value(nil), row...)
		backing[i] = &row[0]
	}
	state := catalog.CloneState(f.state)
	table := *f.table
	table.ObjectName = "renamed"
	table.Owner = f.f.reader
	table.Security.ACL = authorization.ACLValue{Explicit: true}
	foreign := *table.Foreign
	foreign.Options = []catalog.ForeignOption{{Name: "secret", Value: "replacement"}}
	table.Foreign = &foreign
	state.SetDescriptor(table.Address(), &table)
	namespace := *old.Catalog.MustNamespace(f.f.schema)
	namespace.ObjectName = "renamed_schema"
	state.SetDescriptor(namespace.Address(), &namespace)
	serverDescriptor, ok := old.Catalog.Foreign(f.table.Foreign.Server)
	if !ok {
		t.Fatal("missing fixture server")
	}
	server := *serverDescriptor
	server.ObjectName = "renamed_server"
	state.SetDescriptor(server.Address(), &server)
	_, denied := foreignTablesRows(t, f.input(t, state, f.f.owner))
	if len(denied) != 0 {
		t.Fatal("fresh ownership/ACL", denied)
	}
	fresh := f.input(t, state, f.f.reader)
	auth := fresh.Runtime.(core.EngineCatalogRuntime).Auth
	originalRole, _ := old.Roles.Find(f.f.reader)
	renamedRole := originalRole
	renamedRole.Name = "renamed_reader"
	roles := cloneRoleFixture(auth.RoleRecord)
	putFixtureRole(roles, renamedRole)
	auth.RoleRecord = roles
	auth.Recapture(fresh.Catalog)
	fresh.Roles = auth.Roles
	fresh.Runtime = core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}
	if retainedRole, _ := old.Roles.Find(f.f.reader); retainedRole != originalRole || roles == oldRecord {
		t.Fatal("role capture backing changed")
	}
	_, got := foreignTablesRows(t, fresh)
	if !reflect.DeepEqual(got, map[[5]string]int{{"captured_database", "renamed_schema", "renamed", "captured_database", "renamed_server"}: 1}) {
		t.Fatal("fresh capture", got)
	}
	if old.Catalog.Get(f.table.Address()) != f.table || &f.table.Foreign.Options[0] != oldOptionBacking || !reflect.DeepEqual(oldOptions, f.table.Foreign.Options) || old.Runtime.(core.EngineCatalogRuntime).Auth.RoleRecord != oldRecord || retained[0][0].Type() != oldDomain || fresh.Source.Columns[0].Usage.Type != oldDomain {
		t.Fatal("retained catalog/role/domain backing")
	}
	if old.Catalog.Get(serverDescriptor.Address()) != serverDescriptor || serverDescriptor.Name() != "ift_server" {
		t.Fatal("retained server association backing changed")
	}
	if !reflect.DeepEqual(retained, saved) {
		t.Fatal("retained values changed")
	}
	for i, row := range retained {
		if &row[0] != backing[i] {
			t.Fatal("row backing replaced")
		}
	}
	_, oldBag := foreignTablesRows(t, old)
	if !reflect.DeepEqual(oldBag, original) {
		t.Fatal("old authority changed")
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoForeignTablesOID)
	for _, mutate := range []func(*pgcatalog.Input){
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
			runtime.Auth.RoleRecord = nil
			in.Runtime = runtime
		},
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(core.EngineCatalogRuntime)
			runtime.Auth.State.CurrentUser = authorization.PrincipalRef{}
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
				source.Columns = source.Columns[:4]
			case "relation-name":
				source.ObjectName = "wrong"
			case "namespace":
				source.ObjectNamespace.ObjectID++
			case "name":
				source.Columns[0].Name = "wrong"
			case "domain":
				source.Columns[0].Usage.Type, _ = old.Catalog.LookupType(sqltype.Text)
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
				source.Attributes = source.Attributes[:4]
			case "missing-owner":
				table := *f.table
				table.Owner = f.f.e.RoleStore.Domain.Principal(99999)
				state.SetDescriptor(table.Address(), &table)
			}
			state.SetDescriptor(source.Address(), &source)
			if rows, err := produce(f.input(t, state, f.f.owner)); rows != nil || err == nil {
				t.Fatal("malformed metadata accepted", rows, err)
			}
		})
	}
	for _, kind := range []string{"missing-server", "wrong-server-class", "missing-wrapper", "wrong-wrapper-class"} {
		state := catalog.CloneState(f.state)
		table := *f.table
		foreign := *table.Foreign
		table.Foreign = &foreign
		serverDescriptor, ok := old.Catalog.Foreign(foreign.Server)
		if !ok {
			t.Fatal("missing fixture server")
		}
		server := *serverDescriptor
		switch kind {
		case "missing-server":
			foreign.Server.ObjectID = 999999
		case "wrong-server-class":
			foreign.Server = server.Wrapper
		case "missing-wrapper":
			server.Wrapper.ObjectID = 999999
		case "wrong-wrapper-class":
			server.Wrapper = server.Address()
		}
		state.SetDescriptor(table.Address(), &table)
		state.SetDescriptor(server.Address(), &server)
		rows, err := produce(f.input(t, state, f.f.owner))
		var stateError interface{ SQLState() string }
		if rows != nil || err == nil || errors.As(err, &stateError) {
			t.Fatal("invalid association must fail without SQLSTATE", kind, rows, err)
		}
	}
}

func TestInfoForeignTablesCancellation(t *testing.T) {
	f := newForeignTablesFixture(t)
	old := f.input(t, f.state, f.f.owner)
	_, original := foreignTablesRows(t, old)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoForeignTablesOID)
	if !ok {
		t.Fatal("missing foreign_tables producer")
	}
	// Walk every deterministic cancellation boundary, including relation prefixes
	// and the final publication fence; no timing or public SQL is involved.
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
		t.Fatal("capture did not traverse foreign tables")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in := old
	in.Context = ctx
	if source, err := core.CaptureCatalogSourceRows(in, produce); source != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled publication", source, err)
	}
	_, unchanged := foreignTablesRows(t, old)
	if !reflect.DeepEqual(unchanged, original) {
		t.Fatal("cancellation mutated catalog")
	}
}
