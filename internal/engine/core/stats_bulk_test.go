package core_test

import (
	"context"
	"errors"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
	"stuntgres/internal/sqltype"
	"testing"
)

func statsOwnerTimestamp(t *testing.T, e *core.Engine, s *session.Session, ref ids.ObjectRef, capture bool) sqltype.Value {
	t.Helper()
	op := enginetest.TransactionOperation(t, s)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	if capture {
		e.Mu.Lock()
		auth, err := e.AuthorizationLocked(op)
		e.Mu.Unlock()
		if err != nil {
			t.Fatal(err)
		}
		runtime := core.NewCatalogRuntime(core.CatalogRuntimeFields{Engine: e, Auth: auth, Settings: s.Settings})
		runtime.TableStatistics(ref.Address)
	}
	ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Host: core.HostOf(scope)}}
	value, err := ctx.StatsSnapshotValue(false, enginetest.Usage(e, sqltype.Timestamptz).Type)
	if err != nil {
		t.Fatal(err)
	}
	if err = scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	return value
}
func statsBulkLifecycle(t *testing.T) {
	t.Helper()
	t.Run("direct_late_finish", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE stats_bulk_source(id integer)")
		enginetest.SessionSQL(t, s, "INSERT INTO stats_bulk_source VALUES (1),(2)")
		var ref ids.ObjectRef
		func() {
			op := enginetest.TransactionOperation(t, s)
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			result, err := scope.Execute(enginetest.Statement(t, "CREATE TABLE stats_bulk_direct AS SELECT id FROM stats_bulk_source"), 0, nil)
			if err != nil || result.Count != 2 || !scope.Completed || scope.Catalog != nil {
				t.Fatal("statistics changed physical direct-publication route", result, err)
			}
			ref = enginetest.RoleTableRef(t, e, "stats_bulk_direct")
			late := errors.New("late Finish failure")
			if err = scope.Finish(late); !errors.Is(err, late) {
				t.Fatal("lost late primary", err)
			}
		}()
		statsEnd(t, s)
		if f := e.Statistics.Read(statistics.Key{Object: ref}); f.Inserts != 2 || f.Live != 2 || f.Dead != 0 {
			t.Fatal("late Finish retracted accepted physical population", f)
		}
	})
	t.Run("rejected_population", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE stats_bulk_source(id integer)")
		enginetest.SessionSQL(t, s, "INSERT INTO stats_bulk_source VALUES (1),(2)")
		sentinel := errors.New("population publication veto")
		var address ids.ObjectAddress
		e.Catalog.Providers[ids.RelationClassID] = statsPopulationVeto{Provider: e.Catalog.Providers[ids.RelationClassID], address: &address, failure: sentinel}
		func() {
			op := enginetest.TransactionOperation(t, s)
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			_, err = scope.Execute(enginetest.Statement(t, "CREATE TABLE stats_bulk_rejected AS SELECT id FROM stats_bulk_source"), 0, nil)
			if !errors.Is(err, sentinel) {
				t.Fatal("missed real definition rejection", err)
			}
			if failure := scope.Finish(err); !errors.Is(failure, sentinel) {
				t.Fatal(failure)
			}
		}()
		if address == (ids.ObjectAddress{}) || e.CatalogRead().Get(address) != nil || e.Storage.Relations[address] != nil {
			t.Fatal("rejected population published physical authority")
		}
		statsEnd(t, s)
		key := statistics.Key{Object: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), address)}
		if f := e.Statistics.Read(key); f.Inserts != 2 || f.Live != 0 || f.Dead != 2 {
			t.Fatal("rejected population lost attempts or published estimates", f)
		}
	})
	t.Run("bulk_prepare_reader_transfer", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		peer := enginetest.StartSession(t, e, nil)
		enginetest.SessionSQL(t, s, "CREATE TABLE stats_bulk_source(id integer)")
		enginetest.SessionSQL(t, s, "INSERT INTO stats_bulk_source VALUES (1),(2)")
		configureStats(s, "snapshot")
		enginetest.SessionSQL(t, s, "BEGIN")
		enginetest.SessionSQL(t, s, "CREATE TABLE stats_bulk_pending AS SELECT id FROM stats_bulk_source")
		ref := enginetest.RoleTableRef(t, e, "stats_bulk_source")
		if statsOwnerTimestamp(t, e, s, ref, true).IsNull() {
			t.Fatal("missing pre-PREPARE reader")
		}
		// The pending object exists only in the current journal, not the catalog.
		var key statistics.Key
		if len(s.Transactions.Stats) != 1 {
			t.Fatal("bulk ledger captured unrelated objects")
		}
		for candidate, fact := range s.Transactions.Stats {
			if fact.Inserts == 2 {
				key = candidate
			}
		}
		if key.Object == (ids.ObjectRef{}) {
			t.Fatal("bulk stage omitted transaction ledger")
		}
		enginetest.SessionSQL(t, s, "PREPARE TRANSACTION 'stats_bulk_prepared'")
		if !statsOwnerTimestamp(t, e, s, ref, false).IsNull() {
			t.Fatal("PREPARE retained old reader")
		}
		statsEnd(t, s)
		if f := e.Statistics.Read(key); f.Inserts != 0 || f.Live != 0 || f.InsertsSince != 0 {
			t.Fatal("bulk PREPARE exit published unresolved tuple counters", f)
		}
		enginetest.SessionSQL(t, peer, "COMMIT PREPARED 'stats_bulk_prepared'")
		statsEnd(t, peer)
		if f := e.Statistics.Read(key); f.Inserts != 2 || f.Live != 2 {
			t.Fatal("bulk prepared commit lost/doubled ledger", f)
		}
	})
	t.Run("concurrent_monitoring_not_physical_rewrite", func(t *testing.T) {
		e := engine.New()
		s := enginetest.StartSession(t, e, nil)
		for _, q := range []string{"CREATE TABLE stats_bulk_source(id integer)", "INSERT INTO stats_bulk_source VALUES (1),(2)", "CREATE MATERIALIZED VIEW stats_bulk_mv AS SELECT id FROM stats_bulk_source", "CREATE UNIQUE INDEX stats_bulk_key ON stats_bulk_mv(id)", "REFRESH MATERIALIZED VIEW CONCURRENTLY stats_bulk_mv"} {
			enginetest.SessionSQL(t, s, q)
		}
		ref := enginetest.RoleTableRef(t, e, "stats_bulk_mv")
		statsEnd(t, s)
		if f := e.Statistics.Read(statistics.Key{Object: ref}); f.Inserts != 2 || f.Deletes != 0 || f.Live != 2 {
			t.Fatal("unchanged concurrent refresh recollected replacement rows", f)
		}
		e.Mu.Lock()
		physical := e.Storage.Relations[ref.Address].Maintenance.Inserts
		e.Mu.Unlock()
		if physical != 4 {
			t.Fatal("monitoring collection changed existing physical rewrite bookkeeping", physical)
		}
	})
}

type statsPopulationVeto struct {
	catalog.Provider
	address *ids.ObjectAddress
	failure error
}

func (p statsPopulationVeto) Validate(read catalog.Read, d catalog.Descriptor) error {
	if err := p.Provider.Validate(read, d); err != nil {
		return err
	}
	if d.Name() == "stats_bulk_rejected" {
		*p.address = d.Address()
		return p.failure
	}
	return nil
}
