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

type mappingOptionsFixture struct {
	f       defaultUnitFixture
	state   *catalog.State
	server  *catalog.ForeignObject
	mapping *catalog.ForeignObject
}

func newMappingOptionsFixture(t *testing.T) mappingOptionsFixture {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	server := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignServerClassID, ObjectID: 22500}, ObjectName: "options_server", ObjectRevision: 1, Owner: f.owner, ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f, authorization.UsagePrivilege, 0)}}, Options: []catalog.ForeignOption{{Name: "server-only", Value: "decoy"}}}
	state.SetDescriptor(server.Address(), server)
	var first *catalog.ForeignObject
	for i, role := range []authorization.PrincipalRef{f.reader, f.owner, {}, f.member} {
		options := []catalog.ForeignOption{{Name: "password", Value: "private=é🙂"}}
		if i == 0 {
			options = append(options, catalog.ForeignOption{Name: "empty", Value: ""})
		}
		if i == 2 {
			options = []catalog.ForeignOption{{Name: "public", Value: "public-secret"}}
		}
		if i == 3 {
			options = nil
		}
		mapping := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignMappingClassID, ObjectID: ids.OID(22501 + i)}, ObjectName: catalog.ForeignMappingName(server.Address(), role), ObjectRevision: 1, Server: server.Address(), MappedRole: role, MappedName: "obsolete diagnostic", Options: options}
		state.SetDescriptor(mapping.Address(), mapping)
		if i == 0 {
			first = mapping
		}
	}
	wrapper := &catalog.ForeignObject{ObjectAddress: ids.ObjectAddress{ClassID: ids.ForeignWrapperClassID, ObjectID: 22505}, ObjectName: "decoy_wrapper", ObjectRevision: 1, Owner: f.owner, Options: []catalog.ForeignOption{{Name: "wrapper-only", Value: "decoy"}}}
	state.SetDescriptor(wrapper.Address(), wrapper)
	return mappingOptionsFixture{f: f, state: state, server: server, mapping: first}
}

func (f mappingOptionsFixture) input(t *testing.T, state *catalog.State, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	view := f.f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f.f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoUserMappingOptionsOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}}
}

