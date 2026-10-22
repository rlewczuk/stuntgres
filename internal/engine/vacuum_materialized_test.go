package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"testing"
)

func vacuumMaterializedStatistics(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		for _, retained := range []bool{false, true} {
			t.Run(map[bool]string{false: "ordinary", true: "concurrent"}[concurrent]+"/"+map[bool]string{false: "autocommit", true: "implicit_message"}[retained], func(t *testing.T) {
				e := New()
				s := sessionStart(t, e, nil)
				for _, q := range []string{"CREATE TABLE mv_source(id INT)", "INSERT INTO mv_source VALUES(1),(2)", "CREATE MATERIALIZED VIEW mv_stats AS SELECT id FROM mv_source", "CREATE UNIQUE INDEX mv_stats_key ON mv_stats(id)", "VACUUM mv_stats"} {
					sessionSQL(t, s, q)
				}
				ref, old := vacuumStore(t, e, "mv_stats")
				before := old.Maintenance
				sessionSQL(t, s, "INSERT INTO mv_source VALUES(3)")
				var message *batchUnitMessage
				if retained {
					message = batchUnitStart(t, s, context.Background())
					defer message.end(context.Canceled)
				}
				refresh := "REFRESH MATERIALIZED VIEW "
				if concurrent {
					refresh += "CONCURRENTLY "
				}
				refresh += "mv_stats"
				if retained {
					if _, err := message.step(statement(t, refresh)); err != nil {
						t.Fatal(err)
					}
				} else {
					sessionSQL(t, s, refresh)
				}
				if retained {
					if e.storage.Relations[ref.Address] != old || old.Maintenance != before {
						t.Fatal("private refresh changed published maintenance")
					}
					if err := message.end(nil); err != nil {
						t.Fatal(err)
					}
				}
				afterRef, fresh := vacuumStore(t, e, "mv_stats")
				m := fresh.Maintenance
				if afterRef != ref || fresh == old || len(fresh.Versions) != 3 {
					t.Fatal("refresh did not replace storage for same actual object")
				}
				if m.VacuumCount != before.VacuumCount || m.LastVacuum != before.LastVacuum || m.Scanned != before.Scanned || m.Tuples != before.Tuples || m.Updates != before.Updates || m.Deletes != before.Deletes {
					t.Fatal("same-object refresh reset cumulative maintenance", before, m)
				}
				if m.Inserts != before.Inserts+3 || m.Modifications != before.Modifications+3 || m.InsertsSince != before.InsertsSince+3 || m.Live != 3 || m.Dead != 0 {
					t.Fatal("refresh birth deltas not actual once", before, m)
				}
				publisher := fresh.Versions[0].InsertFullXID
				if publisher < 3 || m.FrozenXID != publisher {
					t.Fatal("refresh retained obsolete freezing horizon", publisher, m)
				}
				for _, row := range fresh.Versions {
					if row.Frozen || row.ImportFrozen || row.InsertFullXID != publisher {
						t.Fatal("refresh lacks actual new publisher provenance", row)
					}
				}
				if old.Maintenance != before {
					t.Fatal("refresh mutated predecessor facts")
				}
				stable := fresh.Maintenance
				rollback := batchUnitStart(t, s, context.Background())
				defer rollback.end(context.Canceled)
				if _, err := rollback.step(statement(t, refresh)); err != nil {
					t.Fatal(err)
				}
				_, discardErr := rollback.step(statement(t, "SELECT missing"))
				if discardErr == nil {
					t.Fatal("late message error unexpectedly succeeded")
				}
				if err := rollback.end(discardErr); err != discardErr {
					t.Fatal("message discard lost actual error", err, discardErr)
				}
				if e.storage.Relations[ref.Address] != fresh || fresh.Maintenance != stable {
					t.Fatal("rolled-back refresh changed published maintenance")
				}
				sessionSQL(t, s, "VACUUM mv_stats")
				if fresh.Maintenance.VacuumCount != 2 || fresh.Maintenance.Tuples != 3 {
					t.Fatal("next real vacuum did not advance scan/count", fresh.Maintenance)
				}
				scanned := fresh.Maintenance
				if retained {
					emptyMessage := batchUnitStart(t, s, context.Background())
					defer emptyMessage.end(context.Canceled)
					if _, err := emptyMessage.step(statement(t, "REFRESH MATERIALIZED VIEW mv_stats WITH NO DATA")); err != nil {
						t.Fatal(err)
					}
					if err := emptyMessage.end(nil); err != nil {
						t.Fatal(err)
					}
				} else {
					sessionSQL(t, s, "REFRESH MATERIALIZED VIEW mv_stats WITH NO DATA")
				}
				_, empty := vacuumStore(t, e, "mv_stats")
				if len(empty.Versions) != 0 || empty.Maintenance.Live != 0 || empty.Maintenance.Dead != 0 || empty.Maintenance.FrozenXID < 3 || empty.Maintenance.VacuumCount != scanned.VacuumCount || empty.Maintenance.LastVacuum != scanned.LastVacuum {
					t.Fatal("empty refresh inherited stale contents or invented vacuum", empty.Maintenance)
				}
				sessionSQL(t, s, "DROP MATERIALIZED VIEW mv_stats")
				sessionSQL(t, s, "CREATE MATERIALIZED VIEW mv_stats AS SELECT id FROM mv_source")
				recreatedRef, recreated := vacuumStore(t, e, "mv_stats")
				if recreatedRef == ref || recreated.Maintenance.VacuumCount != 0 || !recreated.Maintenance.LastVacuum.IsZero() || recreated.Maintenance.Scanned {
					t.Fatal("same-name new MV inherited maintenance")
				}
			})
		}
	}
	t.Run("detached_preflight", vacuumMaterializedPreflight)
	t.Run("explicit_boundary", vacuumMaterializedExplicitBoundary)
}

