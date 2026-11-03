package functions_test

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// An owned context pauses genuine detached layout work, not a replacement host
// or a test hook in production. Stack selection avoids coupling to Err call counts.
type visibilityCheckBarrier struct {
	context.Context
	once             sync.Once
	entered, release chan struct{}
}

func (c *visibilityCheckBarrier) Err() error {
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if strings.HasSuffix(frame.Function, "/storage.CaptureVisibility") || strings.HasSuffix(frame.Function, "/storage.PreparedVisibilityFacts.Capture") {
			c.once.Do(func() {
				close(c.entered)
				select {
				case <-c.release:
				case <-c.Context.Done():
				}
			})
			break
		}
		if !more {
			break
		}
	}
	return c.Context.Err()
}

type visibilityCheckOutcome struct {
	items []ids.VersionID
	err   error
}

func visibilityCheckCapture(ctx context.Context, e *core.Engine, s *session.Session, n plan.InstalledPgvisibilityExpr, ref ids.ObjectRef) (items []ids.VersionID, err error) {
	op, err := s.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		return nil, err
	}
	setup, err := parser.Parse("SELECT 1")
	if err == nil {
		result, failure := scope.Execute(setup[0], 0, nil)
		if result.Rows != nil {
			result.Rows.Close()
		}
		err = failure
	}
	if gate, ok := ctx.(*visibilityCheckBarrier); ok && err == nil {
		// Session admission owns a cancellable child. Wrap that ACTUAL active
		// context after setup so Err observes detached capture without replacing
		// its cancellation ownership or creating a parent/value cycle.
		op.Ctx = &visibilityCheckBarrier{Context: op.Context(), entered: gate.entered, release: gate.release}
	}
	if err == nil {
		items, err = core.HostOf(scope).Contrib().PgvisibilityCheck(n, uint32(ref.Address.ObjectID))
	}
	return items, scope.Finish(err)
}

func TestPgvisibilityCheckCaptureLifecycle(t *testing.T) {
	for _, phase := range []string{"map_root", "revoke", "replacement", "cancel"} {
		t.Run("detached_"+phase, func(t *testing.T) {
			e, ref := visibilityCheckFixture(t, true, false)
			for _, q := range []string{"CREATE ROLE visibility_check_reader", "GRANT USAGE ON SCHEMA public TO visibility_check_reader", "GRANT EXECUTE ON FUNCTION pg_check_visible(regclass) TO visibility_check_reader"} {
				enginetest.Run(t, e, q)
			}
			n := visibilityCheckNode(t, e, "pg_check_visible")
			observer := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, observer, "SET SESSION AUTHORIZATION visibility_check_reader")
			// Materialize a real prior root, so replacing even an equal-valued
			// capture cannot be mistaken for benign concurrent first publication.
			items, err := visibilityCheckCapture(t.Context(), e, observer, n, ref)
			if err != nil || len(items) != 0 {
				t.Fatal(items, err)
			}
			original := e.Storage.Relations[ref.Address]
			oldRoot := original.Visibility
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			gate := &visibilityCheckBarrier{Context: ctx, entered: make(chan struct{}), release: make(chan struct{})}
			var release sync.Once
			unblock := func() { release.Do(func() { close(gate.release) }) }
			done := make(chan visibilityCheckOutcome, 1)
			joined := false
			defer func() {
				unblock()
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(30 * time.Second):
						t.Error("capture worker failed to join")
					}
				}
			}()
			go func() {
				items, err := visibilityCheckCapture(gate, e, observer, n, ref)
				done <- visibilityCheckOutcome{items, err}
			}()
			select {
			case <-gate.entered:
			case out := <-done:
				joined = true
				t.Fatal("capture missed detached work", out)
			case <-time.After(30 * time.Second):
				t.Fatal("detached capture not reached")
			}
			// Prove both owner/Engine and manager mutexes are free while real
			// layout work is detached. This uses actual ownership, no fake locks.
			free := make(chan struct{})
			go func() {
				observer.Transactions.Mu.Lock()
				e.Mu.Lock()
				e.Mu.Unlock()
				observer.Transactions.Mu.Unlock()
				e.Locks.Inspect()
				close(free)
			}()
			defer func() {
				unblock()
				select {
				case <-free:
				case <-time.After(30 * time.Second):
					t.Error("mutex proof worker failed to join")
				}
			}()
			select {
			case <-free:
			case <-time.After(30 * time.Second):
				t.Fatal("capture held an owner/engine/manager mutex during layout")
			}
			switch phase {
			case "map_root":
				e.Mu.Lock()
				original.Visibility = storage.EmptyVisibilityRoot()
				e.Mu.Unlock()
			case "revoke":
				enginetest.Run(t, e, "REVOKE EXECUTE ON FUNCTION pg_check_visible(regclass) FROM visibility_check_reader")
			case "replacement":
				e.Mu.Lock()
				replacement := *original
				e.Storage.InstallRelationLocked(ref.Address, &replacement)
				e.Mu.Unlock()
			case "cancel":
				cancel()
			}
			unblock()
			var out visibilityCheckOutcome
			select {
			case out = <-done:
				joined = true
			case <-time.After(30 * time.Second):
				t.Fatal("capture did not finish")
			}
			if out.items != nil {
				t.Fatal("failed admission published finding prefix", out.items)
			}
			switch phase {
			case "cancel":
				if !errors.Is(out.err, context.Canceled) {
					t.Fatal(out.err)
				}
			case "revoke":
				var diagnostic *sqlstate.Error
				if !errors.As(out.err, &diagnostic) || diagnostic.Code != "42501" {
					t.Fatal("current EXECUTE not readmitted", out.err)
				}
			default:
				if !errors.Is(out.err, ids.ErrCatalogReplaced) {
					t.Fatal("root/incarnation not fenced", out.err)
				}
			}
			if oldRoot.Invalidated || oldRoot.Map.AllVisible != 1 {
				t.Fatal("captured immutable root changed")
			}
			if phase == "map_root" && !original.Visibility.Invalidated {
				t.Fatal("stale capture republished positives after truncate")
			}
			if phase != "replacement" {
				enginetest.Run(t, e, "GRANT EXECUTE ON FUNCTION pg_check_visible(regclass) TO visibility_check_reader")
				items, err := visibilityCheckCapture(t.Context(), e, observer, n, ref)
				if err != nil || len(items) != 0 {
					t.Fatal("later admission failed", items, err)
				}
				holder := enginetest.StartSession(t, e, nil)
				for _, q := range []string{"BEGIN", "LOCK TABLE visibility_heap IN ACCESS EXCLUSIVE MODE NOWAIT", "ROLLBACK"} {
					enginetest.SessionSQL(t, holder, q)
				}
			}
		})
	}
	t.Run("physical_invalidation_and_noop_vacuum", visibilityCheckVacuumState)
	t.Run("prepared_private_owner", visibilityCheckPreparedOwner)
	t.Run("owner_free_private_basis", visibilityCheckPrivateIdentity)
	t.Run("producer_catalog_map_fence", visibilityCheckCatalogMap)
	t.Run("preserving_codec_publication", visibilityCheckCodecPublication)
}

