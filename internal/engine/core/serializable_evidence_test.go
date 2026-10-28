package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func serializableUnitRelation(t *testing.T, e *core.Engine) ids.ObjectRef {
	t.Helper()
	base := e.CatalogRead()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: catalogUnitOID(t, e)}
	d := &catalog.Relation{ObjectAddress: address, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "ssi_unit", ObjectRevision: 1, Owner: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), RelationKind: catalog.TableRelation, Columns: []catalog.Column{{Name: "v", Usage: enginetest.Usage(e, sqltype.Int4)}}, Index: map[string]int{"v": 0}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
	d.TableMethod = 2
	stage := e.BeginCatalogStage(base)
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	return e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), address)
}
func serializableUnitScope(node *visibility.SerializableTx) *core.StatementScope {
	return &core.StatementScope{Owner: session.TxOwnerOf(node.Ref), TxRef: node.Ref}
}
func TestSerializableEvidenceEmptyReadAndAcceptedWrite(t *testing.T) {
	e := engine.New()
	ref := serializableUnitRelation(t, e)
	reader := serializableUnitNode(t, e, false)
	writer := serializableUnitNode(t, e, false)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	if err := e.SerializableReadLocked(serializableUnitScope(reader), ref); err != nil {
		t.Fatal(err)
	}
	event := [1]visibility.SerializableWrite{{Row: 1, Version: 1}}
	if err := e.SerializableWriteLocked(serializableUnitScope(writer), ref, event[:]); err != nil {
		t.Fatal(err)
	}
	if len(reader.Reads) != 1 || len(reader.Outgoing) != 1 || len(writer.Incoming) != 1 || !writer.DidWrite || e.Visibility.Serializable.Creators[1] != writer.Ref {
		t.Fatal("empty read/write evidence omitted")
	}
}
func TestSerializableEvidenceInvisibleCreatorAndVisibleEnder(t *testing.T) {
	for _, visible := range []bool{false, true} {
		t.Run(map[bool]string{false: "invisible_creator", true: "visible_ender"}[visible], func(t *testing.T) {
			e := engine.New()
			ref := serializableUnitRelation(t, e)
			reader := serializableUnitNode(t, e, false)
			creator := serializableUnitNode(t, e, false)
			ender := serializableUnitNode(t, e, false)
			e.Mu.Lock()
			defer e.Mu.Unlock()
			creator.Writes[ref.Address] = map[visibility.WriteKey]visibility.SerializableWrite{{Version: 1}: {Row: 1, Version: 1}}
			creator.DidWrite = true
			e.Visibility.Serializable.Creators[1] = creator.Ref
			e.Visibility.SerializableCommittedLocked(creator.Ref, reader.Frontier+1)
			if visible {
				reader.Frontier = creator.CommitFrontier
				reader.SnapshotOrder = creator.Order
			}
			ender.Writes[ref.Address] = map[visibility.WriteKey]visibility.SerializableWrite{{Version: 2}: {Row: 1, Version: 2, Base: 1, BaseBorn: creator.CommitFrontier, Creator: creator.Ref}}
			if err := e.SerializableReadLocked(serializableUnitScope(reader), ref); err != nil {
				t.Fatal(err)
			}
			want := creator
			if visible {
				want = ender
			}
			if len(reader.Outgoing) != 1 {
				t.Fatal("selected zero/multiple writers", len(reader.Outgoing))
			}
			if _, ok := reader.Outgoing[want]; !ok {
				t.Fatal("wrong creator/ender identity")
			}
		})
	}
}
func TestSerializableEvidenceRejectsForeignStaleAndInvalidBeforeMutation(t *testing.T) {
	e := engine.New()
	ref := serializableUnitRelation(t, e)
	node := serializableUnitNode(t, e, false)
	scope := serializableUnitScope(node)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	for _, kind := range []string{"foreign", "stale", "subobject", "row", "version", "transaction"} {
		bad := ref
		event := [1]visibility.SerializableWrite{{Row: 1, Version: 1}}
		s := *scope
		switch kind {
		case "foreign":
			bad = (&ids.Domain{}).Ref(bad.Epoch, bad.Address)
		case "stale":
			bad.Epoch++
		case "subobject":
			bad.Address.SubID = 1
		case "row":
			event[0].Row = 0
		case "version":
			event[0].Version = 0
		case "transaction":
			s.TxRef = withTxID(s.TxRef, s.TxRef.ID()+1)
		}
		err := e.SerializableWriteLocked(&s, bad, event[:])
		var diagnostic *sqlstate.Error
		if err == nil || errors.As(err, &diagnostic) {
			t.Fatal("invalid identity did not stay ordinary", kind, err)
		}
		if node.DidWrite || len(node.Writes) != 0 || len(node.Incoming) != 0 || len(node.Outgoing) != 0 {
			t.Fatal("invalid evidence mutated graph", kind)
		}
	}
}
func TestSerializableEvidenceRollbackKeepsReadsEdgesDoomAndStickyWrite(t *testing.T) {
	e := engine.New()
	ref := serializableUnitRelation(t, e)
	a := serializableUnitNode(t, e, false)
	b := serializableUnitNode(t, e, false)
	scope := serializableUnitScope(b)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	b.Reads[ref.Address] = struct{}{}
	event := [1]visibility.SerializableWrite{{Row: 1, Version: 1}}
	if err := e.SerializableWriteLocked(scope, ref, event[:]); err != nil {
		t.Fatal(err)
	}
	if err := e.Visibility.SerializableEdgeLocked(a, a, b); err != nil {
		t.Fatal(err)
	}
	b.Doomed = true
	core.EngineReconcileSerializableWritesLocked(e, session.TxOwnerOf(b.Ref))
	if len(b.Writes) != 0 || len(e.Visibility.Serializable.Creators) != 0 || !b.DidWrite || !b.Doomed || len(b.Reads) != 1 || len(b.Incoming) != 1 {
		t.Fatal("rollback erased top-level state or retained discarded payload identity")
	}
	serializableUnitDiagnostic(t, e.SerializableWriteLocked(scope, ref, event[:]), "Canceled on identification as a pivot, during conflict in checking.")
	serializableUnitDiagnostic(t, e.SerializableReadLocked(scope, ref), "Canceled on identification as a pivot, during conflict out checking.")
	serializableUnitDiagnostic(t, e.Visibility.SerializablePrecommitLocked(b.Ref), "Canceled on identification as a pivot, during commit attempt.")
}
func TestSerializableEvidenceExemptSourcesAndUnrelatedFrontier(t *testing.T) {
	e := engine.New()
	ref := serializableUnitRelation(t, e)
	node := serializableUnitNode(t, e, false)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	catalog := catalog.CloneState(e.Catalog.State)
	original := catalog.MustRelation(ref.Address)
	copy := *original
	copy.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	catalog.SetDescriptor(ref.Address, &copy)
	e.Catalog.PublishLocked(catalog)
	if err := e.SerializableReadLocked(serializableUnitScope(node), ref); err != nil {
		t.Fatal(err)
	}
	event := [1]visibility.SerializableWrite{{Row: 1, Version: 1}}
	if err := e.SerializableWriteLocked(serializableUnitScope(node), ref, event[:]); err != nil {
		t.Fatal(err)
	}
	if len(node.Reads) != 0 || len(node.Writes) != 0 || node.DidWrite {
		t.Fatal("system source participated")
	}
	e.Visibility.SetCommitSeqLocked(e.Visibility.CommitSeq() + 100)
	if err := e.Visibility.SerializablePrecommitLocked(node.Ref); err != nil {
		t.Fatal("unrelated frontier aborted owner", err)
	}
}

