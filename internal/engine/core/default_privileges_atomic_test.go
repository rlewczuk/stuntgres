package core_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDefaultPrivilegesAtomicity(t *testing.T) {
	cases := []struct {
		name          string
		s             *parser.AlterDefaultPrivileges
		code, message string
	}{
		{"late creator", defaultUnitStatement("tables", "select", []string{"default_owner", "missing"}, nil, []string{"default_reader"}), "42704", `role "missing" does not exist`},
		{"late schema", defaultUnitStatement("tables", "select", []string{"default_owner"}, []string{"default_schema", "missing"}, []string{"default_reader"}), "3F000", `schema "missing" does not exist`},
		{"grantee before bit", defaultUnitStatement("tables", "bogus", []string{"default_owner"}, nil, []string{"default_reader", "missing"}), "42704", `role "missing" does not exist`},
		{"columns", defaultUnitStatement("tables", "select", []string{"default_owner"}, nil, []string{"default_reader"}), "0LP01", "default privileges cannot be set for columns"},
		{"schema class", defaultUnitStatement("schemas", "usage", []string{"default_owner"}, []string{"default_schema"}, []string{"default_reader"}), "0LP01", "cannot use IN SCHEMA clause when using GRANT/REVOKE ON SCHEMAS"},
		{"PUBLIC options", defaultUnitStatement("tables", "select", []string{"default_owner"}, nil, []string{"default_reader", "PUBLIC"}), "0LP01", "grant options can only be granted to roles"},
		{"schema class before later missing", defaultUnitStatement("schemas", "usage", []string{"default_owner"}, []string{"default_schema", "missing"}, []string{"default_reader"}), "0LP01", "cannot use IN SCHEMA clause when using GRANT/REVOKE ON SCHEMAS"},
		{"missing before schema class", defaultUnitStatement("schemas", "usage", []string{"default_owner"}, []string{"missing", "default_schema"}, []string{"default_reader"}), "3F000", `schema "missing" does not exist`},
		{"PUBLIC options before later missing", defaultUnitStatement("tables", "select", []string{"default_owner"}, []string{"default_schema", "missing"}, []string{"default_reader", "PUBLIC"}), "0LP01", "grant options can only be granted to roles"},
		{"missing before PUBLIC options", defaultUnitStatement("tables", "select", []string{"default_owner"}, []string{"missing", "default_schema"}, []string{"default_reader", "PUBLIC"}), "3F000", `schema "missing" does not exist`},
	}
	cases[3].s.Privileges[0].Columns = []parser.Identifier{{Text: "x"}}
	cases[5].s.GrantOption = true
	cases[8].s.GrantOption = true
	cases[9].s.GrantOption = true
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newDefaultUnitFixture(t)
			catalog, roles := f.e.Catalog.State, f.e.RoleStore.Published
			err := f.apply(tc.s)
			defaultUnitDiagnostic(t, err, tc.code, tc.message)
			if f.e.Catalog.State != catalog || f.e.RoleStore.Published != roles || catalog.DefaultACLCount() != 0 {
				t.Fatal("failed utility published a partial candidate")
			}
		})
	}
	f := newDefaultUnitFixture(t)
	f.actor(f.reader)
	s := defaultUnitStatement("tables", "select", []string{"default_owner"}, []string{"missing"}, []string{"default_reader"})
	defaultUnitDiagnostic(t, f.apply(s), "42501", "permission denied to change default privileges")
}

func TestDefaultPrivilegesRevision(t *testing.T) {
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	namespace := *state.MustNamespace(f.schema)
	namespace.ObjectAddress.ObjectID = 20012
	namespace.ObjectName = "default_other_schema"
	state.SetDescriptor(namespace.Address(), &namespace)
	state.BindName(catalog.DescriptorNameKey(&namespace), namespace.Address())
	f.e.Catalog.PublishLocked(state)
	oldRevision := f.e.RoleStore.Published.Revision
	s := defaultUnitStatement("tables", "select", []string{"default_owner", "default_owner"}, []string{"default_schema", "default_other_schema", "default_schema"}, []string{"default_reader", "default_reader"})
	if err := f.apply(s); err != nil {
		t.Fatal(err)
	}
	if f.e.RoleStore.Published.Revision != oldRevision+1 || f.e.Catalog.State.DefaultACLCount() != 2 {
		t.Fatal("multiple schemas/repeated operands not one publication")
	}
	firstCatalog, firstRoles := f.e.Catalog.State, f.e.RoleStore.Published
	if err := f.apply(s); err != nil {
		t.Fatal(err)
	}
	if f.e.Catalog.State != firstCatalog || f.e.RoleStore.Published != firstRoles {
		t.Fatal("canonical no-op published")
	}
	s.Revoke = true
	if err := f.apply(s); err != nil {
		t.Fatal(err)
	}
	if f.e.Catalog.State.DefaultACLCount() != 0 || f.e.RoleStore.Published.Revision != oldRevision+2 {
		t.Fatal("baseline reversal")
	}
	roles := *f.e.RoleStore.Published
	roles.Revision = ^uint64(0)
	f.e.RoleStore.PublishLocked(&roles)
	firstCatalog, firstRoles = f.e.Catalog.State, f.e.RoleStore.Published
	s.Revoke = false
	err := f.apply(s)
	if err == nil || !strings.Contains(err.Error(), "authorization revision exhausted") || f.e.Catalog.State != firstCatalog || f.e.RoleStore.Published != firstRoles {
		t.Fatal("revision overflow published", err)
	}
	s.Revoke = true
	if err = f.apply(s); err != nil || f.e.RoleStore.Published != firstRoles {
		t.Fatal("saturated canonical no-op", err)
	}
}

