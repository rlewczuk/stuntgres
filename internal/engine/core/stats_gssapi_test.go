package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/core/catalogrt"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

func gssapiUnitSource(t *testing.T, e *core.Engine, s *session.Session, clear bool) *exec.Source {
	t.Helper()
	return gssapiUnitCapture(t, e, s, pgcatalog.StatGSSAPIOID, clear)
}

func gssapiUnitCapture(t *testing.T, e *core.Engine, s *session.Session, oid ids.OID, clear bool) *exec.Source {
	t.Helper()
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	runtime := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
	if clear {
		if _, err := runtime.StatsSnapshotValue(true, enginetest.Usage(e, sqltype.Void).Type); err != nil {
			t.Fatal(err)
		}
	}
	view := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}
	ref := e.Catalog.Domain.Ref(view.Epoch(), address)
	e.Mu.Lock()
	// Existing test seam consumes Engine.mu, just like the real source walker.
	source, err := core.EngineOpenCatalogSourceLocked(e, scope, ref, view.MustRelation(address), nil)
	if err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	if source == nil || source.Ref != ref || source.Anonymous == nil {
		t.Fatal("common catalog source ownership", source)
	}
	if stamp, err := runtime.StatsSnapshotValue(false, enginetest.Usage(e, sqltype.Timestamptz).Type); err != nil || !stamp.IsNull() {
		t.Fatal("activity changed cumulative timestamp", stamp, err)
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(source.Close)
	return source
}

func gssapiUnitPIDs(t *testing.T, source *exec.Source) map[int64]bool {
	t.Helper()
	pids := make(map[int64]bool)
	for _, row := range source.Anonymous {
		if len(row) != 5 {
			t.Fatal("GSSAPI tuple width", row)
		}
		for i, typ := range []sqltype.ID{sqltype.Int4, sqltype.Bool, sqltype.Text, sqltype.Bool, sqltype.Bool} {
			if row[i].Type().ID() != typ {
				t.Fatal("GSSAPI typed cell", i, row[i].Type().ID(), typ)
			}
		}
		pid, err := row[0].Integer()
		if err != nil || pid <= 0 || pids[pid] {
			t.Fatal("GSSAPI positive unique PID", pid, err)
		}
		pids[pid] = true
		if !row[2].IsNull() {
			t.Fatal("fabricated GSS principal", row[2])
		}
		for _, i := range []int{1, 3, 4} {
			if value, err := row[i].Boolean(); err != nil || value {
				t.Fatal("fabricated GSS transport/authentication", i, value, err)
			}
		}
	}
	return pids
}