func mappingOptionsRows(t *testing.T, in pgcatalog.Input) ([][]sqltype.Value, map[[5]any]int) {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoUserMappingOptionsOID)
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
	bag := map[[5]any]int{}
	for _, row := range source.Anonymous {
		if len(row) != 5 {
			t.Fatal("width", row)
		}
		var values [5]any
		for i, value := range row {
			column := in.Source.Columns[i]
			if value.Type() != column.Usage.Type || !value.ValidResult(column.Usage) {
				t.Fatal("domain carrier", i, value)
			}
			if value.IsNull() {
				if i != 4 {
					t.Fatal("NULL identity", i)
				}
				continue
			}
			base, err := value.BaseValue()
			want := sqltype.Name
			if i == 4 {
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

func mappingOptionsExpected(disclose string) map[[5]any]int {
	bag := map[[5]any]int{}
	for _, item := range []struct{ role, name, value string }{{"default_reader", "password", "private=é🙂"}, {"default_reader", "empty", ""}, {"default_owner", "password", "private=é🙂"}, {"PUBLIC", "public", "public-secret"}} {
		var value any = item.value
		if disclose != "all" && disclose != item.role && !(disclose == "owner" && (item.role == "PUBLIC" || item.role == "default_owner")) {
			value = nil
		}
		bag[[5]any{item.role, "captured_database", "options_server", item.name, value}]++
	}
	return bag
}

func TestInfoUserMappingOptionsProducer(t *testing.T) {
	f := newMappingOptionsFixture(t)
	in := f.input(t, f.state, f.f.owner)
	if in.Source.Name() != "user_mapping_options" || in.Source.Namespace().ObjectID != 13229 || in.Source.Address().ObjectID != 16235 || !in.Source.VirtualView {
		t.Fatal("source identity", in.Source)
	}
	row := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(in.Source.RowType)})
	array := in.Catalog.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)})
	if in.Source.RowType != 13572 || row.Array != 13571 || row.OwningRelation != in.Source.Address() || array.OwningRelation != in.Source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("row/array ownership")
	}
	names := []string{"authorization_identifier", "foreign_server_catalog", "foreign_server_name", "option_name", "option_value"}
	for i, column := range in.Source.Columns {
		domainID := sqltype.ID(13248)
		baseID := sqltype.Name
		if i == 4 {
			domainID, baseID = 13246, sqltype.VarChar
		}
		domain, _ := in.Catalog.LookupType(domainID)
		base, _ := in.Catalog.LookupType(baseID)
		if column.Name != names[i] || column.Usage != domain.Usage() || domain.BaseType() != base || !column.Nullable || !column.NullabilityKnown || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.DeclaredDimensions != 0 || column.OriginTable != 16235 || column.OriginAttribute != int16(i+1) || in.Source.Attributes[i] != int32(i+1) {
			t.Fatal("source metadata", i, column)
		}
	}
	admin := in.Runtime.(core.EngineCatalogRuntime).Auth.RoleRecord.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	for _, tc := range []struct {
		actor    authorization.PrincipalRef
		disclose string
	}{{admin, "all"}, {f.f.owner, "owner"}, {f.f.member, "PUBLIC"}, {f.f.reader, "default_reader"}} {
		_, got := mappingOptionsRows(t, f.input(t, f.state, tc.actor))
		if !reflect.DeepEqual(got, mappingOptionsExpected(tc.disclose)) {
			t.Fatal("visibility/disclosure", tc.disclose, got)
		}
	}
	for _, kind := range []string{"empty-catalog", "server-only", "optionless", "denied-server"} {
		state := catalog.CloneState(f.f.e.Catalog.State)
		if kind != "empty-catalog" {
			server := *f.server
			server.ACL = authorization.ACLValue{Explicit: true}
			state.SetDescriptor(server.Address(), &server)
		}
		if kind == "optionless" || kind == "denied-server" {
			mapping := *f.mapping
			if kind == "optionless" {
				mapping.Options = nil
			}
			state.SetDescriptor(mapping.Address(), &mapping)
		}
		actor := f.f.owner
		if kind == "denied-server" {
			actor = f.f.reader
		}
		rows, bag := mappingOptionsRows(t, f.input(t, state, actor))
		if rows == nil || len(bag) != 0 {
			t.Fatal("fabricated rows", kind, bag)
		}
	}
	// Source read access uses the ordinary PUBLIC relation ACL, not producer admission.
	rights, err := in.Runtime.Effective(in.Ref, false)
	if err != nil || rights&authorization.SelectPrivilege == 0 {
		t.Fatal("PUBLIC SELECT", rights, err)
	}
}

