package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestTemporaryTableCleanupClosure(t *testing.T) {
	e, op, c, table := temporaryCatalogCandidate(t, parser.OnCommitPreserve)
	options, _, err := sequence.BindOptions(nil, storage.SequenceGenerator{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	options.Persistence, options.Owned = "temporary", table.Address()
	options.Owned.SubID = 1
	columns := sequence.Columns(e.Catalog.Current())
	sequence := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20003}, ObjectNamespace: table.Namespace(), ObjectName: "owned", ObjectRevision: 1, SequencePlanStamp: 1, Owner: table.Owner, Columns: columns, Index: map[string]int{"last_value": 0, "log_cnt": 1, "is_called": 2}, Attributes: []int32{1, 2, 3}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}, RelationKind: catalog.SequenceRelation, Sequence: &options}
	class, _ := sqltype.DefaultIndexClass(table.Columns[0].Usage.Type)
	index := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20004}, ObjectNamespace: table.Namespace(), ObjectName: "backing", ObjectRevision: 1, Owner: table.Owner, Columns: table.Columns, Index: table.Index, Attributes: table.Attributes, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}, RelationKind: catalog.IndexRelation, SQLIndex: &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: table.Address(), Keys: []catalog.IndexKey{{Attribute: 1, Class: class}}}}
	peer := sessionStart(t, e, nil)
	peerNamespace := &catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20005}, ObjectName: "pg_temp_peer", ObjectRevision: 1, Owner: table.Owner, Temporary: peer.SessionToken}
	peerTable := *table
	peerTable.ObjectAddress.ObjectID = 20006
	peerTable.ObjectNamespace = peerNamespace.Address()
	for _, d := range []catalog.Descriptor{sequence, index, peerNamespace, &peerTable} {
		e.CatalogChangeLocked(c, d.Address(), d)
	}
	e.Catalog.PublishLocked(e.PrivateCatalogLocked(c).State())
	op.Session.TemporaryNamespace = table.Namespace()
	rows := []storage.RowVersion{{Row: 1, Version: 1, Values: []sqltype.Value{sqltype.Null(table.Columns[0].Usage.Type)}}}
	store := &storage.Relation{Versions: rows}
	peerStore := &storage.Relation{}
	e.Mu.Lock()
	e.Storage.InstallRelationLocked(table.Address(), store)
	e.Storage.InstallRelationLocked(peerTable.Address(), peerStore)
	e.Mu.Unlock()
	generator := &storage.SequenceGenerator{Last: options.Start, Generation: 1}
	e.Mu.Lock()
	e.Storage.InstallSequenceLocked(sequence.Address(), generator)
	e.Mu.Unlock()
	root := &storage.IndexRoot{}
	e.Mu.Lock()
	e.Storage.ReplaceIndexRootsLocked(map[ids.ObjectAddress]*storage.IndexRoot{})
	e.Mu.Unlock()
	e.Mu.Lock()
	e.Storage.InstallIndexRootLocked(index.Address(), root)
	e.Mu.Unlock()
	epoch := e.Catalog.State.Epoch()
	if err := e.endTemporarySession(op.Session); err != nil {
		t.Fatal(err)
	}
	for _, a := range []ids.ObjectAddress{table.Address(), sequence.Address(), index.Address()} {
		if e.Catalog.State.Get(a) != nil || e.Storage.Relations[a] != nil || e.Storage.Sequences[a] != nil || e.Storage.IndexRoots[a] != nil {
			t.Fatal("orphan owner dependency", a)
		}
	}
	if ns, ok := e.Catalog.State.Namespace(table.Namespace()); !ok || ns.Temporary != op.Session.SessionToken {
		t.Fatal("cleanup lost retained namespace identity")
	}
	if e.Catalog.State.Get(peerTable.Address()) != &peerTable || e.Storage.Relations[peerTable.Address()] != peerStore || e.Catalog.State.Epoch() != epoch {
		t.Fatal("peer or epoch changed")
	}
	if len(store.Versions) != 1 || &store.Versions[0] != &rows[0] {
		t.Fatal("retained row snapshot changed")
	}
	if err := e.endTemporarySession(op.Session); err != nil {
		t.Fatal(err)
	}
	if e.Storage.Relations[peerTable.Address()] != peerStore {
		t.Fatal("repeat cleanup changed peer")
	}
}

