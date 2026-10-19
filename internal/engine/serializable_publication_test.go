package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"runtime"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqltype"
	"sync/atomic"
	"testing"
	"time"
)

// Structural snapshots complement the shared public SQL schedules: immutable
// heads, spare append capacity, index entries, generators and catalog authority.
type serializableStorageSnapshot struct {
	pointer    *storage.Relation
	generation uint64
	ends       *storage.DeletionRoot
	rows       []storage.RowVersion
	endRows    map[ids.RowID]storage.RowEnd
}
type serializableIndexSnapshot struct {
	pointer *storage.IndexRoot
	entries []storage.IndexEntry
}
type serializablePublicationSnapshot struct {
	catalog             *catalog.State
	descriptors         map[ids.ObjectAddress]catalog.Descriptor
	names               map[ids.NameKey]ids.ObjectAddress
	namespaceGeneration uint64
	revision            ids.DefinitionRevision
	frontier            ids.CommitSeq
	order               uint64
	storage             map[ids.ObjectAddress]serializableStorageSnapshot
	indexes             map[ids.ObjectAddress]serializableIndexSnapshot
	sequences           map[ids.ObjectAddress]*storage.SequenceGenerator
	generators          map[ids.ObjectAddress]storage.SequenceGenerator
	providers           map[ids.OID]reflect.Type
}

func serializableSnapshotPublication(e *Engine) serializablePublicationSnapshot {
	s := serializablePublicationSnapshot{catalog: e.catalog.state, descriptors: maps.Collect(e.catalog.state.Descriptors()), names: maps.Collect(e.catalog.state.Names()), namespaceGeneration: e.catalog.state.NamespaceGeneration(), revision: e.catalog.nextRevision, frontier: e.visibility.CommitSeq(), order: e.visibility.SerializableOrder(), storage: make(map[ids.ObjectAddress]serializableStorageSnapshot), indexes: make(map[ids.ObjectAddress]serializableIndexSnapshot), sequences: maps.Clone(e.storage.Sequences), generators: make(map[ids.ObjectAddress]storage.SequenceGenerator), providers: make(map[ids.OID]reflect.Type)}
	for address, store := range e.storage.Relations {
		rows := append([]storage.RowVersion(nil), store.Versions[:cap(store.Versions)]...)
		for i := range rows {
			rows[i].Values = append([]sqltype.Value(nil), rows[i].Values...)
		}
		var ends map[ids.RowID]storage.RowEnd
		if store.Ends != nil {
			ends = maps.Clone(store.Ends.Rows)
		}
		s.storage[address] = serializableStorageSnapshot{store, store.Generation, store.Ends, rows, ends}
	}
	for address, root := range e.storage.IndexRoots {
		entries := append([]storage.IndexEntry(nil), root.Entries...)
		for i := range entries {
			entries[i].Keys = append([]sqltype.Value(nil), entries[i].Keys...)
		}
		s.indexes[address] = serializableIndexSnapshot{root, entries}
	}
	for address, generator := range e.storage.Sequences {
		s.generators[address] = *generator
	}
	for class, provider := range e.catalog.Providers {
		s.providers[class] = reflect.TypeOf(provider)
	}
	return s
}
func serializableRequirePublicationUnchanged(t *testing.T, e *Engine, before serializablePublicationSnapshot) {
	t.Helper()
	after := serializableSnapshotPublication(e)
	if e.catalog.state != before.catalog || !maps.Equal(e.storage.Sequences, before.sequences) {
		t.Fatal("failed publication replaced catalog/generator identities")
	}
	for address, store := range before.storage {
		if e.storage.Relations[address] != store.pointer || e.storage.Relations[address].Ends != store.ends {
			t.Fatal("failed publication replaced storage/end root")
		}
	}
	for address, index := range before.indexes {
		if e.storage.IndexRoots[address] != index.pointer {
			t.Fatal("failed publication replaced index root")
		}
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("failed publication changed catalog/descriptors/names/revisions/roots/spare rows/ends/indexes/generators/providers/frontiers")
	}
}
func serializableUnitCycle(t *testing.T, e *Engine, node *visibility.SerializableTx) *visibility.SerializableTx {
	t.Helper()
	peer := serializableUnitNode(t, e, false)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.visibility.SerializableEdgeLocked(node, node, peer); err != nil {
		t.Fatal(err)
	}
	if err := e.visibility.SerializableEdgeLocked(peer, peer, node); err != nil {
		t.Fatal(err)
	}
	return peer
}

