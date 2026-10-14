package engine

import (
	"context"
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
	sequence := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 20003}, namespace: table.namespace, name: "owned", revision: 1, sequencePlanStamp: 1, owner: table.owner, columns: e.sequenceColumnsLocked(), index: map[string]int{"last_value": 0, "log_cnt": 1, "is_called": 2}, attributes: []int32{1, 2, 3}, attributeHighWater: 3, relationKind: sequenceRelation, sequence: &options}
	class, _ := sqltype.DefaultIndexClass(table.columns[0].Usage.Type)
	index := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 20004}, namespace: table.namespace, name: "backing", revision: 1, owner: table.owner, columns: table.columns, index: table.index, attributes: table.attributes, attributeHighWater: 1, relationKind: indexRelation, sqlIndex: &indexDefinition{parent: table.address, keys: []indexKey{{attribute: 1, class: class}}}}
	peer := sessionStart(t, e, nil)
	peerNamespace := &namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: 20005}, name: "pg_temp_peer", revision: 1, owner: table.owner, temporary: peer.token}
	peerTable := *table
	peerTable.address.ObjectID = 20006
	peerTable.namespace = peerNamespace.address
	for _, d := range []Descriptor{sequence, index, peerNamespace, &peerTable} {
		e.catalogChangeLocked(c, d.Address(), d)
	}
	e.state = e.privateCatalogLocked(c).state
	op.session.temporaryNamespace = table.namespace
	rows := []rowVersion{{row: 1, version: 1, values: []sqltype.Value{sqltype.Null(table.columns[0].Usage.Type)}}}
	store := &relationStorage{versions: rows}
	peerStore := &relationStorage{}
	e.storage[table.address], e.storage[peerTable.address] = store, peerStore
	generator := &sequenceGenerator{last: options.start, generation: 1}
	e.sequences[sequence.address] = generator
	root := &logicalIndexRoot{}
	e.indexRoots = map[ObjectAddress]*logicalIndexRoot{}
	e.indexRoots[index.address] = root
	epoch := e.state.epoch
	if err := e.endTemporarySession(op.session); err != nil {
		t.Fatal(err)
	}
	for _, a := range []ObjectAddress{table.address, sequence.address, index.address} {
		if e.state.descriptors[a] != nil || e.storage[a] != nil || e.sequences[a] != nil || e.indexRoots[a] != nil {
			t.Fatal("orphan owner dependency", a)
		}
	}
	if ns, ok := e.state.descriptors[table.namespace].(*namespaceDescriptor); !ok || ns.temporary != op.session.token {
		t.Fatal("cleanup lost retained namespace identity")
	}
	if e.state.descriptors[peerTable.address] != &peerTable || e.storage[peerTable.address] != peerStore || e.state.epoch != epoch {
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
	owner := e.state.descriptors[e.state.names[NameKey{ClassID: RelationClassID, Namespace: namespace, Name: "cleanup_owner"}]].(*relationDescriptor)
	sequence := e.state.descriptors[e.state.names[NameKey{ClassID: RelationClassID, Namespace: namespace, Name: "cleanup_owned"}]].(*relationDescriptor)
	roleExecute(t, e, op, "CREATE TABLE public.cleanup_survivor(v INT CHECK(nextval('cleanup_owned')>0))")
	address := e.state.names[NameKey{ClassID: RelationClassID, Namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, Name: "cleanup_survivor"}]
	survivor := e.state.descriptors[address].(*relationDescriptor)
	if len(survivor.checkConstraints) != 1 || len(survivor.checkConstraints[0].dependencies) == 0 {
		t.Fatal("missing real CHECK manifest")
	}
	return e, op, owner, sequence, survivor
}