func TestDefaultPrivilegesCancellation(t *testing.T) {
	f := newDefaultUnitFixture(t)
	catalog, roles := f.e.Catalog.State, f.e.RoleStore.Published
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.op.Ctx = ctx
	err := f.apply(defaultUnitStatement("tables", "select", []string{"default_owner"}, []string{"default_schema"}, []string{"default_reader"}))
	if !errors.Is(err, context.Canceled) || f.e.Catalog.State != catalog || f.e.RoleStore.Published != roles {
		t.Fatal("canceled utility publication", err)
	}
}

func TestTransactionalPrivilegesCancellation(t *testing.T) {
	for _, retained := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "retained"}[retained], func(t *testing.T) {
			f := newDefaultUnitFixture(t)
			if _, err := f.e.Begin(f.op, session.TxOptions{}); err != nil {
				t.Fatal(err)
			}
			var message *batchUnitMessage
			if retained {
				scope, err := f.e.StartStatement(f.op)
				if err != nil {
					t.Fatal(err)
				}
				if err = scope.RetainMessage(); err != nil {
					_ = scope.Finish(err)
					t.Fatal(err)
				}
				message = &batchUnitMessage{scope: scope, op: f.op}
				defer func() { _ = message.scope.FinishMessage(context.Canceled) }()
			}
			committed, roles := f.e.Catalog.State, f.e.RoleStore.Published
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			f.op.Ctx = ctx
			statement := defaultUnitStatement("tables", "select", []string{"default_owner"}, nil, []string{"default_reader"})
			var err error
			if message != nil {
				_, err = message.step(statement)
			} else {
				err = f.apply(statement)
			}
			if !errors.Is(err, context.Canceled) || f.e.Catalog.State != committed || f.e.RoleStore.Published != roles || f.op.Session.Transactions.Catalog != nil {
				t.Fatal("cancellation installed catalog/roles/owner-journal state", err)
			}
		})
	}
}

