package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Bind a real query against genuine private TEMP descriptors, then exercise the
// catalog-stage functions directly. No public SQL execution is duplicated here.
func temporaryViewCandidate(t *testing.T, action parser.OnCommitAction, query string) (*Engine, *session.Operation, *session.Journal, *catalog.Relation, *CatalogStage, *catalog.Relation) {
	t.Helper()
	e, op, changes, table := temporaryCatalogCandidate(t, action)
	raw := statement(t, query).(*parser.CreateView)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	auth.Catalog = e.PrivateCatalogLocked(changes)
	auth.Recapture(auth.Catalog)
	auth.StagedTemporary = table.Namespace()
	b, err := Describe(op.Context(), e.AnalysisHost(nil), raw, auth, ids.ObjectRef{}, sqltype.InputEnvironment{}, nil, nil)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	definition, err := view.FreezeDefinition(DDLHost{E: e}, auth.Catalog, CatalogSourceTemporaryOwner(auth), raw.Query, b, false, false, 20008, catalog.ViewRelation, true)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	d := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20007}, ObjectNamespace: table.Namespace(), ObjectName: "candidate_view", ObjectRevision: 1, Owner: table.Owner, RelationKind: catalog.ViewRelation, View: stored.ViewPayload(definition), Columns: append([]catalog.Column(nil), b.Query.Root.Schema()...), Index: map[string]int{"v": 0}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1}}
	definition = definition.With(func(p *stored.ViewParts) { p.Consumer = d.Address() })
	d.View = stored.ViewPayload(definition)
	stage := e.BeginCatalogStage(auth.Catalog)
	err = e.StageRowTypeLocked(stage, d)
	e.Mu.Unlock()
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
	before := e.Catalog.State
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		t.Fatal(err)
	}
	if e.Catalog.State != before || op.Session.TemporaryNamespace != (ids.ObjectAddress{}) || e.Catalog.State.Get(view.Address()) != nil {
		t.Fatal("private namespace/view escaped validation")
	}
	candidate := changes.Clone()
	e.Mu.Lock()
	err := EngineCaptureCatalogStageLocked(e, stage, candidate, e.PrivateCatalogLocked(changes).State(), nil)
	if err == nil {
		EngineStageCatalogChangesLocked(e, stage, candidate)
	}
	catalog := e.PrivateCatalogLocked(candidate)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	rowAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(view.RowType)}
	row, ok := catalog.Type(rowAddress)
	if !ok || row.OwningRelation != view.Address() || row.Array == 0 {
		t.Fatal("missing real associated rowtype", row)
	}
	arrayAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(row.Array)}
	if catalog.Get(arrayAddress) == nil || e.Catalog.State.Get(rowAddress) != nil || e.Catalog.State.Get(arrayAddress) != nil || e.Storage.Relations[view.Address()] != nil {
		t.Fatal("type/storage publication escaped private stage")
	}
	removal := e.BeginCatalogStage(catalog)
	defer e.DiscardCatalogStage(removal)
	if err := removal.Remove(e.Catalog.Domain.Ref(catalog.Epoch(), view.Address())); err != nil {
		t.Fatal(err)
	}
	retired := e.StageRead(removal)
	for _, address := range []ids.ObjectAddress{view.Address(), rowAddress, arrayAddress} {
		if retired.Get(address) != nil {
			t.Fatal("drop left associated artifact", address)
		}
	}
	if catalog.Get(view.Address()) != view || e.Catalog.State != before {
		t.Fatal("removal mutated predecessor")
	}
}
func TestTemporaryViewTerminalClosure(t *testing.T) {
	e, op, changes, table, stage, view := temporaryViewCandidate(t, parser.OnCommitDrop, "CREATE VIEW candidate_view AS SELECT v FROM pg_temp.candidate WHERE v>0")
	if _, err := e.ValidateCatalogStage(op.Context(), stage); err != nil {
		t.Fatal(err)
	}
	e.Mu.Lock()
	err := EngineCaptureCatalogStageLocked(e, stage, changes, e.PrivateCatalogLocked(changes).State(), nil)
	if err == nil {
		EngineStageCatalogChangesLocked(e, stage, changes)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	writes := map[ids.ObjectAddress][]storage.RowVersion{table.Address(): {{Row: 1, Version: 1, Values: []sqltype.Value{sqltype.Null(table.Columns[0].Usage.Type)}}}}
	_, _, next, err := EnginePrepareTemporaryCommit(e, op, (*ids.Domain)(nil).Tx(ids.SessionToken{}, 0, e.Catalog.State.Epoch(), nil, 0), writes, nil, changes)
	if err != nil {
		t.Fatal(err)
	}
	catalogView := e.PrivateCatalogLocked(next)
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
	if err := scope.Gate(raw); err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	e.Mu.Lock()
	err = EngineCaptureCatalogStageLocked(e, stage, changes, e.PrivateCatalogLocked(changes).State(), nil)
	if err == nil {
		EngineStageCatalogChangesLocked(e, stage, changes)
	}
	e.Mu.Unlock()
	if err != nil {
		scope.Finish(err)
		t.Fatal(err)
	}
	before := e.Catalog.State
	ctx, cancel := context.WithCancel(op.Context())
	cancel()
	op.Ctx = ctx
	err = EnginePublishRows(e, op, scope.TxRef, nil, nil, changes, nil, nil)
	scope.Finish(err)
	if !errors.Is(err, context.Canceled) || e.Catalog.State != before || e.Catalog.State.Get(view.Address()) != nil || op.Session.TemporaryNamespace != (ids.ObjectAddress{}) || e.Storage.Relations[view.Address()] != nil {
		t.Fatal("canceled publication leaked TEMP artifacts", err)
	}
}

func TestTemporaryViewReplacementProvenance(t *testing.T) {
	e, _, changes, table, stage, view := temporaryViewCandidate(t, parser.OnCommitPreserve, "CREATE VIEW candidate_view AS SELECT v FROM pg_temp.candidate WHERE v>0")
	e.Mu.Lock()
	err := EngineCaptureCatalogStageLocked(e, stage, changes, e.PrivateCatalogLocked(changes).State(), nil)
	if err == nil {
		EngineStageCatalogChangesLocked(e, stage, changes)
	}
	before := e.PrivateCatalogLocked(changes)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next := catalog.CloneState(before.State())
	next.SetEpoch(next.Epoch() + 1)
	retained := e.Catalog.Read(next)
	ref := stored.ViewOf(view).Sources()[0].Ref
	if !stored.ViewReferenceValid(retained, view, ref) {
		t.Fatal("exact retained source rejected")
	}
	if err := e.ValidateObjectRef(retained, ref); !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatal("generic validator acquired contextual exemption", err)
	}
	fake := *view
	fake.ObjectAddress.ObjectID++
	if stored.ViewReferenceValid(retained, &fake, ref) || stored.ViewReferenceValid(New().CatalogRead(), view, ref) {
		t.Fatal("unretained/foreign consumer accepted")
	}
	for _, bad := range []ids.ObjectRef{New().Catalog.Domain.Ref(ref.Epoch, ref.Address), e.Catalog.Domain.Ref(next.Epoch()+1, ref.Address), e.Catalog.Domain.Ref(ref.Epoch, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: table.Address().ObjectID, SubID: 99})} {
		if stored.ViewReferenceValid(retained, view, bad) {
			t.Fatal("unrecorded/foreign/future ref accepted", bad)
		}
	}
	peerNamespace := *next.MustNamespace(table.Namespace())
	peerNamespace.Temporary = withSessionID(peerNamespace.Temporary, peerNamespace.Temporary.ID()+1)
	next.SetDescriptor(table.Namespace(), &peerNamespace)
	if stored.ViewReferenceValid(retained, view, ref) {
		t.Fatal("peer namespace accepted")
	}
	next.SetDescriptor(table.Namespace(), before.Get(table.Namespace()))
	next.RemoveDescriptor(table.Address())
	if stored.ViewReferenceValid(retained, view, ref) {
		t.Fatal("retired source resurrected")
	}
}