func temporaryCleanupPreparedStage(t *testing.T, e *Engine, owner, sequence *relationDescriptor) *CatalogStage {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	stage.terminalCleanup = true
	targets := []ObjectAddress{owner.address, sequence.address}
	for _, address := range targets {
		if err := stage.Remove(ObjectRef{owner: e.owner, Epoch: stage.base.Epoch(), Address: address}); err != nil {
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
		current := e.state
		e.mu.Lock()
		installed, err := e.installTemporarySessionStageLocked(op.session, stage, []ObjectAddress{owner.namespace}, nil)
		e.mu.Unlock()
		if err != nil || installed || e.state != current || e.state.descriptors[survivor.address] != survivor || e.sequences[sequence.address] == nil || op.session.temporaryNamespace != owner.namespace {
			t.Fatal("stale preparation installed", installed, err)
		}
		e.DiscardCatalogStage(stage)
		lateAddress := current.names[NameKey{ClassID: RelationClassID, Namespace: survivor.namespace, Name: "cleanup_late"}]
		late := current.descriptors[lateAddress].(*relationDescriptor)
		if err := e.endTemporarySession(op.session); err != nil {
			t.Fatal(err)
		}
		for _, original := range []*relationDescriptor{survivor, late} {
			replacement := e.state.descriptors[original.address].(*relationDescriptor)
			if replacement == original || len(replacement.checkConstraints) != 0 || len(original.checkConstraints) != 1 || replacement.revision != original.revision+1 || e.state.names[descriptorNameKey(original)] != original.address {
				t.Fatal("reprepare missed consumer or changed captured definition")
			}
		}
		if e.nextRevision <= e.state.descriptors[lateAddress].Revision() {
			t.Fatal("replacement frontier not published")
		}
	})
	t.Run("CurrentDMLAndUnrelatedRoots", func(t *testing.T) {
		e, op, owner, sequence, survivor := temporaryCleanupConsumer(t)
		roleExecute(t, e, op, "CREATE SEQUENCE public.cleanup_unrelated")
		roleExecute(t, e, op, "CREATE INDEX cleanup_survivor_v_idx ON public.cleanup_survivor(v)")
		unrelated := e.state.names[NameKey{ClassID: RelationClassID, Namespace: survivor.namespace, Name: "cleanup_unrelated"}]
		index := e.state.names[NameKey{ClassID: RelationClassID, Namespace: survivor.namespace, Name: "cleanup_survivor_v_idx"}]
		stage := temporaryCleanupPreparedStage(t, e, owner, sequence)
		captured := e.state
		// A real row publication replaces storage/index heads, not the catalog.
		roleExecute(t, e, op, "INSERT INTO public.cleanup_survivor VALUES(7)")
		currentStore, currentRoot, currentGenerator := e.storage[survivor.address], e.indexRoots[index], e.sequences[unrelated]
		if e.state != captured || currentStore == nil || len(currentStore.versions) != 1 || currentRoot == nil || currentGenerator == nil {
			t.Fatal("missing genuine row/index/generator fixture")
		}
		epoch, generation := e.state.epoch, e.state.namespaceGeneration
		e.mu.Lock()
		installed, err := e.installTemporarySessionStageLocked(op.session, stage, []ObjectAddress{owner.namespace}, nil)
		e.mu.Unlock()
		if err != nil || !installed || e.storage[survivor.address] != currentStore || e.indexRoots[index] != currentRoot || e.sequences[unrelated] != currentGenerator || e.state.epoch != epoch || e.state.namespaceGeneration != generation {
			t.Fatal("current DML or unrelated roots lost", installed, err)
		}
		assertTemporaryNamespacePair(t, e, op.session.token, owner.namespace)
		if len(survivor.checkConstraints) != 1 || len(e.state.descriptors[survivor.address].(*relationDescriptor).checkConstraints) != 0 || e.storage[owner.address] != nil || e.sequences[sequence.address] != nil {
			t.Fatal("incomplete terminal stage")
		}
		e.DiscardCatalogStage(stage)
	})
}

func TestTemporaryTableTerminalRevisionPreparation(t *testing.T) {
	e, _, _, sequence, survivor := temporaryCleanupConsumer(t)
	original := e.state
	maximum := *survivor
	maximum.revision = DefinitionRevision(^uint64(0))
	e.state = cloneCatalog(original)
	e.state.descriptors[survivor.address] = &maximum
	// Restore the unexhausted setup before its automatic session retirement.
	t.Cleanup(func() { e.state = original })
	stage := e.BeginCatalogStage(e.CatalogRead())
	stage.terminalCleanup = true
	_, err := stageCheckDependents(stage, []ObjectAddress{sequence.address}, parser.DropCascade)
	if err != nil {
		t.Fatal("terminal CHECK preparation must preserve saturated revision with fresh identity:", err)
	}
	replacement, ok := stage.descriptors[survivor.address].(*relationDescriptor)
	if !ok || replacement == &maximum || replacement.revision != maximum.revision || len(replacement.checkConstraints) != 0 || len(maximum.checkConstraints) != 1 {
		t.Fatal("terminal saturation changed original or omitted replacement")
	}
	e.DiscardCatalogStage(stage)
}

