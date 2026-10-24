package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestInfoRoleColumnGrantsCaptureOwnership(t *testing.T) {
	f := newRoleColumnGrantsFixture(t)
	relation := *f.relation
	relation.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.ColumnGrantPrivileges, 0)}}
	f.state.SetDescriptor(relation.Address(), &relation)
	in := f.input(t, f.state, f.f.reader)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoRoleColumnGrantsOID)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	bad := in
	bad.Context = canceled
	if rows, err := produce(bad); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancel", rows, err)
	}
	for _, remaining := range []int{4, 8, 20} {
		bad := in
		bad.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(bad); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("mid-scan cancellation exposed prefix", rows, err)
		}
	}
	foreign := New()
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &foreign.catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref = foreign.catalog.Owner.Domain.Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) { copy := *in.Source; copy.ObjectAddress.ObjectID++; in.Source = &copy },
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(engineCatalogRuntime)
			runtime.auth.RoleRecord = nil
			in.Runtime = runtime
		},
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(engineCatalogRuntime)
			runtime.auth.State.CurrentUser = foreign.Roles().LookupRef("postgres")
			in.Runtime = runtime
		},
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(engineCatalogRuntime)
			runtime.auth.State.CurrentUser = f.f.e.roles.domain.Principal(999999)
			in.Runtime = runtime
		},
	} {
		bad := in
		mutate(&bad)
		if rows, err := produce(bad); rows != nil || err == nil {
			t.Fatal("invalid capture accepted", rows, err)
		}
	}
	for _, column := range []int{0, 6, 7} {
		for _, fault := range []string{"width", "base", "wrong_domain", "foreign_domain"} {
			state := catalog.CloneState(f.state)
			source := *in.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			switch fault {
			case "width":
				source.Columns = source.Columns[:7]
			case "base":
				source.Columns[column].Usage.Type = source.Columns[column].Usage.Type.BaseType()
			case "wrong_domain":
				source.Columns[column].Usage.Type, _ = in.Catalog.LookupType(13243)
			case "foreign_domain":
				source.Columns[column].Usage.Type, _ = foreign.CatalogRead().LookupType(source.Columns[column].Usage.Type.ID())
			}
			state.SetDescriptor(source.Address(), &source)
			bad := f.input(t, state, f.f.reader)
			if rows, err := produce(bad); rows != nil || err == nil {
				t.Fatal("malformed metadata accepted", column, fault, rows, err)
			}
		}
	}
	// No SQLSTATE is invented for ordinary cancellation/invariant errors.
	if _, err := produce(bad); err != nil {
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("invariant error acquired SQLSTATE", err)
		}
	}
}

func TestInfoRoleColumnGrantsFrontier(t *testing.T) {
	f := newRoleColumnGrantsFixture(t)
	relation := *f.relation
	relation.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}}}
	f.state.SetDescriptor(relation.Address(), &relation)
	old := f.input(t, f.state, f.f.reader)
	retained := roleColumnGrantsRows(t, old)
	before := roleColumnGrantsBag(t, retained)
	want := map[[8]string]int{roleColumnGrantsWant("default_owner", "default_reader", "a", "SELECT", "NO"): 1}
	if !reflect.DeepEqual(before, want) {
		t.Fatal(before, want)
	}
	next := catalog.CloneState(f.state)
	changed := relation
	changed.Columns = append([]catalog.Column(nil), relation.Columns...)
	changed.Columns[0].Name = "renamed_attribute"
	changed.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, authorization.SelectPrivilege)}}}
	next.SetDescriptor(changed.Address(), &changed)
	roles := cloneRoleFixture(f.f.e.roles.published)
	reader := fixtureRole(roles, f.f.reader)
	reader.Name = "renamed_reader"
	putFixtureRole(roles, reader)
	owner := fixtureRole(roles, f.f.owner)
	owner.Name = "renamed_owner"
	putFixtureRole(roles, owner)
	f.f.e.roles.published = roles
	f.f.e.catalog.PublishLocked(next)
	fresh := roleColumnGrantsBag(t, roleColumnGrantsRows(t, f.input(t, next, f.f.reader)))
	freshWant := map[[8]string]int{roleColumnGrantsWant("renamed_owner", "renamed_reader", "renamed_attribute", "SELECT", "YES"): 1}
	if !reflect.DeepEqual(fresh, freshWant) {
		t.Fatal("fresh frontier", fresh, freshWant)
	}
	if got := roleColumnGrantsBag(t, roleColumnGrantsRows(t, old)); !reflect.DeepEqual(got, want) {
		t.Fatal("recaptured live authority", got, want)
	}
	if got := roleColumnGrantsBag(t, retained); !reflect.DeepEqual(got, want) {
		t.Fatal("retained values changed", got, want)
	}
	// A membership-only publication changes fresh visibility, never old authority.
	membership := cloneRoleFixture(roles)
	setFixtureEdges(membership, append(fixtureEdges(membership), authorization.Membership{Role: f.f.reader, Member: f.f.member, Grantor: f.f.e.Roles().LookupRef("postgres"), Inherit: true}))
	f.f.e.roles.published = membership
	f.check(t, next, f.f.member, roleColumnGrantsWant("renamed_owner", "renamed_reader", "renamed_attribute", "SELECT", "YES"))
	if got := roleColumnGrantsBag(t, roleColumnGrantsRows(t, old)); !reflect.DeepEqual(got, want) {
		t.Fatal("membership changed old frontier", got, want)
	}
	// Domain output remains attached to the original source after publication.
	for _, row := range retained {
		for i, cell := range row {
			if cell.Type() != old.Source.Columns[i].Usage.Type || cell.IsNull() || cell.Type().BaseType() == nil || cell.Type().BaseType().ID() != []sqltype.ID{sqltype.Name, sqltype.Name, sqltype.Name, sqltype.Name, sqltype.Name, sqltype.Name, sqltype.VarChar, sqltype.VarChar}[i] {
				t.Fatal("retained domain", cell)
			}
		}
	}
}
