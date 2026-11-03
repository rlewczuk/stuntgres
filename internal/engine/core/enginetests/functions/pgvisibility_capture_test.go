package functions_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/contrib"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func visibilityCaptureFixture(t *testing.T) (*core.Engine, ids.ObjectRef, plan.InstalledPgvisibilityExpr) {
	t.Helper()
	e := engine.New()
	for _, q := range []string{"CREATE EXTENSION pg_visibility", "CREATE TABLE visibility_heap(n integer)", "INSERT INTO visibility_heap VALUES(1)", "VACUUM (FREEZE) visibility_heap"} {
		enginetest.Run(t, e, q)
	}
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), "visibility_heap")
	binder := enginetest.ExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	node, err := binder.BindFunctionProvider(enginetest.FunctionCall("pg_visibility_map_summary", enginetest.StringLiteral("-")))
	if err != nil {
		t.Fatal(err)
	}
	return e, ref, node.(plan.InstalledPgvisibilityExpr)
}
func visibilityScope(t *testing.T, e *core.Engine, s *session.Session) (*core.StatementScope, func(error) error) {
	t.Helper()
	op, err := s.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	finished := false
	finish := func(failure error) error {
		if finished {
			return failure
		}
		finished = true
		err := scope.Finish(failure)
		op.Done()
		return err
	}
	t.Cleanup(func() { finish(nil) })
	// Acquire a genuine current command snapshot through ordinary setup. The
	// assertions below inspect owner roots/capabilities, not SQL result rows.
	result, err := scope.Execute(enginetest.Statement(t, "SELECT 1"), 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	if err != nil {
		finish(err)
		t.Fatal(err)
	}
	return scope, finish
}
func visibilityHost(scope *core.StatementScope) contrib.Host[core.AuthorizationView] {
	return core.HostOf(scope).(contrib.Host[core.AuthorizationView])
}
func visibilityFactsAt(t *testing.T, scope *core.StatementScope, ref ids.ObjectRef) ([]storage.HeapTupleFact, storage.VisibilityHorizon) {
	t.Helper()
	h := visibilityHost(scope)
	writes, deletes := h.ColumnProjectionRoots(ref.Address)
	owners, err := h.LockHeapStatisticsCohort()
	if err != nil {
		t.Fatal(err)
	}
	store := h.PrivateStorageLocked(ref.Address)
	facts := h.HeapStatisticsFactsLocked(owners, store, ref.Address, writes, deletes)
	horizon := h.PgvisibilityHorizonLocked(owners)
	h.UnlockHeapStatisticsCohortLocked(owners)
	return facts, horizon
}
func visibilityCapture(ctx context.Context, e *core.Engine, s *session.Session, n plan.InstalledPgvisibilityExpr, ref ids.ObjectRef) (storage.VisibilityMap, error) {
	op, err := s.Acquire(ctx)
	if err != nil {
		return storage.VisibilityMap{}, err
	}
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		return storage.VisibilityMap{}, err
	}
	setup, err := parser.Parse("SELECT 1")
	if err != nil {
		return storage.VisibilityMap{}, scope.Finish(err)
	}
	result, err := scope.Execute(setup[0], 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	var captured storage.VisibilityMap
	if err == nil {
		captured, err = core.HostOf(scope).Contrib().Pgvisibility(n, uint32(ref.Address.ObjectID), 0)
	}
	return captured, scope.Finish(err)
}

func TestPgvisibilityCaptureOwnership(t *testing.T) {
	t.Run("physical_predecessors_and_prepared_owner", func(t *testing.T) {
		e, ref, n := visibilityCaptureFixture(t)
		original := e.Storage.Relations[ref.Address].Versions[0]
		writer, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		for _, q := range []string{"BEGIN", "UPDATE visibility_heap SET n=2", "SAVEPOINT kept", "UPDATE visibility_heap SET n=3"} {
			enginetest.SessionSQL(t, writer, q)
		}
		private := writer.Transactions.Writes[ref.Address][0]
		scope, finish := visibilityScope(t, e, observer)
		facts, _ := visibilityFactsAt(t, scope, ref)
		if len(facts) != 3 || private.PrivateHistory == nil || private.PrivateHistory.Next == nil {
			t.Fatal("fixture lacks genuine private predecessor chain", facts)
		}
		versions := map[ids.VersionID]bool{}
		for _, fact := range facts {
			versions[fact.Version.Version] = true
			if fact.Version.Version == original.Version && (&fact.Version.Values[0] != &original.Values[0] || fact.Ender != writer.Transactions.Ref) {
				t.Fatal("committed predecessor lost genuine backing/end owner")
			}
			if fact.Version.Born == 0 && fact.Inserter != writer.Transactions.Ref {
				t.Fatal("private version lost actual inserter")
			}
		}
		if !versions[original.Version] || !versions[private.Version] || !versions[private.PrivateHistory.Version.Version] {
			t.Fatal("capture deduplicated by RowID/value rather than VersionID")
		}
		if err := finish(nil); err != nil {
			t.Fatal(err)
		}
		enginetest.SessionSQL(t, writer, "ROLLBACK TO kept")
		if private.PrivateHistory.Next.Version.Version != original.Version || private.Version == writer.Transactions.Writes[ref.Address][0].Version {
			t.Fatal("rollback mutated retained private root")
		}
		for _, q := range []string{"UPDATE visibility_heap SET n=4", "DELETE FROM visibility_heap"} {
			enginetest.SessionSQL(t, writer, q)
		}
		before := writer.Transactions.Writes[ref.Address][0]
		scope, finish = visibilityScope(t, e, observer)
		preparedFacts, _ := visibilityFactsAt(t, scope, ref)
		if err := finish(nil); err != nil {
			t.Fatal(err)
		}
		writerRef := writer.Transactions.Ref
		enginetest.SessionSQL(t, writer, "PREPARE TRANSACTION 'visibility_owner'")
		prepared := true
		defer func() {
			// A failed assertion can leave the latest observer scope active.
			// Release its admission before reentering that session for cleanup.
			if err := finish(nil); err != nil {
				t.Error(err)
			}
			if prepared {
				enginetest.SessionSQL(t, observer, "ROLLBACK PREPARED 'visibility_owner'")
			}
		}()
		if writer.Transactions.Writes != nil || writer.TransactionState() != session.TxIdle {
			t.Fatal("origin retained prepared ownership")
		}
		writer.RequestEnd(session.CleanClose)
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		err := writer.WaitEnd(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		scope, finish = visibilityScope(t, e, observer)
		after, _ := visibilityFactsAt(t, scope, ref)
		if len(after) != len(preparedFacts) {
			t.Fatal("detached prepared versions disappeared or duplicated", len(after), len(preparedFacts))
		}
		for _, fact := range after {
			if fact.Aborted {
				if fact.Inserter != (ids.TxRef{}) || fact.Ender != (ids.TxRef{}) {
					t.Fatal("discarded capture retained live ownership")
				}
			} else if fact.Version.Born == 0 && fact.Inserter != writerRef {
				t.Fatal("prepared capture invented an owner")
			}
		}
		if err := finish(nil); err != nil {
			t.Fatal(err)
		}
		capture, err := visibilityCapture(t.Context(), e, observer, n, ref)
		if err != nil || capture.AllVisible != 0 || capture.AllFrozen != 0 || capture.Blocks == 0 {
			t.Fatal("detached prepared universal proof", capture, err)
		}
		enginetest.SessionSQL(t, observer, "ROLLBACK PREPARED 'visibility_owner'")
		prepared = false
		enginetest.Run(t, e, "VACUUM (FREEZE) visibility_heap")
		if before.PrivateHistory == nil || private.PrivateHistory.Next.Version.Version != original.Version || &facts[0].Version.Values[0] != &original.Values[0] {
			t.Fatal("maintenance changed retained physical captures")
		}
	})
	t.Run("caller_stage_and_peer_merge", func(t *testing.T) {
		e, ref, _ := visibilityCaptureFixture(t)
		writer, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, writer, "BEGIN")
		op := enginetest.TransactionOperation(t, writer)
		active, err := e.StartStatement(op)
		if err != nil {
			op.Done()
			t.Fatal(err)
		}
		finished := false
		defer func() {
			if !finished {
				active.Finish(nil)
				op.Done()
			}
		}()
		_, err = active.Execute(enginetest.Statement(t, "UPDATE visibility_heap SET n=2"), 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(active.Stage.Rows[ref.Address]) != 1 || len(writer.Transactions.Writes[ref.Address]) != 0 {
			t.Fatal("not at genuine statement-stage boundary")
		}
		own, _ := visibilityFactsAt(t, active, ref)
		peerScope, peerFinish := visibilityScope(t, e, observer)
		peer, _ := visibilityFactsAt(t, peerScope, ref)
		if len(own) != 2 || len(peer) != 1 {
			t.Fatal("caller stage omitted or peer unpublished stage leaked", len(own), len(peer))
		}
		peerFinish(nil)
		err = active.Finish(nil)
		op.Done()
		finished = true
		if err != nil {
			t.Fatal(err)
		}
		peerScope, peerFinish = visibilityScope(t, e, observer)
		merged, _ := visibilityFactsAt(t, peerScope, ref)
		peerFinish(nil)
		if len(merged) != 2 || len(writer.Transactions.Writes[ref.Address]) != 1 {
			t.Fatal("atomic owner merge lost private physical versions")
		}
		enginetest.SessionSQL(t, writer, "ROLLBACK")
	})
	t.Run("known_frontier_unknown_rc_and_detached_view", visibilitySnapshotOwnership)
}

func visibilitySnapshotOwnership(t *testing.T) {
	e, ref, n := visibilityCaptureFixture(t)
	// Retain an actual immutable View past its scope; it is not an advertised
	// active snapshot over future current heaps and must not create a registry.
	observer := enginetest.StartSession(t, e, nil)
	scope, finish := visibilityScope(t, e, observer)
	h := visibilityHost(scope)
	e.Mu.Lock()
	view, local, err := h.RelationDataViewLocked(ref)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	retained := view.Retain()
	if local {
		view.Release()
	}
	defer retained.Release()
	old := retained.Data().Committed[ref.Address][0]
	if err := finish(nil); err != nil {
		t.Fatal(err)
	}
	// Advance real writer ordering without advancing the committed row
	// frontier. The later genuine Xmin will exceed the new tuple's Born.
	burner := enginetest.StartSession(t, e, nil)
	for range 32 {
		for _, q := range []string{"BEGIN", "INSERT INTO visibility_heap VALUES(99)", "ROLLBACK"} {
			enginetest.SessionSQL(t, burner, q)
		}
	}
	reader := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY")
	enginetest.SessionSQL(t, reader, "SELECT n FROM visibility_heap")
	frontier := reader.Transactions.Frontier
	if reader.Transactions.Ticket.AssignedFullXID() != 0 || reader.Transactions.CurrentSnapshot == nil {
		t.Fatal("fixture assigned read-only XID or omitted retained snapshot")
	}
	enginetest.Run(t, e, "INSERT INTO visibility_heap VALUES(2)")
	enginetest.Run(t, e, "VACUUM (FREEZE) visibility_heap")
	current := e.Storage.Relations[ref.Address]
	if !current.Versions[1].Frozen || current.Versions[1].Born <= frontier {
		t.Fatal("fixture did not distinguish Frozen from protected CommitSeq")
	}
	e.Mu.Lock()
	entry := e.Visibility.Entries[reader.Transactions.Ref]
	e.Mu.Unlock()
	if entry.Xmin <= uint64(current.Versions[1].Born) {
		t.Fatal("fixture failed to separate real Xmin from CommitSeq", entry.Xmin, current.Versions[1].Born)
	}
	scope, finish = visibilityScope(t, e, observer)
	_, horizon := visibilityFactsAt(t, scope, ref)
	if !horizon.Protected || horizon.Unknown || horizon.Frontier != frontier {
		t.Fatal("old read-only frontier not captured", horizon, frontier)
	}
	finish(nil)
	capture, err := visibilityCapture(t.Context(), e, observer, n, ref)
	if err != nil || capture.AllVisible != 0 || capture.MappedBlocks == 0 {
		t.Fatal("Frozen bypassed protected frontier or erased VM layout", capture, err)
	}
	enginetest.SessionSQL(t, reader, "ROLLBACK")
	enginetest.Run(t, e, "VACUUM (FREEZE) visibility_heap")
	capture, err = visibilityCapture(t.Context(), e, observer, n, ref)
	if err != nil || capture.AllVisible != 1 || capture.AllFrozen != 1 {
		t.Fatal("detached old View was treated as an ongoing snapshot", capture, err)
	}
	if retained.Data().Committed[ref.Address][0].Version != old.Version || &retained.Data().Committed[ref.Address][0].Values[0] != &old.Values[0] {
		t.Fatal("maintenance changed retained immutable view")
	}
	// A currently active RC command has a real pair, while its owner's
	// SnapshotSet/Frontier is not that pair. Its peer remains unknown.
	enginetest.SessionSQL(t, reader, "BEGIN")
	rc, rcFinish := visibilityScope(t, e, reader)
	reader.Transactions.Mu.Lock()
	saved := reader.Transactions.Frontier
	reader.Transactions.Frontier = 0
	reader.Transactions.Mu.Unlock()
	_, own := visibilityFactsAt(t, rc, ref)
	peer, peerFinish := visibilityScope(t, e, observer)
	_, unknown := visibilityFactsAt(t, peer, ref)
	peerFinish(nil)
	reader.Transactions.Mu.Lock()
	reader.Transactions.Frontier = saved
	reader.Transactions.Mu.Unlock()
	if own.Unknown || !own.Protected || own.Frontier < current.Versions[1].Born || !unknown.Unknown {
		t.Fatal("RC owner fields substituted for actual command pair", own, unknown)
	}
	rcFinish(nil)
	enginetest.SessionSQL(t, reader, "ROLLBACK")
	before := retained.Data()
	if _, err := e.ReplaceCatalog(t.Context(), e, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	if retained.Data() != before || retained.Data().Committed[ref.Address][0].Version != old.Version {
		t.Fatal("replacement changed a detached View")
	}
}

type visibilityCaptureOutcome struct {
	capture storage.VisibilityMap
	err     error
}

func visibilityWait(t *testing.T, e *core.Engine, ref ids.ObjectRef, s *session.Session, done <-chan visibilityCaptureOutcome) *visibilityCaptureOutcome {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		for resource, state := range e.Locks.Inspect().Resources {
			if resource.ObjectRef != ref || resource.Row != 0 {
				continue
			}
			for _, waiter := range state.Pending {
				if waiter.Ticket.Session() == s.SessionToken && waiter.Mode == lock.AccessShare && !waiter.Terminal {
					return nil
				}
			}
		}
		select {
		case result := <-done:
			return &result
		case <-deadline.C:
			t.Fatal("exact visibility AccessShare waiter not admitted")
		default:
			runtime.Gosched()
		}
	}
}

func TestPgvisibilityCaptureLifecycle(t *testing.T) {
	t.Run("partitioned_index_wrongkind", func(t *testing.T) {
		e, _, _ := visibilityCaptureFixture(t)
		enginetest.Run(t, e, "CREATE TABLE visibility_parent(n integer) PARTITION BY RANGE(n)")
		enginetest.Run(t, e, "CREATE INDEX visibility_index ON visibility_heap(n)")
		view := e.CatalogRead()
		parent := enginetest.CatalogRef(t, e, view, "visibility_parent")
		index := enginetest.CatalogRef(t, e, view, "visibility_index")
		target := *view.MustRelation(index.Address)
		if view.MustRelation(parent.Address).Partition == nil || target.SQLIndex == nil {
			t.Fatal("missing genuine parent/index descriptor fixture")
		}
		// Public partitioned-index construction is unavailable. Publish an owned
		// copy of the real index descriptor with its genuine partitioned parent;
		// invoke the actual installed host, not an inspection export or fake error.
		definition := *target.SQLIndex
		definition.Parent = parent.Address
		target.SQLIndex = &definition
		target.ObjectRevision = target.Revision() + 1
		before := e.Catalog.State
		t.Cleanup(func() { e.Mu.Lock(); e.Catalog.PublishLocked(before); e.Mu.Unlock() })
		state := catalog.CloneState(before)
		state.SetDescriptor(index.Address, &target)
		e.Mu.Lock()
		e.Catalog.PublishLocked(state)
		e.Mu.Unlock()
		observer := enginetest.StartSession(t, e, nil)
		for _, spec := range []struct {
			name  string
			block bool
		}{
			{"pg_visibility_map", false}, {"pg_visibility", false}, {"pg_visibility_map_summary", false},
			{"pg_visibility_map", true}, {"pg_visibility", true},
			{"pg_check_visible", false}, {"pg_check_frozen", false}, {"pg_truncate_visibility_map", false},
		} {
			binder := enginetest.ExpressionBinder(t, t.Context(), e, sqltype.InputEnvironment{}, nil)
			binder.Clause.AggregateClause = "FROM"
			binder.Query.Namespace = &analyze.QueryNamespace{}
			args := []parser.Expr{enginetest.StringLiteral("-")}
			if spec.block {
				args = append(args, parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: -1}})
			}
			node, err := binder.BindFunctionSource(enginetest.FunctionCall(spec.name, args...))
			if err != nil {
				t.Fatal(err)
			}
			scope, finish := visibilityScope(t, e, observer)
			installed := node.(plan.InstalledPgvisibilityExpr)
			var capture storage.VisibilityMap
			switch {
			case installed.Kind.Check():
				var findings []ids.VersionID
				findings, err = core.HostOf(scope).Contrib().PgvisibilityCheck(installed, uint32(index.Address.ObjectID))
				if findings != nil {
					t.Fatal("wrong-kind check published findings", findings)
				}
			case installed.Kind == functions.VisibilityTruncateMap:
				err = core.HostOf(scope).Contrib().PgvisibilityTruncate(installed, uint32(index.Address.ObjectID))
			default:
				capture, err = contrib.Pgvisibility(visibilityHost(scope), installed, uint32(index.Address.ObjectID), -1)
			}
			err = finish(err)
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `relation "visibility_index" is of wrong relation kind`, Detail: "This operation is not supported for partitioned indexes."}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(*diagnostic, want) {
				t.Fatalf("%s block=%t full diagnostic = %#v (%v), want %#v", spec.name, spec.block, diagnostic, err, want)
			}
			if !reflect.DeepEqual(capture, storage.VisibilityMap{}) {
				t.Fatal("wrong-kind capture published a result prefix", capture)
			}
		}
	})
	for _, phase := range []string{"cancel", "revoke", "replacement"} {
		t.Run(phase, func(t *testing.T) {
			e, ref, n := visibilityCaptureFixture(t)
			for _, q := range []string{"CREATE ROLE visibility_reader", "GRANT USAGE ON SCHEMA public TO visibility_reader", "GRANT EXECUTE ON FUNCTION pg_visibility_map_summary(regclass) TO visibility_reader"} {
				enginetest.Run(t, e, q)
			}
			holder, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, observer, "SET SESSION AUTHORIZATION visibility_reader")
			enginetest.SessionSQL(t, holder, "BEGIN")
			enginetest.SessionSQL(t, holder, "LOCK TABLE visibility_heap IN ACCESS EXCLUSIVE MODE")
			defer func() { enginetest.SessionSQL(t, holder, "ROLLBACK") }()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan visibilityCaptureOutcome, 1)
			joined := false
			defer func() {
				cancel()
				if !joined {
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("visibility worker failed to join")
					}
				}
			}()
			go func() {
				capture, err := visibilityCapture(ctx, e, observer, n, ref)
				done <- visibilityCaptureOutcome{capture, err}
			}()
			if early := visibilityWait(t, e, ref, observer, done); early != nil {
				joined = true
				t.Fatal("capture completed before exact lock admission", early.err)
			}
			switch phase {
			case "cancel":
				cancel()
			case "revoke":
				enginetest.Run(t, e, "REVOKE EXECUTE ON FUNCTION pg_visibility_map_summary(regclass) FROM visibility_reader")
			case "replacement":
				if _, err := e.ReplaceCatalog(t.Context(), e, e.CatalogRead()); err != nil {
					t.Fatal(err)
				}
			}
			enginetest.SessionSQL(t, holder, "ROLLBACK")
			var result visibilityCaptureOutcome
			select {
			case result = <-done:
				joined = true
			case <-time.After(5 * time.Second):
				t.Fatal("visibility worker did not finish")
			}
			if result.capture.Blocks != 0 || result.capture.Runs != nil {
				t.Fatal("failed capture published result prefix", result)
			}
			switch phase {
			case "cancel":
				if !errors.Is(result.err, context.Canceled) {
					t.Fatal(result.err)
				}
			case "revoke":
				var diagnostic *sqlstate.Error
				if !errors.As(result.err, &diagnostic) || diagnostic.Code != "42501" {
					t.Fatal("postwait authority not readmitted", result.err)
				}
			case "replacement":
				if !errors.Is(result.err, ids.ErrCatalogReplaced) {
					t.Fatal("old epoch capture rebound", result.err)
				}
			}
			if phase != "replacement" {
				enginetest.Run(t, e, "GRANT EXECUTE ON FUNCTION pg_visibility_map_summary(regclass) TO visibility_reader")
				capture, err := visibilityCapture(t.Context(), e, observer, n, ref)
				if err != nil || capture.AllVisible != 1 {
					t.Fatal("later read admission leaked", capture, err)
				}
				enginetest.SessionSQL(t, holder, "BEGIN")
				enginetest.SessionSQL(t, holder, "SET LOCAL lock_timeout='1s'")
				enginetest.SessionSQL(t, holder, "LOCK TABLE visibility_heap IN ACCESS EXCLUSIVE MODE")
				enginetest.SessionSQL(t, holder, "ROLLBACK")
			}
		})
	}
	t.Run("opposing_cohort_captures", func(t *testing.T) {
		e, ref, n := visibilityCaptureFixture(t)
		a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		for i, s := range []*session.Session{a, b} {
			enginetest.SessionSQL(t, s, "BEGIN")
			enginetest.SessionSQL(t, s, fmt.Sprintf("INSERT INTO visibility_heap VALUES(%d)", i+2))
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		start := make(chan struct{})
		done := make(chan error, 2)
		remaining := 2
		defer func() {
			cancel()
			for remaining > 0 {
				select {
				case <-done:
					remaining--
				case <-time.After(5 * time.Second):
					t.Error("opposing captures failed to join")
					return
				}
			}
		}()
		for _, s := range []*session.Session{a, b} {
			go func(s *session.Session) {
				<-start
				capture, err := visibilityCapture(ctx, e, s, n, ref)
				if err == nil && (capture.AllVisible != 0 || capture.AllFrozen != 0) {
					err = fmt.Errorf("private cohort declared universal: %+v", capture)
				}
				done <- err
			}(s)
		}
		close(start)
		for remaining > 0 {
			select {
			case err := <-done:
				remaining--
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("opposing owner captures deadlocked")
			}
		}
		enginetest.SessionSQL(t, a, "COMMIT")
		enginetest.SessionSQL(t, b, "ROLLBACK")
		enginetest.Run(t, e, "VACUUM (FREEZE) visibility_heap")
		capture, err := visibilityCapture(t.Context(), e, a, n, ref)
		if err != nil || capture.AllVisible != 1 || capture.AllFrozen != 1 {
			t.Fatal(capture, err)
		}
	})
	t.Run("prepared_transfer_and_session_close", func(t *testing.T) {
		e, ref, n := visibilityCaptureFixture(t)
		writer, observer, closing := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, writer, "BEGIN")
		enginetest.SessionSQL(t, writer, "INSERT INTO visibility_heap VALUES(2)")
		ticket := writer.Transactions.Ticket
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		start := make(chan struct{})
		done := make(chan error, 3)
		remaining := 3
		prepared := false
		defer func() {
			cancel()
			for remaining > 0 {
				select {
				case <-done:
					remaining--
				case <-time.After(5 * time.Second):
					t.Error("prepared/capture/close workers failed to join")
					return
				}
			}
			// Inspect includes requested retired tickets, whose Detached bit is
			// historical. Only a still-live prepared owner needs actual rollback.
			if !ticket.OwnershipEnded() && (prepared || e.Locks.Inspect(ticket).Detached[ticket]) {
				enginetest.SessionSQL(t, observer, "ROLLBACK PREPARED 'visibility_transfer'")
			}
		}()
		go func() {
			<-start
			capture, err := visibilityCapture(ctx, e, observer, n, ref)
			if err == nil && (capture.AllVisible != 0 || capture.AllFrozen != 0) {
				err = fmt.Errorf("prepared transfer became universal: %+v", capture)
			}
			done <- err
		}()
		go func() {
			<-start
			op, err := writer.Acquire(ctx)
			if err != nil {
				done <- err
				return
			}
			scope, err := e.StartStatement(op)
			if err == nil {
				_, err = scope.Execute(&parser.TransactionControl{Kind: parser.KindPrepareTransaction, GID: "visibility_transfer"}, 0, nil)
				err = scope.Finish(err)
			}
			op.Done()
			done <- err
		}()
		go func() { <-start; closing.RequestEnd(session.CleanClose); done <- closing.WaitEnd(ctx) }()
		close(start)
		var failures []error
		for remaining > 0 {
			select {
			case err := <-done:
				remaining--
				if err != nil {
					failures = append(failures, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("prepared/capture/close deadlock")
			}
		}
		prepared = writer.TransactionState() == session.TxIdle && e.Locks.Inspect(ticket).Detached[ticket]
		if !prepared || len(failures) != 0 || ticket.OwnershipEnded() {
			t.Fatal("prepared ownership did not transfer through cohort discovery", prepared, failures)
		}
		writer.RequestEnd(session.CleanClose)
		if err := writer.WaitEnd(t.Context()); err != nil {
			t.Fatal(err)
		}
		capture, err := visibilityCapture(t.Context(), e, observer, n, ref)
		if err != nil || capture.AllVisible != 0 || capture.AllFrozen != 0 {
			t.Fatal("origin close dropped detached prepared facts", capture, err)
		}
		enginetest.SessionSQL(t, observer, "ROLLBACK PREPARED 'visibility_transfer'")
		prepared = false
		enginetest.Run(t, e, "VACUUM (FREEZE) visibility_heap")
	})
}

func TestPgvisibilityCaptureMaintenance(t *testing.T) {
	e, ref, n := visibilityCaptureFixture(t)
	store := e.Storage.Relations[ref.Address]
	original := store.Versions[0]
	bound := store.Maintenance.FrozenXID
	if !original.Frozen || original.HeapXmin() == 2 || original.HeapXmin() != uint32(original.InsertFullXID) || bound == 0 {
		t.Fatal("ordinary freezing lost raw insertion identity or maintenance provenance")
	}
	writer, observer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
	capture := func(wantFrozen int64) storage.VisibilityMap {
		t.Helper()
		result, err := visibilityCapture(t.Context(), e, observer, n, ref)
		if err != nil || result.AllVisible != 1 || result.AllFrozen != wantFrozen {
			t.Fatal("effective frozen-header/maintenance classification", result, err)
		}
		return result
	}
	raw := func() lock.TupleMaintenance {
		t.Helper()
		scope, finish := visibilityScope(t, e, observer)
		facts := []lock.TupleMaintenance{{Tuple: lock.MultiTuple{Resource: lock.Resource{ObjectRef: ref, Row: original.Row}, Version: original.Version}}}
		err := e.Locks.CaptureTupleMaintenance(t.Context(), scope.LockTicket, facts)
		if err = finish(err); err != nil {
			t.Fatal(err)
		}
		return facts[0]
	}
	old := capture(1)
	for _, q := range []string{"BEGIN", "SELECT n FROM visibility_heap FOR KEY SHARE"} {
		result := enginetest.SessionSQL(t, writer, q)
		if result.Rows != nil {
			result.Rows.Close()
		}
	}
	xid := writer.Transactions.Ticket.AssignedFullXID()
	held := raw()
	if !held.Present || !held.Active || held.NewestFullXID != xid || xid < bound {
		t.Fatal("capture lost actual live lock/full-XID provenance", held, xid, bound)
	}
	capture(0)
	enginetest.SessionSQL(t, writer, "COMMIT")
	ended := raw()
	if !ended.Present || ended.Active || ended.NewestFullXID != xid || ended.Xmax != held.Xmax || ended.Multi != held.Multi {
		t.Fatal("completion erased raw header or retained active ownership", held, ended)
	}
	// Ending a locker is not physical maintenance. Its raw history remains
	// observable, and the previous successful scan cannot freeze a later XID.
	capture(0)
	enginetest.Run(t, e, "VACUUM (FREEZE) visibility_heap")
	current := store.Versions[0]
	if store.Maintenance.FrozenXID <= xid || current.Version != original.Version || current.HeapXmin() != original.HeapXmin() || &current.Values[0] != &original.Values[0] {
		t.Fatal("maintenance failed to advance its real cutoff or changed insertion/backing")
	}
	after := raw()
	if after != ended {
		t.Fatal("visibility maintenance rewrote manager history", ended, after)
	}
	capture(1)
	if old.AllVisible != 1 || old.AllFrozen != 1 || old.Probe(0) != (storage.VisibilityBits{AllVisible: true, AllFrozen: true}) || !original.Frozen || original.HeapXmin() == 2 {
		t.Fatal("later lock/maintenance mutated old frozen capture")
	}
}