func vacuumMaterializedPreflight(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "fresh_predecessor", true: "cancel"}[canceled], func(t *testing.T) {
			e := New()
			owner, worker := sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE mv_pending_source(id INT)", "INSERT INTO mv_pending_source VALUES(1),(2)", "CREATE MATERIALIZED VIEW mv_pending AS SELECT id FROM mv_pending_source", "VACUUM mv_pending"} {
				sessionSQL(t, owner, q)
			}
			ref, old := vacuumStore(t, e, "mv_pending")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := worker.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			if err := scope.gate(statement(t, "REFRESH MATERIALIZED VIEW mv_pending")); err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			d := stage.Base.MustRelation(ref.Address)
			if err := e.stageMaterializedRows(ctx, stage, d, old, refreshUnitRows(t, d, 3), true, scope.txRef); err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			root, ends := old.Versions, old.Ends
			sessionSQL(t, owner, "VACUUM mv_pending")
			latest := old.Maintenance
			if canceled {
				cancel()
			}
			_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()})
			if canceled {
				if !errors.Is(err, context.Canceled) || e.storage.Relations[ref.Address] != old || old.Maintenance != latest || !storage.SameVersionRoot(root, old.Versions) || old.Ends != ends {
					scope.Finish(err)
					t.Fatal("failed preflight changed published facts", err)
				}
			} else {
				if err != nil {
					scope.Finish(err)
					t.Fatal(err)
				}
				scope.completed = true
				fresh := e.storage.Relations[ref.Address]
				if fresh == old || fresh.Maintenance.VacuumCount != 2 || fresh.Maintenance.LastVacuum != latest.LastVacuum || fresh.Maintenance.Inserts != latest.Inserts+3 {
					scope.Finish(nil)
					t.Fatal("prepared facts overwrote final predecessor", latest, fresh.Maintenance)
				}
			}
			if finishErr := scope.Finish(err); !canceled && finishErr != nil {
				t.Fatal(finishErr)
			}
		})
	}
}

// Fresh REFRESH after BEGIN is an existing native capability boundary even
// with genuine command admission. Retained implicit messages above are distinct.
func vacuumMaterializedExplicitBoundary(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "concurrent"}[concurrent], func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE explicit_source(id INT)", "INSERT INTO explicit_source VALUES(1),(2)", "CREATE MATERIALIZED VIEW explicit_mv AS SELECT id FROM explicit_source", "CREATE UNIQUE INDEX explicit_key ON explicit_mv(id)", "VACUUM explicit_mv"} {
				sessionSQL(t, s, q)
			}
			ref, store := vacuumStore(t, e, "explicit_mv")
			root, ends, generation, facts, base := store.Versions, store.Ends, store.Generation, store.Maintenance, e.catalog.state
			m := batchUnitStart(t, s, context.Background())
			defer m.end(context.Canceled)
			if _, err := m.step(statement(t, "BEGIN")); err != nil {
				t.Fatal(err)
			}
			q := "REFRESH MATERIALIZED VIEW "
			if concurrent {
				q += "CONCURRENTLY "
			}
			q += "explicit_mv"
			_, err := m.step(statement(t, q))
			var diagnostic *sqlstate.Error
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "statement is unsupported in an explicit transaction"}
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
				t.Fatal("explicit refresh boundary", diagnostic, err, want)
			}
			if finishErr := m.end(err); finishErr != err {
				t.Fatal("message lost explicit error", finishErr, err)
			}
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Execute(op, statement(t, "SELECT 1"), 0, nil)
			op.Done()
			want = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}
			if !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
				t.Fatal("explicit refresh abort boundary", diagnostic, err, want)
			}
			sessionSQL(t, s, "ROLLBACK")
			if e.catalog.state != base || e.storage.Relations[ref.Address] != store || !storage.SameVersionRoot(root, store.Versions) || store.Ends != ends || store.Generation != generation || store.Maintenance != facts {
				t.Fatal("rejected explicit refresh changed published roots/facts")
			}
			lockEmpty(t, e)
		})
	}
}