func TestSerializableEvidenceOwnCreatorSurvivesEndingRollback(t *testing.T) {
	e := engine.New()
	relation := serializableUnitRelation(t, e)
	reader := serializableUnitNode(t, e, false)
	writer := serializableUnitNode(t, e, false)
	scope := serializableUnitScope(writer)
	owner := session.TxOwnerOf(writer.Ref)
	e.Mu.Lock()
	defer e.Mu.Unlock()
	create := [1]visibility.SerializableWrite{{Row: 41, Version: 73}}
	if err := e.SerializableWriteLocked(scope, relation, create[:]); err != nil {
		t.Fatal(err)
	}
	owner.Writes = map[ids.ObjectAddress][]storage.RowVersion{relation.Address: {{Row: 41, Version: 73}}}
	ending := [1]visibility.SerializableWrite{{Row: 41, Version: 73, Base: 73, Creator: writer.Ref, Deletion: true}}
	if err := e.SerializableWriteLocked(scope, relation, ending[:]); err != nil {
		t.Fatal(err)
	}
	owner.Deletes = map[ids.ObjectAddress]*visibility.PrivateDeletes{relation.Address: {Rows: map[ids.RowID]visibility.DeleteCandidate{41: {Row: 41, Version: 73, Own: true}}}}
	core.EngineReconcileSerializableWritesLocked(e, owner)
	if len(writer.Writes[relation.Address]) != 2 {
		t.Fatal("ending overwrote same-version creator")
	}
	// Restore the saved owner overlays: insertion remains, ending is discarded.
	owner.Deletes = nil
	core.EngineReconcileSerializableWritesLocked(e, owner)
	event, ok := writer.Writes[relation.Address][visibility.WriteKey{Version: 73}]
	if !ok || event.Row != 41 || event.Version != 73 || event.Deletion || len(writer.Writes[relation.Address]) != 1 || e.Visibility.Serializable.Creators[73] != writer.Ref || !writer.DidWrite {
		t.Fatal("rollback lost surviving exact creator identity")
	}
	e.Visibility.SerializableCommittedLocked(writer.Ref, e.Visibility.CommitSeq()+1)
	if err := e.SerializableReadLocked(serializableUnitScope(reader), relation); err != nil {
		t.Fatal(err)
	}
	if _, ok := reader.Outgoing[writer]; !ok {
		t.Fatal("old-view read omitted surviving creator dependency")
	}
	owner.Writes = nil
	core.EngineReconcileSerializableWritesLocked(e, owner)
	if len(writer.Writes) != 0 || len(e.Visibility.Serializable.Creators) != 0 || !writer.DidWrite || len(reader.Outgoing) != 1 {
		t.Fatal("full overlay discard lost sticky graph or retained creator")
	}
}

