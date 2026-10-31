package core_test

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/parser"
)

func statsPrivateMaintenanceKey(t *testing.T, e *core.Engine, s *session.Session, journal *session.Journal, name string) statistics.Key {
	t.Helper()
	if journal != nil {
		for a, d := range journal.Descriptors {
			if r, ok := d.(*catalog.Relation); ok && r.Name() == name {
				return statistics.Key{Object: e.Catalog.Domain.Ref(s.Transactions.Ref.Epoch(), a), Category: statistics.Relation}
			}
		}
	}
	t.Fatal("missing actual private relation", name)
	return statistics.Key{}
}

func TestStatsSnapshotMaintenanceReportingAuthority(t *testing.T) {
	t.Run("ordinary_deadline", statsMaintenanceDeadlineProof)
	t.Run("dropped_creation", statsMaintenanceDroppedCreations)
	t.Run("local_sample_not_inherited_optimizer_population", func(t *testing.T) {
		e := engine.New()
		setup := enginetest.StartSession(t, e, nil)
		for _, q := range []string{
			"CREATE TABLE stats_local_parent(id integer) PARTITION BY RANGE(id)",
			"CREATE TABLE stats_local_leaf PARTITION OF stats_local_parent FOR VALUES FROM(0) TO(10)",
			"INSERT INTO stats_local_parent VALUES(1),(2)",
		} {
			enginetest.SessionSQL(t, setup, q)
		}
		parent := enginetest.RoleTableRef(t, e, "stats_local_parent")
		statsEnd(t, setup)
		actor := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, actor, "BEGIN")
		enginetest.SessionSQL(t, actor, "ANALYZE stats_local_parent")
		f := e.Statistics.Read(statistics.Key{Object: parent})
		if f.AnalyzeCount != 1 || f.Live != 0 || f.Dead != 0 || f.Inserts != 0 || f.Modifications != 0 {
			t.Fatal("inherited optimizer population became local monitoring tuples", f)
		}
		e.Mu.Lock()
		metadata := e.Storage.RelationMetadata[parent.Address]
		validOptimizer := metadata != nil && metadata.Scanned && metadata.Tuples == 2 && metadata.InheritedScanned && metadata.InheritedTuples == 2
		e.Mu.Unlock()
		if !validOptimizer {
			t.Fatal("local monitoring correction changed inherited optimizer cardinality", metadata)
		}
		enginetest.SessionSQL(t, actor, "ROLLBACK")
		if after := e.Statistics.Read(statistics.Key{Object: parent}); after.AnalyzeCount != 1 || after.Live != 0 || after.Dead != 0 {
			t.Fatal("partition report changed during rollback", after)
		}
	})
	t.Run("retained_pl_creation", func(t *testing.T) {
		e := engine.New()
		actor := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"BEGIN", "CREATE TABLE stats_pl_retained(id integer)", "INSERT INTO stats_pl_retained VALUES(1),(2)"} {
			enginetest.SessionSQL(t, actor, q)
		}
		key := statsPrivateMaintenanceKey(t, e, actor, actor.Transactions.Catalog, "stats_pl_retained")
		scope, _ := truncateUnitScope(t, e, actor, context.Background())
		if err := scope.Gate(&parser.Do{}); err != nil {
			t.Fatal(err)
		}
		mark, err := scope.BeginPLException()
		if err != nil {
			t.Fatal(err)
		}
		restore := scope.ActivateSQLRoutine(nil, e.CatalogRead())
		_, executeErr := scope.Execute(enginetest.Statement(t, "ANALYZE stats_pl_retained"), 0, nil)
		if err := restore(executeErr); err != nil {
			t.Fatal(err)
		}
		if err := scope.EndPLException(mark, true, nil); err != nil {
			t.Fatal(err)
		}
		if f := e.Statistics.Read(key); f.AnalyzeCount != 1 || f.Inserts != 0 {
			t.Fatal("PL unwind retired a retained owner creation", f)
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		scope.Op.Done()
		enginetest.SessionSQL(t, actor, "COMMIT")
		statsEnd(t, actor)
		if f := e.Statistics.Read(key); f.AnalyzeCount != 1 || f.Inserts != 2 || f.Live != 2 {
			t.Fatal("retained creation report did not survive commit", f)
		}
	})
	t.Run("private_iud_sample", func(t *testing.T) {
		e := engine.New()
		setup := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"CREATE TABLE stats_sample(id integer)", "INSERT INTO stats_sample VALUES(11),(12)"} {
			enginetest.SessionSQL(t, setup, q)
		}
		key := statistics.Key{Object: enginetest.RoleTableRef(t, e, "stats_sample")}
		statsEnd(t, setup)
		actor := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"BEGIN", "INSERT INTO stats_sample VALUES(1),(2)", "UPDATE stats_sample SET id=101 WHERE id=1", "DELETE FROM stats_sample WHERE id=2", "ANALYZE stats_sample"} {
			enginetest.SessionSQL(t, actor, q)
		}
		if f := e.Statistics.Read(key); f.Inserts != 2 || f.Updates != 0 || f.Deletes != 0 || f.Live != 2 || f.Dead != 0 || f.AnalyzeCount != 1 {
			t.Fatal("private sample double-counted current I/U/D", f)
		}
		enginetest.SessionSQL(t, actor, "ROLLBACK")
		statsEnd(t, actor)
		if f := e.Statistics.Read(key); f.Inserts != 4 || f.Updates != 1 || f.Deletes != 1 || f.Live != 2 || f.Dead != 3 || f.Modifications != 0 || f.InsertsSince != 4 || f.AnalyzeCount != 1 {
			t.Fatal("post-sample abort ordering", f)
		}
	})
	t.Run("current_pending_raw_adjustment", func(t *testing.T) {
		e := engine.New()
		setup := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"CREATE TABLE stats_maint(id integer)", "INSERT INTO stats_maint VALUES(11),(12)"} {
			enginetest.SessionSQL(t, setup, q)
		}
		ref := enginetest.RoleTableRef(t, e, "stats_maint")
		key := statistics.Key{Object: ref, Category: statistics.Relation}
		statsEnd(t, setup)
		actor := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"BEGIN", "INSERT INTO stats_maint VALUES(1),(2),(3)", "UPDATE stats_maint SET id=id WHERE id IN(1,2)", "DELETE FROM stats_maint WHERE id=3", "SAVEPOINT child", "INSERT INTO stats_maint VALUES(21),(22)", "ROLLBACK TO child", "RELEASE child"} {
			enginetest.SessionSQL(t, actor, q)
		}
		scope, _ := truncateUnitScope(t, e, actor, context.Background())
		if _, err := scope.Execute(enginetest.Statement(t, "INSERT INTO stats_maint VALUES(4),(5)"), 0, nil); err != nil {
			t.Fatal(err)
		}
		ownerBefore, stageBefore := scope.Owner.Stats.Clone(), scope.Stage.Stats.Clone()
		now := time.Now()
		e.Mu.Lock()
		scope.RecordStatsMaintenance(ref, true, now, now, 20, 10, true)
		e.Mu.Unlock()
		f := e.Statistics.Read(key)
		// Actual owner I3/U2/D1 + actual stage I2 and aborted child pending dead2.
		if f.Live != 16 || f.Dead != 5 || f.Inserts != 2 || f.Updates != 0 || f.Deletes != 0 || f.Modifications != 0 || f.InsertsSince != 2 || f.AnalyzeCount != 1 {
			t.Fatal("raw/unresolved/pending reconciliation", f)
		}
		if scope.Owner.Stats.CurrentCounts(key, scope.Stage.Stats) != ownerBefore.CurrentCounts(key, stageBefore) {
			t.Fatal("report consumed unresolved ownership")
		}
		primary := errors.New("abort after completed report")
		if got := scope.Finish(primary); !errors.Is(got, primary) {
			t.Fatal(got)
		}
		scope.Op.Done()
		enginetest.SessionSQL(t, actor, "ROLLBACK")
		statsEnd(t, actor)
		if got := e.Statistics.Read(key); got.AnalyzeCount != 1 || got.LastAnalyze != now {
			t.Fatal("outer abort retracted maintenance", got)
		}
	})
	t.Run("birth_invisible_not_dead", func(t *testing.T) {
		e := engine.New()
		setup := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"CREATE TABLE stats_birth(id integer)", "INSERT INTO stats_birth VALUES(1),(2)"} {
			enginetest.SessionSQL(t, setup, q)
		}
		ref := enginetest.RoleTableRef(t, e, "stats_birth")
		statsEnd(t, setup)
		actor := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, actor, "BEGIN ISOLATION LEVEL REPEATABLE READ")
		enginetest.TransactionRows(t, actor, "SELECT id FROM stats_birth ORDER BY id", 1, 2)
		peer := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, peer, "INSERT INTO stats_birth VALUES(3)")
		statsEnd(t, peer)
		enginetest.SessionSQL(t, actor, "ANALYZE stats_birth")
		if f := e.Statistics.Read(statistics.Key{Object: ref}); f.Dead != 0 || f.AnalyzeCount != 1 {
			t.Fatal("invisible birth fabricated dead tuples", f)
		}
		enginetest.SessionSQL(t, actor, "ROLLBACK")
	})
	for _, kind := range []string{"abort", "commit", "savepoint", "savepoint_retained", "prepared_abort", "prepared_commit"} {
		t.Run("new_"+kind, func(t *testing.T) {
			e := engine.New()
			actor := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, actor, "BEGIN")
			if kind == "savepoint" {
				enginetest.SessionSQL(t, actor, "SAVEPOINT creation")
			}
			for _, q := range []string{"CREATE TABLE stats_new(id integer)", "INSERT INTO stats_new VALUES(1),(2)"} {
				enginetest.SessionSQL(t, actor, q)
			}
			if kind == "savepoint_retained" {
				enginetest.SessionSQL(t, actor, "SAVEPOINT creation")
			}
			enginetest.SessionSQL(t, actor, "ANALYZE stats_new")
			key := statsPrivateMaintenanceKey(t, e, actor, actor.Transactions.Catalog, "stats_new")
			if f := e.Statistics.Read(key); f.AnalyzeCount != 1 || f.Inserts != 0 || f.Live != 0 {
				t.Fatal("private report deferred or tuple attempts published", f)
			}
			if kind == "prepared_abort" || kind == "prepared_commit" {
				enginetest.SessionSQL(t, actor, "PREPARE TRANSACTION 'stats_new_owner'")
				finisher := enginetest.StartSession(t, e, nil)
				end := "ROLLBACK PREPARED 'stats_new_owner'"
				if kind == "prepared_commit" {
					end = "COMMIT PREPARED 'stats_new_owner'"
				}
				t.Cleanup(func() {
					if core.EnginePreparedRef(e).Lookup("stats_new_owner") != nil {
						enginetest.SessionSQL(t, finisher, "ROLLBACK PREPARED 'stats_new_owner'")
					}
				})
				enginetest.SessionSQL(t, finisher, end)
				statsEnd(t, finisher)
			} else if kind == "savepoint" || kind == "savepoint_retained" {
				enginetest.SessionSQL(t, actor, "ROLLBACK TO creation")
				enginetest.SessionSQL(t, actor, "COMMIT")
			} else {
				end := "ROLLBACK"
				if kind == "commit" {
					end = "COMMIT"
				}
				enginetest.SessionSQL(t, actor, end)
			}
			statsEnd(t, actor)
			f := e.Statistics.Read(key)
			if kind == "commit" || kind == "prepared_commit" || kind == "savepoint_retained" {
				if f.AnalyzeCount != 1 || f.Inserts != 2 || f.Live != 2 || f.Modifications != 2 {
					t.Fatal("committed creation lost immediate report", f)
				}
			} else if f.AnalyzeCount != 0 || f.Inserts != 0 || f.Dead != 0 {
				t.Fatal("aborted creation left orphan statistics", f)
			}
		})
	}
	t.Run("new_pl_exception", func(t *testing.T) {
		e := engine.New()
		actor := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, actor, "BEGIN")
		scope, _ := truncateUnitScope(t, e, actor, context.Background())
		if err := scope.Gate(&parser.Do{}); err != nil {
			t.Fatal(err)
		}
		mark, err := scope.BeginPLException()
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"CREATE TABLE stats_pl_new(id integer)", "INSERT INTO stats_pl_new VALUES(1),(2)", "ANALYZE stats_pl_new"} {
			restore := scope.ActivateSQLRoutine(nil, e.CatalogRead())
			_, executeErr := scope.Execute(enginetest.Statement(t, q), 0, nil)
			if err := restore(executeErr); err != nil {
				t.Fatal(err)
			}
		}
		key := statsPrivateMaintenanceKey(t, e, actor, scope.Catalog, "stats_pl_new")
		if e.Statistics.Read(key).AnalyzeCount != 1 {
			t.Fatal("private PL maintenance not immediately shared")
		}
		if err := scope.EndPLException(mark, true, nil); err != nil {
			t.Fatal(err)
		}
		if f := e.Statistics.Read(key); f.AnalyzeCount != 0 || f.Inserts != 0 {
			t.Fatal("PL creation subabort did not retire key", f)
		}
		if err := scope.Finish(nil); err != nil {
			t.Fatal(err)
		}
		scope.Op.Done()
		enginetest.SessionSQL(t, actor, "ROLLBACK")
	})
}