// Setup binds an actual stored CHECK/manifest; assertions exercise the private
// terminal stage and publication seams rather than repeat driver SQL scenarios.
func temporaryCleanupConsumer(t *testing.T) (*Engine, *session.Operation, *catalog.Relation, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	roleExecute(t, e, op, "CREATE TEMP TABLE cleanup_owner(v INT)")
	roleExecute(t, e, op, "CREATE TEMP SEQUENCE cleanup_owned OWNED BY cleanup_owner.v")
	namespace := op.Session.TemporaryNamespace
	assertTemporaryNamespacePair(t, e, op.Session.SessionToken, namespace)
	owner := e.Catalog.State.MustRelation(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "cleanup_owner"}))
	sequence := e.Catalog.State.MustRelation(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: namespace, Name: "cleanup_owned"}))
	roleExecute(t, e, op, "CREATE TABLE public.cleanup_survivor(v INT CHECK(nextval('cleanup_owned')>0))")
	address := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "cleanup_survivor"})
	survivor := e.Catalog.State.MustRelation(address)
	if len(survivor.Constraints.Check) != 1 || len(survivor.Constraints.Check[0].Dependencies) == 0 {
		t.Fatal("missing real CHECK manifest")
	}
	return e, op, owner, sequence, survivor
}

func temporaryCleanupPreparedStage(t *testing.T, e *Engine, owner, sequence *catalog.Relation) *CatalogStage {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	stage.terminalCleanup = true
	targets := []ids.ObjectAddress{owner.Address(), sequence.Address()}
	for _, address := range targets {
		if err := stage.Remove(e.Catalog.Domain.Ref(stage.Base.Epoch(), address)); err != nil {
			t.Fatal(err)
		}
	}
	if err := StageDropProvider(context.Background(), stage, nil, nil, nil).ApplyViewDependents(targets, parser.DropCascade); err != nil {
		t.Fatal(err)
	}
	return stage
}

func TestTemporaryTableCleanupPublication(t *testing.T) {
	t.Run("CatalogRootFenceAndNewConsumer", func(t *testing.T) {
		e, op, owner, sequence, survivor := temporaryCleanupConsumer(t)
		stage := temporaryCleanupPreparedStage(t, e, owner, sequence)
		roleExecute(t, e, op, "CREATE TABLE public.cleanup_late(v INT CHECK(nextval('cleanup_owned')>0))")
		current := e.Catalog.State
		e.Mu.Lock()
		installed, err := e.installTemporarySessionStageLocked(op.Session, stage, []ids.ObjectAddress{owner.Namespace()}, nil)
		e.Mu.Unlock()
		if err != nil || installed || e.Catalog.State != current || e.Catalog.State.Get(survivor.Address()) != survivor || e.Storage.Sequences[sequence.Address()] == nil || op.Session.TemporaryNamespace != owner.Namespace() {
			t.Fatal("stale preparation installed", installed, err)
		}
		e.DiscardCatalogStage(stage)
		lateAddress := current.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: "cleanup_late"})
		late := current.MustRelation(lateAddress)
		if err := e.endTemporarySession(op.Session); err != nil {
			t.Fatal(err)
		}
		for _, original := range []*catalog.Relation{survivor, late} {
			replacement := e.Catalog.State.MustRelation(original.Address())
			if replacement == original || len(replacement.Constraints.Check) != 0 || len(original.Constraints.Check) != 1 || replacement.Revision() != original.Revision()+1 || e.Catalog.State.NameAddress(catalog.DescriptorNameKey(original)) != original.Address() {
				t.Fatal("reprepare missed consumer or changed captured definition")
			}
		}
		if e.Catalog.NextRevision() <= e.Catalog.State.Get(lateAddress).Revision() {
			t.Fatal("replacement frontier not published")
		}
	})
	t.Run("CurrentDMLAndUnrelatedRoots", func(t *testing.T) {
		e, op, owner, sequence, survivor := temporaryCleanupConsumer(t)
		roleExecute(t, e, op, "CREATE SEQUENCE public.cleanup_unrelated")
		roleExecute(t, e, op, "CREATE INDEX cleanup_survivor_v_idx ON public.cleanup_survivor(v)")
		unrelated := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: "cleanup_unrelated"})
		index := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: "cleanup_survivor_v_idx"})
		stage := temporaryCleanupPreparedStage(t, e, owner, sequence)
		captured := e.Catalog.State
		// A real row publication replaces storage/index heads, not the catalog.
		roleExecute(t, e, op, "INSERT INTO public.cleanup_survivor VALUES(7)")
		currentStore, currentRoot, currentGenerator := e.Storage.Relations[survivor.Address()], e.Storage.IndexRoots[index], e.Storage.Sequences[unrelated]
		if e.Catalog.State != captured || currentStore == nil || len(currentStore.Versions) != 1 || currentRoot == nil || currentGenerator == nil {
			t.Fatal("missing genuine row/index/generator fixture")
		}
		epoch, generation := e.Catalog.State.Epoch(), e.Catalog.State.NamespaceGeneration()
		e.Mu.Lock()
		installed, err := e.installTemporarySessionStageLocked(op.Session, stage, []ids.ObjectAddress{owner.Namespace()}, nil)
		e.Mu.Unlock()
		if err != nil || !installed || e.Storage.Relations[survivor.Address()] != currentStore || e.Storage.IndexRoots[index] != currentRoot || e.Storage.Sequences[unrelated] != currentGenerator || e.Catalog.State.Epoch() != epoch || e.Catalog.State.NamespaceGeneration() != generation {
			t.Fatal("current DML or unrelated roots lost", installed, err)
		}
		assertTemporaryNamespacePair(t, e, op.Session.SessionToken, owner.Namespace())
		if len(survivor.Constraints.Check) != 1 || len(e.Catalog.State.MustRelation(survivor.Address()).Constraints.Check) != 0 || e.Storage.Relations[owner.Address()] != nil || e.Storage.Sequences[sequence.Address()] != nil {
			t.Fatal("incomplete terminal stage")
		}
		e.DiscardCatalogStage(stage)
	})
}

