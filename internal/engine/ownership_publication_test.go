package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"sync"
	"testing"
	"time"
)

func ownershipCleanupStage(t *testing.T) (*Engine, *CatalogStage, *namespaceDescriptor) {
	t.Helper()
	e, owner, source, target, reader := ownershipUnitRoles()
	setFixtureEdges(e.roles, append(fixtureEdges(e.roles), authorization.Membership{Role: target, Member: source, Grantor: owner, Admin: true}, authorization.Membership{Role: target, Member: reader, Grantor: source, Inherit: true, Set: true}))
	ns := &namespaceDescriptor{address: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 200000}, name: "ownership_guard", revision: 1, owner: owner, acl: authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{objectACLUnitEntry(owner, source, authorization.UsagePrivilege, authorization.UsagePrivilege), objectACLUnitEntry(source, reader, authorization.UsagePrivilege, 0)}}}
	setup := e.BeginCatalogStage(e.CatalogRead())
	if err := setup.Create(ns); err != nil {
		t.Fatal(err)
	}
	ownershipUnitPublish(t, e, setup)
	stage := e.BeginCatalogStage(e.CatalogRead())
	stage.roleRevision = e.roles.revision
	if err := stageOwnedCleanup(context.Background(), stage, e.roleRead(stage.base, e.roles), e.roles, map[authorization.PrincipalRef]bool{source: true}); err != nil {
		t.Fatal(err)
	}
	if stage.roleCandidate == nil || stage.descriptors[ns.address] == nil {
		t.Fatal("fixture lacks both private candidates")
	}
	return e, stage, ns
}
func TestRoleOwnershipPublicationAtomicCleanup(t *testing.T) {
	e, stage, ns := ownershipCleanupStage(t)
	oldState, oldRoles := e.catalog.state, e.roles
	oldEdges := append([]authorization.Membership(nil), fixtureEdges(oldRoles)...)
	oldACL := append([]authorization.ACLEntry(nil), ns.acl.Entries...)
	ownershipUnitPublish(t, e, stage)
	current := e.catalog.state.descriptors[ns.address].(*namespaceDescriptor)
	if e.catalog.state == oldState || e.roles != stage.roleCandidate || len(current.acl.Entries) != 0 || len(fixtureEdges(e.roles)) != len(oldEdges)-1 {
		t.Fatal("catalog and membership did not commit together")
	}
	if !reflect.DeepEqual(fixtureEdges(oldRoles), oldEdges) || !reflect.DeepEqual(ns.acl.Entries, oldACL) || oldState.descriptors[ns.address] != ns {
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
				auth, err := op.Authorization()
				if err != nil {
					t.Fatal(err)
				}
				stage.authorizeLocked(auth, op.session)
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
				next := *e.roles
				next.revision++
				e.roles = &next
			case "catalog":
				replacement := *ns
				replacement.revision++
				other := e.BeginCatalogStage(e.CatalogRead())
				if err := other.Replace(&replacement); err != nil {
					t.Fatal(err)
				}
				ownershipUnitPublish(t, e, other)
			case "database":
				replacement := *e.catalog.state.descriptors[currentDatabaseAddress].(*databaseDescriptor)
				replacement.owner = e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("ownership_target")
				replacement.revision++
				other := e.BeginCatalogStage(e.CatalogRead())
				if err := other.Replace(&replacement); err != nil {
					t.Fatal(err)
				}
				roles, epoch := e.roles, e.catalog.state.epoch
				ownershipUnitPublish(t, e, other)
				if e.roles != roles || e.catalog.state.epoch != epoch {
					t.Fatal("database-only publication changed role authority or epoch")
				}
			case "reset":
				if err := e.Reset(ctx); err != nil {
					t.Fatal(err)
				}
			case "session":
				stage.authSession.mu.Lock()
				stage.authSession.authorization.revision++
				stage.authSession.mu.Unlock()
			case "namespace-headroom":
				next := cloneCatalog(e.catalog.state)
				next.namespaceGeneration = ^uint64(0)
				e.catalog.state = next
			case "role-headroom":
				next := *e.roles
				next.revision = ^uint64(0)
				e.roles = &next
			case "invalid-member":
				next := *stage.roleCandidate
				edges := fixtureEdges(&next)
				edges[0].Set = !edges[0].Set
				setFixtureEdges(&next, edges)
				stage.roleCandidate = &next
			}
			state, roles := e.catalog.state, e.roles
			if _, err = e.PublishCatalog(p, PublicationToken{Epoch: stage.base.Epoch()}); err == nil {
				t.Fatal("stale/exhausted publication succeeded")
			}
			if e.catalog.state != state || e.roles != roles {
				t.Fatal("failure partially installed catalog or membership")
			}
			if name == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if name == "database" && !errors.Is(err, ErrCatalogReplaced) {
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
	state, roles := e.catalog.state, e.roles
	ctx.armed = true
	result := make(chan error, 1)
	go func() { _, err := e.PublishCatalog(p, PublicationToken{Epoch: stage.base.Epoch()}); result <- err }()
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
	if e.catalog.state != state || e.roles != roles {
		t.Fatal("final-install cancellation partially published")
	}
}

type ownershipCountingWriter struct{ calls int }

func (w *ownershipCountingWriter) Write(p []byte) (int, error) { w.calls++; return len(p), nil }
func TestRoleOwnershipNamespaceACLPrewriterGuard(t *testing.T) {
	e, _, _ := ownershipCleanupStage(t)
	state, roles := e.catalog.state, e.roles
	writer := &ownershipCountingWriter{}
	err := e.Dump(context.Background(), writer)
	if err == nil || err.Error() != "stuntgres: dump cannot serialize explicit namespace ACL" || writer.calls != 0 {
		t.Fatal("namespace ACL silently lost or writer invoked", err, writer.calls)
	}
	var sql interface{ SQLState() string }
	if errors.As(err, &sql) {
		t.Fatal("ordinary control error became SQL diagnostic", err)
	}
	if e.catalog.state != state || e.roles != roles {
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
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	address := auth.catalog.state.names[ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "ownership_fenced"}]
	stage := e.ownershipStage(auth, op)
	if err = stageOwnership(stage, auth.catalog.state.descriptors[address], target); err != nil {
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
		_, err := e.PublishCatalog(p, PublicationToken{Epoch: stage.base.Epoch()})
		result <- err
	}()
	<-ready
	// An independent physical session changes real membership after candidate
	// validation. The delayed publisher must retain that change, not overwrite it.
	_, grantErr := testExecute(e, context.Background(), statement(t, "GRANT ownership_target TO ownership_reader"), nil)
	state, roles, store := e.catalog.state, e.roles, e.storage[address]
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
	if e.catalog.state != state || e.roles != roles || e.storage[address] != store || state.descriptors[address].(*relationDescriptor).owner == target {
		t.Fatal("stale publication erased membership or changed table")
	}
}