func visibilityCheckVacuumState(t *testing.T) {
	e, ref := visibilityCheckFixture(t, true, false)
	store := e.Storage.Relations[ref.Address]
	observer := enginetest.StartSession(t, e, nil)
	scope, finish := visibilityScope(t, e, observer)
	read := visibilityCheckNode(t, e, "pg_visibility_map_summary")
	old, err := core.HostOf(scope).Contrib().Pgvisibility(read, uint32(ref.Address.ObjectID), 0)
	if err != nil || old.AllVisible != 1 {
		t.Fatal(old, err)
	}
	root := store.Visibility
	captured := table.VacuumCapture{Rows: store.Versions, Ends: store.Ends, Retained: store.Retained, Discarded: store.Discarded, Headers: store.Headers, Visibility: root, Generation: store.Generation, Frontier: e.Visibility.CommitSeq(), Cutoff: e.Visibility.LastAssignedXID() + 1, Retire: true, Catalog: e.Catalog.State, Relation: e.CatalogRead().MustRelation(ref.Address)}
	pass, err := table.PrepareVacuumPass(t.Context(), captured, store, func(table.VacuumProgress) {})
	if err != nil {
		t.Fatal(err)
	}
	if pass.Generation != store.Generation || !storage.SameVersionRoot(pass.Candidate.Versions, store.Versions) || pass.Candidate.Headers != store.Headers {
		t.Fatal("fixture is not an unchanged VACUUM pass")
	}
	truncate := visibilityCheckNode(t, e, "pg_truncate_visibility_map")
	if err := core.HostOf(scope).Contrib().PgvisibilityTruncate(truncate, uint32(ref.Address.ObjectID)); err != nil {
		t.Fatal(err)
	}
	current := captured
	current.Visibility = store.Visibility
	if captured.SameLocked(current, &e.Storage, false) {
		t.Fatal("detached pre-truncate VACUUM readmitted")
	}
	rewrite, err := storage.NewRewriteRelation(pass.Candidate, store.Generation, store.Maintenance)
	if err != nil || rewrite == store || rewrite.Visibility != nil || !store.Visibility.Invalidated {
		t.Fatal("destructive rewrite inherited physical VM history", rewrite, err)
	}
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
	versions, headers, generation := store.Versions, store.Headers, store.Generation
	enginetest.Run(t, e, "VACUUM visibility_heap")
	if store.Visibility != nil || !storage.SameVersionRoot(versions, store.Versions) || headers != store.Headers || generation != store.Generation {
		t.Fatal("successful no-op VACUUM failed to reenable without changing heap")
	}
	// Every later ordinary generation/header change preserves explicit empty VM.
	enginetest.SessionSQL(t, observer, "BEGIN")
	scope, finish = visibilityScope(t, e, observer)
	if err := core.HostOf(scope).Contrib().PgvisibilityTruncate(truncate, uint32(ref.Address.ObjectID)); err != nil {
		t.Fatal(err)
	}
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
	enginetest.SessionSQL(t, observer, "ROLLBACK")
	if !store.Visibility.Invalidated {
		t.Fatal("transaction rollback reconstructed VM")
	}
	invalidated := store.Visibility
	// Real rejected and canceled VACUUM entry must leave the exact physical
	// invalidation root installed, not merely return equal zero counts.
	enginetest.SessionSQL(t, observer, "BEGIN")
	scope, finish = visibilityScope(t, e, observer)
	result, failure := scope.Execute(enginetest.Statement(t, "VACUUM visibility_heap"), 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	failure = finish(failure)
	var diagnostic *sqlstate.Error
	if !errors.As(failure, &diagnostic) || diagnostic.Code != "25001" || store.Visibility != invalidated {
		t.Fatal("failed VACUUM changed VM root", failure)
	}
	enginetest.SessionSQL(t, observer, "ROLLBACK")
	func() {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		op, err := observer.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		result, err := scope.Execute(enginetest.Statement(t, "VACUUM visibility_heap"), 0, nil)
		if result.Rows != nil {
			result.Rows.Close()
		}
		err = scope.Finish(err)
		if !errors.Is(err, context.Canceled) || store.Visibility != invalidated {
			t.Fatal("canceled VACUUM changed VM root", err)
		}
	}()
	for _, q := range []string{"CREATE ROLE visibility_vacuum_denied", "GRANT USAGE ON SCHEMA public TO visibility_vacuum_denied"} {
		enginetest.Run(t, e, q)
	}
	for _, q := range []string{"SET SESSION AUTHORIZATION visibility_vacuum_denied", "VACUUM visibility_heap", "RESET SESSION AUTHORIZATION"} {
		enginetest.SessionSQL(t, observer, q)
	}
	if store.Visibility != invalidated {
		t.Fatal("unauthorized skipped VACUUM changed VM root")
	}
	holder := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, holder, "BEGIN")
	defer enginetest.SessionSQL(t, holder, "ROLLBACK")
	enginetest.SessionSQL(t, holder, "LOCK TABLE visibility_heap IN SHARE MODE")
	enginetest.SessionSQL(t, observer, "VACUUM (SKIP_LOCKED) visibility_heap")
	if store.Visibility != invalidated {
		t.Fatal("lock-skipped VACUUM changed VM root")
	}
	enginetest.SessionSQL(t, holder, "ROLLBACK")
	enginetest.Run(t, e, "INSERT INTO visibility_heap VALUES(2)")
	if store.Generation == generation || !store.Visibility.Invalidated {
		t.Fatal("DML cleared physical invalidation")
	}
	scope, finish = visibilityScope(t, e, observer)
	mapAfter, err := core.HostOf(scope).Contrib().Pgvisibility(read, uint32(ref.Address.ObjectID), 0)
	if err != nil || mapAfter.AllVisible != 0 || mapAfter.AllFrozen != 0 || mapAfter.MappedBlocks != 0 {
		t.Fatal("fresh capture rebuilt invalidated map", mapAfter, err)
	}
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
	if root.Invalidated || root.Map.AllVisible != 1 {
		t.Fatal("old map root mutated")
	}
	enginetest.Run(t, e, "VACUUM (FREEZE) visibility_heap")
	if store.Visibility != nil {
		t.Fatal("successful VACUUM did not reset explicit invalidation")
	}
}

