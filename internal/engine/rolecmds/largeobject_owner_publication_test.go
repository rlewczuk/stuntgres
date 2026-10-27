package rolecmds_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/engine/session"
)

func TestLargeObjectOwnerPublication(t *testing.T) {
	for _, fence := range []string{"success", "cancel", "role", "catalog", "database", "session", "reset", "namespace-exhausted-success", "role-headroom"} {
		t.Run(fence, func(t *testing.T) {
			e, old, target, reader, grantor := largeOwnerFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, done, err := enginetest.Operation(e, ctx)
			if err != nil { t.Fatal(err) }
			defer done()
			auth, err := core.OperationAuthorization(op)
			if err != nil { t.Fatal(err) }
			stage := core.OwnershipStage(e, auth, op)
			defer e.DiscardCatalogStage(stage)
			if err := role.StageOwnership(stage, old, target); err != nil { t.Fatal(err) }
			if stage.NamespaceChanged() { t.Fatal("large-object ownership staged a namespace change") }
			candidate := stage.Get(old.Address()).(*catalog.LargeObject)
			retainedACL := append([]authorization.ACLEntry(nil), old.ACL.Entries...)
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil { t.Fatal(err) }
			switch fence {
			case "cancel":
				cancel()
			case "role", "role-headroom":
				next := *e.RoleStore.Published
				next.Revision++
				if fence == "role-headroom" { next.Revision = ^uint64(0) }
				e.RoleStore.PublishLocked(&next)
			case "catalog":
				next := *old
				next.ObjectRevision++
				peer := e.BeginCatalogStage(e.CatalogRead())
				if err := peer.Replace(&next); err != nil { t.Fatal(err) }
				ownershipUnitPublish(t, e, peer)
			case "database":
				next := *e.CatalogRead().MustDatabase(catalog.CurrentDatabaseAddress)
				next.Owner, next.ObjectRevision = target, next.Revision()+1
				peer := e.BeginCatalogStage(e.CatalogRead())
				if err := peer.Replace(&next); err != nil { t.Fatal(err) }
				ownershipUnitPublish(t, e, peer)
			case "session":
				op.Session.Mu.Lock()
				op.Session.Authorization.Revision++
				op.Session.Mu.Unlock()
			case "reset":
				if err := engine.Reset(context.Background(), e); err != nil { t.Fatal(err) }
			case "namespace-exhausted-success":
				// Unlike the namespace candidate in ownershipCleanupStage, this
				// descriptor-only transfer consumes no namespace generation.
				next := catalog.CloneState(e.Catalog.State)
				next.SetNamespaceGeneration(^uint64(0))
				e.Catalog.PublishLocked(next)
			}
			state, roles := e.Catalog.State, e.RoleStore.Published
			oids, revisions := e.Catalog.NextOID(), e.Catalog.NextRevision()
			refs := []authorization.PrincipalRef{old.Owner, target, reader, grantor}
			dependencies := make([][]access.RoleDependency, len(refs))
			for i, ref := range refs { dependencies[i] = largeOwnerDependencies(state.Get(old.Address()), ref) }
			_, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
			if fence == "success" || fence == "namespace-exhausted-success" {
				if err != nil || e.Catalog.State == state || e.CatalogRead().Get(old.Address()) != candidate || candidate.Owner != target { t.Fatal("failed successful publication", err) }
				if e.RoleStore.Published != roles || e.Catalog.NextOID() != oids { t.Fatal("owner transfer changed roles or OID allocator") }
				if got := largeOwnerDependencies(candidate, old.Owner); len(got) != 0 { t.Fatal("obsolete owner still referenced", got) }
				if e.Catalog.State.NamespaceGeneration() != state.NamespaceGeneration() {
					t.Fatal("large-object ownership advanced unrelated namespace generation")
				}
				if fence == "namespace-exhausted-success" && e.Catalog.State.NamespaceGeneration() != ^uint64(0) {
					t.Fatal("publication reset exhausted namespace generation")
				}
			} else {
				if err == nil { t.Fatal("publication ignored fence", fence) }
				if fence == "cancel" && !errors.Is(err, context.Canceled) { t.Fatal(err) }
				if e.Catalog.State != state || e.RoleStore.Published != roles || e.Catalog.NextOID() != oids || e.Catalog.NextRevision() != revisions { t.Fatal("failed publication changed catalog/roles/allocators") }
				for i, ref := range refs {
					if got := largeOwnerDependencies(e.Catalog.State.Get(old.Address()), ref); !reflect.DeepEqual(got, dependencies[i]) { t.Fatal("failed publication moved dependencies", ref) }
				}
			}
			if old.Owner == target || !reflect.DeepEqual(old.ACL.Entries, retainedACL) || stage.Base.Get(old.Address()) != old { t.Fatal("publication mutated retained descriptor/root") }
			largeOwnerContentIdentity(t, candidate.Content, old.Content)
		})
	}
	t.Run("private-stage-cancel", func(t *testing.T) {
		e, old, target, reader, _ := largeOwnerFixture(t)
		committed, roles := e.Catalog.State, e.RoleStore.Published
		first := e.BeginCatalogStage(e.CatalogRead())
		if err := role.StageOwnership(first, old, reader); err != nil { t.Fatal(err) }
		journal := session.NewJournal()
		ownershipUnitCapture(t, e, first, journal)
		mark := journal.Clone()
		e.Mu.Lock()
		private := e.PrivateCatalogLocked(journal)
		e.Mu.Unlock()
		predecessor := private.Get(old.Address()).(*catalog.LargeObject)
		second := e.BeginCatalogStage(private)
		if err := role.StageOwnership(second, predecessor, target); err != nil { t.Fatal(err) }
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := e.ValidateCatalogStage(ctx, second); !errors.Is(err, context.Canceled) { t.Fatal(err) }
		e.DiscardCatalogStage(second)
		e.Mu.Lock()
		current, saved := e.PrivateCatalogLocked(journal), e.PrivateCatalogLocked(mark)
		e.Mu.Unlock()
		if e.Catalog.State != committed || e.RoleStore.Published != roles || current.Get(old.Address()) != predecessor || saved.Get(old.Address()) != predecessor || predecessor.Owner != reader || old.Owner == reader { t.Fatal("canceled private stage changed committed/private/savepoint roots") }
		largeOwnerContentIdentity(t, predecessor.Content, old.Content)
	})
	t.Run("final-install-cancel", func(t *testing.T) {
		e, old, target, _, _ := largeOwnerFixture(t)
		stage := e.BeginCatalogStage(e.CatalogRead())
		defer e.DiscardCatalogStage(stage)
		if err := role.StageOwnership(stage, old, target); err != nil { t.Fatal(err) }
		base, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx := &ownershipInstallContext{Context: base, e: e, entered: make(chan struct{}), release: make(chan struct{})}
		prepared, err := e.ValidateCatalogStage(ctx, stage)
		if err != nil { t.Fatal(err) }
		state, roles := e.Catalog.State, e.RoleStore.Published
		oids, revisions := e.Catalog.NextOID(), e.Catalog.NextRevision()
		ctx.armed = true
		result := make(chan error, 1)
		go func() { _, err := enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()}); result <- err }()
		timer := time.NewTimer(5*time.Second)
		defer timer.Stop()
		select {
		case <-ctx.entered:
			cancel()
			close(ctx.release)
		case <-timer.C:
			cancel()
			close(ctx.release)
			<-result
			t.Fatal("final-install cancellation barrier not reached")
		}
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) { t.Fatal(err) }
		case <-timer.C:
			t.Fatal("publisher did not join after cancellation")
		}
		if e.Catalog.State != state || e.RoleStore.Published != roles || e.Catalog.NextOID() != oids || e.Catalog.NextRevision() != revisions || state.Get(old.Address()) != old { t.Fatal("final-install cancellation partially published") }
		largeOwnerContentIdentity(t, stage.Get(old.Address()).(*catalog.LargeObject).Content, old.Content)
	})
}
