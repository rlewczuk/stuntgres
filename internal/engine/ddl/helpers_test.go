package ddl

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// fixture is one catalog owner with a seeded role root, a current database
// and one TEMP namespace, without an engine.
type fixture struct {
	owner     *catalog.Owner
	read      catalog.Read
	record    *access.RoleRecord
	temporary *catalog.Namespace
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	sessions := &ids.Domain{}
	f := fixture{owner: &catalog.Owner{Domain: &ids.Domain{}, Principals: &authorization.Domain{}}}
	bootstrap := f.owner.Principals.Principal(authorization.BootstrapPrincipalID)
	state := catalog.NewState(1)
	database := &catalog.Database{ObjectAddress: catalog.CurrentDatabaseAddress, ObjectRevision: 1, ObjectName: "memory", Owner: bootstrap}
	f.temporary = &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 30000}, ObjectName: "pg_temp_1", ObjectRevision: 1, Owner: bootstrap, Temporary: sessions.Session(1)}
	for _, d := range []catalog.Descriptor{database, f.temporary} {
		state.SetDescriptor(d.Address(), d)
		state.BindName(catalog.DescriptorNameKey(d), d.Address())
	}
	f.read = catalog.NewRead(f.owner, state)
	f.record = &access.RoleRecord{Root: authorization.Seed(f.owner.Principals), Revision: 1}
	return f
}

// view captures the fixture as current, a seeded role.
func (f fixture) view(t *testing.T, current string) access.View {
	t.Helper()
	roles := access.RoleRead(f.owner, f.owner.Principals, f.read, f.record)
	actor := roles.LookupRef(current)
	if actor == (authorization.PrincipalRef{}) {
		t.Fatalf("role %s missing", current)
	}
	bootstrap := f.owner.Principals.Principal(authorization.BootstrapPrincipalID)
	state := authorization.RoleState{Authenticated: bootstrap, SessionUser: bootstrap, CurrentUser: actor}
	return access.View{Owner: f.owner, Catalog: f.read, PairedCatalog: f.read, RoleRecord: f.record, Roles: roles, State: state, SessionRoleState: state, Revision: 1}
}

// authority is an Authority whose session owns temporary.
type authority struct{ temporary ids.ObjectAddress }

func (a authority) TemporaryNamespace() ids.ObjectAddress       { return a.temporary }
func (a authority) ReferencePath() ([]ids.ObjectAddress, error) { return nil, nil }
func (authority) DDLAuthority()                                 {}

// host answers AccessView with one view and records notices. Only the
// methods the helpers call are implemented.
type host struct {
	Host
	view    access.View
	levels  []session.NoticeLevel
	notices []sqlstate.Error
}

func (h *host) AccessView(Authority) access.View { return h.view }
func (h *host) EmitNotice(level session.NoticeLevel, n sqlstate.Error) {
	h.levels = append(h.levels, level)
	h.notices = append(h.notices, n)
}

func TestCheckUsesAuthorityAsEnvironment(t *testing.T) {
	f := newFixture(t)
	h := &host{view: f.view(t, "pg_signal_backend")}
	ref := ObjectRef(h, authority{}, f.temporary.Address())
	req := access.Requirement{Object: ref, Privileges: authorization.UsagePrivilege}
	// USAGE on a TEMP namespace follows the session that owns it, which only
	// the authority (as the environment) knows.
	if err := Check(h, authority{temporary: f.temporary.Address()}, req); err != nil {
		t.Fatalf("owning session denied: %v", err)
	}
	var denied interface{ SQLState() string }
	err := Check(h, authority{}, req)
	if !errors.As(err, &denied) || denied.SQLState() != "42501" {
		t.Fatalf("foreign session: got %v, want 42501", err)
	}
}

func TestObjectRefStampsCatalogEpoch(t *testing.T) {
	f := newFixture(t)
	h := &host{view: f.view(t, "pg_signal_backend")}
	ref := ObjectRef(h, authority{}, f.temporary.Address())
	if ref.Address != f.temporary.Address() || ref.Epoch != f.read.Epoch() || ref.Domain() != f.owner.Domain {
		t.Fatalf("ref = %+v, want address %v at epoch %v in the owner's domain", ref, f.temporary.Address(), f.read.Epoch())
	}
}

func TestNoticeEmitsNoticeLevel(t *testing.T) {
	h := &host{}
	Notice(h, "42P06", `schema "s" already exists, skipping`)
	want := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P06", Message: `schema "s" already exists, skipping`}
	if len(h.notices) != 1 || h.levels[0] != session.LevelNotice || h.notices[0] != want {
		t.Fatalf("notices = %v at %v, want one %+v at NOTICE level", h.notices, h.levels, want)
	}
}

func TestCreationErrorPositionsAtFirstNamePart(t *testing.T) {
	name := parser.ObjectName{Parts: []parser.Identifier{{Text: "s", Pos: parser.Position{Character: 14}}, {Text: "v", Pos: parser.Position{Character: 16}}}}
	original := &sqlstate.Error{Code: "42P07", Message: "relation \"v\" already exists", Position: "3"}
	got := CreationError(original, name)
	var diagnostic *sqlstate.Error
	if !errors.As(got, &diagnostic) || diagnostic.Code != "42P07" || diagnostic.Position != "14" {
		t.Fatalf("CreationError = %#v, want 42P07 at 14", got)
	}
	if original.Position != "3" {
		t.Fatalf("original diagnostic changed: %q", original.Position)
	}
	plain := errors.New("plain")
	if got := CreationError(plain, name); got != plain {
		t.Fatalf("plain error = %v, want it unchanged", got)
	}
	if got := CreationError(original, parser.ObjectName{}); got != original {
		t.Fatalf("empty name = %v, want the original diagnostic", got)
	}
}