func visibilityCheckPreparedOwner(t *testing.T) {
	e, ref := visibilityCheckFixture(t, true, false)
	writer, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, writer, "BEGIN")
	enginetest.SessionSQL(t, writer, "INSERT INTO visibility_heap VALUES(2)")
	version := writer.Transactions.Writes[ref.Address][0].Version
	prepared := false
	defer func() {
		if prepared {
			enginetest.SessionSQL(t, observer, "ROLLBACK PREPARED 'visibility_check_prepared'")
		} else if writer.TransactionState() != session.TxIdle {
			enginetest.SessionSQL(t, writer, "ROLLBACK")
		}
	}()
	var previous *storage.VisibilityRoot
	check := func() {
		scope, finish := visibilityScope(t, e, observer)
		defer finish(nil)
		root, facts := visibilityCheckMark(t, e, scope, ref, true, true)
		if previous != nil && !previous.Basis.Same(e.Storage.Relations[ref.Address], facts) {
			t.Fatal("prepared transfer/session close changed detached private identity")
		}
		previous = root
		visibilityCheckFindings(t, e, scope, ref, "pg_check_visible", []ids.VersionID{version})
		visibilityCheckFindings(t, e, scope, ref, "pg_check_frozen", []ids.VersionID{version})
		if err := finish(nil); err != nil {
			t.Fatal(err)
		}
	}
	check()
	enginetest.SessionSQL(t, writer, "PREPARE TRANSACTION 'visibility_check_prepared'")
	prepared = true
	writer.RequestEnd(session.CleanClose)
	if err := writer.WaitEnd(t.Context()); err != nil {
		t.Fatal(err)
	}
	check()
	enginetest.SessionSQL(t, observer, "ROLLBACK PREPARED 'visibility_check_prepared'")
	prepared = false
}
