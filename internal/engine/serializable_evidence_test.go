package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func serializableUnitRelation(t *testing.T, e *Engine) ids.ObjectRef {
	t.Helper()
	base := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}
	d := &relationDescriptor{ObjectAddress: address, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "ssi_unit", ObjectRevision: 1, Owner: e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), RelationKind: tableRelation, Columns: []ColumnDescriptor{{Name: "v", Usage: testUsage(e, sqltype.Int4)}}, Index: map[string]int{"v": 0}, Attributes: []int32{1}, AttributeHighWater: 1}
	stage := e.BeginCatalogStage(base)
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	return e.catalog.Domain.Ref(e.catalog.state.epoch, address)
}
func serializableUnitScope(node *serializableTransaction) *StatementScope {
	return &StatementScope{owner: txOwnerOf(node.ref), txRef: node.ref}
}
func TestSerializableEvidenceEmptyReadAndAcceptedWrite(t *testing.T) {
	e := New()
	ref := serializableUnitRelation(t, e)
	reader := serializableUnitNode(t, e, false)
	writer := serializableUnitNode(t, e, false)
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.serializableReadLocked(serializableUnitScope(reader), ref); err != nil {
		t.Fatal(err)
	}
	event := [1]serializableWrite{{row: 1, version: 1}}
	if err := e.serializableWriteLocked(serializableUnitScope(writer), ref, event[:]); err != nil {
		t.Fatal(err)
	}
	if len(reader.reads) != 1 || len(reader.outgoing) != 1 || len(writer.incoming) != 1 || !writer.didWrite || e.serializable.creators[1] != writer.ref {
		t.Fatal("empty read/write evidence omitted")
	}
}
func TestSerializableEvidenceInvisibleCreatorAndVisibleEnder(t *testing.T) {
	for _, visible := range []bool{false, true} {
		t.Run(map[bool]string{false: "invisible_creator", true: "visible_ender"}[visible], func(t *testing.T) {
			e := New()
			ref := serializableUnitRelation(t, e)
			reader := serializableUnitNode(t, e, false)
			creator := serializableUnitNode(t, e, false)
			ender := serializableUnitNode(t, e, false)
			e.mu.Lock()
			defer e.mu.Unlock()
			creator.writes[ref.Address] = map[serializableWriteKey]serializableWrite{{version: 1}: {row: 1, version: 1}}
			creator.didWrite = true
			e.serializable.creators[1] = creator.ref
			e.serializableCommittedLocked(creator.ref, reader.frontier+1)
			if visible {
				reader.frontier = creator.commitFrontier
				reader.snapshotOrder = creator.order
			}
			ender.writes[ref.Address] = map[serializableWriteKey]serializableWrite{{version: 2}: {row: 1, version: 2, base: 1, baseBorn: creator.commitFrontier, creator: creator.ref}}
			if err := e.serializableReadLocked(serializableUnitScope(reader), ref); err != nil {
				t.Fatal(err)
			}
			want := creator
			if visible {
				want = ender
			}
			if len(reader.outgoing) != 1 {
				t.Fatal("selected zero/multiple writers", len(reader.outgoing))
			}
			if _, ok := reader.outgoing[want]; !ok {
				t.Fatal("wrong creator/ender identity")
			}
		})
	}
}
func TestSerializableEvidenceRejectsForeignStaleAndInvalidBeforeMutation(t *testing.T) {
	e := New()
	ref := serializableUnitRelation(t, e)
	node := serializableUnitNode(t, e, false)
	scope := serializableUnitScope(node)
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, kind := range []string{"foreign", "stale", "subobject", "row", "version", "transaction"} {
		bad := ref
		event := [1]serializableWrite{{row: 1, version: 1}}
		s := *scope
		switch kind {
		case "foreign":
			bad = (&ids.Domain{}).Ref(bad.Epoch, bad.Address)
		case "stale":
			bad.Epoch++
		case "subobject":
			bad.Address.SubID = 1
		case "row":
			event[0].row = 0
		case "version":
			event[0].version = 0
		case "transaction":
			s.txRef = withTxID(s.txRef, s.txRef.ID()+1)
		}
		err := e.serializableWriteLocked(&s, bad, event[:])
		var diagnostic *sqlstate.Error
		if err == nil || errors.As(err, &diagnostic) {
			t.Fatal("invalid identity did not stay ordinary", kind, err)
		}
		if node.didWrite || len(node.writes) != 0 || len(node.incoming) != 0 || len(node.outgoing) != 0 {
			t.Fatal("invalid evidence mutated graph", kind)
		}
	}
}
func TestSerializableEvidenceRollbackKeepsReadsEdgesDoomAndStickyWrite(t *testing.T) {
	e := New()
	ref := serializableUnitRelation(t, e)
	a := serializableUnitNode(t, e, false)
	b := serializableUnitNode(t, e, false)
	scope := serializableUnitScope(b)
	e.mu.Lock()
	defer e.mu.Unlock()
	b.reads[ref.Address] = struct{}{}
	event := [1]serializableWrite{{row: 1, version: 1}}
	if err := e.serializableWriteLocked(scope, ref, event[:]); err != nil {
		t.Fatal(err)
	}
	if err := e.serializableEdgeLocked(a, a, b); err != nil {
		t.Fatal(err)
	}
	b.doomed = true
	e.reconcileSerializableWritesLocked(txOwnerOf(b.ref))
	if len(b.writes) != 0 || len(e.serializable.creators) != 0 || !b.didWrite || !b.doomed || len(b.reads) != 1 || len(b.incoming) != 1 {
		t.Fatal("rollback erased top-level state or retained discarded payload identity")
	}
	serializableUnitDiagnostic(t, e.serializableWriteLocked(scope, ref, event[:]), "Canceled on identification as a pivot, during conflict in checking.")
	serializableUnitDiagnostic(t, e.serializableReadLocked(scope, ref), "Canceled on identification as a pivot, during conflict out checking.")
	serializableUnitDiagnostic(t, e.serializablePrecommitLocked(b.ref), "Canceled on identification as a pivot, during commit attempt.")
}
func TestSerializableEvidenceExemptSourcesAndUnrelatedFrontier(t *testing.T) {
	e := New()
	ref := serializableUnitRelation(t, e)
	node := serializableUnitNode(t, e, false)
	e.mu.Lock()
	defer e.mu.Unlock()
	catalog := cloneCatalog(e.catalog.state)
	original := catalog.MustRelation(ref.Address)
	copy := *original
	copy.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	catalog.setDescriptor(ref.Address, &copy)
	e.catalog.state = catalog
	if err := e.serializableReadLocked(serializableUnitScope(node), ref); err != nil {
		t.Fatal(err)
	}
	event := [1]serializableWrite{{row: 1, version: 1}}
	if err := e.serializableWriteLocked(serializableUnitScope(node), ref, event[:]); err != nil {
		t.Fatal(err)
	}
	if len(node.reads) != 0 || len(node.writes) != 0 || node.didWrite {
		t.Fatal("system source participated")
	}
	e.commitSeq += 100
	if err := e.serializablePrecommitLocked(node.ref); err != nil {
		t.Fatal("unrelated frontier aborted owner", err)
	}
}

