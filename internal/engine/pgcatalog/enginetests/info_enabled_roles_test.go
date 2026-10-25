package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func enabledRolesUnitInput(t *testing.T, f defaultUnitFixture, view catalog.Read, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	auth := dtpUnitAuthority(t, f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoEnabledRolesOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: engine.EngineCatalogRuntime{Auth: auth}}
}

func enabledRolesUnitRows(t *testing.T, in pgcatalog.Input, names ...string) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoEnabledRolesOID)
	if !ok {
		t.Fatal("missing enabled_roles producer")
	}
	captured, err := engine.CaptureCatalogSourceRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil {
		t.Fatal("anonymous source identity", captured)
	}
	bag, want := map[string]int{}, map[string]int{}
	for _, name := range names {
		want[name]++
	}
	for _, row := range captured.Anonymous {
		if len(row) != 1 {
			t.Fatal("projection width", row)
		}
		value, column := row[0], in.Source.Columns[0]
		if value.IsNull() || value.Type() != column.Usage.Type || value.Type().ID() != 13248 || !value.ValidResult(column.Usage) {
			t.Fatal("source-owned identifier carrier", value)
		}
		bag[infoUserMappingsUnitText(t, value)]++
	}
	if !reflect.DeepEqual(bag, want) {
		t.Fatalf("roles=%v want=%v", bag, want)
	}
	return captured.Anonymous
}

func TestInfoEnabledRolesProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := enabledRolesUnitInput(t, f, view, f.reader)
	enabledRolesUnitRows(t, in, "default_reader")
	source := in.Source
	if !source.VirtualView || source.RelationKind != catalog.CatalogRelation || source.RowType != 13348 || len(source.Columns) != 1 {
		t.Fatal("view identity", source)
	}
	column := source.Columns[0]
	if column.Name != "role_name" || !column.Nullable || !column.NullabilityKnown || column.OriginTable != 16208 || column.OriginAttribute != 1 || column.Usage.Type.ID() != 13248 || column.Usage.Type.BaseType().ID() != sqltype.Name {
		t.Fatal("column contract", column)
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	row, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13348})
	if !ok || row.OwningRelation != source.Address() || row.Array != 13347 {
		t.Fatal("owned row type", row)
	}
	array, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13347})
	if !ok || array.OwningRelation != source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("owned array type", array)
	}
}

func TestInfoEnabledRolesSemantics(t *testing.T) {
	f := newDefaultUnitFixture(t)
	original := f.e.RoleStore.Published
	bootstrap := fixtureRole(original, f.e.Roles().LookupRef("postgres"))
	dbOwner := fixtureRole(original, f.e.Roles().LookupRef("pg_database_owner"))
	roles := emptyRoleFixture(f.e)
	// A controlled all-live inventory makes superuser expectations literal.
	for _, role := range []authorization.RoleDescriptor{bootstrap, dbOwner, fixtureRole(original, f.owner), fixtureRole(original, f.reader), fixtureRole(original, f.member)} {
		putFixtureRole(roles, role)
	}
	wheel := f.e.RoleStore.Domain.Principal(21001)
	bridge := f.e.RoleStore.Domain.Principal(21002)
	adminOnly := f.e.RoleStore.Domain.Principal(21003)
	for ref, name := range map[authorization.PrincipalRef]string{wheel: "wheel", bridge: "bridge", adminOnly: "adminonly"} {
		putFixtureRole(roles, authorization.RoleDescriptor{Ref: ref, Name: name, Inherit: true})
	}
	edges := []authorization.Membership{
		{Role: f.owner, Member: f.reader, Grantor: bootstrap.Ref, Inherit: true},
		{Role: wheel, Member: f.owner, Grantor: bootstrap.Ref, Set: true},
		{Role: adminOnly, Member: f.reader, Grantor: bootstrap.Ref, Admin: true},
	}
	setFixtureEdges(roles, edges)
	f.e.RoleStore.PublishLocked(roles)
	view := f.e.CatalogRead()
	input := func(actor authorization.PrincipalRef) pgcatalog.Input {
		return enabledRolesUnitInput(t, f, view, actor)
	}
	retainedInput := input(f.reader)
	retained := enabledRolesUnitRows(t, retainedInput, "default_reader", "default_owner")
	enabledRolesUnitRows(t, input(f.member), "default_member")
	enabledRolesUnitRows(t, input(bootstrap.Ref), "postgres", "pg_database_owner", "default_owner", "default_reader", "default_member", "wheel", "bridge", "adminonly")

	changed := cloneRoleFixture(roles)
	edges = append(edges,
		authorization.Membership{Role: bridge, Member: f.reader, Grantor: bootstrap.Ref, Inherit: true},
		authorization.Membership{Role: wheel, Member: bridge, Grantor: bootstrap.Ref, Inherit: true},
		authorization.Membership{Role: wheel, Member: f.reader, Grantor: bootstrap.Ref, Inherit: true})
	setFixtureEdges(changed, edges)
	// Role-level NOINHERIT cannot override explicit inherited membership edges.
	reader := fixtureRole(changed, f.reader)
	reader.Inherit = false
	putFixtureRole(changed, reader)
	f.e.RoleStore.PublishLocked(changed)
	enabledRolesUnitRows(t, input(f.reader), "default_reader", "default_owner", "bridge", "wheel")
	// Distinct grantors and diamond paths still produce just one target row.
	edges = append(edges, authorization.Membership{Role: wheel, Member: f.reader, Grantor: f.owner, Inherit: true})
	changed = cloneRoleFixture(changed)
	setFixtureEdges(changed, edges)
	f.e.RoleStore.PublishLocked(changed)
	enabledRolesUnitRows(t, input(f.reader), "default_reader", "default_owner", "bridge", "wheel")

	changed = cloneRoleFixture(changed)
	power := fixtureRole(changed, f.owner)
	power.Superuser = true
	putFixtureRole(changed, power)
	f.e.RoleStore.PublishLocked(changed)
	enabledRolesUnitRows(t, input(f.reader), "default_reader", "default_owner", "bridge", "wheel")
	// Rename/drop/create publish a new graph without rewriting old captured names.
	renamed := cloneRoleFixture(changed)
	power.Name = "renamed_owner"
	putFixtureRole(renamed, power)
	retireFixtureRole(renamed, adminOnly)
	newRef := f.e.RoleStore.Domain.Principal(21004)
	putFixtureRole(renamed, authorization.RoleDescriptor{Ref: newRef, Name: "new_role"})
	f.e.RoleStore.PublishLocked(renamed)
	enabledRolesUnitRows(t, input(f.reader), "default_reader", "renamed_owner", "bridge", "wheel")
	enabledRolesUnitRows(t, input(bootstrap.Ref), "postgres", "pg_database_owner", "renamed_owner", "default_reader", "default_member", "wheel", "bridge", "new_role")
	oldBag := map[string]int{}
	for _, row := range retained {
		oldBag[infoUserMappingsUnitText(t, row[0])]++
	}
	if !reflect.DeepEqual(oldBag, map[string]int{"default_reader": 1, "default_owner": 1}) {
		t.Fatal("retained names changed", oldBag)
	}
	enabledRolesUnitRows(t, retainedInput, "default_reader", "default_owner")
	// The implicit database-owner hop uses the paired catalog, not session state.
	state := catalog.CloneState(view.State())
	database, _ := view.Database(catalog.CurrentDatabaseAddress)
	owned := *database
	owned.Owner = f.reader
	state.SetDescriptor(owned.Address(), &owned)
	ownedInput := enabledRolesUnitInput(t, f, f.e.Catalog.Read(state), f.reader)
	enabledRolesUnitRows(t, ownedInput, "default_reader", "renamed_owner", "bridge", "wheel", "pg_database_owner")
}

