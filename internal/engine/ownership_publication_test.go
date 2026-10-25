package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"sync"
	"testing"
	"time"
)

func ownershipCleanupStage(t *testing.T) (*Engine, *CatalogStage, *catalog.Namespace) {
	t.Helper()
	e, owner, source, target, reader := ownershipUnitRoles()
	setFixtureEdges(e.RoleStore.Published, append(fixtureEdges(e.RoleStore.Published), authorization.Membership{Role: target, Member: source, Grantor: owner, Admin: true}, authorization.Membership{Role: target, Member: reader, Grantor: source, Inherit: true, Set: true}))
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 200000}, ObjectName: "ownership_guard", ObjectRevision: 1, Owner: owner, ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, source, authorization.UsagePrivilege, authorization.UsagePrivilege), objectACLUnitEntry(source, reader, authorization.UsagePrivilege, 0)}}}
	setup := e.BeginCatalogStage(e.CatalogRead())
	if err := setup.Create(ns); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, setup)
	stage := e.BeginCatalogStage(e.CatalogRead())
	stage.RoleRevision = e.RoleStore.Published.Revision
	if err := role.StageOwnedCleanup(context.Background(), stage, e.RoleStore.PublishedRead(&e.Catalog.Owner, stage.Base), e.RoleStore.Published, map[authorization.PrincipalRef]bool{source: true}); err != nil {
		t.Fatal(err)
	}
	if stage.RoleCandidateRecord == nil || stage.Get(ns.Address()) == nil {
		t.Fatal("fixture lacks both private candidates")
	}
	return e, stage, ns
}
func TestRoleOwnershipPublicationAtomicCleanup(t *testing.T) {
	e, stage, ns := ownershipCleanupStage(t)
	oldState, oldRoles := e.Catalog.State, e.RoleStore.Published
	oldEdges := append([]authorization.Membership(nil), fixtureEdges(oldRoles)...)
	oldACL := append([]authorization.ACLEntry(nil), ns.ACL.Entries...)
	ownershipUnitPublish(t, e, stage)
	current := e.Catalog.State.MustNamespace(ns.Address())
	if e.Catalog.State == oldState || e.RoleStore.Published != stage.RoleCandidateRecord || len(current.ACL.Entries) != 0 || len(fixtureEdges(e.RoleStore.Published)) != len(oldEdges)-1 {
		t.Fatal("catalog and membership did not commit together")
	}
	if !reflect.DeepEqual(fixtureEdges(oldRoles), oldEdges) || !reflect.DeepEqual(ns.ACL.Entries, oldACL) || oldState.Get(ns.Address()) != ns {
		t.Fatal("publication mutated retained snapshots")
	}
}
func TestRoleOwnershipPublicationFences(t *testing.T) {
	for _, name := range []string{"cancel", "role", "catalog", "database", "reset", "session", "namespace-headroom", "role-headroom", "invalid-member"} {
		t.Run(name, func(t *testing.T) {
			e, stage, ns := ownershipCleanupStage(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var done func()
			if name == "session" || name == "database" {
				op, cleanup, err := testOperation(e, ctx)
				if err != nil {
					t.Fatal(err)
				}
				done = cleanup
				auth, err := OperationAuthorization(op)
				if err != nil {
					t.Fatal(err)
				}
				stage.AuthorizeLocked(auth, op.Session)
			}
			if done != nil {
				defer done()
			}
			p, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				t.Fatal(err)
			}
			switch name {
			case "cancel":
				cancel()
			case "role":
				next := *e.RoleStore.Published
				next.Revision++
				e.RoleStore.PublishLocked(&next)
			case "catalog":
				replacement := *ns
				replacement.ObjectRevision++
				other := e.BeginCatalogStage(e.CatalogRead())
				if err := other.Replace(&replacement); err != nil {
					t.Fatal(err)
				}
				ownershipUnitPublish(t, e, other)
			case "database":
				replacement := *e.Catalog.State.MustDatabase(catalog.CurrentDatabaseAddress)
				replacement.Owner = e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("ownership_target")
				replacement.ObjectRevision++
				other := e.BeginCatalogStage(e.CatalogRead())
				if err := other.Replace(&replacement); err != nil {
					t.Fatal(err)
				}
				roles, epoch := e.RoleStore.Published, e.Catalog.State.Epoch()
				ownershipUnitPublish(t, e, other)
				if e.RoleStore.Published != roles || e.Catalog.State.Epoch() != epoch {
					t.Fatal("database-only publication changed role authority or epoch")
				}
			case "reset":
				if err := Reset(ctx, e); err != nil {
					t.Fatal(err)
				}
			case "session":
				stage.AuthSession.Mu.Lock()
				stage.AuthSession.Authorization.Revision++
				stage.AuthSession.Mu.Unlock()
			case "namespace-headroom":
				next := catalog.CloneState(e.Catalog.State)
				next.SetNamespaceGeneration(^uint64(0))
				e.Catalog.PublishLocked(next)
			case "role-headroom":
				next := *e.RoleStore.Published
				next.Revision = ^uint64(0)
				e.RoleStore.PublishLocked(&next)
			case "invalid-member":
				next := *stage.RoleCandidateRecord
				edges := fixtureEdges(&next)
				edges[0].Set = !edges[0].Set
				setFixtureEdges(&next, edges)
				stage.RoleCandidateRecord = &next
			}
			state, roles := e.Catalog.State, e.RoleStore.Published
			if _, err = e.PublishCatalog(p, PublicationToken{Epoch: stage.Base.Epoch()}); err == nil {
				t.Fatal("stale/exhausted publication succeeded")
			}
			if e.Catalog.State != state || e.RoleStore.Published != roles {
				t.Fatal("failure partially installed catalog or membership")
			}
			if name == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if name == "database" && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("missing database predecessor veto", err)
			}
		})
	}
}

