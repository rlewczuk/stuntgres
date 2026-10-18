package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
)

func TestDriverBatchMaterializedReplacementRoots(t *testing.T) {
	for _, private := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			t.Run(map[bool]string{false: "existing_", true: "private_"}[private]+map[bool]string{false: "rollback", true: "commit"}[commit], func(t *testing.T) {
				e := New()
				s := sessionStart(t, e, nil)
				for _, q := range []string{"CREATE TABLE refresh_source(v INT)", "INSERT INTO refresh_source VALUES(1)", "CREATE INDEX refresh_source_idx ON refresh_source(v)", "CREATE MATERIALIZED VIEW refresh_target AS SELECT v FROM refresh_source"} {
					sessionSQL(t, s, q)
				}
				base, frontier := e.catalog.state, e.commitSeq
				source := catalogRef(t, e, e.CatalogRead(), "refresh_source")
				index := catalogRef(t, e, e.CatalogRead(), "refresh_source_idx")
				indexRoot := e.indexRoots[index.Address]
				m := batchUnitStart(t, s, context.Background())
				defer m.end(context.Canceled)
				name := "refresh_target"
				if private {
					name = "refresh_private"
					if _, err := m.step(statement(t, "CREATE MATERIALIZED VIEW "+name+" AS SELECT v FROM refresh_source WITH DATA")); err != nil {
						t.Fatal(err)
					}
				}
				e.mu.Lock()
				view := e.privateCatalogLocked(s.transactions.catalog)
				e.mu.Unlock()
				target := catalogRef(t, e, view, name)
				original := e.storage[target.Address]
				originalDescriptor := e.catalog.state.Get(target.Address)
				var root []rowVersion
				var ends *deletionRoot
				var generation uint64
				if original != nil {
					root, ends, generation = original.versions, original.ends, original.generation
				}
				if _, err := m.step(statement(t, "INSERT INTO refresh_source VALUES(2)")); err != nil {
					t.Fatal(err)
				}
				var previous *relationStorage
				for _, suffix := range []string{"WITH DATA", "WITH NO DATA", "WITH DATA"} {
					e.mu.Lock()
					entry := e.privateStorageLocked(s.transactions.catalog, target.Address)
					e.mu.Unlock()
					if _, err := m.step(statement(t, "REFRESH MATERIALIZED VIEW "+name+" "+suffix)); err != nil {
						t.Fatal(err)
					}
					shape := s.transactions.catalog.storage[target.Address]
					if shape.original != original || !sameVersionRoot(shape.root, root) || shape.ends != ends || shape.generation != generation || shape.replacement == entry || shape.replacement == previous || shape.replacement.generation != entry.generation+1 || !shape.rewritten || shape.hasSnapshot || shape.relation != s.transactions.catalog.descriptors[target.Address] {
						t.Fatal("replacement lost first committed or immediate private predecessor", shape)
					}
					if len(s.transactions.writes[target.Address]) != 0 {
						t.Fatal("private CREATE initial rows appended to replacement")
					}
					want := 2
					if suffix == "WITH NO DATA" {
						want = 0
					}
					if len(shape.replacement.versions) != want || shape.relation.Populated != (want != 0) {
						t.Fatal("incomplete private population", shape)
					}
					for _, row := range shape.replacement.versions {
						if row.born != 0 {
							t.Fatal("replacement row born before true end", row)
						}
					}
					if e.catalog.state != base || e.commitSeq != frontier || e.storage[target.Address] != original || e.catalog.state.Get(target.Address) != originalDescriptor || e.indexRoots[index.Address] != indexRoot {
						t.Fatal("replacement/source index escaped before message end")
					}
					previous = shape.replacement
				}
				latest := previous
				if commit {
					if err := m.end(nil); err != nil {
						t.Fatal(err)
					}
					if e.storage[target.Address] != latest || e.commitSeq != frontier+1 || len(e.storage[source.Address].versions) != 2 || len(e.indexRoots[index.Address].entries) != 2 || len(indexRoot.entries) != 1 {
						t.Fatal("source rows/index and latest replacement did not publish together")
					}
					for _, row := range latest.versions {
						if row.born != frontier+1 {
							t.Fatal("replacement row birth is not true-end frontier", row)
						}
					}
				} else {
					_, err := m.step(statement(t, "SELECT missing"))
					if err == nil || m.end(err) != err {
						t.Fatal("late failure disposition", err)
					}
					if e.catalog.state != base || e.commitSeq != frontier || e.storage[target.Address] != original || e.indexRoots[index.Address] != indexRoot || len(e.storage[source.Address].versions) != 1 {
						t.Fatal("late failure published replacement/source/index")
					}
					for _, row := range latest.versions {
						if row.born != 0 {
							t.Fatal("discarded private replacement acquired birth", row)
						}
					}
				}
				if original != nil && (!sameVersionRoot(original.versions, root) || original.ends != ends || original.generation != generation) {
					t.Fatal("refresh mutated retained predecessor")
				}
			})
		}
	}
}