func TestInfoEnabledRolesCaptureOwnership(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := enabledRolesUnitInput(t, f, view, f.reader)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoEnabledRolesOID)
	for _, actor := range []authorization.PrincipalRef{f.reader, f.e.Roles().LookupRef("postgres")} {
		for _, remaining := range []int{1, 4, 6} {
			canceled := enabledRolesUnitInput(t, f, view, actor)
			canceled.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
			if rows, err := produce(canceled); rows != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation exposed partial rows", rows, err)
			}
		}
	}
	foreign := engine.New()
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &foreign.Catalog.Owner },
		func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref = foreign.Catalog.Owner.Domain.Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(engine.EngineCatalogRuntime)
			runtime.Auth.RoleRecord = nil
			in.Runtime = runtime
		},
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(engine.EngineCatalogRuntime)
			runtime.Auth.State.CurrentUser = foreign.Roles().LookupRef("postgres")
			in.Runtime = runtime
		},
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(engine.EngineCatalogRuntime)
			runtime.Auth.State.CurrentUser = f.e.RoleStore.Domain.Principal(999999)
			in.Runtime = runtime
		},
	} {
		bad := in
		mutate(&bad)
		if rows, err := produce(bad); rows != nil || err == nil {
			t.Fatal("invalid capture accepted", rows, err)
		}
	}
	for _, malformed := range []string{"width", "base", "wrong_domain", "foreign_domain"} {
		state := catalog.CloneState(view.State())
		source := *in.Source
		source.Columns = append([]catalog.Column(nil), source.Columns...)
		switch malformed {
		case "width":
			source.Columns = nil
		case "base":
			source.Columns[0].Usage.Type, _ = view.LookupType(sqltype.Name)
		case "wrong_domain":
			source.Columns[0].Usage.Type, _ = view.LookupType(13243)
		case "foreign_domain":
			source.Columns[0].Usage.Type, _ = foreign.CatalogRead().LookupType(13248)
		}
		state.SetDescriptor(source.Address(), &source)
		bad := enabledRolesUnitInput(t, f, f.e.Catalog.Read(state), f.reader)
		if rows, err := produce(bad); rows != nil || err == nil {
			t.Fatal("malformed metadata accepted", malformed, rows, err)
		}
	}
}

func emptyRoleFixture(e *engine.Engine) *access.RoleRecord {
	root := authorization.Seed(&e.RoleStore.Domain)
	targets := make(map[authorization.PrincipalRef]authorization.RoleDescriptor)
	for ref, d := range root.Read(authorization.PrincipalRef{}).RoleEntries() {
		targets[ref] = d
	}
	return &access.RoleRecord{Root: root.Retire(targets), Revision: 1}
}

func retireFixtureRole(r *access.RoleRecord, ref authorization.PrincipalRef) {
	d := fixtureRole(r, ref)
	r.Root = r.Root.Retire(map[authorization.PrincipalRef]authorization.RoleDescriptor{ref: d})
}
