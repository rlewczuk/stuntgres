package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTemporaryTableCleanupClosure(t *testing.T) {
	e, op, c, table := temporaryCatalogCandidate(t, parser.OnCommitPreserve)
	options, _, err := bindSequenceOptions(nil, sequenceGenerator{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	options.persistence, options.owned = "temporary", table.address
	options.owned.SubID = 1
	sequence := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20003}, namespace: table.namespace, name: "owned", revision: 1, SequencePlanStamp: 1, Owner: table.Owner, Columns: e.sequenceColumnsLocked(), Index: map[string]int{"last_value": 0, "log_cnt": 1, "is_called": 2}, Attributes: []int32{1, 2, 3}, AttributeHighWater: 3, RelationKind: sequenceRelation, Sequence: &options}
	class, _ := sqltype.DefaultIndexClass(table.Columns[0].Usage.Type)
	index := &relationDescriptor{address: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20004}, namespace: table.namespace, name: "backing", revision: 1, Owner: table.Owner, Columns: table.Columns, Index: table.Index, Attributes: table.Attributes, AttributeHighWater: 1, RelationKind: indexRelation, SQLIndex: &indexDefinition{parent: table.address, keys: []indexKey{{attribute: 1, class: class}}}}
	peer := sessionStart(t, e, nil)
	peerNamespace := &namespaceDescriptor{address: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20005}, name: "pg_temp_peer", revision: 1, Owner: table.Owner, Temporary: peer.token}
	peerTable := *table
	peerTable.address.ObjectID = 20006
	peerTable.namespace = peerNamespace.address
	for _, d := range []Descriptor{sequence, index, peerNamespace, &peerTable} {
		e.catalogChangeLocked(c, d.Address(), d)
	}
	e.catalog.state = e.privateCatalogLocked(c).state
	op.session.temporaryNamespace = table.namespace
	rows := []rowVersion{{row: 1, version: 1, values: []sqltype.Value{sqltype.Null(table.Columns[0].Usage.Type)}}}
	store := &relationStorage{versions: rows}
	peerStore := &relationStorage{}
	e.storage[table.address], e.storage[peerTable.address] = store, peerStore
	generator := &sequenceGenerator{last: options.start, generation: 1}
	e.sequences[sequence.address] = generator
	root := &logicalIndexRoot{}
	e.indexRoots = map[ids.ObjectAddress]*logicalIndexRoot{}
	e.indexRoots[index.address] = root
	epoch := e.catalog.state.epoch
	if err := e.endTemporarySession(op.session); err != nil {
		t.Fatal(err)
	}
	for _, a := range []ids.ObjectAddress{table.address, sequence.address, index.address} {
		if e.catalog.state.Get(a) != nil || e.storage[a] != nil || e.sequences[a] != nil || e.indexRoots[a] != nil {
			t.Fatal("orphan owner dependency", a)
		}
	}
	if ns, ok := e.catalog.state.Namespace(table.namespace); !ok || ns.Temporary != op.session.token {
		t.Fatal("cleanup lost retained namespace identity")
	}
	if e.catalog.state.Get(peerTable.address) != &peerTable || e.storage[peerTable.address] != peerStore || e.catalog.state.epoch != epoch {
		t.Fatal("peer or epoch changed")
	}
	if len(store.versions) != 1 || &store.versions[0] != &rows[0] {
		t.Fatal("retained row snapshot changed")
	}
	if err := e.endTemporarySession(op.session); err != nil {
		t.Fatal(err)
	}
	if e.storage[peerTable.address] != peerStore {
		t.Fatal("repeat cleanup changed peer")
	}
}