func TestSerializableEvidenceActualTemporaryNamespaceAndSequenceProviders(t *testing.T) {
	e := engine.New()
	session := sessionStart(t, e, nil)
	sessionSQL(t, session, "CREATE SEQUENCE persistent_s")
	sessionSQL(t, session, "CREATE TEMP SEQUENCE temporary_s")
	sessionSQL(t, session, "CREATE TEMP TABLE eligibility(v INT)")
	view := e.CatalogRead()
	matches := view.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: session.TemporaryNamespace, Name: "eligibility"})
	if len(matches) != 1 {
		t.Fatal("actual owner TEMP table missing", matches)
	}
	relation := e.Catalog.Domain.Ref(view.Epoch(), matches[0])
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
			_ = enginetest.Tuples(result)
			result.Rows.Close()
		}
		e.Mu.Lock()
		node := e.Visibility.Serializable.Nodes[session.Transactions.Ref]
		if node == nil || !node.Captured || len(node.Reads) != 0 || len(node.Writes) != 0 || node.DidWrite || len(node.Incoming) != 0 || len(node.Outgoing) != 0 {
			t.Fatal("actual sequence/catalog provider registered SSI evidence", query)
		}
		e.Mu.Unlock()
	}
	e.Mu.Lock()
	node := e.Visibility.Serializable.Nodes[session.Transactions.Ref]
	namespace := e.Catalog.State.MustNamespace(session.TemporaryNamespace)
	if namespace.Temporary != session.SessionToken {
		t.Fatal("fixture did not create actual session-owned temporary namespace")
	}
	scope := &core.StatementScope{Owner: session.Transactions, TxRef: session.Transactions.Ref}
	if core.EngineSerializableEligibleLocked(e, relation, nil) {
		t.Fatal("temporary stored relation enrolled")
	}
	if err := e.SerializableReadLocked(scope, relation); err != nil {
		t.Fatal(err)
	}
	event := [1]visibility.SerializableWrite{{Row: 1, Version: 1}}
	if err := e.SerializableWriteLocked(scope, relation, event[:]); err != nil {
		t.Fatal(err)
	}
	if len(node.Reads) != 0 || len(node.Writes) != 0 || node.DidWrite || len(node.Incoming) != 0 || len(node.Outgoing) != 0 {
		t.Fatal("temporary stored relation registered evidence")
	}
	var persistentGenerator, temporaryGenerator *storage.SequenceGenerator
	for address, descriptor := range e.Catalog.State.Descriptors() {
		if d, ok := descriptor.(*catalog.Relation); ok && d.RelationKind == catalog.SequenceRelation {
			if d.Name() == "persistent_s" {
				persistentGenerator = e.Storage.Sequences[address]
			}
			if d.Name() == "temporary_s" {
				temporaryGenerator = e.Storage.Sequences[address]
			}
		}
	}
	if persistentGenerator == nil || temporaryGenerator == nil || persistentGenerator.Last != 1 || temporaryGenerator.Last != 1 || !persistentGenerator.Called || !temporaryGenerator.Called {
		t.Fatal("actual provider draws missing")
	}
	e.Mu.Unlock()
	sessionSQL(t, session, "ROLLBACK")
	if persistentGenerator.Last != 1 || temporaryGenerator.Last != 1 || !persistentGenerator.Called || !temporaryGenerator.Called {
		t.Fatal("SSI rollback undid nontransactional sequence draws")
	}
}
