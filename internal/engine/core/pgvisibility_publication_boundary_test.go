package core_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func visibilityBoundarySQL(t *testing.T, s *session.Session, q string) {
	t.Helper()
	result := enginetest.SessionSQL(t, s, q)
	if result.Rows == nil {
		return
	}
	values := make([]sqltype.Value, len(result.Rows.Schema()))
	for {
		present, err := result.Rows.Next(t.Context(), values)
		if err != nil {
			_ = result.Rows.Close()
			t.Fatal(err)
		}
		if !present {
			break
		}
	}
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
}

// This is the same cooperative, outside-lock MergeIndexRoot boundary used by
// the serializable publication tests, selected by stack identity, not counts.
type visibilityPublicationGate struct {
	context.Context
	once             sync.Once
	entered, release chan struct{}
}

func (g *visibilityPublicationGate) Err() error {
	var pcs [48]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, ".MergeIndexRoot") {
			g.once.Do(func() {
				close(g.entered)
				select {
				case <-g.release:
				case <-g.Context.Done():
				}
			})
			break
		}
		if !more {
			break
		}
	}
	return g.Context.Err()
}

func TestPgvisibilityCheckDetachedPublication(t *testing.T) {
	for _, phase := range []string{"detached_index_merge", "foreign_settlement_reentry"} {
		t.Run(phase, func(t *testing.T) {
			e := engine.New()
			actor, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				"CREATE EXTENSION pg_visibility", "CREATE TYPE visibility_boundary_mood AS ENUM ('old','other')",
				"CREATE TABLE visibility_boundary_heap(n visibility_boundary_mood)", "INSERT INTO visibility_boundary_heap VALUES('old')", "VACUUM (FREEZE) visibility_boundary_heap",
				"SELECT * FROM pg_visibility_map_summary('visibility_boundary_heap'::regclass)",
				"CREATE TABLE visibility_boundary_aux(n integer)", "CREATE INDEX visibility_boundary_idx ON visibility_boundary_aux(n)", "INSERT INTO visibility_boundary_aux VALUES(0)",
			} {
				visibilityBoundarySQL(t, peer, q)
			}
			view := e.CatalogRead()
			ref := enginetest.CatalogRef(t, e, view, "visibility_boundary_heap")
			aux := enginetest.CatalogRef(t, e, view, "visibility_boundary_aux")
			idx := enginetest.CatalogRef(t, e, view, "visibility_boundary_idx")
			original, auxiliary, indexRoot := e.Storage.Relations[ref.Address], e.Storage.Relations[aux.Address], e.Storage.IndexRoots[idx.Address]
			root := original.Visibility
			if root == nil || root.Invalidated || root.Map.AllVisible != 1 {
				t.Fatal("missing actual marked predecessor")
			}
			enginetest.SessionSQL(t, actor, "BEGIN")
			defer func() {
				if actor.TransactionState() != session.TxIdle {
					enginetest.SessionSQL(t, actor, "ROLLBACK")
				}
			}()
			enginetest.SessionSQL(t, actor, "ALTER TYPE visibility_boundary_mood RENAME VALUE 'old' TO 'renamed'")
			change := actor.Transactions.Catalog.Storage[ref.Address]
			if !change.PreservesVisibility || change.ObservedVisibility != root || change.Replacement == original || change.Replacement.Visibility != root {
				t.Fatal("not an actual preserving journal", change)
			}
			// The pending indexed write is on a different relation. It creates
			// real detached merge demand without blocking legal AX on the copy source.
			enginetest.SessionSQL(t, actor, "INSERT INTO visibility_boundary_aux VALUES(7)")
			base := e.CatalogRead().State()
			rows, ends, headers, generation := auxiliary.Versions, auxiliary.Ends, auxiliary.Headers, auxiliary.Generation
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			entered, release := make(chan struct{}), make(chan struct{})
			var unblockOnce sync.Once
			unblock := func() { unblockOnce.Do(func() { close(release) }) }
			var remote *fdwChainTransaction
			if phase == "foreign_settlement_reentry" {
				// Reuse the established transaction participant adapter and genuine
				// commitForeign lifecycle, not a replacement pg_visibility host.
				remote = &fdwChainTransaction{}
				remote.commit = func() error {
					close(entered)
					select {
					case <-release:
						return nil
					case <-ctx.Done():
						return ctx.Err()
					}
				}
				core.OwnerExtensionForeignRef(core.OwnerExtensionOf(actor.Transactions)).Enlist(&fdw.Participant{Tx: remote})
			}
			op, err := actor.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "detached_index_merge" {
				op.Ctx = &visibilityPublicationGate{Context: op.Context(), entered: entered, release: release}
			}
			done := make(chan error, 1)
			joined := false
			defer func() {
				unblock()
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(30 * time.Second):
						t.Error("publication worker did not join")
					}
				}
			}()
			go func() {
				err := core.EnginePublishRows(e, op, actor.Transactions.Ref, actor.Transactions.Writes, actor.Transactions.Deletes, actor.Transactions.Catalog, nil, nil)
				op.Done()
				done <- err
			}()
			select {
			case <-entered:
			case err := <-done:
				joined = true
				t.Fatal("required real publication boundary not reached", err)
			case <-time.After(30 * time.Second):
				t.Fatal("publication boundary was not reached")
			}
			// Initial admission has happened; settlement additionally follows the
			// first final fence. Only final readmission can reject this mutation.
			visibilityBoundarySQL(t, peer, "SELECT pg_truncate_visibility_map('visibility_boundary_heap'::regclass)")
			empty := original.Visibility
			if empty == root || !empty.Invalidated || change.Replacement.Visibility != root {
				t.Fatal("legal predecessor truncate or candidate isolation failed")
			}
			if e.CatalogRead().State() != base || e.Storage.Relations[ref.Address] != original || e.Storage.IndexRoots[idx.Address] != indexRoot || !storage.SameVersionRoot(auxiliary.Versions, rows) {
				t.Fatal("publication installed a prefix while detached")
			}
			unblock()
			select {
			case err = <-done:
				joined = true
			case <-time.After(30 * time.Second):
				t.Fatal("publication did not resume")
			}
			var conflict catalog.Conflict
			if !errors.As(err, &conflict) {
				t.Fatal("final preserving fence was bypassed", err)
			}
			if e.CatalogRead().State() != base || e.Storage.Relations[ref.Address] != original || original.Visibility != empty || !empty.Invalidated || e.Storage.Relations[aux.Address] != auxiliary || !storage.SameVersionRoot(auxiliary.Versions, rows) || auxiliary.Ends != ends || auxiliary.Headers != headers || auxiliary.Generation != generation || e.Storage.IndexRoots[idx.Address] != indexRoot {
				t.Fatal("final rejection installed catalog/heap/index prefix")
			}
			if root.Invalidated || root.Map.AllVisible != 1 || change.Replacement.Visibility != root {
				t.Fatal("retained predecessor/candidate mutated")
			}
			if remote != nil && (len(remote.calls) == 0 || remote.calls[len(remote.calls)-1] != "commit") {
				t.Fatal("real external settlement did not finish before final readmission", remote.calls)
			}
		})
	}
}