func TestTemporaryTableTerminalRevisionPreparation(t *testing.T) {
	e, _, _, sequence, survivor := temporaryCleanupConsumer(t)
	original := e.Catalog.State
	maximum := *survivor
	maximum.ObjectRevision = ids.DefinitionRevision(^uint64(0))
	e.Catalog.PublishLocked(catalog.CloneState(original))
	e.Catalog.State.SetDescriptor(survivor.Address(), &maximum)
	// Restore the unexhausted setup before its automatic session retirement.
	t.Cleanup(func() { e.Catalog.PublishLocked(original) })
	stage := e.BeginCatalogStage(e.CatalogRead())
	stage.terminalCleanup = true
	_, err := stageCheckDependents(stage, []ids.ObjectAddress{sequence.Address()}, parser.DropCascade)
	if err != nil {
		t.Fatal("terminal CHECK preparation must preserve saturated revision with fresh identity:", err)
	}
	replacement, ok := stage.Get(survivor.Address()).(*catalog.Relation)
	if !ok || replacement == &maximum || replacement.Revision() != maximum.Revision() || len(replacement.Constraints.Check) != 0 || len(maximum.Constraints.Check) != 1 {
		t.Fatal("terminal saturation changed original or omitted replacement")
	}
	e.DiscardCatalogStage(stage)
}

