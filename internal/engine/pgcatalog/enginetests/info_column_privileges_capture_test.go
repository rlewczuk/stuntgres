package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestInfoColumnPrivilegesCaptureOwnership(t *testing.T) {
	f := newRoleColumnGrantsFixture(t)
	relation := *f.relation
	relation.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.ColumnGrantPrivileges, 0)}}
	f.state.SetDescriptor(relation.Address(), &relation)
	in := f.columnInput(t, f.state, f.f.reader)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoColumnPrivilegesOID)
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	bad := in
	bad.Context = canceled
	if rows, err := produce(bad); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancel", rows, err)
	}
	for _, remaining := range []int{4, 8, 20, 100} {
		bad := in
		bad.Context = &dtpCancelContext{Context: context.Background(), remaining: remaining}
		if rows, err := produce(bad); rows != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("mid-capture exposed prefix", remaining, rows, err)
		}
		if captured, err := catalogrt.CaptureRows(bad, produce); captured != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("cancellation published a source", captured, err)
		}
	}
	foreign := engine.New()
	for _, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &foreign.Catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref = foreign.Catalog.Owner.Domain.Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) { copy := *in.Source; copy.ObjectAddress.ObjectID++; in.Source = &copy },
		func(in *pgcatalog.Input) {
			runtime := core.CatalogRuntimeFieldsOf(in.Runtime)
			runtime.Auth.RoleRecord = nil
			in.Runtime = core.NewCatalogRuntime(runtime)
		},
		func(in *pgcatalog.Input) {
			runtime := core.CatalogRuntimeFieldsOf(in.Runtime)
			runtime.Auth.State.CurrentUser = foreign.Roles().LookupRef("postgres")
			in.Runtime = core.NewCatalogRuntime(runtime)
		},
		func(in *pgcatalog.Input) {
			runtime := core.CatalogRuntimeFieldsOf(in.Runtime)
			runtime.Auth.State.CurrentUser = f.f.e.RoleStore.Domain.Principal(999999)
			in.Runtime = core.NewCatalogRuntime(runtime)
		},
		func(in *pgcatalog.Input) {
			other := f.input(t, f.state, f.f.reader)
			in.Ref, in.Source = other.Ref, other.Source
		},
	} {
		bad := in
		mutate(&bad)
		if rows, err := produce(bad); rows != nil || err == nil {
			t.Fatal("invalid capture accepted", rows, err)
		} else {
			var state interface{ SQLState() string }
			if errors.As(err, &state) {
				t.Fatal("invariant error acquired SQLSTATE", err)
			}
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
			bad := f.columnInput(t, state, f.f.reader)
			if rows, err := produce(bad); rows != nil || err == nil {
				t.Fatal("malformed metadata accepted", column, fault, rows, err)
			}
		}
	}
	// A legitimate captured source with mismatched paired authority is rejected.
	roles := cloneRoleFixture(f.f.e.RoleStore.Published)
	reader := fixtureRole(roles, f.f.reader)
	reader.Name = "next_reader"
	putFixtureRole(roles, reader)
	f.f.e.RoleStore.PublishLocked(roles)
	fresh := f.columnInput(t, f.state, f.f.reader)
	bad = in
	bad.Roles = fresh.Roles
	if rows, err := produce(bad); rows != nil || err == nil {
		t.Fatal("mixed role generations accepted", rows, err)
	}
}