func statsMaintenanceDeadlineProof(t *testing.T) {
	for _, command := range []string{"ANALYZE", "VACUUM"} {
		t.Run(command, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, s, "CREATE TABLE stats_report_deadline(id integer)")
				key := statistics.Key{Object: enginetest.RoleTableRef(t, e, "stats_report_deadline")}
				enginetest.SessionSQL(t, s, "INSERT INTO stats_report_deadline VALUES(1)")
				enginetest.SessionSQL(t, s, "INSERT INTO stats_report_deadline VALUES(2)")
				time.Sleep(500 * time.Millisecond)
				enginetest.SessionSQL(t, s, command+" stats_report_deadline")
				report := e.Statistics.Read(key)
				if report.Inserts != 1 || report.Live != 2 || report.AnalyzeCount+report.VacuumCount != 1 {
					t.Fatal("maintenance deferred or force-flushed pending prefix", report)
				}
				// The original timer was armed at t=0. Reporting at t=.5 must
				// neither restart it nor consume the pending relation delta.
				time.Sleep(9500 * time.Millisecond)
				synctest.Wait()
				f := e.Statistics.Read(key)
				wantSince := int64(2)
				if command == "VACUUM" {
					wantSince = 1
				}
				if f.Inserts != 2 || f.Live != 3 || f.InsertsSince != wantSince || f.AnalyzeCount != report.AnalyzeCount || f.VacuumCount != report.VacuumCount {
					t.Fatal("maintenance changed timer or reset ordering", f)
				}
			})
		})
	}
}

