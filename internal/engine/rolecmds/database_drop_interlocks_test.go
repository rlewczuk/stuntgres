package rolecmds_test

import (
	"context"
	"errors"
	"maps"
	"sync"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
)

// This is a package-ownership proof, not a SQL compatibility scenario: the
// parsed CREATE must own the real source Share ticket while its private
// relation provider is validating the copy. Public outcomes live in reference.
func TestDatabaseDropTemplateInterlocks(t *testing.T) {
	for _, cancelWait := range []bool{true, false} {
		name := "AX_after_completed_copy"
		if cancelWait {
			name = "cancel_only_DROP_waiter"
		}
		t.Run(name, func(t *testing.T) {
			e, source := templateUnitSource(t)
			enginetest.Run(t, e, "CREATE TABLE interlock_peer(v integer)")
			enginetest.Run(t, e, "INSERT INTO interlock_peer VALUES(22)")
			ctx, cancel := context.WithTimeout(t.Context(), enginetest.Liveness)
			defer cancel()
			copyOp, copyDone, err := enginetest.Operation(e, ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer copyDone()
			dropCtx, dropCancel := context.WithCancel(ctx)
			defer dropCancel()
			dropOp, dropDone, err := enginetest.Operation(e, dropCtx)
			if err != nil {
				t.Fatal(err)
			}
			defer dropDone()
			create := enginetest.Statement(t, "CREATE DATABASE interlock_copy TEMPLATE unit_template")
			drop := enginetest.Statement(t, "DROP DATABASE unit_template")
			provider := &templateConcurrentProvider{
				Provider: e.Catalog.Providers[ids.RelationClassID],
				entered:  make(chan struct{}), release: make(chan struct{}),
			}
			e.Catalog.Providers[ids.RelationClassID] = provider
			defer func() { e.Catalog.Providers[ids.RelationClassID] = provider.Provider }()
			var release sync.Once
			unblock := func() { release.Do(func() { close(provider.release) }) }
			copyResult, dropResult := make(chan error, 1), make(chan error, 1)
			copyJoined, dropJoined := false, true
			// Install release/cancel/join before either worker can enter a callback.
			defer func() {
				unblock()
				dropCancel()
				cancel()
				if !copyJoined {
					<-copyResult
				}
				if !dropJoined {
					<-dropResult
				}
			}()
			go func() { _, err := e.Execute(copyOp, create, 0, nil); copyResult <- err }()
			select {
			case <-provider.entered:
			case <-ctx.Done():
				t.Fatal("CREATE did not reach private copy validation", ctx.Err())
			}
			dropJoined = false
			go func() { _, err := e.Execute(dropOp, drop, 0, nil); dropResult <- err }()
			enginetest.LockPending(t, e, 1)
			resource := e.DatabaseResource(source.Catalog.DatabaseAddress())
			state := e.Locks.Inspect().Resources[resource]
			if state == nil || len(state.Holders) != 1 || len(state.Pending) != 1 || state.Pending[0].Mode != lock.AccessExclusive || state.Pending[0].Ticket.Session() != dropOp.Session.SessionToken {
				t.Fatal("DROP did not queue its own AX behind genuine CREATE Share", state)
			}
			for ticket, hold := range state.Holders {
				if ticket.Persistent() || ticket.Session() != copyOp.Session.SessionToken || hold.Mode != lock.Share {
					t.Fatal("copy source grant has wrong owner/lifetime/mode", ticket, hold)
				}
			}
			if source.ReplacementMu.TryLock() {
				source.ReplacementMu.Unlock()
				t.Fatal("private copy lost its source replacement reservation")
			}
			unchanged := databaseDropTemplateUnchanged(t, e, source)
			if cancelWait {
				dropCancel()
				err = databaseDropInterlockReceive(t, dropResult)
				dropJoined = true
				if !errors.Is(err, context.Canceled) {
					t.Fatal("queued DROP cancellation", err)
				}
				unchanged()
				remaining := e.Locks.Inspect().Resources[resource]
				if remaining == nil || len(remaining.Pending) != 0 || !maps.Equal(remaining.Holders, state.Holders) {
					t.Fatal("DROP cancellation removed or changed the copy's grant", remaining)
				}
				select {
				case err := <-copyResult:
					copyJoined = true
					t.Fatal("canceling DROP completed the paused copy", err)
				default:
				}
			} else {
				select {
				case err := <-dropResult:
					dropJoined = true
					t.Fatal("DROP crossed the copy callback", err)
				default:
				}
				unchanged()
			}
			oldAddress, oldDomain := source.Catalog.DatabaseAddress(), source.Catalog.Domain
			local := primaryKeyUnitRelation(t, source, "t").Address()
			oldHeap := source.Storage.Relations[local]
			unblock()
			if err := databaseDropInterlockReceive(t, copyResult); err != nil {
				copyJoined = true
				t.Fatal("DROP wait/cancellation invalidated the actual copy", err)
			}
			copyJoined = true
			if !cancelWait {
				if err := databaseDropInterlockReceive(t, dropResult); err != nil {
					dropJoined = true
					t.Fatal("AX did not proceed after completed copy", err)
				}
				dropJoined = true
			} else {
				// Retry uses a fresh operation/context, never a canceled lease.
				if _, err := enginetest.Execute(e, ctx, drop, nil); err != nil {
					t.Fatal("completed copy did not enable DROP retry", err)
				}
			}
			clone, err := e.SelectDatabase(ctx, "interlock_copy")
			if err != nil {
				t.Fatal(err)
			}
			if clone.Storage.Relations[local] == nil || clone.Storage.Relations[local] == oldHeap || len(clone.Storage.Relations[local].Versions) != 1 || len(oldHeap.Versions) != 1 || len(source.Storage.Relations) != 0 {
				t.Fatal("source retirement corrupted independent copy or retained old backing")
			}
			if _, err := enginetest.Execute(e, ctx, enginetest.Statement(t, "CREATE DATABASE unit_template"), nil); err != nil {
				t.Fatal(err)
			}
			fresh, err := e.SelectDatabase(ctx, "unit_template")
			if err != nil {
				t.Fatal(err)
			}
			if fresh == source || fresh.Catalog.DatabaseAddress() == oldAddress || fresh.Catalog.Domain == oldDomain || fresh.CatalogRead().Get(local) != nil {
				t.Fatal("same-name recreation adopted the dropped source incarnation")
			}
		})
	}
}

func databaseDropInterlockReceive(t *testing.T, result <-chan error) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), enginetest.Liveness)
	defer cancel()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		t.Fatal("database interlock worker did not finish", ctx.Err())
		return ctx.Err()
	}
}

