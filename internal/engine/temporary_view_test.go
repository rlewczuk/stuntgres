package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// Bind a real query against genuine private TEMP descriptors, then exercise the
// catalog-stage functions directly. No public SQL execution is duplicated here.
func temporaryViewCandidate(t *testing.T, action parser.OnCommitAction, query string) (*Engine, *Operation, *catalogChanges, *relationDescriptor, *CatalogStage, *relationDescriptor) {
	t.Helper()
	e, op, changes, table := temporaryCatalogCandidate(t, action)
	raw := statement(t, query).(*parser.CreateView)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	auth.catalog = e.privateCatalogLocked(changes)
	auth.recaptureCatalog(auth.catalog)
	auth.stagedTemporary = table.namespace
	b, err := e.describe(op.Context(), raw, auth, ObjectRef{}, sqltype.InputEnvironment{}, nil, nil, nil)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	definition, err := e.freezeViewDefinitionLocked(auth, raw.Query, b, false, false, 20008, viewRelation, true)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	d := &relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 20007}, namespace: table.namespace, name: "candidate_view", revision: 1, owner: table.owner, relationKind: viewRelation, view: definition, columns: append([]ColumnDescriptor(nil), b.query.Root.Schema()...), index: map[string]int{"v": 0}, attributes: []int32{1}, attributeHighWater: 1}
	definition.consumer = d.address
	stage := e.BeginCatalogStage(auth.catalog)
	err = e.stageRowTypeLocked(stage, d)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(d); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.DiscardCatalogStage(stage) })
	return e, op, changes, table, stage, d
}

func TestTemporaryViewCatalogPublication(t *testing.T) {
	e, op, changes, _, stage, view := temporaryViewCandidate(t, parser.OnCommitPreserve, "CREATE VIEW candidate_view AS SELECT v FROM pg_temp.candidate WHERE v>0")
	before := e.state
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		t.Fatal(err)
	}
	if e.state != before || op.session.temporaryNamespace != (ObjectAddress{}) || e.state.descriptors[view.address] != nil {
		t.Fatal("private namespace/view escaped validation")
	}
	candidate := cloneCatalogChanges(changes)
	e.mu.Lock()
	err := e.captureCatalogStageLocked(stage, candidate, e.privateCatalogLocked(changes).state, nil)
	if err == nil {
		e.stageCatalogChangesLocked(stage, candidate)
	}
	catalog := e.privateCatalogLocked(candidate)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	rowAddress := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(view.rowType)}
	row, ok := catalog.state.descriptors[rowAddress].(*typeDescriptor)
	if !ok || row.owningRelation != view.address || row.array == 0 {
		t.Fatal("missing real associated rowtype", row)
	}
	arrayAddress := ObjectAddress{ClassID: TypeClassID, ObjectID: OID(row.array)}
	if catalog.state.descriptors[arrayAddress] == nil || e.state.descriptors[rowAddress] != nil || e.state.descriptors[arrayAddress] != nil || e.storage[view.address] != nil {
		t.Fatal("type/storage publication escaped private stage")
	}
	removal := e.BeginCatalogStage(catalog)
	defer e.DiscardCatalogStage(removal)
	if err := removal.Remove(ObjectRef{owner: e.owner, Epoch: catalog.Epoch(), Address: view.address}); err != nil {
		t.Fatal(err)
	}
	retired := e.stageRead(removal)
	for _, address := range []ObjectAddress{view.address, rowAddress, arrayAddress} {
		if retired.state.descriptors[address] != nil {
			t.Fatal("drop left associated artifact", address)
		}
	}
	if catalog.state.descriptors[view.address] != view || e.state != before {
		t.Fatal("removal mutated predecessor")
	}
}
func TestTemporaryViewTerminalClosure(t *testing.T) {
	e, op, changes, table, stage, view := temporaryViewCandidate(t, parser.OnCommitDrop, "CREATE VIEW candidate_view AS SELECT v FROM pg_temp.candidate WHERE v>0")
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	err := e.captureCatalogStageLocked(stage, changes, e.privateCatalogLocked(changes).state, nil)
	if err == nil {
		e.stageCatalogChangesLocked(stage, changes)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	writes := map[ObjectAddress][]rowVersion{table.address: {{row: 1, version: 1, values: []sqltype.Value{sqltype.Null(table.columns[0].Usage.Type)}}}}
	_, _, next, err := e.prepareTemporaryCommit(op, TxRef{epoch: e.state.epoch}, writes, nil, changes)
	if err != nil {
		t.Fatal(err)
	}
	catalog := e.privateCatalogLocked(next)
	for _, d := range []Descriptor{table, view} {
		if catalog.state.descriptors[d.Address()] != nil {
			t.Fatal("ON COMMIT DROP missed view closure", d.Name())
		}
	}
	if changes.descriptors[view.address] != view || len(writes[table.address]) != 1 {
		t.Fatal("terminal preparation mutated original candidate")
	}
}
func TestTemporaryViewCancellationAtomicity(t *testing.T) {
	e, op, changes, _, stage, view := temporaryViewCandidate(t, parser.OnCommitPreserve, "CREATE VIEW candidate_view AS SELECT v FROM pg_temp.candidate WHERE v>0")
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	raw := &parser.CreateView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: view.name}}}, Persistence: "temporary"}
	if err := scope.gate(raw); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	e.mu.Lock()
	err = e.captureCatalogStageLocked(stage, changes, e.privateCatalogLocked(changes).state, nil)
	if err == nil {
		e.stageCatalogChangesLocked(stage, changes)
	}
	e.mu.Unlock()
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	before := e.state
	ctx, cancel := context.WithCancel(op.Context())
	cancel()
	op.ctx = ctx
	err = e.publishRows(op, scope.txRef, nil, nil, changes, nil)
	scope.Finish(err)
	if !errors.Is(err, context.Canceled) || e.state != before || e.state.descriptors[view.address] != nil || op.session.temporaryNamespace != (ObjectAddress{}) || e.storage[view.address] != nil {
		t.Fatal("canceled publication leaked TEMP artifacts", err)
	}
}

