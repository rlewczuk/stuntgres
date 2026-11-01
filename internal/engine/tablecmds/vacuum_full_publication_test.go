package tablecmds_test

import (
	"bytes"
	"context"
	"maps"
	"reflect"
	"slices"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/sqltype"
	"testing"
	"time"
)

type fullOwnedSnapshot struct {
	ref                 ids.ObjectRef
	catalog             *catalog.State
	descriptor          *catalog.Relation
	store               *storage.Relation
	rows                []storage.RowVersion
	headers             []storage.RowVersion
	ends                *storage.DeletionRoot
	endValues           map[ids.RowID]storage.RowEnd
	predecessors        *storage.HeapPredecessor
	history             map[*storage.HeapPredecessor]fullOwnedPredecessor
	generation          uint64
	retainedOrigin      ids.OID
	truncateBoundary    ids.CommitSeq
	materializedLineage *storage.MaterializedLineage
	truncateLineage     *storage.TruncateLineage
	maintenance         storage.Maintenance
	metadata            *storage.RelationMetadata
	indexes             map[ids.ObjectAddress]*storage.IndexRoot
	entries             map[ids.ObjectAddress][]storage.IndexEntry
	buckets             map[ids.ObjectAddress]map[uint64][]storage.HashEntry
	ranges              map[ids.ObjectAddress][]storage.BrinRange
}

type fullOwnedPredecessor struct {
	header  storage.HeapPredecessor
	payload []sqltype.Value
}

func captureFullHistory(root *storage.HeapPredecessor, history map[*storage.HeapPredecessor]fullOwnedPredecessor) {
	for node := root; node != nil; node = node.Next {
		if _, exists := history[node]; exists {
			break
		}
		header := *node
		header.Version.Values = slices.Clone(node.Version.Values)
		history[node] = fullOwnedPredecessor{header: header, payload: node.Version.Values}
	}
}

