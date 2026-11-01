package tablecmds_test

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/engine/visibility"
)

type fullMixedPin struct {
	reader   *session.Session
	root     visibility.Root
	indexes  map[ids.ObjectAddress]visibility.IndexSnapshot
	owned    fullOwnedSnapshot
	frontier ids.CommitSeq
}

func pinFullMixed(t *testing.T, e *core.Engine, name string, role ...string) fullMixedPin {
	t.Helper()
	reader := sessionStart(t, e, nil)
	if len(role) != 0 {
		sessionSQL(t, reader, "SET ROLE "+role[0])
	}
	sessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
	sessionSQL(t, reader, "SELECT id FROM full_mixed_anchor").Rows.Close()
	owned := captureFullOwned(t, e, name)
	reader.Transactions.Mu.Lock()
	root, ok := reader.Transactions.Roots[owned.ref.Address]
	frontier := reader.Transactions.Frontier
	reader.Transactions.Mu.Unlock()
	if !ok || root.Storage != owned.store || !storage.SameVersionRoot(root.Versions, owned.rows) || root.Ends != owned.ends {
		t.Fatal("anchor did not establish the actual untouched target roots")
	}
	for address, index := range owned.indexes {
		snapshot, exists := root.Indexes[address]
		if !exists || snapshot.Root != index || snapshot.Definition != owned.catalog.MustRelation(address) {
			t.Fatal("snapshot did not own the actual unique-index descriptor/root")
		}
	}
	return fullMixedPin{reader: reader, root: root, indexes: maps.Clone(root.Indexes), owned: owned, frontier: frontier}
}

func (pin fullMixedPin) retained(t *testing.T) {
	t.Helper()
	pin.owned.retained(t)
	pin.reader.Transactions.Mu.Lock()
	current := pin.reader.Transactions.Roots[pin.owned.ref.Address]
	frontier := pin.reader.Transactions.Frontier
	pin.reader.Transactions.Mu.Unlock()
	if frontier != pin.frontier || current.Storage != pin.root.Storage || current.Relation != pin.root.Relation || current.Ends != pin.root.Ends || !storage.SameVersionRoot(current.Versions, pin.root.Versions) || !maps.Equal(current.Indexes, pin.indexes) {
		t.Fatal("mixed history replaced the snapshot owner's exact root/index map")
	}
}

func fullMixedRows(t *testing.T, s *session.Session, name string, want ...int64) {
	t.Helper()
	result := sessionSQL(t, s, "SELECT id FROM "+name+" ORDER BY id")
	rows := enginetest.Tuples(result)
	result.Rows.Close()
	if len(rows) != len(want) {
		t.Fatal("mixed-history row count", name, rows, want)
	}
	for i, id := range want {
		if rows[i][0] != id {
			t.Fatal("mixed-history value", name, rows, want)
		}
	}
}

func fullMixedReadError(t *testing.T, e *core.Engine, s *session.Session, name string) error {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	result, err := e.Execute(op, enginetest.Statement(t, "SELECT id FROM "+name), 0, nil)
	if result.Rows != nil {
		result.Rows.Close()
	}
	return err
}

func fullMixedMVStep(t *testing.T, e *core.Engine, writer *session.Session, full, journal bool) {
	t.Helper()
	before := captureFullOwned(t, e, "full_mixed_mv")
	if full {
		sessionSQL(t, writer, "VACUUM FULL full_mixed_mv")
		before.replaced(t, e)
		return
	}
	if journal {
		sessionSQL(t, writer, "BEGIN")
	}
	sessionSQL(t, writer, "REFRESH MATERIALIZED VIEW CONCURRENTLY full_mixed_mv")
	if journal {
		sessionSQL(t, writer, "COMMIT")
	}
	before.retained(t)
	after := captureFullOwned(t, e, "full_mixed_mv")
	origin := before.retainedOrigin
	if origin == 0 {
		origin = before.descriptor.EffectiveStorageLocator(catalog.NewRead(&e.Catalog.Owner, before.catalog))
	}
	if after.store == before.store || after.descriptor.EffectiveStorageLocator(e.CatalogRead()) == before.descriptor.EffectiveStorageLocator(catalog.NewRead(&e.Catalog.Owner, before.catalog)) || after.retainedOrigin != origin || after.truncateBoundary != before.truncateBoundary || after.materializedLineage == nil || before.store.MaterializedLineage != after.materializedLineage || !view.SameRetainedLineage(before.descriptor, after.descriptor, before.store, after.store) {
		t.Fatal("genuine concurrent MV did not preserve actual family/boundary/token with fresh storage")
	}
	for address, old := range before.indexes {
		if after.indexes[address] == old || after.catalog.MustRelation(address).StorageLocator == before.catalog.MustRelation(address).EffectiveStorageLocator(catalog.NewRead(&e.Catalog.Owner, before.catalog)) {
			t.Fatal("changed concurrent MV retained an old logical index incarnation")
		}
	}
}