func TestDriverBatchMaterializedPublicationGuards(t *testing.T) {
	for _, fence := range []string{"cancel", "epoch", "ref", "generation", "ends", "store", "descriptor", "roles", "source_index_generation"} {
		t.Run(fence, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE refresh_source(v INT)", "INSERT INTO refresh_source VALUES(1)", "CREATE INDEX refresh_source_idx ON refresh_source(v)", "CREATE MATERIALIZED VIEW refresh_target AS SELECT v FROM refresh_source"} {
				sessionSQL(t, s, q)
			}
			target := catalogRef(t, e, e.CatalogRead(), "refresh_target")
			source := catalogRef(t, e, e.CatalogRead(), "refresh_source")
			index := catalogRef(t, e, e.CatalogRead(), "refresh_source_idx")
			original, indexRoot := e.storage[target.Address], e.indexRoots[index.Address]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			m := batchUnitStart(t, s, ctx)
			defer m.end(context.Canceled)
			for _, q := range []string{"CREATE INDEX refresh_pending_idx ON refresh_source(v)", "INSERT INTO refresh_source VALUES(2)", "REFRESH MATERIALIZED VIEW refresh_target"} {
				if _, err := m.step(statement(t, q)); err != nil {
					t.Fatal(err)
				}
			}
			e.mu.Lock()
			view := e.privateCatalogLocked(s.transactions.catalog)
			e.mu.Unlock()
			pending := catalogRef(t, e, view, "refresh_pending_idx")
			replacement := s.transactions.catalog.storage[target.Address].replacement
			var err error
			switch fence {
			case "cancel":
				cancel()
			case "epoch":
				next := cloneCatalog(e.catalog.state)
				next.SetEpoch(next.Epoch() + 1)
				e.catalog.state = next
			case "ref":
				stale := s.transactions.ref
				stale = withTxID(stale, stale.ID()+1)
				err = e.publishWrites(m.op, stale, s.transactions.writes, s.transactions.deletes)
				if err == nil {
					t.Fatal("foreign incarnation admitted materialized replacement")
				}
			case "generation":
				original.generation++
			case "ends":
				original.ends = &deletionRoot{}
			case "store":
				copy := *original
				e.storage[target.Address] = &copy
			case "descriptor":
				next := cloneCatalog(e.catalog.state)
				copy := *next.MustRelation(target.Address)
				copy.ObjectRevision++
				next.setDescriptor(target.Address, &copy)
				e.catalog.state = next
			case "roles":
				copy := *e.roles
				copy.revision++
				e.roles = &copy
			case "source_index_generation":
				e.storage[source.Address].generation++
			}
			state, store, frontier := e.catalog.state, e.storage[target.Address], e.commitSeq
			err = m.end(err)
			if err == nil || e.catalog.state != state || e.storage[target.Address] != store || e.commitSeq != frontier || e.indexRoots[index.Address] != indexRoot || e.indexRoots[pending.Address] != nil || len(e.storage[source.Address].versions) != 1 {
				t.Fatal("rejected replacement published rows/catalog/index", err)
			}
			if fence == "cancel" && !errors.Is(err, context.Canceled) || fence == "epoch" && !errors.Is(err, ids.ErrCatalogReplaced) {
				t.Fatal("publication primary changed", err)
			}
			for _, row := range replacement.versions {
				if row.born != 0 {
					t.Fatal("failed final fence stamped private rows", row)
				}
			}
		})
	}
}

func TestDriverBatchMaterializedPromotionSavepoint(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE refresh_source(v INT)", "INSERT INTO refresh_source VALUES(1)", "CREATE MATERIALIZED VIEW refresh_target AS SELECT v FROM refresh_source"} {
		sessionSQL(t, s, q)
	}
	target := catalogRef(t, e, e.CatalogRead(), "refresh_target")
	source := catalogRef(t, e, e.CatalogRead(), "refresh_source")
	original, frontier := e.storage[target.Address], e.commitSeq
	m := batchUnitStart(t, s, context.Background())
	defer m.end(context.Canceled)
	for _, q := range []string{"INSERT INTO refresh_source VALUES(2)", "REFRESH MATERIALIZED VIEW refresh_target"} {
		if _, err := m.step(statement(t, q)); err != nil {
			t.Fatal(err)
		}
	}
	shape := s.transactions.catalog.storage[target.Address]
	ref, ticket, start := s.transactions.ref, s.transactions.ticket, s.transactions.start
	for _, q := range []string{"BEGIN", "SAVEPOINT kept", "INSERT INTO refresh_source VALUES(3)"} {
		if _, err := m.step(statement(t, q)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := m.step(statement(t, "SELECT missing"))
	if err == nil || m.end(err) != err || s.transactions.state != TxAborted {
		t.Fatal("promoted mark did not preserve failed-block recovery", err)
	}
	sessionSQL(t, s, "ROLLBACK TO kept")
	restored := s.transactions.catalog.storage[target.Address]
	if s.transactions.state != TxActive || s.transactions.ref != ref || s.transactions.ticket != ticket || s.transactions.start != start || restored.original != shape.original || restored.replacement != shape.replacement || restored.generation != shape.generation || restored.ends != shape.ends || !restored.rewritten || len(s.transactions.writes[source.Address]) != 1 || e.storage[target.Address] != original || e.commitSeq != frontier {
		t.Fatal("savepoint recovery rebuilt or published retained replacement authority")
	}
	sessionSQL(t, s, "COMMIT")
	if e.storage[target.Address] != shape.replacement || len(shape.replacement.versions) != 2 || e.commitSeq != frontier+1 {
		t.Fatal("promoted replacement did not survive savepoint publication")
	}
	for _, row := range shape.replacement.versions {
		if row.born != frontier+1 {
			t.Fatal("promoted replacement row birth preceded commit", row)
		}
	}
}
