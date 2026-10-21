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
	setFixtureEdges(e.roles.published, append(fixtureEdges(e.roles.published), authorization.Membership{Role: target, Member: source, Grantor: owner, Admin: true}, authorization.Membership{Role: target, Member: reader, Grantor: source, Inherit: true, Set: true}))
	ns := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 200000}, ObjectName: "ownership_guard", ObjectRevision: 1, Owner: owner, ACL: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, source, authorization.UsagePrivilege, authorization.UsagePrivilege), objectACLUnitEntry(source, reader, authorization.UsagePrivilege, 0)}}}
	setup := e.BeginCatalogStage(e.CatalogRead())
	if err := setup.Create(ns); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, setup)
	stage := e.BeginCatalogStage(e.CatalogRead())
	stage.roleRevision = e.roles.published.Revision
	if err := role.StageOwnedCleanup(context.Background(), stage, e.roles.read(e.catalog.owner(), stage.Base, e.roles.published), e.roles.published, map[authorization.PrincipalRef]bool{source: true}); err != nil {
		t.Fatal(err)
	}
	if stage.roleCandidate == nil || stage.Get(ns.Address()) == nil {
		t.Fatal("fixture lacks both private candidates")
	}
	return e, stage, ns
}
func TestRoleOwnershipPublicationAtomicCleanup(t *testing.T) {
	e, stage, ns := ownershipCleanupStage(t)
	oldState, oldRoles := e.catalog.state, e.roles.published
	oldEdges := append([]authorization.Membership(nil), fixtureEdges(oldRoles)...)
	oldACL := append([]authorization.ACLEntry(nil), ns.ACL.Entries...)
	ownershipUnitPublish(t, e, stage)
	current := e.catalog.state.MustNamespace(ns.Address())
	if e.catalog.state == oldState || e.roles.published != stage.roleCandidate || len(current.ACL.Entries) != 0 || len(fixtureEdges(e.roles.published)) != len(oldEdges)-1 {
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
				auth, err := operationAuthorization(op)
				if err != nil {
					t.Fatal(err)
				}
				stage.authorizeLocked(auth, op.Session)
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
				next := *e.roles.published
				next.Revision++
				e.roles.published = &next
			case "catalog":
				replacement := *ns
				replacement.ObjectRevision++
				other := e.BeginCatalogStage(e.CatalogRead())
				if err := other.Replace(&replacement); err != nil {
					t.Fatal(err)
				}
				ownershipUnitPublish(t, e, other)
			case "database":
				replacement := *e.catalog.state.MustDatabase(catalog.CurrentDatabaseAddress)
				replacement.Owner = e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("ownership_target")
				replacement.ObjectRevision++
				other := e.BeginCatalogStage(e.CatalogRead())
				if err := other.Replace(&replacement); err != nil {
					t.Fatal(err)
				}
				roles, epoch := e.roles.published, e.catalog.state.Epoch()
				ownershipUnitPublish(t, e, other)
				if e.roles.published != roles || e.catalog.state.Epoch() != epoch {
					t.Fatal("database-only publication changed role authority or epoch")
				}
			case "reset":
				if err := e.Reset(ctx); err != nil {
					t.Fatal(err)
				}
			case "session":
				stage.authSession.Mu.Lock()
				stage.authSession.Authorization.Revision++
				stage.authSession.Mu.Unlock()
			case "namespace-headroom":
				next := catalog.CloneState(e.catalog.state)
				next.SetNamespaceGeneration(^uint64(0))
				e.catalog.state = next
			case "role-headroom":
				next := *e.roles.published
				next.Revision = ^uint64(0)
				e.roles.published = &next
			case "invalid-member":
				next := *stage.roleCandidate
				edges := fixtureEdges(&next)
				edges[0].Set = !edges[0].Set
				setFixtureEdges(&next, edges)
				stage.roleCandidate = &next
			}
			state, roles := e.catalog.state, e.roles.published
			if _, err = e.PublishCatalog(p, PublicationToken{Epoch: stage.Base.Epoch()}); err == nil {
				t.Fatal("stale/exhausted publication succeeded")
			}
			if e.catalog.state != state || e.roles.published != roles {
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
		if c.e.mu.TryLock() {
			c.e.mu.Unlock()
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
	state, roles := e.catalog.state, e.roles.published
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
	if e.catalog.state != state || e.roles.published != roles {
		t.Fatal("final-install cancellation partially published")
	}
}

type ownershipCountingWriter struct{ calls int }

func (w *ownershipCountingWriter) Write(p []byte) (int, error) { w.calls++; return len(p), nil }
func TestRoleOwnershipNamespaceACLPrewriterGuard(t *testing.T) {
	e, _, _ := ownershipCleanupStage(t)
	state, roles := e.catalog.state, e.roles.published
	writer := &ownershipCountingWriter{}
	err := e.Dump(context.Background(), writer)
	if err == nil || err.Error() != "stuntgres: dump cannot serialize explicit namespace ACL" || writer.calls != 0 {
		t.Fatal("namespace ACL silently lost or writer invoked", err, writer.calls)
	}
	var sql interface{ SQLState() string }
	if errors.As(err, &sql) {
		t.Fatal("ordinary control error became SQL diagnostic", err)
	}
	if e.catalog.state != state || e.roles.published != roles {
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
	auth, err := operationAuthorization(op)
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
	state, roles, store := e.catalog.state, e.roles.published, e.storage.Relations[address]
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
	if e.catalog.state != state || e.roles.published != roles || e.storage.Relations[address] != store || state.MustRelation(address).Owner == target {
		t.Fatal("stale publication erased membership or changed table")
	}
}