func TestInfoUserMappingOptionsCapture(t *testing.T) {
	f := newMappingOptionsFixture(t)
	old := f.input(t, f.state, f.f.reader)
	retained, original := mappingOptionsRows(t, old)
	savedRows := make([][]sqltype.Value, len(retained))
	for i, row := range retained {
		savedRows[i] = append([]sqltype.Value(nil), row...)
	}
	savedFirst := make([]*sqltype.Value, len(retained))
	for i, row := range retained {
		savedFirst[i] = &row[0]
	}
	state := catalog.CloneState(f.state)
	server := *f.server
	server.ObjectName = "renamed_server"
	server.ACL = authorization.ACLValue{Explicit: true}
	state.SetDescriptor(server.Address(), &server)
	mapping := *f.mapping
	mapping.Options = []catalog.ForeignOption{{Name: "new", Value: "replacement"}}
	state.SetDescriptor(mapping.Address(), &mapping)
	denied, bag := mappingOptionsRows(t, f.input(t, state, f.f.reader))
	if denied == nil || len(bag) != 0 {
		t.Fatal("fresh revoked access", bag)
	}
	server.ACL.Entries = []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.UsagePrivilege, 0)}
	state.SetDescriptor(server.Address(), &server)
	fresh := f.input(t, state, f.f.reader)
	auth := fresh.Runtime.(core.EngineCatalogRuntime).Auth
	role, _ := auth.Roles.Find(f.f.reader)
	renamed := role
	renamed.Name = "Renamed Reader"
	root, err := auth.RoleRecord.Root.PutDescriptor(renamed)
	if err != nil {
		t.Fatal(err)
	}
	auth.RoleRecord = &access.RoleRecord{Root: root, Revision: auth.RoleRecord.Revision + 1}
	auth.Recapture(fresh.Catalog)
	fresh.Roles = auth.Roles
	fresh.Runtime = core.EngineCatalogRuntime{Auth: auth, Database: "captured_database"}
	_, got := mappingOptionsRows(t, fresh)
	want := map[[5]any]int{{"Renamed Reader", "captured_database", "renamed_server", "new", "replacement"}: 1, {"default_owner", "captured_database", "renamed_server", "password", nil}: 1, {"PUBLIC", "captured_database", "renamed_server", "public", nil}: 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("fresh role/server/options", got, want)
	}
	if old.Catalog.Get(f.mapping.Address()) != f.mapping || f.mapping.Options[0].Value != "private=é🙂" || f.mapping.MappedName != "obsolete diagnostic" {
		t.Fatal("old mapping backing changed")
	}
	if oldRole, _ := old.Roles.Find(f.f.reader); oldRole != role {
		t.Fatal("old role root changed")
	}
	if !reflect.DeepEqual(retained, savedRows) {
		t.Fatal("retained values changed")
	}
	for i, row := range retained {
		if &row[0] != savedFirst[i] {
			t.Fatal("retained row backing replaced")
		}
	}
	_, oldBag := mappingOptionsRows(t, old)
	if !reflect.DeepEqual(oldBag, original) || !reflect.DeepEqual(original, mappingOptionsExpected("default_reader")) {
		t.Fatal("old authority changed", oldBag)
	}
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoUserMappingOptionsOID)
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
	for _, kind := range []string{"width", "name", "domain", "generation", "base-generation", "typmod", "collation", "nullable", "nullability-known", "dimensions", "origin", "virtual", "attributes", "missing-server", "missing-role"} {
		t.Run(kind, func(t *testing.T) {
			state := catalog.CloneState(f.state)
			source := *old.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			source.Attributes = append([]int32(nil), source.Attributes...)
			switch kind {
			case "width":
				source.Columns = source.Columns[:4]
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
			case "virtual":
				source.VirtualView = false
			case "attributes":
				source.Attributes[0] = 2
			case "missing-server", "missing-role":
				mapping := *f.mapping
				if kind == "missing-server" {
					mapping.Server.ObjectID = 22999
				} else {
					mapping.MappedRole = f.f.e.RoleStore.Domain.Principal(99999)
				}
				state.SetDescriptor(mapping.Address(), &mapping)
			}
			state.SetDescriptor(source.Address(), &source)
			if rows, err := produce(f.input(t, state, f.f.reader)); rows != nil || err == nil {
				t.Fatal("malformed metadata accepted", rows, err)
			}
		})
	}
	for _, remaining := range []int{1, 4, 12} {
		in := old
		in.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(in); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled prefix", rows, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	in := old
	in.Context = ctx
	if source, err := core.CaptureCatalogSourceRows(in, produce); source != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled publication", source, err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	defer cancel()
	in = old
	in.Context = ctx
	calls := 0
	in.Runtime = mappingOptionsCancelRuntime{EngineCatalogRuntime: old.Runtime.(core.EngineCatalogRuntime), cancel: cancel, calls: &calls}
	if source, err := core.CaptureCatalogSourceRows(in, produce); source != nil || !errors.Is(err, context.Canceled) || calls != 3 {
		t.Fatal("partial capture publication", source, err, calls)
	}
	_, unchanged := mappingOptionsRows(t, old)
	if !reflect.DeepEqual(unchanged, original) {
		t.Fatal("cancellation mutated catalog", unchanged)
	}
}

type mappingOptionsCancelRuntime struct {
	core.EngineCatalogRuntime
	cancel context.CancelFunc
	calls  *int
}

func (r mappingOptionsCancelRuntime) ForeignServerVisible(server *catalog.ForeignObject, mapped authorization.PrincipalRef) (bool, error) {
	visible, err := r.EngineCatalogRuntime.ForeignServerVisible(server, mapped)
	*r.calls++
	if *r.calls == 3 {
		r.cancel()
	}
	return visible, err
}