func TestInfoColumnPrivilegesFrontier(t *testing.T) {
	f := newRoleColumnGrantsFixture(t)
	relation := *f.relation
	relation.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}}}
	f.state.SetDescriptor(relation.Address(), &relation)
	old := f.columnInput(t, f.state, f.f.reader)
	oldAuth := core.CatalogRuntimeFieldsOf(old.Runtime).Auth
	oldMember := f.columnInput(t, f.state, f.f.member)
	produce, _ := pgcatalog.ProducerFor(pgcatalog.InfoColumnPrivilegesOID)
	retained, err := catalogrt.CaptureRows(old, produce)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	want := map[[8]string]int{roleColumnGrantsWant("default_owner", "default_reader", "a", "SELECT", "NO"): 1}
	if got := roleColumnGrantsBag(t, retained.Anonymous); !reflect.DeepEqual(got, want) {
		t.Fatal("initial frontier", got, want)
	}
	backing := columnPrivilegeUnitRow(t, retained.Anonymous)
	first := &backing[0]
	oldState, oldSource, oldRef, oldRoles := old.Catalog.State(), old.Source, old.Ref, old.Roles
	next := catalog.CloneState(f.state)
	changed := relation
	changed.Columns = append([]catalog.Column(nil), relation.Columns...)
	changed.Columns[0].Name = "renamed_attribute"
	changed.ObjectRevision++
	changed.Security.ColumnACL = map[int32]authorization.ACLValue{1: {Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, authorization.SelectPrivilege)}}}
	next.SetDescriptor(changed.Address(), &changed)
	roles := cloneRoleFixture(f.f.e.RoleStore.Published)
	reader := fixtureRole(roles, f.f.reader)
	reader.Name = "renamed_reader"
	putFixtureRole(roles, reader)
	owner := fixtureRole(roles, f.f.owner)
	owner.Name = "renamed_owner"
	putFixtureRole(roles, owner)
	// Remove the fixture's automatic owner membership before capturing denial.
	setFixtureEdges(roles, nil)
	f.f.e.RoleStore.PublishLocked(roles)
	f.f.e.Catalog.PublishLocked(next)
	fresh := f.columnInput(t, next, f.f.reader)
	freshRows := columnPrivilegesRows(t, fresh)
	freshWant := map[[8]string]int{roleColumnGrantsWant("renamed_owner", "renamed_reader", "renamed_attribute", "SELECT", "YES"): 1}
	if got := roleColumnGrantsBag(t, freshRows); !reflect.DeepEqual(got, freshWant) {
		t.Fatal("fresh frontier", got, freshWant)
	}
	if old.Catalog.State() != oldState || old.Catalog.State() == fresh.Catalog.State() || old.Source != oldSource || old.Ref != oldRef || old.Roles != oldRoles || old.Roles == fresh.Roles || oldAuth.Catalog != old.Catalog || oldAuth.Roles != old.Roles || oldAuth.RoleRecord == core.CatalogRuntimeFieldsOf(fresh.Runtime).Auth.RoleRecord {
		t.Fatal("old authority not retained independently from fresh publication")
	}
	if old.Catalog.Get(relation.Address()) != &relation || fresh.Catalog.Get(relation.Address()) != &changed {
		t.Fatal("captured catalog backing changed")
	}
	if retained.Ref != oldRef || retained.View != nil || &columnPrivilegeUnitRow(t, retained.Anonymous)[0] != first || &columnPrivilegeUnitRow(t, freshRows)[0] == first {
		t.Fatal("retained source backing changed or shared mutable fresh output")
	}
	if got := roleColumnGrantsBag(t, columnPrivilegesRows(t, old)); !reflect.DeepEqual(got, want) {
		t.Fatal("old input recaptured live authority", got, want)
	}
	denied := f.columnInput(t, next, f.f.member)
	if got := roleColumnGrantsBag(t, columnPrivilegesRows(t, denied)); len(got) != 0 {
		t.Fatal("unrelated member visible before publication", got)
	}
	membership := cloneRoleFixture(roles)
	setFixtureEdges(membership, []authorization.Membership{{Role: f.f.reader, Member: f.f.member, Grantor: f.f.e.Roles().LookupRef("postgres"), Inherit: true}})
	f.f.e.RoleStore.PublishLocked(membership)
	f.columnCheck(t, next, f.f.member, roleColumnGrantsWant("renamed_owner", "renamed_reader", "renamed_attribute", "SELECT", "YES"))
	if got := roleColumnGrantsBag(t, columnPrivilegesRows(t, denied)); len(got) != 0 {
		t.Fatal("membership changed previously denied authority", got)
	}
	// The original member frontier still sees the original grantor/column names.
	if got := roleColumnGrantsBag(t, columnPrivilegesRows(t, oldMember)); !reflect.DeepEqual(got, want) {
		t.Fatal("membership changed original frontier", got, want)
	}
	if got := roleColumnGrantsBag(t, retained.Anonymous); !reflect.DeepEqual(got, want) || &columnPrivilegeUnitRow(t, retained.Anonymous)[0] != first {
		t.Fatal("retained values or backing changed", got, want)
	}
	for i, cell := range backing {
		if cell.Type() != oldSource.Columns[i].Usage.Type || cell.IsNull() || !cell.ValidResult(oldSource.Columns[i].Usage) {
			t.Fatal("retained domain authority", i, cell)
		}
	}
}

func columnPrivilegeUnitRow(t *testing.T, rows [][]sqltype.Value) []sqltype.Value {
	t.Helper()
	for _, row := range rows {
		base, err := row[4].BaseValue()
		if err != nil {
			t.Fatal(err)
		}
		name, err := base.Text()
		if err != nil {
			t.Fatal(err)
		}
		if name == "rcg_unit" {
			return row
		}
	}
	t.Fatal("missing unit relation row")
	return nil
}