func TestTransactionalPrivilegesPublication(t *testing.T) {
	f := newDefaultUnitFixture(t)
	committed, roles := f.e.Catalog.State, f.e.RoleStore.Published
	if _, err := f.e.Begin(f.op, session.TxOptions{}); err != nil {
		t.Fatal(err)
	}
	f.grant(t, "tables", "select", nil)
	changes := f.op.Session.Transactions.Catalog
	if changes == nil {
		t.Fatal("active directive has no journal")
	}
	f.e.Mu.Lock()
	err := f.e.CatalogPreflightLocked(changes)
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal("own unchanged predecessor rejected", err)
	}
	stale := *roles
	stale.Revision++
	f.e.RoleStore.PublishLocked(&stale)
	f.e.Mu.Lock()
	err = f.e.CatalogPreflightLocked(changes)
	f.e.Mu.Unlock()
	if err != nil || f.e.Catalog.State != committed || changes.RolesBase != roles || f.e.RoleStore.Published != &stale {
		t.Fatal("compatible equal-root publication was rejected or installed early", err)
	}
	f.e.RoleStore.PublishLocked(roles)
	peer := roles.Advance()
	f.e.RoleStore.PublishLocked(peer)
	f.e.Mu.Lock()
	published, err := core.EngineJournalRolePublicationLocked(f.e, changes, true)
	f.e.Mu.Unlock()
	if err != nil || published == nil || published.Revision != peer.Revision+changes.Roles.Revision-roles.Revision || !published.SameAuthority(peer) || f.e.RoleStore.Published != peer {
		t.Fatal("independent revision-only publication lost a peer stamp or installed early", published, err)
	}
	mark := changes.Clone()
	if mark.Roles != changes.Roles || mark.RolesBase != roles {
		t.Fatal("cloned/savepoint journal lost exact immutable role delta")
	}
	for _, malformed := range []string{"missing", "zero", "underflow", "overflow", "foreign-root"} {
		candidate := changes.Clone()
		f.e.RoleStore.PublishLocked(roles)
		switch malformed {
		case "missing":
			candidate.RolesBase = nil
		case "zero":
			candidate.Roles = roles.Advance()
			candidate.Roles.Revision = roles.Revision
		case "underflow":
			candidate.Roles = roles.Advance()
			candidate.Roles.Revision = roles.Revision - 1
		case "overflow":
			f.e.RoleStore.PublishLocked(roles.Advance())
			f.e.RoleStore.Published.Revision = ^uint64(0)
		case "foreign-root":
			f.e.RoleStore.PublishLocked(&access.RoleRecord{Root: authorization.Seed(&authorization.Domain{}), Revision: roles.Revision + 1})
		}
		before := f.e.RoleStore.Published
		f.e.Mu.Lock()
		_, err := core.EngineJournalRolePublicationLocked(f.e, candidate, true)
		f.e.Mu.Unlock()
		if err == nil || f.e.RoleStore.Published != before || f.e.Catalog.State != committed {
			t.Fatal("malformed or foreign authority was published", malformed, err)
		}
	}
	// A completed directive may merge an unrelated role publication, but it
	// must not validate its retained ACL against a stale, still-live grantee.
	grantee, err := roles.Root.Read(f.owner).Descriptor(f.reader)
	if err != nil {
		t.Fatal(err)
	}
	retired := roles.Root.Retire(map[authorization.PrincipalRef]authorization.RoleDescriptor{f.reader: grantee})
	f.e.RoleStore.PublishLocked(&access.RoleRecord{Root: retired, Revision: roles.Revision + 1})
	before := f.e.RoleStore.Published
	f.e.Mu.Lock()
	err = f.e.CatalogPreflightLocked(changes)
	f.e.Mu.Unlock()
	if err == nil || f.e.RoleStore.Published != before || f.e.Catalog.State != committed || changes.RolesBase != roles {
		t.Fatal("retired retained-default grantee was adopted or effects installed", err)
	}
	if _, ok := changes.Roles.Root.Read(f.owner).Find(f.reader); !ok {
		t.Fatal("publication validation mutated the retained private authority")
	}
	f.e.RoleStore.PublishLocked(roles)
}

func TestDefaultPrivilegesPublicationFence(t *testing.T) {
	f := newDefaultUnitFixture(t)
	catalog, roles := f.e.Catalog.State, f.e.RoleStore.Published
	scope, err := f.e.StartStatement(f.op)
	if err != nil {
		t.Fatal(err)
	}
	statement := defaultUnitStatement("tables", "select", []string{"default_owner"}, nil, []string{"default_reader"})
	if _, err := scope.Execute(statement, 0, nil); err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	f.op.Session.RequestEnd(session.CleanClose)
	// Isolate the terminal publication gate from the earlier cancellation gate:
	// a fully staged candidate still cannot install into an ending physical owner.
	f.op.Session.Mu.Lock()
	f.op.Ctx = context.Background()
	f.op.Session.Mu.Unlock()
	err = scope.Finish(nil)
	if err == nil || f.e.Catalog.State != catalog || f.e.RoleStore.Published != roles {
		t.Fatal("terminal session published a prepared utility candidate", err)
	}
}