func captureFullOwned(t *testing.T, e *core.Engine, name string) fullOwnedSnapshot {
	t.Helper()
	ref, store := vacuumStore(t, e, name)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	v := fullOwnedSnapshot{ref: ref, catalog: e.Catalog.State, descriptor: e.Catalog.State.MustRelation(ref.Address), store: store, rows: store.Versions, headers: slices.Clone(store.Versions), ends: store.Ends, generation: store.Generation, maintenance: store.Maintenance, retainedOrigin: store.RetainedOrigin, truncateBoundary: store.TruncateBoundary, materializedLineage: store.MaterializedLineage, truncateLineage: store.TruncateLineage, metadata: e.Storage.RelationMetadata[ref.Address], indexes: make(map[ids.ObjectAddress]*storage.IndexRoot)}
	v.predecessors = store.Retained
	v.history = make(map[*storage.HeapPredecessor]fullOwnedPredecessor)
	captureFullHistory(store.Retained, v.history)
	for _, row := range store.Versions {
		captureFullHistory(row.PrivateHistory, v.history)
	}
	if store.Ends != nil {
		v.endValues = maps.Clone(store.Ends.Rows)
	}
	v.entries = make(map[ids.ObjectAddress][]storage.IndexEntry)
	v.buckets = make(map[ids.ObjectAddress]map[uint64][]storage.HashEntry)
	v.ranges = make(map[ids.ObjectAddress][]storage.BrinRange)
	for address, d := range e.Catalog.State.Relations() {
		if d.SQLIndex == nil || d.SQLIndex.Parent != ref.Address {
			continue
		}
		root := e.Storage.IndexRoots[address]
		v.indexes[address] = root
		if root == nil {
			continue
		}
		entries := slices.Clone(root.Entries)
		for i := range entries {
			entries[i].Keys = slices.Clone(entries[i].Keys)
		}
		v.entries[address] = entries
		if root.Hash != nil {
			buckets := make(map[uint64][]storage.HashEntry, len(root.Hash.Buckets))
			for key, bucket := range root.Hash.Buckets {
				buckets[key] = slices.Clone(bucket)
			}
			v.buckets[address] = buckets
		}
		if root.Brin != nil {
			ranges := slices.Clone(root.Brin.Ranges)
			for i := range ranges {
				ranges[i].Keys = slices.Clone(ranges[i].Keys)
			}
			v.ranges[address] = ranges
		}
	}
	for i := range v.headers {
		v.headers[i].Values = append([]sqltype.Value(nil), v.headers[i].Values...)
	}
	return v
}
func (v fullOwnedSnapshot) retained(t *testing.T) {
	t.Helper()
	if !storage.SameVersionRoot(v.rows, v.store.Versions) || v.store.Ends != v.ends || v.store.Generation != v.generation || v.store.Maintenance != v.maintenance || v.store.RetainedOrigin != v.retainedOrigin || v.store.TruncateBoundary != v.truncateBoundary || !reflect.DeepEqual(v.headers, v.store.Versions) {
		t.Fatal("retained heap/header/payload ownership changed")
	}
	if v.store.Retained != v.predecessors {
		t.Fatal("retained physical predecessor root changed")
	}
	for node, owned := range v.history {
		if node.Next != owned.header.Next || node.Version.PrivateHistory != owned.header.Version.PrivateHistory || !reflect.DeepEqual(*node, owned.header) || len(node.Version.Values) != len(owned.payload) || len(owned.payload) != 0 && &node.Version.Values[0] != &owned.payload[0] {
			t.Fatal("retained physical predecessor link/header/end/full-XID/payload changed")
		}
	}
	if v.ends != nil && !maps.Equal(v.endValues, v.ends.Rows) {
		t.Fatal("retained end map mutated")
	}
	for address, root := range v.indexes {
		if root == nil {
			continue
		}
		if !reflect.DeepEqual(v.entries[address], root.Entries) {
			t.Fatal("retained entry/key backing mutated")
		}
		if root.Hash != nil && !reflect.DeepEqual(v.buckets[address], root.Hash.Buckets) {
			t.Fatal("retained hash bucket backing mutated")
		}
		if root.Brin != nil && !reflect.DeepEqual(v.ranges[address], root.Brin.Ranges) {
			t.Fatal("retained BRIN summary backing mutated")
		}
	}
}
func (v fullOwnedSnapshot) rejected(t *testing.T, e *core.Engine) {
	t.Helper()
	v.retained(t)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if v.store.MaterializedLineage != v.materializedLineage || v.store.TruncateLineage != v.truncateLineage {
		t.Fatal("rejected FULL stamped predecessor lineage metadata")
	}
	if e.Catalog.State != v.catalog || e.Storage.Relations[v.ref.Address] != v.store || e.Storage.RelationMetadata[v.ref.Address] != v.metadata || !e.Storage.IndexRootsCurrent(v.indexes) {
		t.Fatal("rejected FULL published a partial authority set")
	}
	if len(e.ClusterProgress) != 0 || len(e.VacuumProgress) != 0 {
		t.Fatal("rejected FULL leaked progress")
	}
}
func (v fullOwnedSnapshot) replaced(t *testing.T, e *core.Engine) *storage.Relation {
	t.Helper()
	v.retained(t)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	next := e.Storage.Relations[v.ref.Address]
	d := e.Catalog.State.MustRelation(v.ref.Address)
	if next == v.store || next == nil || next.Ends == v.ends || next.Generation <= v.generation || d == v.descriptor || d.StorageLocator == 0 || d.StorageLocator == v.descriptor.EffectiveStorageLocator(catalog.NewRead(&e.Catalog.Owner, v.catalog)) {
		t.Fatal("FULL failed to replace coherent heap/catalog incarnation")
	}
	origin := v.retainedOrigin
	if origin == 0 {
		origin = v.descriptor.EffectiveStorageLocator(catalog.NewRead(&e.Catalog.Owner, v.catalog))
	}
	if origin == 0 || next.RetainedOrigin != origin || next.TruncateBoundary != v.truncateBoundary || !view.SameRetainedLineage(v.descriptor, d, v.store, next) {
		t.Fatal("FULL did not preserve the predecessor's family and boundary")
	}
	if v.store.MaterializedLineage != v.materializedLineage || v.store.TruncateLineage != v.truncateLineage || next.MaterializedLineage != v.materializedLineage || next.TruncateLineage != v.truncateLineage {
		t.Fatal("FULL lost actual tokens or stamped predecessor lineage metadata")
	}
	if e.Storage.RelationMetadata[v.ref.Address] != v.metadata {
		t.Fatal("FULL replaced unrelated statistics ownership")
	}
	for address, old := range v.indexes {
		root := e.Storage.IndexRoots[address]
		idx := e.Catalog.State.MustRelation(address)
		if root == nil || root == old || idx.StorageLocator == 0 || idx.StorageLocator == v.catalog.MustRelation(address).EffectiveStorageLocator(catalog.NewRead(&e.Catalog.Owner, v.catalog)) {
			t.Fatal("index root/locator not genuinely rebuilt")
		}
		if root.Construction == nil || old != nil && root.Construction == old.Construction || !index.ConstructionMatches(root, idx) {
			t.Fatal("FULL omitted genuine current index construction provenance")
		}
		if root.Hash != nil && (root.Hash.Storage != next || root.Hash.Generation != next.Generation || root.Hash.Ends != next.Ends || root.Hash.Prefix != len(next.Versions)) {
			t.Fatal("hash root references temporary/predecessor heap")
		}
		if root.Brin != nil && (root.Brin.Storage != next || root.Brin.Generation != next.Generation || root.Brin.Ends != next.Ends || root.Brin.Prefix != len(next.Versions)) {
			t.Fatal("BRIN root references temporary/predecessor heap")
		}
	}
	return next
}