func TestSerializablePublicationDoomedCatalogIndexSequenceProviderAtomicity(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	setup := sessionStart(t, e, nil)
	sessionSQL(t, setup, "CREATE TABLE indexed(v INT UNIQUE)")
	sessionSQL(t, setup, "INSERT INTO indexed VALUES(0)")
	sessionSQL(t, setup, "CREATE SEQUENCE existing_s")
	var validations atomic.Int32
	e.catalog.Providers[ids.RelationClassID] = alterDropProvider{e.catalog.Providers[ids.RelationClassID], func() { validations.Add(1) }}
	sessionSQL(t, a, "BEGIN ISOLATION LEVEL SERIALIZABLE")
	sessionSQL(t, a, "INSERT INTO indexed VALUES(7),(8)")
	sessionSQL(t, a, "ALTER TABLE indexed ADD COLUMN marker INT DEFAULT 7")
	sessionSQL(t, a, "CREATE SEQUENCE private_s START WITH 100")
	sessionSQL(t, a, "ALTER SEQUENCE existing_s RESTART 100")
	node := e.visibility.Serializable.Nodes[a.transactions.ref]
	peer := serializableUnitCycle(t, e, node)
	e.mu.Lock()
	if err := e.visibility.SerializablePrecommitLocked(peer.Ref); err != nil {
		t.Fatal(err)
	}
	e.visibility.SerializableCommittedLocked(peer.Ref, e.visibility.CommitSeq())
	if !node.Doomed {
		t.Fatal("cycle did not doom staged participant")
	}
	before := serializableSnapshotPublication(e)
	e.mu.Unlock()
	validatedBefore := validations.Load()
	op := transactionOperation(t, a)
	err := e.publishRows(op, node.Ref, a.transactions.writes, a.transactions.deletes, a.transactions.catalog, nil, nil)
	serializableUnitDiagnostic(t, err, "Canceled on identification as a pivot, during commit attempt.")
	if validations.Load() <= validatedBefore {
		t.Fatal("real provider did not validate nonempty private catalog preparation")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	serializableRequirePublicationUnchanged(t, e, before)
	if peer.Doomed || !peer.Committed || node.Committed {
		t.Fatal("failed participant changed peer/final status")
	}
}

func TestSerializablePublicationNonemptyLateOrdinaryFailureCannotDoomPeer(t *testing.T) {
	e := New()
	a := sessionStart(t, e, nil)
	setup := sessionStart(t, e, nil)
	sessionSQL(t, setup, "CREATE TABLE indexed(v INT UNIQUE)")
	sessionSQL(t, setup, "INSERT INTO indexed VALUES(0)")
	sessionSQL(t, setup, "CREATE SEQUENCE existing_s")
	sessionSQL(t, a, "BEGIN ISOLATION LEVEL SERIALIZABLE")
	sessionSQL(t, a, "INSERT INTO indexed VALUES(7)")
	sessionSQL(t, a, "CREATE SEQUENCE private_s")
	sessionSQL(t, a, "ALTER SEQUENCE existing_s RESTART 100")
	node := e.visibility.Serializable.Nodes[a.transactions.ref]
	peer := serializableUnitCycle(t, e, node)
	address := catalogRef(t, e, e.CatalogRead(), "indexed").Address
	e.mu.Lock()
	e.storage.Relations[address].Generation = ^uint64(0)
	before := serializableSnapshotPublication(e)
	e.mu.Unlock()
	op := transactionOperation(t, a)
	err := e.publishRows(op, node.Ref, a.transactions.writes, a.transactions.deletes, a.transactions.catalog, nil, nil)
	if err == nil || err.Error() != "stuntgres: storage generation exhausted" {
		t.Fatal("nonempty final ordinary veto", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	serializableRequirePublicationUnchanged(t, e, before)
	if node.Doomed || node.Committed || peer.Doomed || peer.Committed {
		t.Fatal("late ordinary veto ran SSI/finalized a participant")
	}
}

// Err is an existing cooperative seam. Stack identification targets the named
// outside-lock merge and final authority check, not an incidental call count.
// This test-only context adds no production hook, timer race or sleeping oracle.
type serializableRetryContext struct {
	context.Context
	entered     chan struct{}
	release     chan struct{}
	paused      atomic.Bool
	finals      atomic.Int32
	merges      atomic.Int32
	cancel      context.CancelFunc
	veto        bool
	beforeFinal func(int32)
}

func (c *serializableRetryContext) Err() error {
	var pcs [32]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	merge, final := false, false
	for {
		frame, more := frames.Next()
		merge = merge || strings.HasSuffix(frame.Function, ".mergeIndexRoot")
		final = final || strings.HasSuffix(frame.Function, ".publicationOperationLocked")
		if !more {
			break
		}
	}
	if merge {
		c.merges.Add(1)
		if c.paused.CompareAndSwap(false, true) {
			close(c.entered)
			<-c.release
		}
	}
	if final {
		n := c.finals.Add(1)
		if c.beforeFinal != nil {
			c.beforeFinal(n)
		}
		if c.veto && n == 2 {
			c.cancel()
		}
	}
	return c.Context.Err()
}
func TestSerializablePublicationRealChangedPredecessorRetry(t *testing.T) {
	for _, veto := range []bool{false, true} {
		t.Run(map[bool]string{false: "successful_retry", true: "veto_after_retry"}[veto], func(t *testing.T) {
			e := New()
			a := sessionStart(t, e, nil)
			writer := sessionStart(t, e, nil)
			sessionSQL(t, writer, "CREATE TABLE indexed(v INT)")
			sessionSQL(t, writer, "CREATE INDEX indexed_idx ON indexed(v)")
			sessionSQL(t, writer, "INSERT INTO indexed VALUES(0)")
			address := catalogRef(t, e, e.CatalogRead(), "indexed").Address
			index := catalogRef(t, e, e.CatalogRead(), "indexed_idx").Address
			sessionSQL(t, a, "BEGIN ISOLATION LEVEL SERIALIZABLE")
			sessionSQL(t, a, "INSERT INTO indexed VALUES(7)")
			node := e.visibility.Serializable.Nodes[a.transactions.ref]
			peer := serializableUnitCycle(t, e, node)
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			op, err := a.Acquire(base)
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			barrier := &serializableRetryContext{Context: op.Context(), entered: make(chan struct{}), release: make(chan struct{}), cancel: cancel, veto: veto}
			var afterPeer serializablePublicationSnapshot
			barrier.beforeFinal = func(n int32) {
				if node.Doomed || node.Committed || peer.Doomed || peer.Committed {
					t.Error("stale/pre-final preparation ran SSI")
				}
				if !reflect.DeepEqual(afterPeer, serializableSnapshotPublication(e)) {
					t.Error("stale/retried preparation changed committed state before final boundary")
				}
			}
			op.ctx = barrier
			done := make(chan error, 1)
			go func() {
				done <- e.publishRows(op, node.Ref, a.transactions.writes, a.transactions.deletes, nil, nil, nil)
			}()
			released, joined := false, false
			t.Cleanup(func() {
				if !released {
					close(barrier.release)
				}
				if !joined {
					cancel()
					select {
					case <-done:
					case <-time.After(5 * time.Second):
						t.Error("publication worker cleanup did not join")
					}
				}
			})
			select {
			case <-barrier.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("outside-lock index merge barrier not reached")
			}
			// Real peer publication changes exactly the captured store/root/generation.
			sessionSQL(t, writer, "INSERT INTO indexed VALUES(9)")
			e.mu.Lock()
			afterPeer = serializableSnapshotPublication(e)
			e.mu.Unlock()
			close(barrier.release)
			released = true
			select {
			case err = <-done:
				joined = true
			case <-time.After(5 * time.Second):
				t.Fatal("changed-predecessor retry did not join")
			}
			e.mu.Lock()
			defer e.mu.Unlock()
			if barrier.finals.Load() != 2 || barrier.merges.Load() < 2 {
				t.Fatal("did not retry changed predecessor", barrier.finals.Load(), barrier.merges.Load())
			}
			if veto {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				serializableRequirePublicationUnchanged(t, e, afterPeer)
				if node.Committed || peer.Doomed {
					t.Fatal("vetoed retried attempt changed graph")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if !node.Committed || !peer.Doomed || e.visibility.CommitSeq() != afterPeer.frontier+1 || e.visibility.SerializableOrder() != afterPeer.order+1 {
					t.Fatal("retry did not finalize exactly once")
				}
				rows := e.storage.Relations[address].Versions
				if len(rows) != 3 {
					t.Fatal("retry lost peer/current row multiplicity", len(rows))
				}
				for i, want := range []int64{0, 9, 7} {
					value, err := rows[i].Values[0].Integer()
					if err != nil || value != want {
						t.Fatal("retry lost peer/current rows", i, value, want, err)
					}
				}
				if len(e.storage.IndexRoots[index].Entries) != 3 {
					t.Fatal("retry lost index multiplicity")
				}
			}
		})
	}
}

func TestSerializablePublicationCatalogChangeSetVetoWithLiveGraph(t *testing.T) {
	e := New()
	node := serializableUnitNode(t, e, false)
	peer := serializableUnitCycle(t, e, node)
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	descriptor := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20000}, Owner: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), ObjectName: "unpublished", ObjectRevision: 1}
	if err := stage.Create(descriptor); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	prepared, err := e.ValidateCatalogStage(ctx, stage)
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	before := serializableSnapshotPublication(e)
	e.mu.Unlock()
	cancel()
	changes, err := e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()})
	if !errors.Is(err, context.Canceled) || len(changes.Definitions) != 0 || len(changes.Statistics) != 0 || len(changes.Namespaces) != 0 || changes.Replacement {
		t.Fatal("catalog veto emitted ChangeSet", changes, err)
	}
	e.mu.Lock()
	serializableRequirePublicationUnchanged(t, e, before)
	if node.Doomed || node.Committed || peer.Doomed || peer.Committed || len(node.Outgoing) != 1 || len(peer.Outgoing) != 1 {
		t.Fatal("catalog veto changed live SSI graph")
	}
	e.mu.Unlock()
	e.DiscardCatalogStage(stage)
}
