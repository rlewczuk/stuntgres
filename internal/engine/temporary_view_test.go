package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

// Bind a real query against genuine private TEMP descriptors, then exercise the
// catalog-stage functions directly. No public SQL execution is duplicated here.
func temporaryViewCandidate(t *testing.T, action parser.OnCommitAction, query string) (*Engine, *session.Operation, *session.Journal, *catalog.Relation, *CatalogStage, *catalog.Relation) {
	t.Helper()
	e, op, changes, table := temporaryCatalogCandidate(t, action)
	raw := statement(t, query).(*parser.CreateView)
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	auth.Catalog = e.privateCatalogLocked(changes)
	auth.Recapture(auth.Catalog)
	auth.stagedTemporary = table.Namespace()
	b, err := describe(op.Context(), e.analysisHost(nil), raw, auth, ids.ObjectRef{}, sqltype.InputEnvironment{}, nil, nil)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	definition, err := e.freezeViewDefinitionLocked(auth, raw.Query, b, false, false, 20008, catalog.ViewRelation, true)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20007}, ObjectNamespace: table.Namespace(), ObjectName: "candidate_view", ObjectRevision: 1, Owner: table.Owner, RelationKind: catalog.ViewRelation, View: viewPayloadOf(definition), Columns: append([]catalog.Column(nil), b.Query.Root.Schema()...), Index: map[string]int{"v": 0}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
	definition.consumer = d.Address()
	stage := e.BeginCatalogStage(auth.Catalog)
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
	before := e.catalog.state
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		t.Fatal(err)
	}
	if e.catalog.state != before || op.Session.TemporaryNamespace != (ids.ObjectAddress{}) || e.catalog.state.Get(view.Address()) != nil {
		t.Fatal("private namespace/view escaped validation")
	}
	candidate := changes.Clone()
	e.mu.Lock()
	err := e.captureCatalogStageLocked(stage, candidate, e.privateCatalogLocked(changes).State(), nil)
	if err == nil {
		e.stageCatalogChangesLocked(stage, candidate)
	}
	catalog := e.privateCatalogLocked(candidate)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	rowAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(view.RowType)}
	row, ok := catalog.Type(rowAddress)
	if !ok || row.OwningRelation != view.Address() || row.Array == 0 {
		t.Fatal("missing real associated rowtype", row)
	}
	arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)}
	if catalog.Get(arrayAddress) == nil || e.catalog.state.Get(rowAddress) != nil || e.catalog.state.Get(arrayAddress) != nil || e.storage.Relations[view.Address()] != nil {
		t.Fatal("type/storage publication escaped private stage")
	}
	removal := e.BeginCatalogStage(catalog)
	defer e.DiscardCatalogStage(removal)
	if err := removal.Remove(e.catalog.Domain.Ref(catalog.Epoch(), view.Address())); err != nil {
		t.Fatal(err)
	}
	retired := e.stageRead(removal)
	for _, address := range []ids.ObjectAddress{view.Address(), rowAddress, arrayAddress} {
		if retired.Get(address) != nil {
			t.Fatal("drop left associated artifact", address)
		}
	}
	if catalog.Get(view.Address()) != view || e.catalog.state != before {
		t.Fatal("removal mutated predecessor")
	}
}
func TestTemporaryViewTerminalClosure(t *testing.T) {
	e, op, changes, table, stage, view := temporaryViewCandidate(t, parser.OnCommitDrop, "CREATE VIEW candidate_view AS SELECT v FROM pg_temp.candidate WHERE v>0")
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	err := e.captureCatalogStageLocked(stage, changes, e.privateCatalogLocked(changes).State(), nil)
	if err == nil {
		e.stageCatalogChangesLocked(stage, changes)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	writes := map[ids.ObjectAddress][]storage.RowVersion{table.Address(): {{Row: 1, Version: 1, Values: []sqltype.Value{sqltype.Null(table.Columns[0].Usage.Type)}}}}
	_, _, next, err := e.prepareTemporaryCommit(op, (*ids.Domain)(nil).Tx(ids.SessionToken{}, 0, e.catalog.state.Epoch(), nil, 0), writes, nil, changes)
	if err != nil {
		t.Fatal(err)
	}
	catalogView := e.privateCatalogLocked(next)
	for _, d := range []catalog.Descriptor{table, view} {
		if catalogView.Get(d.Address()) != nil {
			t.Fatal("ON COMMIT DROP missed view closure", d.Name())
		}
	}
	if changes.Descriptors[view.Address()] != view || len(writes[table.Address()]) != 1 {
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
	raw := &parser.CreateView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: view.Name()}}}, Persistence: "temporary"}
	if err := scope.gate(raw); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	e.mu.Lock()
	err = e.captureCatalogStageLocked(stage, changes, e.privateCatalogLocked(changes).State(), nil)
	if err == nil {
		e.stageCatalogChangesLocked(stage, changes)
	}
	e.mu.Unlock()
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	before := e.catalog.state
	ctx, cancel := context.WithCancel(op.Context())
	cancel()
	op.Ctx = ctx
	err = e.publishRows(op, scope.txRef, nil, nil, changes, nil, nil)
	scope.Finish(err)
	if !errors.Is(err, context.Canceled) || e.catalog.state != before || e.catalog.state.Get(view.Address()) != nil || op.Session.TemporaryNamespace != (ids.ObjectAddress{}) || e.storage.Relations[view.Address()] != nil {
		t.Fatal("canceled publication leaked TEMP artifacts", err)
	}
}