func TestVacuumFullPublication(t *testing.T) {
	t.Run("analyze_counter_selection", func(t *testing.T) {
		e := engine.New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE full_counters(a INT,b INT)")
		sessionSQL(t, s, "INSERT INTO full_counters VALUES(1,10),(2,20)")
		ref, before := vacuumStore(t, e, "full_counters")
		if before.Maintenance.Modifications != 2 || before.Maintenance.InsertsSince != 2 {
			t.Fatal("missing actual DML counter witnesses")
		}
		for i, q := range []string{"VACUUM (FULL,ANALYZE) full_counters(a)", "ANALYZE full_counters(b)", "VACUUM (FULL,ANALYZE) full_counters"} {
			sessionSQL(t, s, q)
			_, current := vacuumStore(t, e, "full_counters")
			want := int64(2)
			if i == 2 {
				want = 0
			}
			if current.Maintenance.Modifications != want || current.Maintenance.InsertsSince != 2 || current.Maintenance.VacuumCount != 0 {
				t.Fatal("analysis counter ownership", q, current.Maintenance)
			}
			if metadata := e.Storage.RelationMetadata[ref.Address]; metadata == nil || metadata.AnalyzeCount != int64(i+1) || metadata.LastAnalyze.IsZero() {
				t.Fatal("selected/no-list analysis did not publish actual completion", q, metadata)
			}
		}
		lockEmpty(t, e)
	})
	t.Run("installed_wins_late_cancel", func(t *testing.T) {
		e := engine.New()
		s := sessionStart(t, e, nil)
		sessionSQL(t, s, "CREATE TABLE full_late_finish(id INT)")
		sessionSQL(t, s, "INSERT INTO full_late_finish VALUES(1)")
		before := captureFullOwned(t, e, "full_late_finish")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		op, err := s.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		_, err = scope.Execute(enginetest.Statement(t, "VACUUM FULL full_late_finish"), 0, nil)
		if err != nil {
			scope.Finish(err)
			t.Fatal(err)
		}
		cancel()
		if err := scope.Finish(nil); err != nil {
			t.Fatal("late cancel retracted installed rewrite", err)
		}
		before.replaced(t, e)
		lockEmpty(t, e)
	})
	for _, capacity := range []string{"generation", "locator"} {
		t.Run("capacity_"+capacity, func(t *testing.T) {
			e := engine.New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE full_capacity(id INT PRIMARY KEY)")
			sessionSQL(t, s, "INSERT INTO full_capacity VALUES(1)")
			ref, store := vacuumStore(t, e, "full_capacity")
			e.Mu.Lock()
			if capacity == "generation" {
				store.Generation = ^uint64(0)
			} else {
				e.Catalog.InstallOIDFrontierLocked(^ids.OID(0))
			}
			e.Mu.Unlock()
			before := captureFullOwned(t, e, "full_capacity")
			op, err := s.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.Execute(op, enginetest.Statement(t, "VACUUM FULL full_capacity"), 0, nil)
			op.Done()
			if err == nil {
				t.Fatal("exhausted capacity accepted", capacity, ref)
			}
			before.rejected(t, e)
			lockEmpty(t, e)
		})
	}
	for _, mutation := range []string{"catalog", "metadata", "index", "retained"} {
		t.Run("recapture_"+mutation, func(t *testing.T) {
			e := engine.New()
			owner, worker := sessionStart(t, e, nil), sessionStart(t, e, nil)
			for _, q := range []string{"CREATE TABLE full_recapture(id INT PRIMARY KEY)", "INSERT INTO full_recapture VALUES(1),(2)", "UPDATE full_recapture SET id=3 WHERE id=2", "UPDATE full_recapture SET id=4 WHERE id=3", "DELETE FROM full_recapture WHERE id=1", "ANALYZE full_recapture"} {
				sessionSQL(t, owner, q)
			}
			var reader *session.Session
			if mutation == "retained" {
				sessionSQL(t, owner, "CREATE TABLE full_recapture_anchor(id INT)")
				sessionSQL(t, owner, "INSERT INTO full_recapture_anchor VALUES(9)")
				reader = sessionStart(t, e, nil)
				sessionSQL(t, reader, "BEGIN ISOLATION LEVEL REPEATABLE READ")
				sessionSQL(t, reader, "SELECT id FROM full_recapture_anchor").Rows.Close()
			}
			before := captureFullOwned(t, e, "full_recapture")
			task := startVacuumBarrier(t, e, worker, "VACUUM FULL full_recapture", "vacuumFullCompletion")
			expected := before
			switch mutation {
			case "catalog":
				base := e.CatalogRead()
				next := *base.MustRelation(before.ref.Address)
				next.ObjectRevision++
				next.Comment = "compatible current comment"
				stage := e.BeginCatalogStage(base)
				defer e.DiscardCatalogStage(stage)
				if err := stage.Replace(&next); err != nil {
					t.Fatal(err)
				}
				prepared, err := e.ValidateCatalogStage(context.Background(), stage)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := publishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
					t.Fatal(err)
				}
			case "metadata":
				e.Mu.Lock()
				next := *before.metadata
				next.AnalyzeCount++
				e.Storage.RelationMetadata[before.ref.Address] = &next
				expected.metadata = &next
				e.Mu.Unlock()
			case "index":
				e.Mu.Lock()
				for address, root := range before.indexes {
					next := *root
					e.Storage.InstallIndexRootLocked(address, &next)
				}
				e.Mu.Unlock()
			case "retained":
				e.Mu.Lock()
				// A compatible immutable-root replacement forces detached work
				// to recapture actual physical history, even with unchanged heads.
				copy := *before.predecessors
				before.store.Retained = &copy
				e.Mu.Unlock()
				expected = captureFullOwned(t, e, "full_recapture")
				// Only the deliberately replaced root pointer changes the old
				// snapshot expectation; its originally captured nodes stay owned.
				before.predecessors = &copy
			}
			task.join(t, nil)
			next := expected.replaced(t, e)
			if mutation == "retained" {
				if next.Retained != expected.predecessors || len(next.Versions) != 2 || next.Maintenance.Tuples != 4 {
					t.Fatal("FULL published stale retained capture after the final fence")
				}
				sessionSQL(t, reader, "ROLLBACK")
			}
			if mutation == "catalog" && e.CatalogRead().MustRelation(before.ref.Address).Comment != "compatible current comment" {
				t.Fatal("FULL clobbered current catalog metadata")
			}
			before.retained(t)
			lockEmpty(t, e)
		})
	}
	for _, boundary := range []string{"storage.RewriteVacuum", "index.BuildRelationIndexRoot", "vacuumFullCompletion"} {
		for _, outcome := range []string{"cancel", "session_close", "reset", "restore", "success"} {
			t.Run(boundary+"/"+outcome, func(t *testing.T) {
				e := engine.New()
				owner, worker := sessionStart(t, e, nil), sessionStart(t, e, nil)
				for _, q := range []string{"CREATE TABLE full_fence(id INT PRIMARY KEY,v TEXT)", "VACUUM FULL full_fence", "TRUNCATE full_fence", "INSERT INTO full_fence VALUES(1,'gone'),(2,'kept'),(3,NULL)", "CREATE INDEX full_fence_hash ON full_fence USING hash(id)", "CREATE INDEX full_fence_brin ON full_fence USING brin(id)", "UPDATE full_fence SET v='updated' WHERE id=2", "UPDATE full_fence SET v='kept' WHERE id=2", "DELETE FROM full_fence WHERE id=1", "ANALYZE full_fence"} {
					sessionSQL(t, owner, q)
				}
				before := captureFullOwned(t, e, "full_fence")
				if before.predecessors == nil || len(before.history) != 2 {
					t.Fatal("missing genuine repeated UPDATE physical history")
				}
				task := startVacuumBarrier(t, e, worker, "VACUUM FULL full_fence", boundary)
				switch outcome {
				case "cancel":
					task.cancel()
					task.join(t, context.Canceled)
					before.rejected(t, e)
				case "session_close":
					worker.RequestEnd(session.CleanClose)
					task.join(t, context.Canceled)
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := worker.WaitEnd(ctx); err != nil {
						t.Fatal(err)
					}
					before.rejected(t, e)
				case "reset":
					if err := engine.Reset(context.Background(), e); err != nil {
						t.Fatal(err)
					}
					task.join(t, ids.ErrCatalogReplaced)
					before.retained(t)
				case "restore":
					if err := engine.Restore(context.Background(), e, bytes.NewBufferString("CREATE TABLE sentinel(id INT);INSERT INTO sentinel VALUES(9);")); err != nil {
						t.Fatal(err)
					}
					task.join(t, ids.ErrCatalogReplaced)
					before.retained(t)
				case "success":
					task.join(t, nil)
					next := before.replaced(t, e)
					if len(next.Versions) != 2 || next.Retained != nil || next.Maintenance.Tuples != 2 || next.Maintenance.VacuumCount != before.maintenance.VacuumCount || next.Maintenance.InsertsSince != before.maintenance.InsertsSince || next.Maintenance.Live != before.maintenance.Live || next.Maintenance.Dead != before.maintenance.Dead {
						t.Fatal("rewrite facts corrupted", next.Maintenance)
					}
				}
				e.Mu.Lock()
				progress := len(e.ClusterProgress) + len(e.VacuumProgress)
				e.Mu.Unlock()
				if progress != 0 {
					t.Fatal("terminal progress leaked")
				}
				lockEmpty(t, e)
			})
		}
	}
}

