package viewcmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/viewcmds"
	"stuntgres/internal/parser"
)

func vacuumMaterializedStatistics(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		for _, retained := range []bool{false, true} {
			t.Run(map[bool]string{false: "ordinary", true: "concurrent"}[concurrent]+"/"+map[bool]string{false: "autocommit", true: "implicit_message"}[retained], func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				for _, q := range []string{"CREATE TABLE mv_source(id INT)", "INSERT INTO mv_source VALUES(1),(2)", "CREATE MATERIALIZED VIEW mv_stats AS SELECT id FROM mv_source", "CREATE UNIQUE INDEX mv_stats_key ON mv_stats(id)", "VACUUM mv_stats"} {
					enginetest.SessionSQL(t, s, q)
				}
				ref, old := vacuumStore(t, e, "mv_stats")
				before := old.Maintenance
				enginetest.SessionSQL(t, s, "INSERT INTO mv_source VALUES(3)")
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
					if _, err := message.step(enginetest.Statement(t, refresh)); err != nil {
						t.Fatal(err)
					}
				} else {
					enginetest.SessionSQL(t, s, refresh)
				}
				if retained {
					if e.Storage.Relations[ref.Address] != old || old.Maintenance != before {
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
				if _, err := rollback.step(enginetest.Statement(t, refresh)); err != nil {
					t.Fatal(err)
				}
				_, discardErr := rollback.step(enginetest.Statement(t, "SELECT missing"))
				if discardErr == nil {
					t.Fatal("late message error unexpectedly succeeded")
				}
				if err := rollback.end(discardErr); err != discardErr {
					t.Fatal("message discard lost actual error", err, discardErr)
				}
				if e.Storage.Relations[ref.Address] != fresh || fresh.Maintenance != stable {
					t.Fatal("rolled-back refresh changed published maintenance")
				}
				enginetest.SessionSQL(t, s, "VACUUM mv_stats")
				if fresh.Maintenance.VacuumCount != 2 || fresh.Maintenance.Tuples != 3 {
					t.Fatal("next real vacuum did not advance scan/count", fresh.Maintenance)
				}
				scanned := fresh.Maintenance
				if retained {
					emptyMessage := batchUnitStart(t, s, context.Background())
					defer emptyMessage.end(context.Canceled)
					if _, err := emptyMessage.step(enginetest.Statement(t, "REFRESH MATERIALIZED VIEW mv_stats WITH NO DATA")); err != nil {
						t.Fatal(err)
					}
					if err := emptyMessage.end(nil); err != nil {
						t.Fatal(err)
					}
				} else {
					enginetest.SessionSQL(t, s, "REFRESH MATERIALIZED VIEW mv_stats WITH NO DATA")
				}
				_, empty := vacuumStore(t, e, "mv_stats")
				if len(empty.Versions) != 0 || empty.Maintenance.Live != 0 || empty.Maintenance.Dead != 0 || empty.Maintenance.FrozenXID < 3 || empty.Maintenance.VacuumCount != scanned.VacuumCount || empty.Maintenance.LastVacuum != scanned.LastVacuum {
					t.Fatal("empty refresh inherited stale contents or invented vacuum", empty.Maintenance)
				}
				enginetest.SessionSQL(t, s, "DROP MATERIALIZED VIEW mv_stats")
				enginetest.SessionSQL(t, s, "CREATE MATERIALIZED VIEW mv_stats AS SELECT id FROM mv_source")
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
			e := engine.New()
			owner, worker := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE mv_pending_source(id INT)", "INSERT INTO mv_pending_source VALUES(1),(2)", "CREATE MATERIALIZED VIEW mv_pending AS SELECT id FROM mv_pending_source", "VACUUM mv_pending"} {
				enginetest.SessionSQL(t, owner, q)
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
			if err := scope.Gate(enginetest.Statement(t, "REFRESH MATERIALIZED VIEW mv_pending")); err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			stage := e.BeginCatalogStage(e.CatalogRead())
			defer e.DiscardCatalogStage(stage)
			d := stage.Base.MustRelation(ref.Address)
			if err := viewcmds.StageMaterializedRows(e, ctx, stage, d, old, refreshUnitRows(t, d, 3), true, scope.TxRef); err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err != nil {
				scope.Finish(err)
				t.Fatal(err)
			}
			root, ends := old.Versions, old.Ends
			enginetest.SessionSQL(t, owner, "VACUUM mv_pending")
			latest := old.Maintenance
			if canceled {
				cancel()
			}
			_, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: stage.Base.Epoch()})
			if canceled {
				if !errors.Is(err, context.Canceled) || e.Storage.Relations[ref.Address] != old || old.Maintenance != latest || !storage.SameVersionRoot(root, old.Versions) || old.Ends != ends {
					scope.Finish(err)
					t.Fatal("failed preflight changed published facts", err)
				}
			} else {
				if err != nil {
					scope.Finish(err)
					t.Fatal(err)
				}
				scope.Completed = true
				fresh := e.Storage.Relations[ref.Address]
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

// Explicit refresh stages immutable roots and maintenance until the outer end.
func vacuumMaterializedExplicitBoundary(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "concurrent"}[concurrent], func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{"CREATE TABLE explicit_source(id INT)", "INSERT INTO explicit_source VALUES(1),(2)", "CREATE MATERIALIZED VIEW explicit_mv AS SELECT id FROM explicit_source", "CREATE UNIQUE INDEX explicit_key ON explicit_mv(id)", "VACUUM explicit_mv"} {
				enginetest.SessionSQL(t, s, q)
			}
			ref, store := vacuumStore(t, e, "explicit_mv")
			root, ends, generation, facts, base := store.Versions, store.Ends, store.Generation, store.Maintenance, e.Catalog.State
			m := batchUnitStart(t, s, context.Background())
			defer m.end(context.Canceled)
			if _, err := m.step(enginetest.Statement(t, "BEGIN")); err != nil {
				t.Fatal(err)
			}
			q := "REFRESH MATERIALIZED VIEW "
			if concurrent {
				q += "CONCURRENTLY "
			}
			q += "explicit_mv"
			if _, err := m.step(enginetest.Statement(t, q)); err != nil {
				t.Fatal(err)
			}
			if err := m.end(nil); err != nil {
				t.Fatal(err)
			}
			shape := s.Transactions.Catalog.Storage[ref.Address]
			if shape.Original != store || shape.Replacement == nil || shape.Replacement == store || !storage.SameVersionRoot(shape.Root, root) || shape.Ends != ends || shape.Generation != generation {
				t.Fatal("explicit replacement lost committed predecessor", shape)
			}
			if e.Catalog.State != base || e.Storage.Relations[ref.Address] != store || !storage.SameVersionRoot(root, store.Versions) || store.Ends != ends || store.Generation != generation || store.Maintenance != facts {
				t.Fatal("private explicit refresh changed published roots/facts")
			}
			enginetest.SessionSQL(t, s, "ROLLBACK")
			if e.Catalog.State != base || e.Storage.Relations[ref.Address] != store || !storage.SameVersionRoot(root, store.Versions) || store.Ends != ends || store.Generation != generation || store.Maintenance != facts {
				t.Fatal("rolled-back explicit refresh changed published roots/facts")
			}
			enginetest.LockEmpty(t, e)
			enginetest.SessionSQL(t, s, "BEGIN")
			enginetest.SessionSQL(t, s, q)
			private := s.Transactions.Catalog.Storage[ref.Address].Replacement
			enginetest.SessionSQL(t, s, "COMMIT")
			if e.Storage.Relations[ref.Address] != private || private == store || len(private.Versions) != 2 || private.Maintenance.VacuumCount != facts.VacuumCount || private.Maintenance.LastVacuum != facts.LastVacuum {
				t.Fatal("explicit commit lost replacement or cumulative maintenance")
			}
			maintenance := private.Maintenance
			if maintenance.Scanned != facts.Scanned || maintenance.Tuples != facts.Tuples || maintenance.Updates != facts.Updates || maintenance.Deletes != facts.Deletes || maintenance.Inserts != facts.Inserts+2 || maintenance.Modifications != facts.Modifications+2 || maintenance.InsertsSince != facts.InsertsSince+2 || maintenance.Live != 2 || maintenance.Dead != 0 {
				t.Fatal("explicit commit lost final maintenance deltas", facts, maintenance)
			}
			publisher := private.Versions[0].InsertFullXID
			if publisher < 3 || maintenance.FrozenXID != publisher {
				t.Fatal("explicit replacement retained obsolete freezing horizon", publisher, maintenance)
			}
			for _, row := range private.Versions {
				if row.Frozen || row.ImportFrozen || row.InsertFullXID != publisher {
					t.Fatal("explicit replacement lacks final publisher provenance", row)
				}
			}
			if !storage.SameVersionRoot(root, store.Versions) || store.Ends != ends || store.Generation != generation || store.Maintenance != facts {
				t.Fatal("explicit commit mutated predecessor")
			}
			enginetest.LockEmpty(t, e)
		})
	}
}

