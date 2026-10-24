package access

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// roleStoreFixture is an initialized store and the catalog owner it backs,
// with a current database owned by the bootstrap superuser.
type roleStoreFixture struct {
	store *RoleStore
	owner *catalog.Owner
	read  catalog.Read
}

func newRoleStoreFixture(t *testing.T) roleStoreFixture {
	t.Helper()
	f := roleStoreFixture{store: &RoleStore{}}
	f.store.Initialize()
	f.owner = &catalog.Owner{Domain: &ids.Domain{}, Principals: &f.store.Domain, ACLAuthority: &f.store.ACLAuthority}
	state := catalog.NewState(1)
	database := &catalog.Database{ObjectAddress: catalog.CurrentDatabaseAddress, ObjectRevision: 1, ObjectName: "memory", Owner: f.store.Domain.Principal(authorization.BootstrapPrincipalID)}
	state.SetDescriptor(database.Address(), database)
	state.BindName(catalog.DescriptorNameKey(database), database.Address())
	f.read = catalog.NewRead(f.owner, state)
	return f
}

func TestRoleStoreReadRequiresOwnCatalogAndRoot(t *testing.T) {
	f, other := newRoleStoreFixture(t), newRoleStoreFixture(t)
	if _, ok := f.store.PublishedRead(f.owner, f.read).Lookup("postgres"); !ok {
		t.Fatal("own catalog and root: bootstrap role missing")
	}
	if f.store.PublishedRead(other.owner, f.read).BelongsTo(&f.store.Domain) {
		t.Fatal("view of another catalog owner accepted")
	}
	if f.store.Read(f.owner, f.read, nil).BelongsTo(&f.store.Domain) {
		t.Fatal("nil root accepted")
	}
	if f.store.Read(f.owner, f.read, other.store.Published).BelongsTo(&f.store.Domain) {
		t.Fatal("root of another domain accepted")
	}
}

func TestRoleStorePrincipalCandidate(t *testing.T) {
	f := newRoleStoreFixture(t)
	first := f.store.Domain.Principal(authorization.LastPredefinedRoleID + 1)
	for range 2 {
		if got, err := f.store.PrincipalCandidateLocked(); err != nil || got != first {
			t.Fatalf("candidate = %v, %v; want %v (candidates do not allocate)", got, err, first)
		}
	}
	f.store.InstallPrincipalFrontierLocked(1)
	if got, err := f.store.PrincipalCandidateLocked(); err != nil || got != first {
		t.Fatalf("candidate below the predefined roles = %v, %v; want %v", got, err, first)
	}
	root, err := f.store.Published.Root.PutDescriptor(authorization.RoleDescriptor{Ref: first, Name: "taken"})
	if err != nil {
		t.Fatal(err)
	}
	f.store.PublishLocked(&RoleRecord{Root: root, Revision: 2})
	if _, err := f.store.PrincipalCandidateLocked(); err == nil {
		t.Fatal("allocated principal offered again")
	}
	for _, exhausted := range []uint64{0, ^uint64(0)} {
		f.store.InstallPrincipalFrontierLocked(exhausted)
		if _, err := f.store.PrincipalCandidateLocked(); err == nil {
			t.Fatalf("frontier %d: exhaustion not reported", exhausted)
		}
	}
}

func TestRoleStoreRevisionFrontier(t *testing.T) {
	f := newRoleStoreFixture(t)
	if got, err := f.store.RevisionCandidateLocked(); err != nil || got != 2 {
		t.Fatalf("first candidate = %d, %v; want 2", got, err)
	}
	f.store.AdvanceRevisionFrontierLocked(9)
	if got := f.store.NextRevision(); got != 10 {
		t.Fatalf("advanced frontier = %d, want 10", got)
	}
	f.store.AdvanceRevisionFrontierLocked(4)
	if got := f.store.NextRevision(); got != 10 {
		t.Fatalf("frontier moved back to %d", got)
	}
	f.store.PublishLocked(&RoleRecord{Root: f.store.Published.Root, Revision: 20})
	if got, err := f.store.RevisionCandidateLocked(); err != nil || got != 21 {
		t.Fatalf("candidate past the published revision = %d, %v; want 21", got, err)
	}
	f.store.AdvanceRevisionFrontierLocked(^uint64(0))
	if got := f.store.NextRevision(); got != ^uint64(0) {
		t.Fatalf("saturated frontier = %d", got)
	}
	if _, err := f.store.RevisionCandidateLocked(); err == nil {
		t.Fatal("revision exhaustion not reported")
	}
}

func TestRoleStoreInstallCapture(t *testing.T) {
	source, stage := newRoleStoreFixture(t), &RoleStore{}
	stage.Initialize()
	source.store.InstallPrincipalFrontierLocked(42)
	source.store.PublishLocked(&RoleRecord{Root: source.store.Published.Root, Revision: 7})
	targets := stage.InstallCapture(source.store.CaptureLocked())
	if stage.Published.Revision != 7 || stage.NextPrincipal() != 42 {
		t.Fatalf("stage revision %d, principal frontier %d; want 7, 42", stage.Published.Revision, stage.NextPrincipal())
	}
	bootstrap := stage.Domain.Principal(authorization.BootstrapPrincipalID)
	if targets[bootstrap] != source.store.Domain.Principal(authorization.BootstrapPrincipalID) {
		t.Fatal("stage principal does not map to its source")
	}
	if _, ok := stage.Published.Root.Read(authorization.PrincipalRef{}).Find(bootstrap); !ok {
		t.Fatal("captured roles are not in the stage domain")
	}
}