func TestTemporaryViewReplacementProvenance(t *testing.T) {
	e, _, changes, table, stage, view := temporaryViewCandidate(t, parser.OnCommitPreserve, "CREATE VIEW candidate_view AS SELECT v FROM pg_temp.candidate WHERE v>0")
	e.mu.Lock()
	err := e.captureCatalogStageLocked(stage, changes, e.privateCatalogLocked(changes).state, nil)
	if err == nil {
		e.stageCatalogChangesLocked(stage, changes)
	}
	before := e.privateCatalogLocked(changes)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next := cloneCatalog(before.state)
	next.epoch++
	retained := CatalogRead{e, next}
	ref := view.view.sources[0].ref
	if !storedViewReferenceValid(retained, view, ref) {
		t.Fatal("exact retained source rejected")
	}
	if err := e.ValidateObjectRef(retained, ref); !errors.Is(err, ErrCatalogReplaced) {
		t.Fatal("generic validator acquired contextual exemption", err)
	}
	fake := *view
	fake.address.ObjectID++
	if storedViewReferenceValid(retained, &fake, ref) || storedViewReferenceValid(New().CatalogRead(), view, ref) {
		t.Fatal("unretained/foreign consumer accepted")
	}
	for _, bad := range []ObjectRef{{owner: New().owner, Epoch: ref.Epoch, Address: ref.Address}, {owner: e.owner, Epoch: next.epoch + 1, Address: ref.Address}, {owner: e.owner, Epoch: ref.Epoch, Address: ObjectAddress{ClassID: RelationClassID, ObjectID: table.address.ObjectID, SubID: 99}}} {
		if storedViewReferenceValid(retained, view, bad) {
			t.Fatal("unrecorded/foreign/future ref accepted", bad)
		}
	}
	peerNamespace := *next.descriptors[table.namespace].(*namespaceDescriptor)
	peerNamespace.temporary.id++
	next.descriptors[table.namespace] = &peerNamespace
	if storedViewReferenceValid(retained, view, ref) {
		t.Fatal("peer namespace accepted")
	}
	next.descriptors[table.namespace] = before.state.descriptors[table.namespace]
	delete(next.descriptors, table.address)
	if storedViewReferenceValid(retained, view, ref) {
		t.Fatal("retired source resurrected")
	}
}

func TestTemporaryViewCatalogProviderProvenance(t *testing.T) {
	e, _, changes, _, stage, view := temporaryViewCandidate(t, parser.OnCommitPreserve, "CREATE VIEW candidate_view AS SELECT 11 AS v FROM pg_catalog.pg_class WHERE relname='candidate_view'")
	e.mu.Lock()
	err := e.captureCatalogStageLocked(stage, changes, e.privateCatalogLocked(changes).state, nil)
	if err == nil {
		e.stageCatalogChangesLocked(stage, changes)
	}
	before := e.privateCatalogLocked(changes)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next := cloneCatalog(before.state)
	next.epoch++
	retained := CatalogRead{e, next}
	ref := view.view.sources[0].ref
	if !storedViewReferenceValid(retained, view, ref) {
		t.Fatal("exact captured catalog provider rejected")
	}
	old := next.descriptors[ref.Address].(*relationDescriptor)
	impostor := *old
	next.descriptors[ref.Address] = &impostor
	if storedViewReferenceValid(retained, view, ref) {
		t.Fatal("same-address/kind/layout catalog copy accepted")
	}
	next.descriptors[ref.Address] = old
	fake := *view
	fake.view = &viewDefinition{consumer: view.address, temporary: view.view.temporary}
	next.descriptors[view.address] = &fake
	if storedViewReferenceValid(retained, &fake, ref) {
		t.Fatal("missing captured manifest accepted")
	}
	next.descriptors[view.address] = view
	if storedViewReferenceValid(New().CatalogRead(), view, ref) {
		t.Fatal("foreign provider authority accepted")
	}
}

func TestTemporaryViewDescribeNoEffects(t *testing.T) {
	e, op, _, _, stage, view := temporaryViewCandidate(t, parser.OnCommitPreserve, "CREATE VIEW candidate_view AS SELECT v FROM pg_temp.candidate WHERE v>0")
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	before := e.state
	raw := &parser.CreateView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "unpublished"}}}, Persistence: "temporary", Query: view.view.syntax}
	description, err := e.describeOperation(scope, raw)
	scope.Finish(err)
	if err != nil || description.Parameters.Count != 0 || len(description.Results) != 0 || e.state != before || op.session.temporaryNamespace != (ObjectAddress{}) || raw.Persistence != "temporary" || stage.base.state.descriptors[view.address] != nil {
		t.Fatal("utility Describe analyzed/published TEMP", description, err)
	}
}