func TestVacuumFullLocatorOwnership(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE full_locator(id INT PRIMARY KEY)", "INSERT INTO full_locator VALUES(1)", "CREATE INDEX full_locator_hash ON full_locator USING hash(id)"} {
		sessionSQL(t, s, q)
	}
	for range 2 {
		before := captureFullOwned(t, e, "full_locator")
		sessionSQL(t, s, "VACUUM FULL full_locator")
		before.replaced(t, e)
	}
	old := captureFullOwned(t, e, "full_locator")
	zeroOrigin := *old.store
	zeroOrigin.RetainedOrigin = 0
	if view.SameRetainedLineage(old.descriptor, old.descriptor, old.store, &zeroOrigin) {
		t.Fatal("zero new origin granted FULL compatibility")
	}
	// After repeated FULL, the current locator is not the immutable origin.
	// A retained store cannot manufacture continuity from that current locator.
	if old.descriptor.StorageLocator == old.store.RetainedOrigin || view.SameRetainedLineage(old.descriptor, old.descriptor, &zeroOrigin, old.store) {
		t.Fatal("current locator substituted for retained family origin")
	}
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	old.retained(t)
	fresh := captureFullOwned(t, e, "full_locator")
	if fresh.store == old.store || fresh.ref.Epoch == old.ref.Epoch || fresh.descriptor.StorageLocator != 0 || fresh.store.RetainedOrigin != 0 || fresh.store.TruncateBoundary != 0 || fresh.store.MaterializedLineage != nil || fresh.store.TruncateLineage != nil {
		t.Fatal("Restore imported source incarnation")
	}
	for _, q := range []string{"VACUUM FULL full_locator", "TRUNCATE full_locator", "VACUUM FULL full_locator", "ALTER TABLE full_locator ALTER COLUMN id TYPE BIGINT"} {
		before := captureFullOwned(t, e, "full_locator")
		sessionSQL(t, s, q)
		next := e.CatalogRead().MustRelation(before.ref.Address)
		if next.EffectiveStorageLocator(e.CatalogRead()) == before.descriptor.EffectiveStorageLocator(catalog.NewRead(&e.Catalog.Owner, before.catalog)) {
			t.Fatal("actual replacement inherited stale locator", q)
		}
		store := e.Storage.Relations[before.ref.Address]
		if q == "TRUNCATE full_locator" {
			if store.RetainedOrigin != before.retainedOrigin || store.TruncateBoundary != e.Visibility.CommitSeq() || !view.CrossedTruncate(before.descriptor, next, before.store, store) || view.SameRetainedLineage(before.descriptor, next, before.store, store) {
				t.Fatal("TRUNCATE did not preserve family with its actual committed boundary")
			}
		} else if q == "ALTER TABLE full_locator ALTER COLUMN id TYPE BIGINT" {
			if store.RetainedOrigin != 0 || store.TruncateBoundary != 0 || view.SameRetainedLineage(before.descriptor, next, before.store, store) || view.CrossedTruncate(before.descriptor, next, before.store, store) {
				t.Fatal("destructive shape replacement adopted old read continuity")
			}
		}
	}
	reindexBefore := captureFullOwned(t, e, "full_locator")
	indexRef := enginetest.CatalogRef(t, e, e.CatalogRead(), "full_locator_hash")
	indexBefore := e.CatalogRead().MustRelation(indexRef.Address)
	sessionSQL(t, s, "REINDEX INDEX full_locator_hash")
	indexAfter := e.CatalogRead().MustRelation(indexRef.Address)
	if indexAfter.EffectiveStorageLocator(e.CatalogRead()) == indexBefore.EffectiveStorageLocator(catalog.NewRead(&e.Catalog.Owner, reindexBefore.catalog)) || e.Storage.IndexRoots[indexRef.Address] == reindexBefore.indexes[indexRef.Address] || e.Storage.Relations[reindexBefore.ref.Address] != reindexBefore.store {
		t.Fatal("REINDEX locator/root cutover changed heap or retained old index")
	}
	reindexBefore.retained(t)
	sessionSQL(t, s, "CREATE MATERIALIZED VIEW full_locator_mv AS SELECT id FROM full_locator")
	sessionSQL(t, s, "CREATE INDEX full_locator_mv_ix ON full_locator_mv(id)")
	sessionSQL(t, s, "VACUUM FULL full_locator_mv")
	mvBefore := captureFullOwned(t, e, "full_locator_mv")
	sessionSQL(t, s, "REFRESH MATERIALIZED VIEW full_locator_mv")
	mvAfter := e.CatalogRead().MustRelation(mvBefore.ref.Address)
	if e.Storage.Relations[mvBefore.ref.Address] == mvBefore.store || e.Storage.Relations[mvBefore.ref.Address].RetainedOrigin != 0 || e.Storage.Relations[mvBefore.ref.Address].TruncateBoundary != 0 || e.Storage.Relations[mvBefore.ref.Address].MaterializedLineage != nil || e.Storage.Relations[mvBefore.ref.Address].TruncateLineage != nil || view.SameRetainedLineage(mvBefore.descriptor, mvAfter, mvBefore.store, e.Storage.Relations[mvBefore.ref.Address]) || view.CrossedTruncate(mvBefore.descriptor, mvAfter, mvBefore.store, e.Storage.Relations[mvBefore.ref.Address]) || mvAfter.EffectiveStorageLocator(e.CatalogRead()) == mvBefore.descriptor.EffectiveStorageLocator(catalog.NewRead(&e.Catalog.Owner, mvBefore.catalog)) {
		t.Fatal("REFRESH inherited stale FULL incarnation")
	}
	mvBefore.retained(t)
	sessionSQL(t, s, "VACUUM FULL full_locator")
	source := captureFullOwned(t, e, "full_locator")
	destination := engine.New()
	if _, err := destination.ReplaceCatalog(context.Background(), e, e.CatalogRead()); err != nil {
		t.Fatal(err)
	}
	imported := captureFullOwned(t, destination, "full_locator")
	if imported.store == source.store || imported.ref.Domain() == source.ref.Domain() || imported.descriptor.StorageLocator != 0 || imported.store.RetainedOrigin != 0 || imported.store.TruncateBoundary != 0 || imported.store.MaterializedLineage != nil || imported.store.TruncateLineage != nil {
		t.Fatal("foreign import retained source locator/storage authority")
	}
	source.retained(t)
}