func TestPGStatGSSAPICapture(t *testing.T) {
	t.Run("successful_real_attachment_only", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		peer := enginetest.StartSession(t, e, nil)
		var reader statistics.Reader
		before := reader.Activities(&e.Statistics)
		for _, attached := range []*session.Session{s, peer} {
			a := before[attached.Token()]
			if a.Principal != attached.CountedAuthenticated || a.QueryAvailable || a.Active || a.Query != "" || a.BackendType != "client backend" || a.BackendStart.IsZero() || a.State != "idle" {
				t.Fatal("never-commanded attachment missing actual startup facts", before)
			}
		}
		if len(before) != 2 {
			t.Fatal("initial public cohort", before)
		}
		failure := errors.New("GSSAPI startup fixture failure")
		if failed, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: enginetest.NextSessionID(), Defaults: &defaultProvider{err: failure}}); failed != nil || !errors.Is(err, failure) {
			t.Fatal("failed startup", failed, err)
		}
		if duplicate, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: s.Token().ID()}); duplicate != nil || err == nil {
			t.Fatal("duplicate startup", duplicate, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if canceled, err := enginetest.OpenSession(ctx, e, session.SessionStart{ID: enginetest.NextSessionID()}); canceled != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("canceled startup", canceled, err)
		}
		private, err := e.StartRestoreSession(context.Background(), s.CountedAuthenticated)
		if err != nil {
			t.Fatal(err)
		}
		defer statsFunctionRetire(t, private)
		reader.Clear()
		if got := reader.Activities(&e.Statistics); !reflect.DeepEqual(got, before) {
			t.Fatal("private/failed startup changed public membership", got, before)
		}
		enginetest.TransactionBegin(t, s)
		if got := gssapiUnitPIDs(t, gssapiUnitSource(t, e, s, false)); len(got) != 2 || !got[int64(s.Token().ID())] || !got[int64(peer.Token().ID())] {
			t.Fatal("actual source cohort", got)
		}
	})
	t.Run("raw_reader_current_authority_and_retired_login", func(t *testing.T) {
		e := engine.New()
		admin := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, admin, "CREATE ROLE gss_unit_target LOGIN")
		enginetest.SessionSQL(t, admin, "CREATE ROLE gss_unit_actor LOGIN")
		peer, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: enginetest.NextSessionID()}, e.Roles().LookupRef("gss_unit_target"))
		if err != nil {
			t.Fatal(err)
		}
		defer statsFunctionRetire(t, peer)
		actor, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: enginetest.NextSessionID()}, e.Roles().LookupRef("gss_unit_actor"))
		if err != nil {
			t.Fatal(err)
		}
		defer statsFunctionRetire(t, actor)
		enginetest.TransactionBegin(t, actor)
		first := gssapiUnitSource(t, e, actor, false)
		if got := gssapiUnitPIDs(t, first); len(got) != 1 || !got[int64(actor.Token().ID())] {
			t.Fatal("unauthorized peer must be absent", got)
		}
		// Inspect the real transaction reader, not a reconstructed equivalent.
		activity := reflect.ValueOf(core.PayloadOf(actor)).Elem().FieldByName("statsReader").FieldByName("activity")
		if activity.Len() != 3 {
			t.Fatal("filtered reader lost raw membership", activity.Len())
		}
		enginetest.SessionSQL(t, admin, "GRANT gss_unit_target TO gss_unit_actor")
		retained := gssapiUnitSource(t, e, actor, false)
		if got := gssapiUnitPIDs(t, retained); len(got) != 2 || !got[int64(peer.Token().ID())] {
			t.Fatal("cached raw authority did not refresh", got)
		}
		enginetest.SessionSQL(t, admin, "REVOKE gss_unit_target FROM gss_unit_actor")
		if got := gssapiUnitPIDs(t, gssapiUnitSource(t, e, actor, false)); len(got) != 1 {
			t.Fatal("cached filtered rows survived revoke", got)
		}
		oldLogin := peer.CountedAuthenticated
		enginetest.SessionSQL(t, admin, "DROP ROLE gss_unit_target")
		enginetest.SessionSQL(t, admin, "CREATE ROLE gss_unit_target LOGIN")
		if e.Roles().LookupRef("gss_unit_target") == oldLogin {
			t.Fatal("same-name replacement reused principal")
		}
		enginetest.SessionSQL(t, admin, "GRANT gss_unit_target TO gss_unit_actor")
		if got := gssapiUnitPIDs(t, gssapiUnitSource(t, e, actor, false)); len(got) != 1 {
			t.Fatal("replacement role adopted old login", got)
		}
		enginetest.SessionSQL(t, admin, "GRANT pg_read_all_stats TO gss_unit_actor")
		if got := gssapiUnitPIDs(t, gssapiUnitSource(t, e, actor, false)); len(got) != 3 || !got[int64(peer.Token().ID())] {
			t.Fatal("stats reader lost live retired-login backend", got)
		}
		statsFunctionRetire(t, peer)
		if got := gssapiUnitPIDs(t, gssapiUnitSource(t, e, actor, false)); !got[int64(peer.Token().ID())] {
			t.Fatal("transaction lost raw membership before clear", got)
		}
		if got := gssapiUnitPIDs(t, gssapiUnitSource(t, e, actor, true)); len(got) != 2 || got[int64(peer.Token().ID())] {
			t.Fatal("clear retained retired backend", got)
		}
		if got := gssapiUnitPIDs(t, retained); len(got) != 2 || !got[int64(peer.Token().ID())] {
			t.Fatal("retained detached source mutated", got)
		}
	})
	t.Run("admitted_capture_retirement_cancellation", func(t *testing.T) {
		e := engine.New()
		observer, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		enginetest.TransactionBegin(t, observer)
		pause := util.NewExecutionPause("text.like.match")
		defer pause.Resume()
		ctx, cancel := context.WithTimeout(util.WithExecutionPause(context.Background(), pause), enginetest.Liveness)
		defer cancel()
		// The nonconstant predicate reaches the existing scalar pause only after
		// the real query source has captured the transaction's raw membership.
		await := enginetest.ExecuteAsync(t, e, observer, ctx, "SELECT pid FROM pg_catalog.pg_stat_gssapi WHERE pid::text LIKE '%'")
		if err := pause.Wait(ctx); err != nil {
			t.Fatal("actual post-capture predicate admission", err)
		}
		activity := reflect.ValueOf(core.PayloadOf(observer)).Elem().FieldByName("statsReader").FieldByName("activity")
		if activity.Len() != 2 || !activity.MapIndex(reflect.ValueOf(peer.Token())).IsValid() {
			t.Fatal("paused source does not own real raw membership")
		}
		statsFunctionRetire(t, peer)
		var fresh statistics.Reader
		if got := fresh.Activities(&e.Statistics); len(got) != 1 || got[observer.Token()].Principal != observer.CountedAuthenticated {
			t.Fatal("physical teardown not published during detached evaluation", got)
		}
		if activity.Len() != 2 || !activity.MapIndex(reflect.ValueOf(peer.Token())).IsValid() {
			t.Fatal("retirement mutated admitted transaction capture")
		}
		cancel()
		pause.Resume()
		result, err := await()
		if !errors.Is(err, context.Canceled) || result.Rows != nil {
			t.Fatal("canceled captured query published rows", result, err)
		}
		enginetest.SessionSQL(t, observer, "ROLLBACK")
		activity = reflect.ValueOf(core.PayloadOf(observer)).Elem().FieldByName("statsReader").FieldByName("activity")
		if !activity.IsNil() {
			t.Fatal("transaction end retained raw membership")
		}
		enginetest.TransactionBegin(t, observer)
		if got := gssapiUnitPIDs(t, gssapiUnitSource(t, e, observer, false)); len(got) != 1 || !got[int64(observer.Token().ID())] {
			t.Fatal("fresh source retained retired peer", got)
		}
	})
	t.Run("shared_target_visibility_and_real_end_drain", func(t *testing.T) {
		e := engine.New()
		admin, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, admin, "CREATE DATABASE gss_unit_partition")
		target, err := e.SelectDatabase(context.Background(), "gss_unit_partition")
		if err != nil {
			t.Fatal(err)
		}
		var original statistics.Reader
		before := original.Activities(&e.Statistics)
		for _, s := range []*session.Session{admin, peer} {
			if before[s.Token()].Principal != s.CountedAuthenticated {
				t.Fatal("shared activity lost startup authority", before)
			}
		}
		if len(before) != 2 || len(target.Statistics.CaptureActivities()) != 0 {
			t.Fatal("new target mirrored activity instead of sharing the cluster provider")
		}
		targetObserver := enginetest.StartSession(t, target, nil)
		enginetest.TransactionBegin(t, admin)
		enginetest.TransactionBegin(t, targetObserver)
		captured := gssapiUnitSource(t, e, admin, false)
		targetCaptured := gssapiUnitSource(t, target, targetObserver, false)
		for _, source := range []*exec.Source{captured, targetCaptured} {
			if got := gssapiUnitPIDs(t, source); len(got) != 3 || !got[int64(peer.Token().ID())] || !got[int64(admin.Token().ID())] || !got[int64(targetObserver.Token().ID())] {
				t.Fatal("target view did not share the real cluster cohort", got)
			}
		}
		op, err := peer.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		peer.RequestEnd(session.CleanClose)
		// Actual operation ownership prevents teardown. Draining is not physical
		// retirement and must not withdraw the still-attached peer prematurely.
		if got := e.Statistics.CaptureActivities(); got[peer.Token()].Principal != peer.CountedAuthenticated {
			op.Done()
			t.Fatal("drain removed physical lifetime before operation release", got)
		}
		op.Done()
		if err := peer.WaitEnd(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, source := range []*exec.Source{gssapiUnitSource(t, e, admin, true), gssapiUnitSource(t, target, targetObserver, true)} {
			if got := gssapiUnitPIDs(t, source); len(got) != 2 || got[int64(peer.Token().ID())] || !got[int64(admin.Token().ID())] || !got[int64(targetObserver.Token().ID())] {
				t.Fatal("real teardown not visible from both targets", got)
			}
		}
		if len(target.Statistics.CaptureActivities()) != 0 || !reflect.DeepEqual(original.Activities(&e.Statistics), before) {
			t.Fatal("target mirrored activity or retirement mutated retained capture")
		}
		for _, source := range []*exec.Source{captured, targetCaptured} {
			if got := gssapiUnitPIDs(t, source); len(got) != 3 || !got[int64(peer.Token().ID())] {
				t.Fatal("real teardown mutated detached source", got)
			}
		}
	})
	t.Run("gssapi_first_preserves_activity_enrichment", func(t *testing.T) {
		for _, first := range []string{"gssapi_first", "activity_first"} {
			for _, refresh := range []string{"clear", "end"} {
				t.Run(first+"/"+refresh, func(t *testing.T) {
					e := engine.New()
					writer := enginetest.StartSession(t, e, nil)
					reader := enginetest.StartSession(t, e, nil)
					observer := enginetest.StartSession(t, e, nil)
					activityOwnerRun(t, e, writer, "CREATE TABLE gss_unit_xid(n integer)")
					e.Mu.Lock()
					e.Visibility.SetLastAssignedXIDLocked(1<<32 | 40)
					e.Mu.Unlock()
					activityOwnerRun(t, e, writer, "BEGIN", "INSERT INTO gss_unit_xid VALUES(1)")
					activityOwnerRun(t, e, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ", "SELECT n FROM gss_unit_xid")
					activityOwnerRun(t, e, observer, "BEGIN")
					writerRef, readerRef := writer.Transactions.Ref, reader.Transactions.Ref
					e.Mu.Lock()
					writerBefore := e.Visibility.Entries[writerRef]
					readerBefore := e.Visibility.Entries[readerRef]
					wantXID := e.Visibility.AssignedXIDLocked(writerRef)
					wantXmin := uint32(readerBefore.ActivityFullXmin)
					frontier := e.Visibility.LastAssignedXID()
					e.Mu.Unlock()
					if wantXID == 0 || wantXmin == 0 || reader.Transactions.CurrentSnapshot == nil || readerBefore.ActivityFullXmin != reader.Transactions.CurrentSnapshot.Xmin() || readerBefore.AssignedXID != 0 {
						t.Fatal("missing distinct writer/accepted reader authority", writerBefore, readerBefore)
					}
					checkMembership := func(source *exec.Source) {
						t.Helper()
						pids := gssapiUnitPIDs(t, source)
						if len(pids) != 3 || !pids[int64(writer.Token().ID())] || !pids[int64(reader.Token().ID())] || !pids[int64(observer.Token().ID())] {
							t.Fatal("shared cut lost real login membership", pids)
						}
					}
					checkActivity := func(source *exec.Source, completed bool) {
						t.Helper()
						seen := make(map[ids.SessionID]bool)
						for _, row := range source.Anonymous {
							pid, err := row[2].Integer()
							if err != nil {
								t.Fatal(err)
							}
							id := ids.SessionID(pid)
							if id != writer.Token().ID() && id != reader.Token().ID() {
								continue
							}
							if seen[id] {
								t.Fatal("duplicate activity peer", id)
							}
							seen[id] = true
							if completed {
								if !row[17].IsNull() || !row[18].IsNull() {
									t.Fatal("refreshed activity retained completed authority", row[17:19])
								}
								continue
							}
							column, want := 17, wantXID
							absent := 18
							if id == reader.Token().ID() {
								column, want, absent = 18, wantXmin, 17
							}
							if got, err := row[column].XID(); err != nil || got != want || row[column].Type().ID() != sqltype.Xid || !row[absent].IsNull() {
								t.Fatal("shared cut lost canonical peer XID/xmin", id, row[17:19], want, err)
							}
						}
						if len(seen) != 2 {
							t.Fatal("missing writer or reader activity", seen)
						}
					}
					var captured *exec.Source
					if first == "gssapi_first" {
						checkMembership(gssapiUnitSource(t, e, observer, false))
						captured = gssapiUnitCapture(t, e, observer, pgcatalog.StatActivityOID, false)
					} else {
						captured = gssapiUnitCapture(t, e, observer, pgcatalog.StatActivityOID, false)
						checkMembership(gssapiUnitSource(t, e, observer, false))
					}
					checkActivity(captured, false)
					e.Mu.Lock()
					writerAfter := e.Visibility.Entries[writerRef]
					readerAfter := e.Visibility.Entries[readerRef]
					afterFrontier := e.Visibility.LastAssignedXID()
					e.Mu.Unlock()
					if writerAfter != writerBefore || readerAfter != readerBefore || afterFrontier != frontier {
						t.Fatal("monitoring acquired or assigned peer visibility", writerBefore, writerAfter, readerBefore, readerAfter, frontier, afterFrontier)
					}
					activityOwnerRun(t, e, observer, "SET ROLE pg_signal_backend")
					if pids := gssapiUnitPIDs(t, gssapiUnitSource(t, e, observer, false)); len(pids) != 0 {
						t.Fatal("shared cut retained prior observer privileges", pids)
					}
					activityOwnerRun(t, e, observer, "RESET ROLE")
					checkMembership(gssapiUnitSource(t, e, observer, false))
					activityOwnerRun(t, e, writer, "COMMIT")
					activityOwnerRun(t, e, reader, "ROLLBACK")
					checkActivity(captured, false)
					checkActivity(gssapiUnitCapture(t, e, observer, pgcatalog.StatActivityOID, false), false)
					checkMembership(gssapiUnitSource(t, e, observer, false))
					if refresh == "clear" {
						checkMembership(gssapiUnitSource(t, e, observer, true))
					} else {
						activityOwnerRun(t, e, observer, "COMMIT", "BEGIN")
						checkMembership(gssapiUnitSource(t, e, observer, false))
					}
					checkActivity(gssapiUnitCapture(t, e, observer, pgcatalog.StatActivityOID, false), true)
					checkActivity(captured, false)
					activityOwnerRun(t, e, observer, "ROLLBACK")
				})
			}
		}
	})
	t.Run("detached_producer_cancellation_and_reset", func(t *testing.T) {
		e := engine.New()
		a, peer := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
		view := e.CatalogRead()
		address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: pgcatalog.StatGSSAPIOID}
		ref := e.Catalog.Domain.Ref(view.Epoch(), address)
		in := pgcatalog.Input{Context: context.Background(), Catalog: view, Owner: view.Owner(), Ref: ref, Source: view.MustRelation(address), GSSAPISessions: []pgcatalog.GSSAPISession{{PID: int32(a.Token().ID())}, {PID: int32(peer.Token().ID())}}}
		produce := enginetest.RegisteredCatalogProducer(pgcatalog.StatGSSAPIOID)
		retained, err := catalogrt.CaptureRows(in, produce)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		statsFunctionRetire(t, peer)
		for _, at := range []int32{1, 2, 3, 4} {
			ctx, cancel := context.WithCancel(context.Background())
			candidate := in
			candidate.Context = &cancelAtErrCheck{Context: ctx, cancel: cancel, at: at}
			result, err := catalogrt.CaptureRows(candidate, produce)
			cancel()
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("canceled detached capture published prefix", at, result, err)
			}
		}
		if err := engine.Reset(context.Background(), e); err != nil {
			t.Fatal(err)
		}
		if got := gssapiUnitPIDs(t, retained); len(got) != 2 {
			t.Fatal("reset mutated detached facts", got)
		}
		in.GSSAPISessions[0].PID = 0
		if source, err := catalogrt.CaptureRows(in, produce); source != nil || err == nil {
			t.Fatal("invalid PID capture published rows", source, err)
		}
	})
}