func TestTemporaryTableTerminalMaximumPublication(t *testing.T) {
	e, op, owner, sequence, survivor := temporaryCleanupConsumer(t)
	roleExecute(t, e, op, "CREATE INDEX cleanup_survivor_v_idx ON public.cleanup_survivor(v)")
	roleExecute(t, e, op, "INSERT INTO public.cleanup_survivor VALUES(7)")
	index := e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: survivor.Namespace(), Name: "cleanup_survivor_v_idx"})
	store, root := e.Storage.Relations[survivor.Address()], e.Storage.IndexRoots[index]
	// The storage object owns mutable publication heads; views retain the
	// immutable vector/header and end root, not that object's later slice header.
	retained, retainedEnds := store.Versions, store.Ends
	original := e.Catalog.State
	maximum := *survivor
	maximum.ObjectRevision = ids.DefinitionRevision(^uint64(0))
	e.Catalog.PublishLocked(catalog.CloneState(original))
	e.Catalog.State.SetDescriptor(survivor.Address(), &maximum)
	t.Cleanup(func() {
		if e.Catalog.State.Get(sequence.Address()) != nil {
			e.Catalog.PublishLocked(original)
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
	epoch, generation := e.Catalog.State.Epoch(), e.Catalog.State.NamespaceGeneration()
	if err := e.endTemporarySession(op.Session); err != nil {
		t.Fatal(err)
	}
	replacement := e.Catalog.State.MustRelation(survivor.Address())
	if replacement == &maximum || replacement.Revision() != maximum.Revision() || len(replacement.Constraints.Check) != 0 || len(maximum.Constraints.Check) != 1 || e.Catalog.NextRevision() != maximum.Revision() {
		t.Fatal("saturated replacement identity/frontier")
	}
	assertTemporaryNamespacePair(t, e, op.Session.SessionToken, owner.Namespace())
	if e.Storage.Relations[survivor.Address()] != store || e.Storage.IndexRoots[index] != root || e.Catalog.State.Epoch() != epoch || e.Catalog.State.NamespaceGeneration() != generation || e.Storage.Sequences[sequence.Address()] != nil {
		t.Fatal("maximum cleanup changed rows/roots/peer analysis or kept provider")
	}
	result, err := e.Execute(peerOp, held, 0, nil)
	current := e.Storage.Relations[survivor.Address()]
	if err != nil || result.Count != 1 || len(current.Versions) != 2 || len(retained) != 1 || current.Ends != retainedEnds {
		t.Fatal("held AST did not rebind current saturated descriptor", result.Count, err)
	}
	for i, want := range []int64{7, 9} {
		value, err := current.Versions[i].Values[0].Integer()
		if err != nil || value != want {
			t.Fatal("committed row changed", i, value, want, err)
		}
	}
	value, err := retained[0].Values[0].Integer()
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
				sequence = e.Catalog.State.MustRelation(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: consumer.Namespace(), Name: "cleanup_default_source"}))
				consumer = e.Catalog.State.MustRelation(e.Catalog.State.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: consumer.Namespace(), Name: "cleanup_defaults"}))
				if len(consumer.ColumnMeta.Defaults) != 1 {
					t.Fatal("missing actual DEFAULT")
				}
			}
			original := e.Catalog.State
			maximum := *consumer
			maximum.ObjectRevision = ids.DefinitionRevision(^uint64(0))
			e.Catalog.PublishLocked(catalog.CloneState(original))
			e.Catalog.State.SetDescriptor(consumer.Address(), &maximum)
			t.Cleanup(func() { e.Catalog.PublishLocked(original) })
			for _, terminal := range []bool{false, true} {
				stage := e.BeginCatalogStage(e.CatalogRead())
				stage.terminalCleanup = terminal
				var err error
				if kind == "CHECK" {
					_, err = stageCheckDependents(stage, []ids.ObjectAddress{sequence.Address()}, parser.DropCascade)
				} else {
					_, err = stageDefaultDependents(stage, []ids.ObjectAddress{sequence.Address()}, parser.DropCascade)
				}
				if !terminal {
					if err == nil || err.Error() != "stuntgres: definition revision exhausted" || stage.DescriptorCount() != 0 || e.Catalog.State.Get(consumer.Address()) != &maximum {
						t.Fatal("ordinary maximum cascade accepted or mutated", err)
					}
				} else {
					replacement, ok := stage.Get(consumer.Address()).(*catalog.Relation)
					if err != nil || !ok || replacement == &maximum || replacement.Revision() != maximum.Revision() {
						t.Fatal("terminal maximum cascade omitted fresh metadata", err)
					}
					if kind == "CHECK" && (len(replacement.Constraints.Check) != 0 || len(maximum.Constraints.Check) != 1) || kind == "DEFAULT" && (len(replacement.ColumnMeta.Defaults) != 0 || len(maximum.ColumnMeta.Defaults) != 1) {
						t.Fatal("replacement changed captured definition")
					}
				}
				e.DiscardCatalogStage(stage)
			}
			stage := e.BeginCatalogStage(e.Catalog.Read(original))
			stage.terminalCleanup = true
			var err error
			if kind == "CHECK" {
				_, err = stageCheckDependents(stage, []ids.ObjectAddress{sequence.Address()}, parser.DropCascade)
			} else {
				_, err = stageDefaultDependents(stage, []ids.ObjectAddress{sequence.Address()}, parser.DropCascade)
			}
			replacement, ok := stage.Get(consumer.Address()).(*catalog.Relation)
			if err != nil || !ok || replacement.Revision() != consumer.Revision()+1 {
				t.Fatal("below-maximum cascade did not increment", err)
			}
			e.DiscardCatalogStage(stage)
		})
	}
}