func TestSerializableEvidenceOwnCreatorSurvivesEndingRollback(t *testing.T) {
	e := New()
	relation := serializableUnitRelation(t, e)
	reader := serializableUnitNode(t, e, false)
	writer := serializableUnitNode(t, e, false)
	scope := serializableUnitScope(writer)
	owner := txOwnerOf(writer.ref)
	e.mu.Lock()
	defer e.mu.Unlock()
	create := [1]serializableWrite{{row: 41, version: 73}}
	if err := e.serializableWriteLocked(scope, relation, create[:]); err != nil {
		t.Fatal(err)
	}
	owner.writes = map[ids.ObjectAddress][]rowVersion{relation.Address: {{row: 41, version: 73}}}
	ending := [1]serializableWrite{{row: 41, version: 73, base: 73, creator: writer.ref, deletion: true}}
	if err := e.serializableWriteLocked(scope, relation, ending[:]); err != nil {
		t.Fatal(err)
	}
	owner.deletes = map[ids.ObjectAddress]*privateDeletionRoot{relation.Address: {rows: map[ids.RowID]deleteCandidate{41: {row: 41, version: 73, own: true}}}}
	e.reconcileSerializableWritesLocked(owner)
	if len(writer.writes[relation.Address]) != 2 {
		t.Fatal("ending overwrote same-version creator")
	}
	// Restore the saved owner overlays: insertion remains, ending is discarded.
	owner.deletes = nil
	e.reconcileSerializableWritesLocked(owner)
	event, ok := writer.writes[relation.Address][serializableWriteKey{version: 73}]
	if !ok || event.row != 41 || event.version != 73 || event.deletion || len(writer.writes[relation.Address]) != 1 || e.serializable.creators[73] != writer.ref || !writer.didWrite {
		t.Fatal("rollback lost surviving exact creator identity")
	}
	e.serializableCommittedLocked(writer.ref, e.commitSeq+1)
	if err := e.serializableReadLocked(serializableUnitScope(reader), relation); err != nil {
		t.Fatal(err)
	}
	if _, ok := reader.outgoing[writer]; !ok {
		t.Fatal("old-view read omitted surviving creator dependency")
	}
	owner.writes = nil
	e.reconcileSerializableWritesLocked(owner)
	if len(writer.writes) != 0 || len(e.serializable.creators) != 0 || !writer.didWrite || len(reader.outgoing) != 1 {
		t.Fatal("full overlay discard lost sticky graph or retained creator")
	}
}