func vacuumStore(t *testing.T, e *core.Engine, name string) (ids.ObjectRef, *storage.Relation) {
	t.Helper()
	ref := enginetest.CatalogRef(t, e, e.CatalogRead(), name)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	store := e.Storage.Relations[ref.Address]
	if store == nil {
		t.Fatal("missing store", name)
	}
	return ref, store
}

type batchUnitMessage struct {
	scope  *core.StatementScope
	op     *session.Operation
	ended  bool
	result error
}

func (m *batchUnitMessage) step(stmt parser.Statement) (core.Result, error) {
	if m.scope.Finished {
		next, err := m.scope.NextStatement()
		if err != nil {
			return core.Result{}, err
		}
		m.scope = next
	}
	result, err := m.scope.Execute(stmt, 0, nil)
	finish := m.scope.Finish(err)
	if err == nil {
		err = finish
	}
	return result, err
}

func (m *batchUnitMessage) end(err error) error {
	if !m.ended {
		m.result = m.scope.FinishMessage(err)
		m.ended = true
		m.op.Done()
	}
	return m.result
}

func batchUnitStart(t *testing.T, s *session.Session, ctx context.Context) *batchUnitMessage {
	t.Helper()
	op, _, err := s.AcquireCommand(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := core.EngineOf(s).StartStatement(op)
	if err != nil {
		op.Done()
		t.Fatal(err)
	}
	if err = scope.RetainMessage(); err != nil {
		_ = scope.Finish(err)
		op.Done()
		t.Fatal(err)
	}
	return &batchUnitMessage{scope: scope, op: op}
}