// Setup binds an actual stored CHECK/manifest; assertions exercise the private
// terminal stage and publication seams rather than repeat driver SQL scenarios.
func temporaryCleanupConsumer(t *testing.T) (*Engine, *Operation, *relationDescriptor, *relationDescriptor, *relationDescriptor) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	roleExecute(t, e, op, "CREATE TEMP TABLE cleanup_owner(v INT)")
	roleExecute(t, e, op, "CREATE TEMP SEQUENCE cleanup_owned OWNED BY cleanup_owner.v")
	namespace := op.session.temporaryNamespace
	assertTemporaryNamespacePair(t, e, op.session.token, namespace)
	owner := e.catalog.state.MustRelation(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "cleanup_owner"}))
	sequence := e.catalog.state.MustRelation(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "cleanup_owned"}))
	roleExecute(t, e, op, "CREATE TABLE public.cleanup_survivor(v INT CHECK(nextval('cleanup_owned')>0))")
	address := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "cleanup_survivor"})
	survivor := e.catalog.state.MustRelation(address)
	if len(survivor.CheckConstraints) != 1 || len(survivor.CheckConstraints[0].dependencies) == 0 {
		t.Fatal("missing real CHECK manifest")
	}
	return e, op, owner, sequence, survivor
}

func temporaryCleanupPreparedStage(t *testing.T, e *Engine, owner, sequence *relationDescriptor) *CatalogStage {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	stage.terminalCleanup = true
	targets := []ids.ObjectAddress{owner.address, sequence.address}
	for _, address := range targets {
		if err := stage.Remove(e.catalog.domain.Ref(stage.base.Epoch(), address)); err != nil {
			t.Fatal(err)
		}
	}
	if err := stageViewDependents(stage, targets, parser.DropCascade); err != nil {
		t.Fatal(err)
	}
	return stage
}

func TestTemporaryTableCleanupPublication(t *testing.T) {
	t.Run("CatalogRootFenceAndNewConsumer", func(t *testing.T) {
		e, op, owner, sequence, survivor := temporaryCleanupConsumer(t)
		stage := temporaryCleanupPreparedStage(t, e, owner, sequence)
		roleExecute(t, e, op, "CREATE TABLE public.cleanup_late(v INT CHECK(nextval('cleanup_owned')>0))")
		current := e.catalog.state
		e.mu.Lock()
		installed, err := e.installTemporarySessionStageLocked(op.session, stage, []ids.ObjectAddress{owner.namespace}, nil)
		e.mu.Unlock()
		if err != nil || installed || e.catalog.state != current || e.catalog.state.Get(survivor.address) != survivor || e.sequences[sequence.address] == nil || op.session.temporaryNamespace != owner.namespace {
			t.Fatal("stale preparation installed", installed, err)
		}
		e.DiscardCatalogStage(stage)
		lateAddress := current.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.namespace, Name: "cleanup_late"})
		late := current.MustRelation(lateAddress)
		if err := e.endTemporarySession(op.session); err != nil {
			t.Fatal(err)
		}
		for _, original := range []*relationDescriptor{survivor, late} {
			replacement := e.catalog.state.MustRelation(original.address)
			if replacement == original || len(replacement.CheckConstraints) != 0 || len(original.CheckConstraints) != 1 || replacement.revision != original.revision+1 || e.catalog.state.NameAddress(descriptorNameKey(original)) != original.address {
				t.Fatal("reprepare missed consumer or changed captured definition")
			}
		}
		if e.catalog.nextRevision <= e.catalog.state.Get(lateAddress).Revision() {
			t.Fatal("replacement frontier not published")
		}
	})
	t.Run("CurrentDMLAndUnrelatedRoots", func(t *testing.T) {
		e, op, owner, sequence, survivor := temporaryCleanupConsumer(t)
		roleExecute(t, e, op, "CREATE SEQUENCE public.cleanup_unrelated")
		roleExecute(t, e, op, "CREATE INDEX cleanup_survivor_v_idx ON public.cleanup_survivor(v)")
		unrelated := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.namespace, Name: "cleanup_unrelated"})
		index := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.namespace, Name: "cleanup_survivor_v_idx"})
		stage := temporaryCleanupPreparedStage(t, e, owner, sequence)
		captured := e.catalog.state
		// A real row publication replaces storage/index heads, not the catalog.
		roleExecute(t, e, op, "INSERT INTO public.cleanup_survivor VALUES(7)")
		currentStore, currentRoot, currentGenerator := e.storage[survivor.address], e.indexRoots[index], e.sequences[unrelated]
		if e.catalog.state != captured || currentStore == nil || len(currentStore.versions) != 1 || currentRoot == nil || currentGenerator == nil {
			t.Fatal("missing genuine row/index/generator fixture")
		}
		epoch, generation := e.catalog.state.epoch, e.catalog.state.namespaceGeneration
		e.mu.Lock()
		installed, err := e.installTemporarySessionStageLocked(op.session, stage, []ids.ObjectAddress{owner.namespace}, nil)
		e.mu.Unlock()
		if err != nil || !installed || e.storage[survivor.address] != currentStore || e.indexRoots[index] != currentRoot || e.sequences[unrelated] != currentGenerator || e.catalog.state.epoch != epoch || e.catalog.state.namespaceGeneration != generation {
			t.Fatal("current DML or unrelated roots lost", installed, err)
		}
		assertTemporaryNamespacePair(t, e, op.session.token, owner.namespace)
		if len(survivor.CheckConstraints) != 1 || len(e.catalog.state.MustRelation(survivor.address).CheckConstraints) != 0 || e.storage[owner.address] != nil || e.sequences[sequence.address] != nil {
			t.Fatal("incomplete terminal stage")
		}
		e.DiscardCatalogStage(stage)
	})
}