func statsMaintenanceDroppedCreations(t *testing.T) {
	for _, kind := range []string{"commit", "abort", "child_abort", "recreate", "prepared_commit"} {
		t.Run(kind, func(t *testing.T) {
			e := engine.New()
			actor := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, actor, "BEGIN")
			if kind == "child_abort" {
				enginetest.SessionSQL(t, actor, "SAVEPOINT creation")
			}
			for _, q := range []string{"CREATE TABLE stats_dropped(id integer)", "INSERT INTO stats_dropped VALUES(1),(2)", "ANALYZE stats_dropped"} {
				enginetest.SessionSQL(t, actor, q)
			}
			old := statsPrivateMaintenanceKey(t, e, actor, actor.Transactions.Catalog, "stats_dropped")
			enginetest.SessionSQL(t, actor, "DROP TABLE stats_dropped")
			var successor statistics.Key
			if kind == "recreate" {
				for _, q := range []string{"CREATE TABLE stats_dropped(id integer)", "INSERT INTO stats_dropped VALUES(7)", "ANALYZE stats_dropped"} {
					enginetest.SessionSQL(t, actor, q)
				}
				successor = statsPrivateMaintenanceKey(t, e, actor, actor.Transactions.Catalog, "stats_dropped")
				if successor == old {
					t.Fatal("drop/recreate reused statistics identity")
				}
			}
			if kind == "child_abort" {
				enginetest.SessionSQL(t, actor, "ROLLBACK TO creation")
			}
			if kind == "prepared_commit" {
				enginetest.SessionSQL(t, actor, "PREPARE TRANSACTION 'stats_dropped_owner'")
				finisher := enginetest.StartSession(t, e, nil)
				t.Cleanup(func() {
					if core.EnginePreparedRef(e).Lookup("stats_dropped_owner") != nil {
						enginetest.SessionSQL(t, finisher, "ROLLBACK PREPARED 'stats_dropped_owner'")
					}
				})
				enginetest.SessionSQL(t, finisher, "COMMIT PREPARED 'stats_dropped_owner'")
				statsEnd(t, finisher)
			} else {
				end := "COMMIT"
				if kind == "abort" {
					end = "ROLLBACK"
				}
				enginetest.SessionSQL(t, actor, end)
			}
			statsEnd(t, actor)
			if f := e.Statistics.Read(old); f.AnalyzeCount != 0 || f.Inserts != 0 || f.Dead != 0 {
				t.Fatal("dropped creation left ownerless statistics", f)
			}
			if kind == "recreate" {
				if f := e.Statistics.Read(successor); f.AnalyzeCount != 1 || f.Inserts != 1 || f.Live != 1 || f.Modifications != 1 {
					t.Fatal("old retirement removed successor report", f)
				}
			}
		})
	}
}
