package schemacmds_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/typecmds"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestCompositeAlterDescriptorCandidate(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	ref := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), source.Address())
	action := parser.AlterTableAction{Action: parser.AlterColumnType, Column: parser.Identifier{Text: "note"}}
	usage := enginetest.Usage(e, sqltype.Int4)
	e.Mu.Lock()
	prepared, err := tablecmds.PrepareAlterTypeLocked(e, nil, op, auth, source, ref, action, &usage)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next, transform, err := tablecmds.AlterTypeCandidate(e, nil, op, auth, stage, source, source, ref, action, prepared)
	if err != nil || next.Columns[1].Usage.Type.ID() != sqltype.Int4 || transform.Expression != nil || source.Columns[1].Usage.Type.ID() != sqltype.Text {
		t.Fatal("descriptor-only conversion executed assignment or changed capture", err)
	}
	actions, _ := typedef.TypedShapeActions(source, source, table, map[int32]bool{source.Attributes[0]: true})
	if len(actions) != 1 || actions[0].Action.Action != parser.AlterColumnType || actions[0].Action.Column.Text != "id" {
		t.Fatal("identity TYPE propagation lost", actions)
	}
}

func TestCompositeAlterCancellationAndReplacement(t *testing.T) {
	for _, kind := range []string{"cancel", "replacement"} {
		t.Run(kind, func(t *testing.T) {
			e, source, table := typedUnitCatalog(t, false)
			before := e.CatalogRead()
			stage := typedRenameStage(t, e, source, table)
			defer e.DiscardCatalogStage(stage)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			provider := e.Catalog.Providers[ids.RelationClassID]
			calls := 0
			e.Catalog.Providers[ids.RelationClassID] = alterDropProvider{provider, func() {
				calls++
				e.Catalog.Providers[ids.RelationClassID] = provider
				if !e.Mu.TryLock() {
					t.Fatal("provider called under engine lock")
				}
				e.Mu.Unlock()
				if kind == "cancel" {
					cancel()
				} else if err := engine.Reset(context.Background(), e); err != nil {
					t.Fatal(err)
				}
			}}
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err == nil {
				_, err = enginetest.PublishCatalog(e, prepared, core.PublicationToken{Epoch: before.Epoch()})
			}
			if calls == 0 || err == nil {
				t.Fatal("provider phase did not reject publication", calls, err)
			}
			if kind == "cancel" && (!errors.Is(err, context.Canceled) || e.CatalogRead().State() != before.State()) {
				t.Fatal("canceled candidate published", err)
			}
			if source.Columns[1].Name != "note" || table.Columns[1].Name != "note" {
				t.Fatal("retained roots mutated")
			}
		})
	}
}