func TestDefaultPrivilegesCreationFence(t *testing.T) {
	for _, initial := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "present"}[initial], func(t *testing.T) {
			f := newDefaultUnitFixture(t)
			if initial {
				f.grant(t, "tables", "select", nil)
			}
			stage := f.e.BeginCatalogStage(f.e.CatalogRead())
			table := defaultUnitTable(t, f, stage, "paused", f.owner)
			entered, release := make(chan struct{}), make(chan struct{})
			provider := f.e.Catalog.Providers[ids.RelationClassID]
			var once sync.Once
			f.e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{Provider: provider, callback: func() { once.Do(func() { close(entered); <-release }) }}
			type result struct {
				p   *core.PreparedCatalogPublication
				err error
			}
			resultCh := make(chan result, 1)
			go func() { p, err := f.e.ValidateCatalogStage(context.Background(), stage); resultCh <- result{p, err} }()
			<-entered
			// Detached provider validation has captured either a real record or absence.
			// Change precisely that key while it is paused; unrelated-key changes below
			// demonstrate that this is not a gratuitous global catalog-epoch fence.
			f.grant(t, "tables", "insert", nil)
			close(release)
			r := <-resultCh
			f.e.Catalog.Providers[ids.RelationClassID] = provider
			if r.err != nil {
				t.Fatal(r.err)
			}
			_, err := enginetest.PublishCatalog(f.e, r.p, core.PublicationToken{Epoch: stage.Base.Epoch()})
			if err != (catalog.Conflict{}) || f.e.Catalog.State.Get(table.Address()) != nil {
				t.Fatal("stale creation default published", err)
			}
			for key, record := range stage.DefaultReads() {
				if key.Class != authorization.DefaultTables || key.Creator != f.owner || record != stage.Base.State().DefaultACL(key) {
					t.Fatal("incorrect creation observations")
				}
			}
			independent := f.e.BeginCatalogStage(f.e.CatalogRead())
			other := defaultUnitTable(t, f, independent, "unrelated", f.owner)
			f.grant(t, "sequences", "usage", nil)
			if err = defaultUnitPublish(t, f.e, independent); err != nil || f.e.Catalog.State.Get(other.Address()) != other {
				t.Fatal("unrelated class spuriously fenced", err)
			}
		})
	}
	// A consumed recipient or creator cannot be retired between creation capture
	// and final admission, even if a hostile private candidate bypasses DROP.
	f := newDefaultUnitFixture(t)
	f.grant(t, "tables", "select", nil)
	stage := f.e.BeginCatalogStage(f.e.CatalogRead())
	table := defaultUnitTable(t, f, stage, "retired", f.owner)
	roles := cloneRoleFixture(f.e.RoleStore.Published)
	retireFixtureRole(roles, f.reader)
	f.e.RoleStore.PublishLocked(roles)
	if err := defaultUnitPublish(t, f.e, stage); err == nil || f.e.Catalog.State.Get(table.Address()) != nil {
		t.Fatal("retired ACL principal published", err)
	}
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

func newDefaultUnitFixture(t *testing.T) defaultUnitFixture {
	t.Helper()
	e := engine.New()
	roles := cloneRoleFixture(e.RoleStore.Published)
	refs := make([]authorization.PrincipalRef, 3)
	for i, name := range []string{"default_owner", "default_reader", "default_member"} {
		ref := e.RoleStore.Domain.Principal(uint64(20001 + i))
		refs[i] = ref
		putFixtureRole(roles, authorization.RoleDescriptor{Ref: ref, Name: name, Login: true, Inherit: true, ConnectionLimit: -1})

	}
	setFixtureEdges(roles, append(append([]authorization.Membership(nil), fixtureEdges(roles)...), authorization.Membership{Role: refs[0], Member: refs[2], Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Inherit: true, Set: true}))
	// Install a fully identified graph before transactions branch from this
	// fixture. Candidate-only helpers intentionally leave new edge IDs at zero.
	root, frontier, err := roles.Root.AssignMembershipOIDs(authorization.FirstMembershipOID)
	if err != nil { t.Fatal(err) }
	roles.Root = root
	e.RoleStore.InstallMembershipFrontierLocked(frontier)
	e.RoleStore.PublishLocked(roles)
	schema := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20010}
	next := catalog.CloneState(e.Catalog.State)
	namespace := &catalog.Namespace{ObjectAddress: schema, ObjectName: "default_schema", ObjectRevision: 2, Owner: refs[0]}
	next.SetDescriptor(schema, namespace)
	next.BindName(catalog.DescriptorNameKey(namespace), schema)
	e.Catalog.PublishLocked(next)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	t.Cleanup(done)
	return defaultUnitFixture{e: e, op: op, owner: refs[0], reader: refs[1], member: refs[2], schema: schema}
}

// defaultUnitTable owns Engine.mu; callers must not hold it. Resolve the
// immutable stage-base carrier first, without a locking Engine.CatalogRead.
func defaultUnitTable(t *testing.T, f defaultUnitFixture, stage *core.CatalogStage, name string, owner authorization.PrincipalRef) *catalog.Relation {
	t.Helper()
	typ, ok := stage.Base.LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("stage has no int4 carrier")
	}
	f.e.Mu.Lock()
	d, err := table.NewRelation(core.DDLHost{E: f.e}, f.schema, name, owner, []catalog.Column{{Name: "x", Usage: typ.Usage()}}, nil)
	if err == nil {
		d.Security.ACL = stage.CreationACL(owner, f.schema, authorization.DefaultTables)
		err = f.e.StageRowTypeLocked(stage, d)
	}
	f.e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err = stage.Create(d); err != nil {
		t.Fatal(err)
	}
	return d
}

func defaultUnitPublish(t *testing.T, e *core.Engine, stage *core.CatalogStage) error {
	t.Helper()
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		return err
	}
	_, err = enginetest.PublishCatalog(e, p, core.PublicationToken{Epoch: stage.Base.Epoch()})
	return err
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