func TestTemporaryViewCatalogProviderProvenance(t *testing.T) {
	e, _, changes, _, stage, view := temporaryViewCandidate(t, parser.OnCommitPreserve, "CREATE VIEW candidate_view AS SELECT 11 AS v FROM pg_catalog.pg_class WHERE relname='candidate_view'")
	e.Mu.Lock()
	err := EngineCaptureCatalogStageLocked(e, stage, changes, e.PrivateCatalogLocked(changes).State(), nil)
	if err == nil {
		EngineStageCatalogChangesLocked(e, stage, changes)
	}
	before := e.PrivateCatalogLocked(changes)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next := catalog.CloneState(before.State())
	next.SetEpoch(next.Epoch() + 1)
	retained := e.Catalog.Read(next)
	ref := stored.ViewOf(view).Sources()[0].Ref
	if !stored.ViewReferenceValid(retained, view, ref) {
		t.Fatal("exact captured catalog provider rejected")
	}
	old := next.MustRelation(ref.Address)
	impostor := *old
	next.SetDescriptor(ref.Address, &impostor)
	if stored.ViewReferenceValid(retained, view, ref) {
		t.Fatal("same-address/kind/layout catalog copy accepted")
	}
	next.SetDescriptor(ref.Address, old)
	fake := *view
	fake.View = stored.NewView(stored.ViewParts{Consumer: view.Address(), Temporary: stored.ViewOf(view).Temporary()})
	next.SetDescriptor(view.Address(), &fake)
	if stored.ViewReferenceValid(retained, &fake, ref) {
		t.Fatal("missing captured manifest accepted")
	}
	next.SetDescriptor(view.Address(), view)
	if stored.ViewReferenceValid(New().CatalogRead(), view, ref) {
		t.Fatal("foreign provider authority accepted")
	}
}

func TestTemporaryViewDescribeNoEffects(t *testing.T) {
	e, op, _, _, stage, view := temporaryViewCandidate(t, parser.OnCommitPreserve, "CREATE VIEW candidate_view AS SELECT v FROM pg_temp.candidate WHERE v>0")
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	before := e.Catalog.State
	raw := &parser.CreateView{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "unpublished"}}}, Persistence: "temporary", Query: stored.ViewOf(view).Syntax()}
	description, err := EngineDescribeOperation(e, scope, raw)
	scope.Finish(err)
	if err != nil || description.Parameters.Count != 0 || len(description.Results) != 0 || e.Catalog.State != before || op.Session.TemporaryNamespace != (ids.ObjectAddress{}) || raw.Persistence != "temporary" || stage.Base.Get(view.Address()) != nil {
		t.Fatal("utility Describe analyzed/published TEMP", description, err)
	}
}
