package enginetests_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func applicableRolesUnitInput(t *testing.T, f defaultUnitFixture, view catalog.Read, actor authorization.PrincipalRef) pgcatalog.Input {
	t.Helper()
	auth := dtpUnitAuthority(t, f, view)
	auth.State.CurrentUser = actor
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.InfoApplicableRolesOID})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth})}
}

func applicableRolesUnitBag(t *testing.T, rows [][]sqltype.Value, source *catalog.Relation) map[[3]string]int {
	t.Helper()
	bag := map[[3]string]int{}
	for _, row := range rows {
		if len(row) != 3 {
			t.Fatal("projection width", row)
		}
		var triple [3]string
		for i, value := range row {
			column := source.Columns[i]
			if value.IsNull() || value.Type() != column.Usage.Type || !value.Type().IsDomain() || !value.ValidResult(column.Usage) {
				t.Fatal("source-owned domain carrier", i, value)
			}
			base, err := value.BaseValue()
			baseID := sqltype.Name
			if i == 2 {
				baseID = sqltype.VarChar
			}
			if err != nil || base.IsNull() || base.Type() != column.Usage.Type.BaseType() || base.Type().ID() != baseID {
				t.Fatal("physical domain carrier", i, base, err)
			}
			triple[i], err = base.Text()
			if err != nil {
				t.Fatal("physical domain text", i, err)
			}
		}
		bag[triple]++
	}
	return bag
}

func applicableRolesUnitRows(t *testing.T, in pgcatalog.Input, triples ...[3]string) [][]sqltype.Value {
	t.Helper()
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoApplicableRolesOID)
	if !ok {
		t.Fatal("missing applicable_roles producer")
	}
	captured, err := catalogrt.CaptureRows(in, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer captured.Close()
	if captured.Ref != in.Ref || captured.View != nil || captured.Anonymous == nil {
		t.Fatal("anonymous source identity", captured)
	}
	want := map[[3]string]int{}
	for _, triple := range triples {
		want[triple]++
	}
	if got := applicableRolesUnitBag(t, captured.Anonymous, in.Source); !reflect.DeepEqual(got, want) {
		t.Fatalf("triples=%v want=%v", got, want)
	}
	return captured.Anonymous
}

func TestInfoApplicableRolesProducer(t *testing.T) {
	f := newDefaultUnitFixture(t)
	view := f.e.CatalogRead()
	in := applicableRolesUnitInput(t, f, view, f.reader)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoApplicableRolesOID)
	if !ok {
		t.Fatal("missing producer")
	}
	rows, err := produce(in)
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatal("ordinary no-edge nonowner", rows, err)
	}
	applicableRolesUnitRows(t, in)
	source := in.Source
	if !source.VirtualView || source.RelationKind != catalog.CatalogRelation || source.Address().ObjectID != 16257 || source.RowType != 13260 || len(source.Columns) != 3 {
		t.Fatal("view identity", source)
	}
	for i, name := range []string{"grantee", "role_name", "is_grantable"} {
		column := source.Columns[i]
		domainID, baseID, baseTypmod := sqltype.ID(13248), sqltype.Name, int32(-1)
		if i == 2 {
			domainID, baseID, baseTypmod = 13256, sqltype.VarChar, 7
		}
		domain, _ := view.LookupType(domainID)
		base, _ := view.LookupType(baseID)
		if column.Name != name || !column.Nullable || !column.NullabilityKnown || column.OriginTable != 16257 || column.OriginAttribute != int16(i+1) || column.Usage.Type != domain || domain.BaseType() != base || domain.DomainBase().Typmod != baseTypmod || column.Usage.Typmod != -1 || column.Usage.Collation.ID != 950 || column.DeclaredDimensions != 0 {
			t.Fatal("column contract", column)
		}
	}
	acl := source.Security.ACL
	if !acl.Explicit || len(acl.Entries) != 1 || !acl.Entries[0].Grantee.Public || acl.Entries[0].Privileges != authorization.SelectPrivilege {
		t.Fatal("PUBLIC SELECT", acl)
	}
	row, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13260})
	if !ok || row.OwningRelation != source.Address() || row.Array != 13259 {
		t.Fatal("owned row type", row)
	}
	array, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13259})
	if !ok || array.OwningRelation != source.Address() || array.SQLType.Element() != row.SQLType {
		t.Fatal("owned array type", array)
	}
	applicableRolesUnitRows(t, applicableRolesUnitInput(t, f, view, f.member), [3]string{"default_member", "default_owner", "NO"})
}