func databaseDropTemplateUnchanged(t *testing.T, e, source *core.Engine) func() {
	t.Helper()
	roots := databaseCreateUnitUnchanged(t, e)
	roles, nextPrincipal, manager := e.RoleStore.Published, e.RoleStore.NextPrincipal(), e.Locks
	var checks []func()
	for _, target := range []*core.Engine{e, source} {
		heaps, indexes := maps.Clone(target.Storage.Relations), maps.Clone(target.Storage.IndexRoots)
		sequences, metadata := maps.Clone(target.Storage.Sequences), maps.Clone(target.Storage.RelationMetadata)
		row, version := target.Storage.RowFrontier(), target.Storage.VersionFrontier()
		oid, revision := target.Catalog.NextOID(), target.Catalog.NextRevision()
		checks = append(checks, func() {
			if !maps.Equal(target.Storage.Relations, heaps) || !maps.Equal(target.Storage.IndexRoots, indexes) || !maps.Equal(target.Storage.Sequences, sequences) || !maps.Equal(target.Storage.RelationMetadata, metadata) || target.Storage.RowFrontier() != row || target.Storage.VersionFrontier() != version || target.Catalog.NextOID() != oid || target.Catalog.NextRevision() != revision {
				t.Fatal("waiting/canceled DROP changed source or peer backing/frontiers")
			}
		})
	}
	return func() {
		t.Helper()
		roots()
		if e.RoleStore.Published != roles || e.RoleStore.NextPrincipal() != nextPrincipal || e.Locks != manager {
			t.Fatal("waiting/canceled DROP changed role or lock authority")
		}
		for _, check := range checks {
			check()
		}
	}
}