func TestTemporaryViewReplacementProvenance(t *testing.T) {
	e, _, changes, table, stage, view := temporaryViewCandidate(t, parser.OnCommitPreserve, "CREATE VIEW candidate_view AS SELECT v FROM pg_temp.candidate WHERE v>0")
	e.mu.Lock()
	err := e.captureCatalogStageLocked(stage, changes, e.privateCatalogLocked(changes).State(), nil)
	if err == nil {
		e.stageCatalogChangesLocked(stage, changes)
	}
	before := e.privateCatalogLocked(changes)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next := catalog.CloneState(before.State())
	next.SetEpoch(next.Epoch() + 1)
	retained := e.catalog.read(next)
	ref := viewOf(view).sources[0].ref
	if !storedViewReferenceValid(retained, view, ref) {
		t.Fatal("exact retained source rejected")
	}
	if err := e.ValidateObjectRef(retained, ref); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("generic validator acquired contextual exemption", err)
	}
	fake := *view
	fake.ObjectAddress.ObjectID++
	if storedViewReferenceValid(retained, &fake, ref) || storedViewReferenceValid(New().CatalogRead(), view, ref) {
		t.Fatal("unretained/foreign consumer accepted")
	}
	for _, bad := range []ids.ObjectRef{New().catalog.Domain.Ref(ref.Epoch, ref.Address), e.catalog.Domain.Ref(next.Epoch()+1, ref.Address), e.catalog.Domain.Ref(ref.Epoch, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: table.Address().ObjectID, SubID: 99})} {
		if storedViewReferenceValid(retained, view, bad) {
			t.Fatal("unrecorded/foreign/future ref accepted", bad)
		}
	}
	peerNamespace := *next.MustNamespace(table.Namespace())
	peerNamespace.Temporary = withSessionID(peerNamespace.Temporary, peerNamespace.Temporary.ID()+1)
	next.SetDescriptor(table.Namespace(), &peerNamespace)
	if storedViewReferenceValid(retained, view, ref) {
		t.Fatal("peer namespace accepted")
	}
	next.SetDescriptor(table.Namespace(), before.Get(table.Namespace()))
	next.RemoveDescriptor(table.Address())
	if storedViewReferenceValid(retained, view, ref) {
		t.Fatal("retired source resurrected")
	}
}

func TestTemporaryViewCatalogProviderProvenance(t *testing.T) {
	e, _, changes, _, stage, view := temporaryViewCandidate(t, parser.OnCommitPreserve, "CREATE VIEW candidate_view AS SELECT 11 AS v FROM pg_catalog.pg_class WHERE relname='candidate_view'")
	e.mu.Lock()
	err := e.captureCatalogStageLocked(stage, changes, e.privateCatalogLocked(changes).State(), nil)
	if err == nil {
		e.stageCatalogChangesLocked(stage, changes)
	}
	before := e.privateCatalogLocked(changes)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next := catalog.CloneState(before.State())
	next.SetEpoch(next.Epoch() + 1)
	retained := e.catalog.read(next)
	ref := viewOf(view).sources[0].ref
	if !storedViewReferenceValid(retained, view, ref) {
		t.Fatal("exact captured catalog provider rejected")
	}
	old := next.MustRelation(ref.Address)
	impostor := *old
	next.SetDescriptor(ref.Address, &impostor)
	if storedViewReferenceValid(retained, view, ref) {
		t.Fatal("same-address/kind/layout catalog copy accepted")
	}
	next.SetDescriptor(ref.Address, old)
	fake := *view
	fake.View = &viewDefinition{consumer: view.Address(), temporary: viewOf(view).temporary}
	next.SetDescriptor(view.Address(), &fake)
	if storedViewReferenceValid(retained, &fake, ref) {
		t.Fatal("missing captured manifest accepted")
	}
	next.SetDescriptor(view.Address(), view)
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
	before := e.catalog.state
	raw := &parser.CreateView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "unpublished"}}}, Persistence: "temporary", Query: viewOf(view).syntax}
	description, err := e.describeOperation(scope, raw)
	scope.Finish(err)
	if err != nil || description.Parameters.Count != 0 || len(description.Results) != 0 || e.catalog.state != before || op.Session.TemporaryNamespace != (ids.ObjectAddress{}) || raw.Persistence != "temporary" || stage.Base.Get(view.Address()) != nil {
		t.Fatal("utility Describe analyzed/published TEMP", description, err)
	}
}
