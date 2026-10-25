package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
)

func TestTransactionalMaterializedRefreshJournal(t *testing.T) {
	for _, mixed := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(map[bool]string{false: "continuous", true: "mixed"}[mixed]+"/"+map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				peer := enginetest.StartSession(t, e, nil)
				for _, q := range []string{"CREATE TABLE txj_source(i INT)", "INSERT INTO txj_source VALUES(1)", "CREATE MATERIALIZED VIEW txj_mv AS SELECT i FROM txj_source", "CREATE UNIQUE INDEX txj_u ON txj_mv(i)", "CREATE INDEX txj_second ON txj_mv(i)", "CREATE TABLE txj_unrelated(i INT)", "VACUUM txj_mv"} {
					enginetest.SessionSQL(t, s, q)
				}
				ref, old := vacuumStore(t, e, "txj_mv")
				source := enginetest.CatalogRef(t, e, e.CatalogRead(), "txj_source")
				u := enginetest.CatalogRef(t, e, e.CatalogRead(), "txj_u")
				second := enginetest.CatalogRef(t, e, e.CatalogRead(), "txj_second")
				uroot, sroot := e.Storage.IndexRoots[u.Address], e.Storage.IndexRoots[second.Address]
				root, ends, generation, facts, base, frontier := old.Versions, old.Ends, old.Generation, old.Maintenance, e.Catalog.State, e.Visibility.CommitSeq()
				enginetest.SessionSQL(t, s, "BEGIN")
				defer enginetest.SessionSQL(t, s, "ROLLBACK")
				enginetest.SessionSQL(t, s, "INSERT INTO txj_source VALUES(2)")
				enginetest.SessionSQL(t, s, "REFRESH MATERIALIZED VIEW CONCURRENTLY txj_mv")
				first := s.Transactions.Catalog.Storage[ref.Address]
				firstU := index.EffectiveRoot(e.Storage.IndexRoots, s.Transactions.Catalog, u.Address)
				firstSecond := index.EffectiveRoot(e.Storage.IndexRoots, s.Transactions.Catalog, second.Address)
				if first.Original != old || first.Replacement == nil || len(first.Replacement.Versions) != 2 || firstU == uroot || firstSecond == sroot || len(firstU.Entries) != 2 || len(firstSecond.Entries) != 2 {
					t.Fatal("missing private replacement/index roots", first)
				}
				enginetest.SessionSQL(t, s, "SAVEPOINT kept")
				enginetest.SessionSQL(t, s, "DELETE FROM txj_source WHERE i=2")
				enginetest.SessionSQL(t, s, "INSERT INTO txj_source VALUES(3)")
				q := "REFRESH MATERIALIZED VIEW CONCURRENTLY txj_mv"
				if mixed {
					q = "REFRESH MATERIALIZED VIEW txj_mv"
				}
				enginetest.SessionSQL(t, s, q)
				enginetest.SessionSQL(t, s, "REFRESH MATERIALIZED VIEW CONCURRENTLY txj_mv")
				latest := s.Transactions.Catalog.Storage[ref.Address]
				if latest.Original != old || latest.Replacement == first.Replacement || len(latest.Replacement.Versions) != 2 || !latest.MaterializedConcurrent || latest.MaterializedContinuous == mixed {
					t.Fatal("first predecessor/latest-only lineage", latest)
				}
				if e.Catalog.State != base || e.Visibility.CommitSeq() != frontier || e.Storage.Relations[ref.Address] != old || e.Storage.IndexRoots[u.Address] != uroot || e.Storage.IndexRoots[second.Address] != sroot || !storage.SameVersionRoot(root, old.Versions) || old.Ends != ends || old.Generation != generation || old.Maintenance != facts {
					t.Fatal("private refresh mutated committed roots/facts")
				}
				for _, replacement := range []*storage.Relation{first.Replacement, latest.Replacement} {
					for _, row := range replacement.Versions {
						if row.Born != 0 {
							t.Fatal("private row stamped", row)
						}
					}
					if replacement.MaterializedLineage != nil {
						t.Fatal("private lineage stamped")
					}
				}
				// A canceled real statement cannot stamp or replace the previous candidate.
				ctx, cancel := context.WithCancel(context.Background())
				op, err := s.Acquire(ctx)
				if err != nil {
					t.Fatal(err)
				}
				cancel()
				_, err = e.Execute(op, enginetest.Statement(t, "REFRESH MATERIALIZED VIEW CONCURRENTLY txj_mv"), 0, nil)
				op.Done()
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				enginetest.SessionSQL(t, s, "ROLLBACK TO kept")
				restored := s.Transactions.Catalog.Storage[ref.Address]
				if restored.Replacement != first.Replacement || restored.Original != old || !restored.MaterializedContinuous || index.EffectiveRoot(e.Storage.IndexRoots, s.Transactions.Catalog, u.Address) != firstU || index.EffectiveRoot(e.Storage.IndexRoots, s.Transactions.Catalog, second.Address) != firstSecond {
					t.Fatal("savepoint lost exact private roots")
				}
				enginetest.SessionSQL(t, s, "ROLLBACK TO kept")
				enginetest.SessionSQL(t, s, "RELEASE kept")
				enginetest.SessionSQL(t, peer, "INSERT INTO txj_unrelated VALUES(9)")
				peerFrontier := e.Visibility.CommitSeq()
				if commit {
					enginetest.SessionSQL(t, s, "COMMIT")
					fresh := e.Storage.Relations[ref.Address]
					if fresh != first.Replacement || e.Visibility.CommitSeq() != peerFrontier+1 || len(e.Storage.Relations[source.Address].Versions) != 2 || len(e.Storage.IndexRoots[u.Address].Entries) != 2 || len(e.Storage.IndexRoots[second.Address].Entries) != 2 {
						t.Fatal("incomplete combined publication")
					}
					for _, row := range fresh.Versions {
						if row.Born != peerFrontier+1 {
							t.Fatal("wrong final frontier", row)
						}
					}
				} else {
					enginetest.SessionSQL(t, s, "ROLLBACK")
					if e.Storage.Relations[ref.Address] != old || e.Storage.IndexRoots[u.Address] != uroot || e.Storage.IndexRoots[second.Address] != sroot || len(e.Storage.Relations[source.Address].Versions) != 1 {
						t.Fatal("rollback leaked private roots")
					}
				}
				unrelated := enginetest.CatalogRef(t, e, e.CatalogRead(), "txj_unrelated")
				if len(e.Storage.Relations[unrelated.Address].Versions) != 1 {
					t.Fatal("lost unrelated commit")
				}
				if !storage.SameVersionRoot(root, old.Versions) || old.Ends != ends || old.Generation != generation || old.Maintenance != facts {
					t.Fatal("old immutable facts changed")
				}
				enginetest.LockEmpty(t, e)
			})
		}
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
