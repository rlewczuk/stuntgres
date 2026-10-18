package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"sync"
	"testing"
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
			catalog, roles := f.e.state, f.e.roles
			err := f.apply(tc.s)
			defaultUnitDiagnostic(t, err, tc.code, tc.message)
			if f.e.state != catalog || f.e.roles != roles || len(catalog.defaultACLs) != 0 {
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
	state := cloneCatalog(f.e.state)
	namespace := *state.descriptors[f.schema].(*namespaceDescriptor)
	namespace.address.ObjectID = 20012
	namespace.name = "default_other_schema"
	state.descriptors[namespace.address] = &namespace
	state.names[descriptorNameKey(&namespace)] = namespace.address
	f.e.state = state
	oldRevision := f.e.roles.revision
	s := defaultUnitStatement("tables", "select", []string{"default_owner", "default_owner"}, []string{"default_schema", "default_other_schema", "default_schema"}, []string{"default_reader", "default_reader"})
	if err := f.apply(s); err != nil {
		t.Fatal(err)
	}
	if f.e.roles.revision != oldRevision+1 || len(f.e.state.defaultACLs) != 2 {
		t.Fatal("multiple schemas/repeated operands not one publication")
	}
	firstCatalog, firstRoles := f.e.state, f.e.roles
	if err := f.apply(s); err != nil {
		t.Fatal(err)
	}
	if f.e.state != firstCatalog || f.e.roles != firstRoles {
		t.Fatal("canonical no-op published")
	}
	s.Revoke = true
	if err := f.apply(s); err != nil {
		t.Fatal(err)
	}
	if len(f.e.state.defaultACLs) != 0 || f.e.roles.revision != oldRevision+2 {
		t.Fatal("baseline reversal")
	}
	roles := *f.e.roles
	roles.revision = ^uint64(0)
	f.e.roles = &roles
	firstCatalog, firstRoles = f.e.state, f.e.roles
	s.Revoke = false
	err := f.apply(s)
	if err == nil || !strings.Contains(err.Error(), "authorization revision exhausted") || f.e.state != firstCatalog || f.e.roles != firstRoles {
		t.Fatal("revision overflow published", err)
	}
	s.Revoke = true
	if err = f.apply(s); err != nil || f.e.roles != firstRoles {
		t.Fatal("saturated canonical no-op", err)
	}
}

func TestDefaultPrivilegesCancellation(t *testing.T) {
	f := newDefaultUnitFixture(t)
	catalog, roles := f.e.state, f.e.roles
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.op.ctx = ctx
	err := f.apply(defaultUnitStatement("tables", "select", []string{"default_owner"}, []string{"default_schema"}, []string{"default_reader"}))
	if !errors.Is(err, context.Canceled) || f.e.state != catalog || f.e.roles != roles {
		t.Fatal("canceled utility publication", err)
	}
}

func TestDefaultPrivilegesPublicationFence(t *testing.T) {
	f := newDefaultUnitFixture(t)
	catalog, roles := f.e.state, f.e.roles
	f.e.mu.Lock()
	auth, err := f.e.authorizationLocked(f.op)
	f.e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	f.op.session.RequestEnd(CleanClose)
	// Isolate the terminal publication gate from the earlier cancellation gate:
	// a fully staged candidate still cannot install into an ending physical owner.
	f.op.session.mu.Lock()
	f.op.ctx = context.Background()
	f.op.session.mu.Unlock()
	f.e.mu.Lock()
	_, err = f.e.alterDefaultPrivilegesLocked(f.op, auth, defaultUnitStatement("tables", "select", []string{"default_owner"}, nil, []string{"default_reader"}))
	f.e.mu.Unlock()
	if err == nil || f.e.state != catalog || f.e.roles != roles {
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
			provider := f.e.providers[ids.RelationClassID]
			var once sync.Once
			f.e.providers[ids.RelationClassID] = alterDropProvider{CatalogProvider: provider, callback: func() { once.Do(func() { close(entered); <-release }) }}
			type result struct {
				p   *PreparedCatalogPublication
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
			f.e.providers[ids.RelationClassID] = provider
			if r.err != nil {
				t.Fatal(r.err)
			}
			_, err := f.e.PublishCatalog(r.p, PublicationToken{Epoch: stage.base.Epoch()})
			if err != (CatalogConflict{}) || f.e.state.descriptors[table.address] != nil {
				t.Fatal("stale creation default published", err)
			}
			for key, record := range stage.defaultReads {
				if key.class != authorization.DefaultTables || key.creator != f.owner || record != stage.base.state.defaultACLs[key] {
					t.Fatal("incorrect creation observations")
				}
			}
			independent := f.e.BeginCatalogStage(f.e.CatalogRead())
			other := defaultUnitTable(t, f, independent, "unrelated", f.owner)
			f.grant(t, "sequences", "usage", nil)
			if err = defaultUnitPublish(t, f.e, independent); err != nil || f.e.state.descriptors[other.address] != other {
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
	roles := cloneRoleFixture(f.e.roles)
	retireFixtureRole(roles, f.reader)
	f.e.roles = roles
	if err := defaultUnitPublish(t, f.e, stage); err == nil || f.e.state.descriptors[table.address] != nil {
		t.Fatal("retired ACL principal published", err)
	}
}