// Exercise catalog constructors and publication, not just a hand-built domain.
// A lookup-only associated array used to fail the holder's own-row constructor.
func TestCompositeDomainArrayGenerationOwnership(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.wrapped AS public.pair"))
	before := e.CatalogRead()
	domain := domainUnitType(t, before, "wrapped")
	array, ok := before.LookupArray(domain.SQLType)
	if !ok || !array.Concrete() || array.Element() != domain.SQLType || !array.Usage().Valid() {
		t.Fatal("domain did not publish an executable reciprocal array", array)
	}
	holder := domainUnitTable(t, e, op, "CREATE TABLE public.domain_holder(v public.wrapped[])")
	if holder.Columns[0].Usage.Type != array {
		t.Fatal("holder borrowed a different domain-array generation")
	}
	original, err := array.Input(context.Background(), `[0:2]={"(7,x)",NULL,"(9,y)"}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, typedRenameStage(t, e, source, table))
	evolved := e.CatalogRead()
	destination := engine.New()
	if _, err := destination.ReplaceCatalog(context.Background(), e, evolved); err != nil {
		t.Fatal(err)
	}
	var evolvedArray *sqltype.Descriptor
	for _, view := range []catalog.Read{evolved, destination.CatalogRead()} {
		row, ok := view.LookupType(source.RowType)
		if !ok || row.CompositeMember(1).Name != "label" || row.CompositeMember(1).Attribute != source.Attributes[1] {
			t.Fatal("composite lost renamed stable member")
		}
		wrapped := domainUnitType(t, view, "wrapped")
		current, ok := view.LookupArray(wrapped.SQLType)
		if !ok || !current.Concrete() || current.Element() != wrapped.SQLType || wrapped.SQLType.DomainBase().Type != row || current.ID() != array.ID() || wrapped.SQLType.ID() != domain.SQLType.ID() {
			t.Fatal("domain-array generation lost identity or reciprocity", current)
		}
		owned := view.MustRelation(holder.Address())
		ownRow, ok := view.LookupType(owned.RowType)
		if !ok || owned.Columns[0].Usage.Type != current || ownRow.CompositeMember(0).Usage.Type != current {
			t.Fatal("holder and own-row member borrowed stale domain-array codec")
		}
		if current == array || wrapped.SQLType == domain.SQLType || row == domain.SQLType.DomainBase().Type {
			t.Fatal("new generation reused retained source codecs")
		}
		if evolvedArray == nil {
			evolvedArray = current
		} else if current == evolvedArray {
			t.Fatal("replacement borrowed foreign array codec")
		}
		mapped, err := typedef.ReownDomainDatum(original, current, nil, nil, nil)
		if err != nil {
			t.Fatal("domain-array payload reownership failed", err)
		}
		text, err := current.Output(context.Background(), mapped)
		if err != nil || text != `[0:2]={"(7,x)",NULL,"(9,y)"}` {
			t.Fatal("domain-array mapping changed bounds, NULLs or fields", text, err)
		}
		_, err = current.Input(context.Background(), `{"(bad,x)"}`, -1)
		domainUnitDiagnostic(t, err, "22P02", `invalid input syntax for type integer: "bad"`)
	}
	if domain.SQLType.DomainBase().Type.CompositeMember(1).Name != "note" || array.Element() != domain.SQLType || holder.Columns[0].Usage.Type != array || source.Columns[1].Name != "note" {
		t.Fatal("generation reownership mutated retained catalog roots")
	}
	text, err := array.Output(context.Background(), original)
	if err != nil || text != `[0:2]={"(7,x)",NULL,"(9,y)"}` {
		t.Fatal("generation reownership mutated retained payload", text, err)
	}
}

// Package fixtures construct providers, not SQL sessions or public-driver mocks.
func typedUnitCatalog(t *testing.T, empty bool) (*core.Engine, *catalog.Relation, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	defer e.DiscardCatalogStage(stage)
	var columns []catalog.Column
	if !empty {
		columns = []catalog.Column{{Name: "id", Usage: enginetest.Usage(e, sqltype.Int4), Nullable: true, NullabilityKnown: true}, {Name: "note", Usage: enginetest.Usage(e, sqltype.Text), Nullable: true, NullabilityKnown: true}}
	}
	index := make(map[string]int, len(columns))
	for i, column := range columns {
		index[column.Name] = i
	}
	e.Mu.Lock()
	source, err := table.NewRelation(core.DDLHost{E: e}, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, "pair", auth.State.CurrentUser, append([]catalog.Column(nil), columns...), index)
	if err == nil {
		source.RelationKind = catalog.CompositeRelation
		if !empty {
			source.Attributes = []int32{2, 4}
			source.ColumnMeta.HighWater = 4
			source.Columns[0].OriginAttribute = 2
			source.Columns[1].OriginAttribute = 4
		}
		err = e.StageRowTypeLocked(stage, source)
	}
	var tbl *catalog.Relation
	if err == nil {
		tbl, err = table.NewRelation(core.DDLHost{E: e}, source.Namespace(), "typed", source.Owner, append([]catalog.Column(nil), columns...), index)
	}
	if err == nil {
		tbl.OfType = ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.RowType)}
		if !empty {
			tbl.Attributes = []int32{1, 7}
			tbl.ColumnMeta.HighWater = 7
			tbl.Columns[1].OriginAttribute = 7
		}
		err = e.StageRowTypeLocked(stage, tbl)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(source); err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(tbl); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	view := e.CatalogRead()
	return e, view.MustRelation(source.Address()), view.MustRelation(tbl.Address())
}

func typedRenameStage(t *testing.T, e *core.Engine, source, tbl *catalog.Relation) *core.CatalogStage {
	t.Helper()
	stage := e.BeginCatalogStage(e.CatalogRead())
	typecmds.TypedStage(stage).Source = tbl.OfType
	typecmds.TypedStage(stage).Observed = table.TypedClosure(stage.Base, tbl.OfType)
	for _, old := range []*catalog.Relation{source, tbl} {
		next, err := table.RenameColumnCandidate(old, "note", "label")
		if err != nil {
			t.Fatal(err)
		}
		next.ObjectRevision++
		if err := stage.Replace(next); err != nil {
			t.Fatal(err)
		}
	}
	return stage
}

type alterDropProvider struct {
	catalog.Provider
	callback func()
}

func (p alterDropProvider) Validate(v catalog.Read, d catalog.Descriptor) error {
	if err := p.Provider.Validate(v, d); err != nil {
		return err
	}
	p.callback()
	return nil
}