func TestTemporaryTableTerminalRevisionPreparation(t *testing.T) {
	e, _, _, sequence, survivor := temporaryCleanupConsumer(t)
	original := e.catalog.state
	maximum := *survivor
	maximum.revision = ids.DefinitionRevision(^uint64(0))
	e.catalog.state = cloneCatalog(original)
	e.catalog.state.setDescriptor(survivor.address, &maximum)
	// Restore the unexhausted setup before its automatic session retirement.
	t.Cleanup(func() { e.catalog.state = original })
	stage := e.BeginCatalogStage(e.CatalogRead())
	stage.terminalCleanup = true
	_, err := stageCheckDependents(stage, []ids.ObjectAddress{sequence.address}, parser.DropCascade)
	if err != nil {
		t.Fatal("terminal CHECK preparation must preserve saturated revision with fresh identity:", err)
	}
	replacement, ok := stage.descriptors[survivor.address].(*relationDescriptor)
	if !ok || replacement == &maximum || replacement.revision != maximum.revision || len(replacement.CheckConstraints) != 0 || len(maximum.CheckConstraints) != 1 {
		t.Fatal("terminal saturation changed original or omitted replacement")
	}
	e.DiscardCatalogStage(stage)
}

func TestTemporaryTableTerminalMaximumPublication(t *testing.T) {
	e, op, owner, sequence, survivor := temporaryCleanupConsumer(t)
	roleExecute(t, e, op, "CREATE INDEX cleanup_survivor_v_idx ON public.cleanup_survivor(v)")
	roleExecute(t, e, op, "INSERT INTO public.cleanup_survivor VALUES(7)")
	index := e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.namespace, Name: "cleanup_survivor_v_idx"})
	store, root := e.storage[survivor.address], e.indexRoots[index]
	// The storage object owns mutable publication heads; views retain the
	// immutable vector/header and end root, not that object's later slice header.
	retained, retainedEnds := store.versions, store.ends
	original := e.catalog.state
	maximum := *survivor
	maximum.revision = ids.DefinitionRevision(^uint64(0))
	e.catalog.state = cloneCatalog(original)
	e.catalog.state.setDescriptor(survivor.address, &maximum)
	t.Cleanup(func() {
		if e.catalog.state.Get(sequence.address) != nil {
			e.catalog.state = original
		}
	})
	peer := sessionStart(t, e, nil)
	peerOp, err := peer.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(peerOp.Done)
	held := statement(t, "INSERT INTO public.cleanup_survivor VALUES(9)")
	if _, err := e.Describe(peerOp, held); err != nil {
		t.Fatal("held peer description", err)
	}
	epoch, generation := e.catalog.state.epoch, e.catalog.state.namespaceGeneration
	if err := e.endTemporarySession(op.session); err != nil {
		t.Fatal(err)
	}
	replacement := e.catalog.state.MustRelation(survivor.address)
	if replacement == &maximum || replacement.revision != maximum.revision || len(replacement.CheckConstraints) != 0 || len(maximum.CheckConstraints) != 1 || e.catalog.nextRevision != maximum.revision {
		t.Fatal("saturated replacement identity/frontier")
	}
	assertTemporaryNamespacePair(t, e, op.session.token, owner.namespace)
	if e.storage[survivor.address] != store || e.indexRoots[index] != root || e.catalog.state.epoch != epoch || e.catalog.state.namespaceGeneration != generation || e.sequences[sequence.address] != nil {
		t.Fatal("maximum cleanup changed rows/roots/peer analysis or kept provider")
	}
	result, err := e.Execute(peerOp, held, 0, nil)
	current := e.storage[survivor.address]
	if err != nil || result.Count != 1 || len(current.versions) != 2 || len(retained) != 1 || current.ends != retainedEnds {
		t.Fatal("held AST did not rebind current saturated descriptor", result.Count, err)
	}
	for i, want := range []int64{7, 9} {
		value, err := current.versions[i].values[0].Integer()
		if err != nil || value != want {
			t.Fatal("committed row changed", i, value, want, err)
		}
	}
	value, err := retained[0].values[0].Integer()
	if err != nil || value != 7 {
		t.Fatal("retained immutable row changed", value, err)
	}
}

