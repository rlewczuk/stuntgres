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
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/sqltype"
)

func TestInfoTablePrivilegesCaptureOwnershipAndCancellation(t *testing.T) {
	f := newTablePrivilegesFixture(t)
	relation := *f.relation
	relation.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.TableGrantPrivileges, 0)}}
	f.state.SetDescriptor(relation.Address(), &relation)
	in := f.input(t, f.state, f.f.reader)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.TablePrivilegesOID)
	if !ok {
		t.Fatal("missing producer")
	}
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
	// Count the deterministic Err checkpoints, then interrupt the final one:
	// even cancellation after otherwise complete emission must discard all rows.
	probe := &dtpCancelContext{Context: context.Background(), remaining: 1000000}
	bad = in
	bad.Context = probe
	if rows, err := produce(bad); rows == nil || err != nil {
		t.Fatal("checkpoint probe", rows, err)
	}
	bad.Context = &dtpCancelContext{Context: context.Background(), remaining: 1000000 - probe.remaining}
	if rows, err := produce(bad); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("late cancellation exposed prefix", rows, err)
	}
	foreign := engine.New()
	for i, mutate := range []func(*pgcatalog.Input){
		func(in *pgcatalog.Input) { in.Owner = &foreign.Catalog.Owner },
		func(in *pgcatalog.Input) { in.Catalog = foreign.CatalogRead() },
		func(in *pgcatalog.Input) { in.Roles = foreign.Roles() },
		func(in *pgcatalog.Input) { in.Ref.Epoch++ },
		func(in *pgcatalog.Input) { in.Ref = foreign.Catalog.Owner.Domain.Ref(in.Ref.Epoch, in.Ref.Address) },
		func(in *pgcatalog.Input) { in.Ref.Address.SubID = 1 },
		func(in *pgcatalog.Input) { copy := *in.Source; in.Source = &copy },
		func(in *pgcatalog.Input) { copy := *in.Source; copy.ObjectAddress.ObjectID++; in.Source = &copy },
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(core.EngineCatalogRuntime)
			runtime.Auth.RoleRecord = nil
			in.Runtime = runtime
		},
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(core.EngineCatalogRuntime)
			runtime.Auth.RoleRecord = foreign.RoleStore.Published
			in.Runtime = runtime
		},
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(core.EngineCatalogRuntime)
			runtime.Auth.PairedCatalog = foreign.CatalogRead()
			in.Runtime = runtime
		},
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(core.EngineCatalogRuntime)
			runtime.Auth.State.CurrentUser = foreign.Roles().LookupRef("postgres")
			in.Runtime = runtime
		},
		func(in *pgcatalog.Input) {
			runtime := in.Runtime.(core.EngineCatalogRuntime)
			runtime.Auth.State.CurrentUser = f.f.e.RoleStore.Domain.Principal(999999)
			in.Runtime = runtime
		},
	} {
		bad := in
		mutate(&bad)
		rows, err := produce(bad)
		if rows != nil || err == nil {
			t.Fatalf("invalid capture fault %d accepted: rows=%d nonnil=%t err=%v", i, len(rows), rows != nil, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("invariant error acquired SQLSTATE", err)
		}
	}
	for _, column := range []int{0, 5, 6, 7} {
		for _, fault := range []string{"width", "base", "wrong_domain", "foreign_domain", "missing_domain", "missing_base", "foreign_base"} {
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
			case "missing_domain":
				state.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.Columns[column].Usage.Type.ID())})
			case "missing_base":
				state.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.Columns[column].Usage.Type.BaseType().ID())})
			case "foreign_base":
				baseID := source.Columns[column].Usage.Type.BaseType().ID()
				address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(baseID)}
				base := *in.Catalog.Get(address).(*catalog.Type)
				base.SQLType, _ = foreign.CatalogRead().LookupType(sqltype.ID(baseID))
				state.SetDescriptor(address, &base)
			}
			state.SetDescriptor(source.Address(), &source)
			bad := f.input(t, state, f.f.reader)
			if rows, err := produce(bad); rows != nil || err == nil {
				t.Fatal("malformed metadata accepted", column, fault, rows, err)
			}
		}
	}
	for _, fault := range []string{"owner", "grantor", "grantee", "namespace"} {
		state := catalog.CloneState(f.state)
		broken := relation
		entry := defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)
		missing := f.f.e.RoleStore.Domain.Principal(999999)
		switch fault {
		case "owner":
			broken.Owner = missing
		case "grantor":
			entry.Grantor = missing
		case "grantee":
			entry.Grantee.Role = missing
		case "namespace":
			broken.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 999999}
		}
		broken.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}
		state.SetDescriptor(broken.Address(), &broken)
		if rows, err := produce(f.input(t, state, f.f.reader)); rows != nil || err == nil {
			t.Fatal("missing authority accepted", fault, rows, err)
		}
	}
}