func TestTemporaryTableTerminalMaximumPublication(t *testing.T) {
	e, op, owner, sequence, survivor := temporaryCleanupConsumer(t)
	roleExecute(t, e, op, "CREATE INDEX cleanup_survivor_v_idx ON public.cleanup_survivor(v)")
	roleExecute(t, e, op, "INSERT INTO public.cleanup_survivor VALUES(7)")
	index := e.state.names[NameKey{ClassID: RelationClassID, Namespace: survivor.namespace, Name: "cleanup_survivor_v_idx"}]
	store, root := e.storage[survivor.address], e.indexRoots[index]
	// The storage object owns mutable publication heads; views retain the
	// immutable vector/header and end root, not that object's later slice header.
	retained, retainedEnds := store.versions, store.ends
	original := e.state
	maximum := *survivor
	maximum.revision = DefinitionRevision(^uint64(0))
	e.state = cloneCatalog(original)
	e.state.descriptors[survivor.address] = &maximum
	t.Cleanup(func() {
		if e.state.descriptors[sequence.address] != nil {
			e.state = original
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
	epoch, generation := e.state.epoch, e.state.namespaceGeneration
	if err := e.endTemporarySession(op.session); err != nil {
		t.Fatal(err)
	}
	replacement := e.state.descriptors[survivor.address].(*relationDescriptor)
	if replacement == &maximum || replacement.revision != maximum.revision || len(replacement.checkConstraints) != 0 || len(maximum.checkConstraints) != 1 || e.nextRevision != maximum.revision {
		t.Fatal("saturated replacement identity/frontier")
	}
	assertTemporaryNamespacePair(t, e, op.session.token, owner.namespace)
	if e.storage[survivor.address] != store || e.indexRoots[index] != root || e.state.epoch != epoch || e.state.namespaceGeneration != generation || e.sequences[sequence.address] != nil {
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
				sequence = e.state.descriptors[e.state.names[NameKey{ClassID: RelationClassID, Namespace: consumer.namespace, Name: "cleanup_default_source"}]].(*relationDescriptor)
				consumer = e.state.descriptors[e.state.names[NameKey{ClassID: RelationClassID, Namespace: consumer.namespace, Name: "cleanup_defaults"}]].(*relationDescriptor)
				if len(consumer.defaults) != 1 {
					t.Fatal("missing actual DEFAULT")
				}
			}
			original := e.state
			maximum := *consumer
			maximum.revision = DefinitionRevision(^uint64(0))
			e.state = cloneCatalog(original)
			e.state.descriptors[consumer.address] = &maximum
			t.Cleanup(func() { e.state = original })
			for _, terminal := range []bool{false, true} {
				stage := e.BeginCatalogStage(e.CatalogRead())
				stage.terminalCleanup = terminal
				var err error
				if kind == "CHECK" {
					_, err = stageCheckDependents(stage, []ObjectAddress{sequence.address}, parser.DropCascade)
				} else {
					_, err = stageDefaultDependents(stage, []ObjectAddress{sequence.address}, parser.DropCascade)
				}
				if !terminal {
					if err == nil || err.Error() != "stuntgres: definition revision exhausted" || len(stage.descriptors) != 0 || e.state.descriptors[consumer.address] != &maximum {
						t.Fatal("ordinary maximum cascade accepted or mutated", err)
					}
				} else {
					replacement, ok := stage.descriptors[consumer.address].(*relationDescriptor)
					if err != nil || !ok || replacement == &maximum || replacement.revision != maximum.revision {
						t.Fatal("terminal maximum cascade omitted fresh metadata", err)
					}
					if kind == "CHECK" && (len(replacement.checkConstraints) != 0 || len(maximum.checkConstraints) != 1) || kind == "DEFAULT" && (len(replacement.defaults) != 0 || len(maximum.defaults) != 1) {
						t.Fatal("replacement changed captured definition")
					}
				}
				e.DiscardCatalogStage(stage)
			}
			stage := e.BeginCatalogStage(CatalogRead{owner: e, state: original})
			stage.terminalCleanup = true
			var err error
			if kind == "CHECK" {
				_, err = stageCheckDependents(stage, []ObjectAddress{sequence.address}, parser.DropCascade)
			} else {
				_, err = stageDefaultDependents(stage, []ObjectAddress{sequence.address}, parser.DropCascade)
			}
			replacement, ok := stage.descriptors[consumer.address].(*relationDescriptor)
			if err != nil || !ok || replacement.revision != consumer.revision+1 {
				t.Fatal("below-maximum cascade did not increment", err)
			}
			e.DiscardCatalogStage(stage)
		})
	}
}