func TestTemporaryTableCascadeRevisionBounds(t *testing.T) {
	for _, kind := range []string{"CHECK", "DEFAULT"} {
		t.Run(kind, func(t *testing.T) {
			e, op, _, sequence, consumer := temporaryCleanupConsumer(t)
			if kind == "DEFAULT" {
				roleExecute(t, e, op, "CREATE SEQUENCE public.cleanup_default_source")
				roleExecute(t, e, op, "CREATE TABLE public.cleanup_defaults(v BIGINT DEFAULT nextval('public.cleanup_default_source'))")
				sequence = e.catalog.state.MustRelation(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: consumer.namespace, Name: "cleanup_default_source"}))
				consumer = e.catalog.state.MustRelation(e.catalog.state.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: consumer.namespace, Name: "cleanup_defaults"}))
				if len(consumer.Defaults) != 1 {
					t.Fatal("missing actual DEFAULT")
				}
			}
			original := e.catalog.state
			maximum := *consumer
			maximum.revision = ids.DefinitionRevision(^uint64(0))
			e.catalog.state = cloneCatalog(original)
			e.catalog.state.setDescriptor(consumer.address, &maximum)
			t.Cleanup(func() { e.catalog.state = original })
			for _, terminal := range []bool{false, true} {
				stage := e.BeginCatalogStage(e.CatalogRead())
				stage.terminalCleanup = terminal
				var err error
				if kind == "CHECK" {
					_, err = stageCheckDependents(stage, []ids.ObjectAddress{sequence.address}, parser.DropCascade)
				} else {
					_, err = stageDefaultDependents(stage, []ids.ObjectAddress{sequence.address}, parser.DropCascade)
				}
				if !terminal {
					if err == nil || err.Error() != "stuntgres: definition revision exhausted" || len(stage.descriptors) != 0 || e.catalog.state.Get(consumer.address) != &maximum {
						t.Fatal("ordinary maximum cascade accepted or mutated", err)
					}
				} else {
					replacement, ok := stage.descriptors[consumer.address].(*relationDescriptor)
					if err != nil || !ok || replacement == &maximum || replacement.revision != maximum.revision {
						t.Fatal("terminal maximum cascade omitted fresh metadata", err)
					}
					if kind == "CHECK" && (len(replacement.CheckConstraints) != 0 || len(maximum.CheckConstraints) != 1) || kind == "DEFAULT" && (len(replacement.Defaults) != 0 || len(maximum.Defaults) != 1) {
						t.Fatal("replacement changed captured definition")
					}
				}
				e.DiscardCatalogStage(stage)
			}
			stage := e.BeginCatalogStage(e.catalog.read(original))
			stage.terminalCleanup = true
			var err error
			if kind == "CHECK" {
				_, err = stageCheckDependents(stage, []ids.ObjectAddress{sequence.address}, parser.DropCascade)
			} else {
				_, err = stageDefaultDependents(stage, []ids.ObjectAddress{sequence.address}, parser.DropCascade)
			}
			replacement, ok := stage.descriptors[consumer.address].(*relationDescriptor)
			if err != nil || !ok || replacement.revision != consumer.revision+1 {
				t.Fatal("below-maximum cascade did not increment", err)
			}
			e.DiscardCatalogStage(stage)
		})
	}
}