// The publisher calls Err while holding the engine mutex immediately before
// its final install. TryLock distinguishes that point from unlocked preflight;
// channels make cancellation deterministic without sleeps or timing assertions.
type ownershipInstallContext struct {
	context.Context
	e                *Engine
	armed            bool
	once             sync.Once
	entered, release chan struct{}
}

func (c *ownershipInstallContext) Err() error {
	if c.armed {
		if c.e.Mu.TryLock() {
			c.e.Mu.Unlock()
		} else {
			c.once.Do(func() { close(c.entered); <-c.release })
		}
	}
	return c.Context.Err()
}
func TestRoleOwnershipPublicationFinalInstallCancellation(t *testing.T) {
	e, stage, _ := ownershipCleanupStage(t)
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &ownershipInstallContext{Context: base, e: e, entered: make(chan struct{}), release: make(chan struct{})}
	p, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	state, roles := e.Catalog.State, e.RoleStore.Published
	ctx.armed = true
	result := make(chan error, 1)
	go func() { _, err := e.PublishCatalog(p, PublicationToken{Epoch: stage.Base.Epoch()}); result <- err }()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.entered:
		cancel()
		close(ctx.release)
	case <-timer.C:
		cancel()
		close(ctx.release)
		<-result
		t.Fatal("final-install barrier was not reached")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-timer.C:
		t.Fatal("canceled publisher did not finish")
	}
	if e.Catalog.State != state || e.RoleStore.Published != roles {
		t.Fatal("final-install cancellation partially published")
	}
}

type ownershipCountingWriter struct{ calls int }

func (w *ownershipCountingWriter) Write(p []byte) (int, error) { w.calls++; return len(p), nil }
func TestRoleOwnershipNamespaceACLPrewriterGuard(t *testing.T) {
	e, _, _ := ownershipCleanupStage(t)
	state, roles := e.Catalog.State, e.RoleStore.Published
	writer := &ownershipCountingWriter{}
	err := Dump(context.Background(), e, writer)
	if err == nil || err.Error() != "stuntgres: dump cannot serialize explicit namespace ACL" || writer.calls != 0 {
		t.Fatal("namespace ACL silently lost or writer invoked", err, writer.calls)
	}
	var sql interface{ SQLState() string }
	if errors.As(err, &sql) {
		t.Fatal("ordinary control error became SQL diagnostic", err)
	}
	if e.Catalog.State != state || e.RoleStore.Published != roles {
		t.Fatal("failed Dump mutated state")
	}
}

func TestReassignOwnedPublicationCompetingRoleChange(t *testing.T) {
	e, _, _, target, _ := ownershipUnitRoles()
	ownershipUnitExec(t, e, "CREATE TABLE ownership_fenced(id INT)")
	ownershipUnitExec(t, e, "INSERT INTO ownership_fenced VALUES(7)")
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	auth, err := OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	address := auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ownership_fenced"})
	stage := e.ownershipStage(auth, op)
	if err = role.StageOwnership(stage, auth.Catalog.Get(address), target); err != nil {
		t.Fatal(err)
	}
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	ready, release := make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	go func() {
		close(ready)
		<-release
		_, err := e.PublishCatalog(p, PublicationToken{Epoch: stage.Base.Epoch()})
		result <- err
	}()
	<-ready
	// An independent physical session changes real membership after candidate
	// validation. The delayed publisher must retain that change, not overwrite it.
	_, grantErr := testExecute(e, context.Background(), statement(t, "GRANT ownership_target TO ownership_reader"), nil)
	state, roles, store := e.Catalog.State, e.RoleStore.Published, e.Storage.Relations[address]
	close(release)
	select {
	case err = <-result:
		if err == nil {
			t.Fatal("stale role authority accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fenced publication did not finish")
	}
	if grantErr != nil {
		t.Fatal(grantErr)
	}
	if e.Catalog.State != state || e.RoleStore.Published != roles || e.Storage.Relations[address] != store || state.MustRelation(address).Owner == target {
		t.Fatal("stale publication erased membership or changed table")
	}
}