func TestVacuumFullMixedRetention(t *testing.T) {
	for _, fullFirst := range []bool{false, true} {
		for _, journal := range []bool{false, true} {
			name := map[bool]string{false: "concurrent_then_full", true: "full_then_concurrent"}[fullFirst] + map[bool]string{false: "/direct", true: "/journal"}[journal]
			t.Run(name, func(t *testing.T) {
				e := engine.New()
				writer := sessionStart(t, e, nil)
				for _, query := range []string{"CREATE TABLE full_mixed_anchor(id INT)", "INSERT INTO full_mixed_anchor VALUES(9)", "CREATE TABLE full_mixed_base(id INT PRIMARY KEY)", "INSERT INTO full_mixed_base VALUES(1),(2)", "CREATE MATERIALIZED VIEW full_mixed_mv AS SELECT id FROM full_mixed_base", "CREATE UNIQUE INDEX full_mixed_mv_ix ON full_mixed_mv(id)"} {
					sessionSQL(t, writer, query)
				}
				a := pinFullMixed(t, e, "full_mixed_mv")
				origin := a.owned.descriptor.EffectiveStorageLocator(e.CatalogRead())
				sessionSQL(t, writer, "UPDATE full_mixed_base SET id=3 WHERE id=1")
				fullMixedMVStep(t, e, writer, fullFirst, journal)
				fullMixedMVStep(t, e, writer, !fullFirst, journal)
				b := pinFullMixed(t, e, "full_mixed_mv")
				sessionSQL(t, writer, "UPDATE full_mixed_base SET id=4 WHERE id=2")
				fullMixedMVStep(t, e, writer, fullFirst, journal)
				fullMixedMVStep(t, e, writer, !fullFirst, journal)
				current := captureFullOwned(t, e, "full_mixed_mv")
				if current.retainedOrigin != origin || current.truncateBoundary != 0 || current.materializedLineage == nil || current.truncateLineage != nil {
					t.Fatal("alternation lost bounded preservation provenance")
				}
				fullMixedRows(t, a.reader, "full_mixed_mv", 1, 2)
				fullMixedRows(t, b.reader, "full_mixed_mv", 2, 3)
				fullMixedRows(t, writer, "full_mixed_mv", 3, 4)
				a.retained(t)
				b.retained(t)
				sessionSQL(t, a.reader, "ROLLBACK")
				sessionSQL(t, b.reader, "ROLLBACK")
				before := captureFullOwned(t, e, "full_mixed_mv")
				sessionSQL(t, writer, "VACUUM FULL full_mixed_mv")
				reclaimed := before.replaced(t, e)
				if len(reclaimed.Versions) != 2 || len(reclaimed.Ends.Rows) != 0 {
					t.Fatal("quiescent mixed history did not reclaim obsolete heads")
				}
				a.owned.retained(t)
				b.owned.retained(t)
				fullMixedRows(t, writer, "full_mixed_mv", 3, 4)
				lockEmpty(t, e)
			})
		}
	}
	t.Run("repeated_truncate_full", func(t *testing.T) {
		e := engine.New()
		writer := sessionStart(t, e, nil)
		for _, query := range []string{"CREATE TABLE full_mixed_anchor(id INT)", "INSERT INTO full_mixed_anchor VALUES(9)", "CREATE TABLE full_mixed_heap(id INT PRIMARY KEY)", "INSERT INTO full_mixed_heap VALUES(1),(2)"} {
			sessionSQL(t, writer, query)
		}
		a := pinFullMixed(t, e, "full_mixed_heap")
		origin := a.owned.descriptor.EffectiveStorageLocator(e.CatalogRead())
		rewrite := func() {
			before := captureFullOwned(t, e, "full_mixed_heap")
			sessionSQL(t, writer, "VACUUM FULL full_mixed_heap")
			before.replaced(t, e)
		}
		truncate := func() ids.CommitSeq {
			before := captureFullOwned(t, e, "full_mixed_heap")
			sessionSQL(t, writer, "TRUNCATE full_mixed_heap")
			before.retained(t)
			after := captureFullOwned(t, e, "full_mixed_heap")
			if after.retainedOrigin != origin || after.truncateBoundary != e.Visibility.CommitSeq() || after.truncateBoundary <= before.truncateBoundary || after.truncateLineage == nil || before.store.TruncateLineage != after.truncateLineage || after.descriptor.StorageLocator == before.descriptor.EffectiveStorageLocator(catalog.NewRead(&e.Catalog.Owner, before.catalog)) || view.SameRetainedLineage(before.descriptor, after.descriptor, before.store, after.store) || !view.CrossedTruncate(before.descriptor, after.descriptor, before.store, after.store) {
				t.Fatal("TRUNCATE did not publish its actual new boundary and distinct policy")
			}
			return after.truncateBoundary
		}
		rewrite()
		firstBoundary := truncate()
		sessionSQL(t, writer, "INSERT INTO full_mixed_heap VALUES(3)")
		b := pinFullMixed(t, e, "full_mixed_heap")
		rewrite()
		secondBoundary := truncate()
		if secondBoundary <= firstBoundary {
			t.Fatal("repeated genuine boundary did not advance")
		}
		sessionSQL(t, writer, "INSERT INTO full_mixed_heap VALUES(4)")
		c := pinFullMixed(t, e, "full_mixed_heap")
		rewrite()
		current := captureFullOwned(t, e, "full_mixed_heap")
		if current.retainedOrigin != origin || current.truncateBoundary != secondBoundary || current.truncateLineage != b.owned.truncateLineage || current.materializedLineage != nil || len(current.rows) != 1 || current.rows[0].Born <= a.frontier || current.rows[0].Born <= b.frontier || current.rows[0].Born > c.frontier {
			t.Fatal("FULL changed actual truncate provenance or insertion birth/frontiers")
		}
		fullMixedRows(t, a.reader, "full_mixed_heap")
		fullMixedRows(t, b.reader, "full_mixed_heap")
		fullMixedRows(t, c.reader, "full_mixed_heap", 4)
		fullMixedRows(t, writer, "full_mixed_heap", 4)
		sessionSQL(t, c.reader, "INSERT INTO full_mixed_heap VALUES(5)")
		fullMixedRows(t, c.reader, "full_mixed_heap", 4, 5)
		fullMixedRows(t, writer, "full_mixed_heap", 4)
		for _, pin := range []fullMixedPin{a, b, c} {
			pin.retained(t)
			sessionSQL(t, pin.reader, "ROLLBACK")
		}
		fullMixedRows(t, writer, "full_mixed_heap", 4)
		backward := *current.store
		backward.TruncateBoundary = firstBoundary
		if view.SameRetainedLineage(current.descriptor, current.descriptor, current.store, &backward) || view.CrossedTruncate(current.descriptor, current.descriptor, current.store, &backward) {
			t.Fatal("reverse truncate boundary licensed a read policy")
		}
		lockEmpty(t, e)
	})
	t.Run("rejected_truncate_and_private_destructive_boundary", func(t *testing.T) {
		e := engine.New()
		writer := sessionStart(t, e, nil)
		for _, query := range []string{"CREATE TABLE full_mixed_anchor(id INT)", "INSERT INTO full_mixed_anchor VALUES(9)", "CREATE TABLE full_mixed_heap(id INT PRIMARY KEY)", "INSERT INTO full_mixed_heap VALUES(1)", "VACUUM FULL full_mixed_heap"} {
			sessionSQL(t, writer, query)
		}
		before := captureFullOwned(t, e, "full_mixed_heap")
		func() {
			op, err := writer.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(errors.New("unfinished mixed TRUNCATE"))
			if _, err := scope.Execute(enginetest.Statement(t, "TRUNCATE full_mixed_heap"), 0, nil); err != nil {
				t.Fatal(err)
			}
			candidate := scope.Catalog.Storage[before.ref.Address]
			if !candidate.Truncated || candidate.Replacement.RetainedOrigin != 0 || candidate.Replacement.TruncateBoundary != 0 || before.store.TruncateLineage != before.truncateLineage {
				t.Fatal("staging invented a committed family/boundary/token stamp")
			}
			if err := scope.Finish(context.Canceled); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled TRUNCATE", err)
			}
		}()
		before.rejected(t, e)
		sessionSQL(t, writer, "TRUNCATE full_mixed_heap")
		sessionSQL(t, writer, "INSERT INTO full_mixed_heap VALUES(3)")
		before = captureFullOwned(t, e, "full_mixed_heap")
		if before.truncateBoundary == 0 {
			t.Fatal("missing genuine prior boundary")
		}
		reader := pinFullMixed(t, e, "full_mixed_heap")
		sessionSQL(t, writer, "BEGIN")
		sessionSQL(t, writer, "ALTER TABLE full_mixed_heap ALTER COLUMN id TYPE BIGINT USING id::bigint")
		sessionSQL(t, writer, "TRUNCATE full_mixed_heap")
		shape := writer.Transactions.Catalog.Storage[before.ref.Address]
		if shape.Truncated || shape.Replacement.RetainedOrigin != 0 || shape.Replacement.TruncateBoundary != 0 {
			t.Fatal("private destructive rewrite acquired pure TRUNCATE authority")
		}
		sessionSQL(t, writer, "COMMIT")
		before.retained(t)
		after := captureFullOwned(t, e, "full_mixed_heap")
		if after.retainedOrigin != 0 || after.truncateBoundary != 0 || after.truncateLineage == before.truncateLineage || view.SameRetainedLineage(before.descriptor, after.descriptor, before.store, after.store) || view.CrossedTruncate(before.descriptor, after.descriptor, before.store, after.store) {
			t.Fatal("destructive private history was smuggled into a retained family")
		}
		reader.retained(t)
		if err := fullMixedReadError(t, e, reader.reader, "full_mixed_heap"); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("shape boundary admitted an old stable root", err)
		}
		reader.owned.retained(t)
		sessionSQL(t, reader.reader, "ROLLBACK")
		lockEmpty(t, e)
	})
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "actual_commit_not_staged_frontier", true: "prepared_commit_not_staged_frontier"}[prepared], func(t *testing.T) {
			e := engine.New()
			writer, peer := sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, query := range []string{"CREATE TABLE full_mixed_anchor(id INT)", "INSERT INTO full_mixed_anchor VALUES(9)", "CREATE TABLE full_mixed_heap(id INT PRIMARY KEY)", "INSERT INTO full_mixed_heap VALUES(1)", "VACUUM FULL full_mixed_heap"} {
				sessionSQL(t, writer, query)
			}
			a := pinFullMixed(t, e, "full_mixed_heap")
			sessionSQL(t, writer, "BEGIN")
			sessionSQL(t, writer, "TRUNCATE full_mixed_heap")
			sessionSQL(t, writer, "TRUNCATE full_mixed_heap")
			staged := e.Visibility.CommitSeq()
			shape := writer.Transactions.Catalog.Storage[a.owned.ref.Address]
			if !shape.Truncated || shape.Replacement.TruncateBoundary != 0 || shape.Replacement.RetainedOrigin != 0 {
				t.Fatal("staged truncate advertised a future commit boundary")
			}
			preparedActive := false
			t.Cleanup(func() {
				if preparedActive {
					sessionSQL(t, peer, "ROLLBACK PREPARED 'full_mixed_truncate'")
				}
			})
			if prepared {
				sessionSQL(t, writer, "PREPARE TRANSACTION 'full_mixed_truncate'")
				preparedActive = true
				if shape.Replacement.TruncateBoundary != 0 || shape.Replacement.RetainedOrigin != 0 || a.owned.store.TruncateLineage != a.owned.truncateLineage {
					t.Fatal("PREPARE stamped private or old published provenance")
				}
			}
			sessionSQL(t, peer, "INSERT INTO full_mixed_anchor VALUES(10)")
			priorCommit := e.Visibility.CommitSeq()
			if prepared {
				sessionSQL(t, peer, "COMMIT PREPARED 'full_mixed_truncate'")
				preparedActive = false
			} else {
				sessionSQL(t, writer, "COMMIT")
			}
			current := captureFullOwned(t, e, "full_mixed_heap")
			if current.truncateBoundary != priorCommit+1 || current.truncateBoundary != e.Visibility.CommitSeq() || current.truncateBoundary <= staged+1 {
				t.Fatal("boundary was guessed at staging or counted commands instead of actual commit")
			}
			before := current
			sessionSQL(t, writer, "VACUUM FULL full_mixed_heap")
			before.replaced(t, e)
			fullMixedRows(t, a.reader, "full_mixed_heap")
			a.retained(t)
			sessionSQL(t, a.reader, "ROLLBACK")
			lockEmpty(t, e)
		})
	}
	t.Run("nonconcurrent_mv_is_a_hard_boundary", func(t *testing.T) {
		e := engine.New()
		writer := sessionStart(t, e, nil)
		for _, query := range []string{"CREATE TABLE full_mixed_anchor(id INT)", "INSERT INTO full_mixed_anchor VALUES(9)", "CREATE TABLE full_mixed_base(id INT PRIMARY KEY)", "INSERT INTO full_mixed_base VALUES(1),(2)", "CREATE MATERIALIZED VIEW full_mixed_mv AS SELECT id FROM full_mixed_base", "CREATE UNIQUE INDEX full_mixed_mv_ix ON full_mixed_mv(id)", "REFRESH MATERIALIZED VIEW CONCURRENTLY full_mixed_mv", "VACUUM FULL full_mixed_mv"} {
			sessionSQL(t, writer, query)
		}
		a := pinFullMixed(t, e, "full_mixed_mv")
		sessionSQL(t, writer, "UPDATE full_mixed_base SET id=3 WHERE id=1")
		sessionSQL(t, writer, "REFRESH MATERIALIZED VIEW full_mixed_mv")
		current := captureFullOwned(t, e, "full_mixed_mv")
		if current.retainedOrigin != 0 || current.truncateBoundary != 0 || current.materializedLineage != nil || current.truncateLineage != nil {
			t.Fatal("nonconcurrent refresh retained preservation provenance")
		}
		sessionSQL(t, writer, "VACUUM FULL full_mixed_mv")
		current.replaced(t, e)
		a.retained(t)
		if err := fullMixedReadError(t, e, a.reader, "full_mixed_mv"); !errors.Is(err, ids.ErrCatalogReplaced) {
			t.Fatal("hard MV boundary adopted old snapshot", err)
		}
		a.owned.retained(t)
		sessionSQL(t, a.reader, "ROLLBACK")
		fullMixedRows(t, writer, "full_mixed_mv", 2, 3)
		lockEmpty(t, e)
	})
	for _, foreign := range []bool{false, true} {
		t.Run(map[bool]string{false: "restore_clears_pair", true: "foreign_import_clears_pair"}[foreign], func(t *testing.T) {
			e := engine.New()
			writer := sessionStart(t, e, nil)
			for _, query := range []string{"CREATE TABLE full_mixed_heap(id INT PRIMARY KEY)", "VACUUM FULL full_mixed_heap", "TRUNCATE full_mixed_heap", "INSERT INTO full_mixed_heap VALUES(4)", "VACUUM FULL full_mixed_heap"} {
				sessionSQL(t, writer, query)
			}
			before := captureFullOwned(t, e, "full_mixed_heap")
			if before.retainedOrigin == 0 || before.truncateBoundary == 0 || before.truncateLineage == nil {
				t.Fatal("missing actual retained import source history")
			}
			destination := e
			if foreign {
				destination = engine.New()
				if _, err := destination.ReplaceCatalog(context.Background(), e, e.CatalogRead()); err != nil {
					t.Fatal(err)
				}
			} else {
				var snapshot bytes.Buffer
				if err := engine.Dump(context.Background(), e, &snapshot); err != nil {
					t.Fatal(err)
				}
				if err := engine.Restore(context.Background(), e, &snapshot); err != nil {
					t.Fatal(err)
				}
			}
			before.retained(t)
			current := captureFullOwned(t, destination, "full_mixed_heap")
			if current.store == before.store || current.retainedOrigin != 0 || current.truncateBoundary != 0 || current.materializedLineage != nil || current.truncateLineage != nil || current.descriptor.StorageLocator != 0 {
				t.Fatal("replacement imported live read continuity")
			}
			if err := enginetest.ValidateObjectRef(destination, destination.CatalogRead(), before.ref); err == nil {
				t.Fatal("old domain/epoch reference entered replaced catalog")
			}
			lockEmpty(t, e)
			if foreign {
				lockEmpty(t, destination)
			}
		})
	}
	for _, metadataFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "truncate_then_compatible_rename_owner", true: "compatible_rename_owner_then_truncate"}[metadataFirst], func(t *testing.T) {
			e := engine.New()
			writer := sessionStart(t, e, nil)
			for _, query := range []string{"CREATE ROLE full_mixed_owner", "GRANT USAGE,CREATE ON SCHEMA public TO full_mixed_owner", "CREATE TABLE full_mixed_anchor(id INT)", "INSERT INTO full_mixed_anchor VALUES(9)", "CREATE TABLE full_mixed_meta(id INT)", "CREATE UNIQUE INDEX full_mixed_meta_ix ON full_mixed_meta(id)", "INSERT INTO full_mixed_meta VALUES(1),(2)"} {
				sessionSQL(t, writer, query)
			}
			a := pinFullMixed(t, e, "full_mixed_meta")
			initial := captureFullOwned(t, e, "full_mixed_meta")
			sessionSQL(t, writer, "VACUUM FULL full_mixed_meta")
			initial.replaced(t, e)
			if !metadataFirst {
				sessionSQL(t, writer, "TRUNCATE full_mixed_meta")
			}
			sessionSQL(t, writer, "ALTER TABLE full_mixed_meta RENAME TO full_mixed_renamed")
			sessionSQL(t, writer, "ALTER TABLE full_mixed_renamed OWNER TO full_mixed_owner")
			if metadataFirst {
				sessionSQL(t, writer, "TRUNCATE full_mixed_renamed")
			}
			sessionSQL(t, writer, "INSERT INTO full_mixed_renamed VALUES(4)")
			before := captureFullOwned(t, e, "full_mixed_renamed")
			sessionSQL(t, writer, "VACUUM FULL full_mixed_renamed")
			before.replaced(t, e)
			current := captureFullOwned(t, e, "full_mixed_renamed")
			if view.SameRetainedLineage(a.owned.descriptor, current.descriptor, a.owned.store, current.store) || !view.CrossedTruncate(a.owned.descriptor, current.descriptor, a.owned.store, current.store) || a.owned.descriptor.Owner == current.descriptor.Owner || a.owned.descriptor.Name() == current.descriptor.Name() {
				t.Fatal("current-root policy incorrectly inherited old-definition metadata gates")
			}
			fullMixedRows(t, a.reader, "full_mixed_renamed")
			fullMixedRows(t, writer, "full_mixed_renamed", 4)
			a.retained(t)
			sessionSQL(t, a.reader, "ROLLBACK")
			lockEmpty(t, e)
		})
	}
}