func TestInfoTablePrivilegesFrontier(t *testing.T) {
	f := newTablePrivilegesFixture(t)
	relation := *f.relation
	relation.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, 0)}}
	f.state.SetDescriptor(relation.Address(), &relation)
	old := f.input(t, f.state, f.f.reader)
	retained := tablePrivilegesRows(t, old)
	want := map[[8]string]int{tablePrivilegesWant("default_owner", "default_reader", "SELECT", "NO"): 1}
	if got := tablePrivilegesBag(t, retained); !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	next := catalog.CloneState(f.state)
	changed := relation
	changed.ObjectName = "tp_renamed"
	changed.Security.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.SelectPrivilege, authorization.SelectPrivilege)}}
	next.SetDescriptor(changed.Address(), &changed)
	roles := cloneRoleFixture(f.f.e.RoleStore.Published)
	reader := fixtureRole(roles, f.f.reader)
	reader.Name = "renamed_reader"
	putFixtureRole(roles, reader)
	owner := fixtureRole(roles, f.f.owner)
	owner.Name = "renamed_owner"
	putFixtureRole(roles, owner)
	setFixtureEdges(roles, append(fixtureEdges(roles), authorization.Membership{Role: f.f.reader, Member: f.f.member, Grantor: f.f.e.Roles().LookupRef("postgres"), Inherit: true}))
	f.f.e.RoleStore.PublishLocked(roles)
	f.f.e.Catalog.PublishLocked(next)
	fresh := f.input(t, next, f.f.member)
	var matched int
	for _, row := range tablePrivilegesRows(t, fresh) {
		base, err := row[4].BaseValue()
		if err != nil {
			t.Fatal(err)
		}
		name, err := base.Text()
		if err != nil {
			t.Fatal(err)
		}
		if name != "tp_renamed" {
			continue
		}
		expected := tablePrivilegesWant("renamed_owner", "renamed_reader", "SELECT", "YES")
		expected[4] = "tp_renamed"
		for i, cell := range row {
			base, err := cell.BaseValue()
			if err != nil {
				t.Fatal(err)
			}
			text, err := base.Text()
			if err != nil || text != expected[i] {
				t.Fatal("fresh frontier", i, text, err, expected)
			}
		}
		matched++
	}
	if matched != 1 {
		t.Fatal("fresh origin count", matched)
	}
	if got := tablePrivilegesBag(t, tablePrivilegesRows(t, old)); !reflect.DeepEqual(got, want) {
		t.Fatal("recaptured live authority", got, want)
	}
	if got := tablePrivilegesBag(t, retained); !reflect.DeepEqual(got, want) {
		t.Fatal("retained values changed", got, want)
	}
	for _, row := range retained {
		for i, cell := range row {
			if cell.Type() != old.Source.Columns[i].Usage.Type || cell.Type().BaseType() != old.Source.Columns[i].Usage.Type.BaseType() {
				t.Fatal("retained domain", i, cell)
			}
		}
	}
}