func TestSerializableEvidenceActualTemporaryNamespaceAndSequenceProviders(t *testing.T) {
	e := New()
	session := sessionStart(t, e, nil)
	sessionSQL(t, session, "CREATE SEQUENCE persistent_s")
	sessionSQL(t, session, "CREATE TEMP SEQUENCE temporary_s")
	sessionSQL(t, session, "CREATE TEMP TABLE eligibility(v INT)")
	view := e.CatalogRead()
	matches := view.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: session.temporaryNamespace, Name: "eligibility"})
	if len(matches) != 1 {
		t.Fatal("actual owner TEMP table missing", matches)
	}
	relation := e.catalog.Domain.Ref(view.Epoch(), matches[0])
	sessionSQL(t, session, "BEGIN ISOLATION LEVEL SERIALIZABLE")
	for _, query := range []string{
		"SELECT last_value,is_called FROM persistent_s",
		"SELECT last_value,is_called FROM temporary_s",
		"SELECT relname FROM pg_class WHERE relname='persistent_s'",
		"SELECT nextval('persistent_s')",
		"SELECT nextval('temporary_s')",
	} {
		result := sessionSQL(t, session, query)
		if result.Rows != nil {
			_ = tuples(result)
			result.Rows.Close()
		}
		e.mu.Lock()
		node := e.serializable.nodes[session.transactions.ref]
		if node == nil || !node.captured || len(node.reads) != 0 || len(node.writes) != 0 || node.didWrite || len(node.incoming) != 0 || len(node.outgoing) != 0 {
			t.Fatal("actual sequence/catalog provider registered SSI evidence", query)
		}
		e.mu.Unlock()
	}
	e.mu.Lock()
	node := e.serializable.nodes[session.transactions.ref]
	namespace := e.catalog.state.MustNamespace(session.temporaryNamespace)
	if namespace.Temporary != session.token {
		t.Fatal("fixture did not create actual session-owned temporary namespace")
	}
	scope := &StatementScope{owner: session.transactions, txRef: session.transactions.ref}
	if e.serializableEligibleLocked(relation, nil) {
		t.Fatal("temporary stored relation enrolled")
	}
	if err := e.serializableReadLocked(scope, relation); err != nil {
		t.Fatal(err)
	}
	event := [1]serializableWrite{{row: 1, version: 1}}
	if err := e.serializableWriteLocked(scope, relation, event[:]); err != nil {
		t.Fatal(err)
	}
	if len(node.reads) != 0 || len(node.writes) != 0 || node.didWrite || len(node.incoming) != 0 || len(node.outgoing) != 0 {
		t.Fatal("temporary stored relation registered evidence")
	}
	var persistentGenerator, temporaryGenerator *sequenceGenerator
	for address, descriptor := range e.catalog.state.Descriptors() {
		if d, ok := descriptor.(*relationDescriptor); ok && d.RelationKind == sequenceRelation {
			if d.Name() == "persistent_s" {
				persistentGenerator = e.sequences[address]
			}
			if d.Name() == "temporary_s" {
				temporaryGenerator = e.sequences[address]
			}
		}
	}
	if persistentGenerator == nil || temporaryGenerator == nil || persistentGenerator.last != 1 || temporaryGenerator.last != 1 || !persistentGenerator.called || !temporaryGenerator.called {
		t.Fatal("actual provider draws missing")
	}
	e.mu.Unlock()
	sessionSQL(t, session, "ROLLBACK")
	if persistentGenerator.last != 1 || temporaryGenerator.last != 1 || !persistentGenerator.called || !temporaryGenerator.called {
		t.Fatal("SSI rollback undid nontransactional sequence draws")
	}
}