func TestInfoApplicableRolesSemantics(t *testing.T) {
	f := newDefaultUnitFixture(t)
	original := f.e.RoleStore.Published
	bootstrap := fixtureRole(original, f.e.Roles().LookupRef("postgres"))
	dbOwner := fixtureRole(original, f.e.Roles().LookupRef("pg_database_owner"))
	roles := emptyRoleFixture(f.e)
	for _, role := range []authorization.RoleDescriptor{bootstrap, dbOwner, fixtureRole(original, f.owner), fixtureRole(original, f.reader), fixtureRole(original, f.member)} {
		putFixtureRole(roles, role)
	}
	wheel := f.e.RoleStore.Domain.Principal(21101)
	bridge := f.e.RoleStore.Domain.Principal(21102)
	adminOnly := f.e.RoleStore.Domain.Principal(21103)
	hidden := f.e.RoleStore.Domain.Principal(21104)
	secret := f.e.RoleStore.Domain.Principal(21105)
	leaf := f.e.RoleStore.Domain.Principal(21106)
	for ref, name := range map[authorization.PrincipalRef]string{wheel: "wheel", bridge: "bridge", adminOnly: "adminonly", hidden: "hidden", secret: "secret", leaf: "leaf"} {
		putFixtureRole(roles, authorization.RoleDescriptor{Ref: ref, Name: name, Inherit: true})
	}
	edges := []authorization.Membership{
		{Role: f.owner, Member: f.reader, Grantor: bootstrap.Ref, Inherit: true, Set: true},
		{Role: wheel, Member: f.owner, Grantor: bootstrap.Ref, Set: true},
		{Role: adminOnly, Member: f.reader, Grantor: bootstrap.Ref, Admin: true},
		{Role: secret, Member: hidden, Grantor: bootstrap.Ref, Inherit: true},
		{Role: leaf, Member: wheel, Grantor: bootstrap.Ref},
	}
	setFixtureEdges(roles, edges)
	f.e.RoleStore.PublishLocked(roles)
	view := f.e.CatalogRead()
	input := func(actor authorization.PrincipalRef) pgcatalog.Input {
		return applicableRolesUnitInput(t, f, view, actor)
	}
	initial := [][3]string{{"default_reader", "default_owner", "NO"}, {"default_owner", "wheel", "NO"}, {"default_reader", "adminonly", "YES"}}
	retainedInput := input(f.reader)
	retained := applicableRolesUnitRows(t, retainedInput, initial...)
	oldBag := map[[3]string]int{
		{"default_reader", "default_owner", "NO"}: 1,
		{"default_owner", "wheel", "NO"}:          1,
		{"default_reader", "adminonly", "YES"}:    1,
	}
	applicableRolesUnitRows(t, input(f.member)) // No synthetic self/PUBLIC or closure edges.
	applicableRolesUnitRows(t, input(f.owner), [3]string{"default_owner", "wheel", "NO"})
	applicableRolesUnitRows(t, input(wheel), [3]string{"wheel", "leaf", "NO"})
	applicableRolesUnitRows(t, input(bootstrap.Ref),
		[3]string{"default_reader", "default_owner", "NO"}, [3]string{"default_owner", "wheel", "NO"},
		[3]string{"default_reader", "adminonly", "YES"}, [3]string{"hidden", "secret", "NO"},
		[3]string{"wheel", "leaf", "NO"}, [3]string{"postgres", "pg_database_owner", "NO"})
	firstOwned := catalog.CloneState(view.State())
	firstDatabase, _ := view.Database(catalog.CurrentDatabaseAddress)
	firstOwner := *firstDatabase
	firstOwner.Owner = f.owner
	firstOwned.SetDescriptor(firstOwner.Address(), &firstOwner)
	firstOwnedView := f.e.Catalog.Read(firstOwned)
	applicableRolesUnitRows(t, applicableRolesUnitInput(t, f, firstOwnedView, f.owner),
		[3]string{"default_owner", "wheel", "NO"}, [3]string{"default_owner", "pg_database_owner", "NO"})
	applicableRolesUnitRows(t, applicableRolesUnitInput(t, f, firstOwnedView, f.reader),
		[3]string{"default_reader", "default_owner", "NO"}, [3]string{"default_owner", "wheel", "NO"},
		[3]string{"default_reader", "adminonly", "YES"}, [3]string{"default_owner", "pg_database_owner", "NO"})

	changed := cloneRoleFixture(roles)
	edges = append(edges,
		authorization.Membership{Role: bridge, Member: f.reader, Grantor: bootstrap.Ref, Inherit: true},
		authorization.Membership{Role: wheel, Member: bridge, Grantor: bootstrap.Ref, Inherit: true},
		authorization.Membership{Role: wheel, Member: f.reader, Grantor: bootstrap.Ref, Inherit: true})
	setFixtureEdges(changed, edges)
	reader := fixtureRole(changed, f.reader)
	reader.Inherit = false
	putFixtureRole(changed, reader)
	f.e.RoleStore.PublishLocked(changed)
	alternate := [][3]string{
		{"default_reader", "default_owner", "NO"}, {"default_owner", "wheel", "NO"}, {"default_reader", "adminonly", "YES"},
		{"default_reader", "bridge", "NO"}, {"bridge", "wheel", "NO"}, {"default_reader", "wheel", "NO"}, {"wheel", "leaf", "NO"},
	}
	applicableRolesUnitRows(t, input(f.reader), alternate...)
	// An inherited SUPERUSER descriptor does not expose the hidden grantee.
	power := fixtureRole(changed, f.owner)
	power.Superuser = true
	changed = cloneRoleFixture(changed)
	putFixtureRole(changed, power)
	f.e.RoleStore.PublishLocked(changed)
	applicableRolesUnitRows(t, input(f.reader), alternate...)
	// Equal triples from distinct grantors collapse; ADMIN is part of the key.
	edges = append(edges, authorization.Membership{Role: wheel, Member: f.reader, Grantor: f.member, Inherit: true})
	changed = cloneRoleFixture(changed)
	setFixtureEdges(changed, edges)
	f.e.RoleStore.PublishLocked(changed)
	applicableRolesUnitRows(t, input(f.reader), alternate...)
	edges[len(edges)-1].Admin = true
	changed = cloneRoleFixture(changed)
	setFixtureEdges(changed, edges)
	f.e.RoleStore.PublishLocked(changed)
	withAdmin := append(append([][3]string(nil), alternate...), [3]string{"default_reader", "wheel", "YES"})
	applicableRolesUnitRows(t, input(f.reader), withAdmin...)

	// Rename/drop/recreate publish new identities without adopting retired edges.
	renamed := cloneRoleFixture(changed)
	power.Name = "renamed_owner"
	putFixtureRole(renamed, power)
	retireFixtureRole(renamed, adminOnly)
	newRef := f.e.RoleStore.Domain.Principal(21107)
	putFixtureRole(renamed, authorization.RoleDescriptor{Ref: newRef, Name: "adminonly", Inherit: true})
	f.e.RoleStore.PublishLocked(renamed)
	applicableRolesUnitRows(t, input(f.reader),
		[3]string{"default_reader", "renamed_owner", "NO"}, [3]string{"renamed_owner", "wheel", "NO"},
		[3]string{"default_reader", "bridge", "NO"}, [3]string{"bridge", "wheel", "NO"},
		[3]string{"default_reader", "wheel", "NO"}, [3]string{"default_reader", "wheel", "YES"}, [3]string{"wheel", "leaf", "NO"})
	applicableRolesUnitRows(t, input(newRef))
	if got := applicableRolesUnitBag(t, retained, retainedInput.Source); !reflect.DeepEqual(got, oldBag) {
		t.Fatal("retained names/options/carriers changed", got, oldBag)
	}
	applicableRolesUnitRows(t, retainedInput, initial...)

	// One paired database authority supplies the implicit owner grantee.
	state := catalog.CloneState(view.State())
	database, _ := view.Database(catalog.CurrentDatabaseAddress)
	owned := *database
	owned.Owner = f.owner
	state.SetDescriptor(owned.Address(), &owned)
	ownedView := f.e.Catalog.Read(state)
	ownedInput := applicableRolesUnitInput(t, f, ownedView, f.reader)
	applicableRolesUnitRows(t, ownedInput,
		[3]string{"default_reader", "renamed_owner", "NO"}, [3]string{"renamed_owner", "wheel", "NO"},
		[3]string{"default_reader", "bridge", "NO"}, [3]string{"bridge", "wheel", "NO"},
		[3]string{"default_reader", "wheel", "NO"}, [3]string{"default_reader", "wheel", "YES"}, [3]string{"wheel", "leaf", "NO"},
		[3]string{"renamed_owner", "pg_database_owner", "NO"})
	applicableRolesUnitRows(t, applicableRolesUnitInput(t, f, ownedView, f.member))
	applicableRolesUnitRows(t, applicableRolesUnitInput(t, f, ownedView, bootstrap.Ref),
		[3]string{"default_reader", "renamed_owner", "NO"}, [3]string{"renamed_owner", "wheel", "NO"},
		[3]string{"default_reader", "bridge", "NO"}, [3]string{"bridge", "wheel", "NO"},
		[3]string{"default_reader", "wheel", "NO"}, [3]string{"default_reader", "wheel", "YES"}, [3]string{"wheel", "leaf", "NO"},
		[3]string{"hidden", "secret", "NO"}, [3]string{"renamed_owner", "pg_database_owner", "NO"})
	// The supported graph explicitly forbids an equal explicit implicit edge.
	request := &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: "pg_database_owner"}}, Members: []parser.RoleSpec{{Name: "renamed_owner"}}}
	_, changedGraph, _, err := authorization.MembershipCandidate(t.Context(), ownedInput.Roles, authorization.RoleState{CurrentUser: bootstrap.Ref, SessionUser: bootstrap.Ref}, request, nil, false, false)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != `role "pg_database_owner" cannot have explicit members` || changedGraph {
		t.Fatal("explicit owner membership rejection", changedGraph, err)
	}
	applicableRolesUnitRows(t, ownedInput,
		[3]string{"default_reader", "renamed_owner", "NO"}, [3]string{"renamed_owner", "wheel", "NO"},
		[3]string{"default_reader", "bridge", "NO"}, [3]string{"bridge", "wheel", "NO"},
		[3]string{"default_reader", "wheel", "NO"}, [3]string{"default_reader", "wheel", "YES"}, [3]string{"wheel", "leaf", "NO"},
		[3]string{"renamed_owner", "pg_database_owner", "NO"})
	applicableRolesUnitRows(t, applicableRolesUnitInput(t, f, ownedView, wheel), [3]string{"wheel", "leaf", "NO"})
	// Reassigning the database owner does not rewrite the retained paired input.
	reassigned := catalog.CloneState(ownedView.State())
	nextOwner := owned
	nextOwner.Owner = f.member
	reassigned.SetDescriptor(nextOwner.Address(), &nextOwner)
	applicableRolesUnitRows(t, applicableRolesUnitInput(t, f, f.e.Catalog.Read(reassigned), f.member), [3]string{"default_member", "pg_database_owner", "NO"})
	applicableRolesUnitRows(t, applicableRolesUnitInput(t, f, ownedView, f.member))
}
