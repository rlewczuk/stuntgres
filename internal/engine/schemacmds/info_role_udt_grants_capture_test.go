package schemacmds_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestInfoRoleUdtGrantsCapture(t *testing.T) {
	f := newRoleUdtGrantsFixture(t)
	udt := *f.udt
	udt.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.UsagePrivilege, 0)}}
	f.state.SetDescriptor(udt.Address(), &udt)
	in := f.input(t, f.state, f.f.reader)
	produce, ok := pgcatalog.ProducerFor(pgcatalog.InfoRoleUdtGrantsOID)
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
			t.Fatal("invalid capture accepted", rows, err)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("invariant error acquired SQLSTATE", err)
		}
	}
	for _, column := range []int{0, 5, 6} {
		for _, fault := range []string{"width", "base", "wrong_domain", "foreign_domain"} {
			state := catalog.CloneState(f.state)
			source := *in.Source
			source.Columns = append([]catalog.Column(nil), source.Columns...)
			switch fault {
			case "width":
				source.Columns = source.Columns[:6]
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
	for _, fault := range []string{"namespace", "owner", "grantor", "grantee"} {
		state := catalog.CloneState(f.state)
		broken := udt
		entry := defaultUnitReaderEntry(f.f, authorization.UsagePrivilege, 0)
		missing := f.f.e.RoleStore.Domain.Principal(999999)
		switch fault {
		case "namespace":
			broken.ObjectNamespace.ObjectID = 999999
		case "owner":
			broken.Owner = missing
		case "grantor":
			entry.Grantor = missing
		case "grantee":
			entry.Grantee.Role = missing
		}
		broken.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{entry}}
		state.SetDescriptor(broken.Address(), &broken)
		if rows, err := produce(f.input(t, state, f.f.reader)); rows != nil || err == nil {
			t.Fatal("missing authority accepted", fault, rows, err)
		}
	}
}

func TestInfoRoleUdtGrantsCaptureFrontier(t *testing.T) {
	f := newRoleUdtGrantsFixture(t)
	udt := *f.udt
	udt.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.UsagePrivilege, 0)}}
	f.state.SetDescriptor(udt.Address(), &udt)
	old := f.input(t, f.state, f.f.reader)
	retained := roleUdtGrantsRows(t, old)
	want := map[[7]string]int{roleUdtGrantsWant("default_owner", "default_reader", "TYPE USAGE", "NO"): 1}
	if got := roleUdtGrantsBag(t, retained, "ug_unit"); !reflect.DeepEqual(got, want) {
		t.Fatal(got, want)
	}
	next := catalog.CloneState(f.state)
	changed := udt
	members := make([]sqltype.CompositeMember, udt.SQLType.CompositeLen())
	for i := range members {
		members[i] = udt.SQLType.CompositeMember(i)
	}
	var err error
	changed.SQLType, err = sqltype.Composite(udt.SQLType.ID(), "ug_renamed", "default_schema.ug_renamed", udt.Array, members)
	if err != nil {
		t.Fatal(err)
	}
	changed.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{defaultUnitReaderEntry(f.f, authorization.UsagePrivilege, authorization.UsagePrivilege)}}
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
	for _, row := range roleUdtGrantsRows(t, fresh) {
		base, err := row[4].BaseValue()
		if err != nil {
			t.Fatal(err)
		}
		name, err := base.Text()
		if err != nil {
			t.Fatal(err)
		}
		if name != "ug_renamed" {
			continue
		}
		expected := roleUdtGrantsWant("renamed_owner", "renamed_reader", "TYPE USAGE", "YES")
		expected[4] = "ug_renamed"
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
	if got := roleUdtGrantsBag(t, roleUdtGrantsRows(t, old), "ug_unit"); !reflect.DeepEqual(got, want) {
		t.Fatal("recaptured live authority", got, want)
	}
	if got := roleUdtGrantsBag(t, retained, "ug_unit"); !reflect.DeepEqual(got, want) {
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

func defaultUnitReaderEntry(f defaultUnitFixture, privileges, options authorization.Privileges) authorization.ACLEntry {
	return authorization.ACLEntry{Grantor: f.owner, Grantee: authorization.ACLGrantee{Role: f.reader}, Privileges: privileges, GrantOptions: options}
}

// A deterministic cancellation point inside enumeration, without timing races.
type dtpCancelContext struct {
	context.Context
	remaining int
}

func (c *dtpCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func fixtureRole(r *access.RoleRecord, ref authorization.PrincipalRef) authorization.RoleDescriptor {
	d, _ := r.Root.Read(authorization.PrincipalRef{}).Find(ref)
	return d
}

// Build private graph fixtures through genuine storage and membership candidates.
// Temporary descriptor names/flags exist only on unpublished roots and are restored.
func setFixtureEdges(r *access.RoleRecord, edges []authorization.Membership) {
	candidate := *r
	destination := r
	r = &candidate
	read := r.Root.Read(authorization.PrincipalRef{})
	origins := make(map[authorization.PrincipalRef]bool)
	for edge := range read.Edges() {
		origins[edge.Grantor] = true
	}
	if len(origins) != 0 {
		root, err := authorization.OwnedMembershipCandidate(context.Background(), read, origins)
		if err != nil {
			panic(err)
		}
		r.Root = root
	}
	for _, edge := range edges {
		original := fixtureRole(r, edge.Grantor)
		oldBootstrap, hasBootstrap := r.Root.Read(authorization.PrincipalRef{}).Lookup("postgres")
		if hasBootstrap && oldBootstrap.Ref != original.Ref {
			temp := oldBootstrap
			temp.Name = fmt.Sprintf("fixture_bootstrap_%d", temp.Ref.ID())
			putFixtureRole(r, temp)
		}
		elevated := original
		elevated.Name = "postgres"
		elevated.Superuser = true
		putFixtureRole(r, elevated)
		target := fixtureRole(r, edge.Role)
		member := fixtureRole(r, edge.Member)
		request := &parser.RoleMembership{Targets: []parser.RoleSpec{{Name: target.Name}}, Members: []parser.RoleSpec{{Name: member.Name}}, Options: []parser.MembershipOption{{Name: "admin", Value: edge.Admin}, {Name: "inherit", Value: edge.Inherit}, {Name: "set", Value: edge.Set}}}
		root, changed, _, err := authorization.MembershipCandidate(context.Background(), r.Root.Read(authorization.PrincipalRef{}), authorization.RoleState{CurrentUser: edge.Grantor, SessionUser: edge.Grantor}, request, nil, false, false)
		if err != nil {
			panic(err)
		}
		if changed {
			r.Root = root
		}
		putFixtureRole(r, original)
		if hasBootstrap && oldBootstrap.Ref != original.Ref {
			putFixtureRole(r, oldBootstrap)
		}
	}
	destination.Root = candidate.Root
}

func fixtureEdges(r *access.RoleRecord) []authorization.Membership {
	return r.Root.Read(authorization.PrincipalRef{}).Memberships()
}

type defaultUnitFixture struct {
	e                     *core.Engine
	op                    *session.Operation
	owner, reader, member authorization.PrincipalRef
	schema                ids.ObjectAddress
}

func (f defaultUnitFixture) apply(s *parser.AlterDefaultPrivileges) error {
	_, err := f.e.Execute(f.op, s, 0, nil)
	return err
}

func (f defaultUnitFixture) grant(t *testing.T, class, privilege string, schemas []string) *parser.AlterDefaultPrivileges {
	t.Helper()
	s := defaultUnitStatement(class, privilege, []string{"default_owner"}, schemas, []string{"default_reader"})
	if err := f.apply(s); err != nil {
		t.Fatal(err)
	}
	return s
}

func (f defaultUnitFixture) actor(ref authorization.PrincipalRef) {
	f.e.Mu.Lock()
	f.op.Session.Mu.Lock()
	f.op.Session.Authorization.State.CurrentUser = ref
	f.op.Session.Authorization.Revision++
	f.op.Session.Mu.Unlock()
	f.e.Mu.Unlock()
}

func defaultUnitStatement(class, privilege string, creators, schemas, grantees []string) *parser.AlterDefaultPrivileges {
	s := &parser.AlterDefaultPrivileges{Class: class, Privileges: []parser.GrantPrivilege{{Name: privilege, All: privilege == "all"}}}
	if creators != nil {
		option := parser.DefaultPrivilegesOption{Roles: make([]parser.RoleSpec, len(creators))}
		for i, name := range creators {
			option.Roles[i] = parser.RoleSpec{Name: name}
		}
		s.Options = append(s.Options, option)
	}
	if schemas != nil {
		option := parser.DefaultPrivilegesOption{Schemas: make([]parser.Identifier, len(schemas))}
		for i, name := range schemas {
			option.Schemas[i] = parser.Identifier{Text: name}
		}
		s.Options = append(s.Options, option)
	}
	for _, name := range grantees {
		s.Grantees = append(s.Grantees, parser.GrantGrantee{Public: name == "PUBLIC", Role: parser.RoleSpec{Name: name}})
	}
	return s
}
