package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCompositeAlterDescriptorCandidate(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.catalog)
	defer e.DiscardCatalogStage(stage)
	ref := e.catalog.domain.Ref(auth.catalog.Epoch(), source.address)
	action := parser.AlterTableAction{Action: parser.AlterColumnType, Column: parser.Identifier{Text: "note"}}
	usage := testUsage(e, sqltype.Int4)
	e.mu.Lock()
	prepared, err := e.prepareAlterTypeLocked(nil, op, auth, source, ref, action, &usage)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	next, transform, err := e.alterTypeCandidate(nil, op, auth, stage, source, source, ref, action, prepared)
	if err != nil || next.columns[1].Usage.Type.ID() != sqltype.Int4 || transform.expression != nil || source.columns[1].Usage.Type.ID() != sqltype.Text {
		t.Fatal("descriptor-only conversion executed assignment or changed capture", err)
	}
	actions, _ := typedShapeActions(source, source, table, map[int32]bool{source.attributes[0]: true})
	if len(actions) != 1 || actions[0].action.Action != parser.AlterColumnType || actions[0].action.Column.Text != "id" {
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
			provider := e.catalog.providers[ids.RelationClassID]
			calls := 0
			e.catalog.providers[ids.RelationClassID] = alterDropProvider{provider, func() {
				calls++
				e.catalog.providers[ids.RelationClassID] = provider
				if !e.mu.TryLock() {
					t.Fatal("provider called under engine lock")
				}
				e.mu.Unlock()
				if kind == "cancel" {
					cancel()
				} else if err := e.Reset(context.Background()); err != nil {
					t.Fatal(err)
				}
			}}
			prepared, err := e.ValidateCatalogStage(ctx, stage)
			if err == nil {
				_, err = e.PublishCatalog(prepared, PublicationToken{Epoch: before.Epoch()})
			}
			if calls == 0 || err == nil {
				t.Fatal("provider phase did not reject publication", calls, err)
			}
			if kind == "cancel" && (!errors.Is(err, context.Canceled) || e.CatalogRead().state != before.state) {
				t.Fatal("canceled candidate published", err)
			}
			if source.columns[1].Name != "note" || table.columns[1].Name != "note" {
				t.Fatal("retained roots mutated")
			}
		})
	}
}

func TestCompositeArrayStorageAdmission(t *testing.T) {
	e, source, _ := typedUnitCatalog(t, false)
	view := e.CatalogRead()
	row, ok := view.LookupType(source.rowType)
	if !ok {
		t.Fatal("missing composite codec")
	}
	array, ok := view.LookupArray(row)
	if !ok || !array.Concrete() || !array.Usage().Valid() {
		t.Fatal("missing executable reciprocal array")
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	usage, err := e.resolveType(parser.TypeName{Name: "pair", ArrayBounds: []int32{-1}}, op)
	if err != nil || usage.Type != array || !usage.Valid() {
		t.Fatal("real column Usage rejected concrete composite array", usage, err)
	}
	domain, err := sqltype.Domain(60000, "wrapped", "public.wrapped", row.Usage())
	if err != nil {
		t.Fatal(err)
	}
	domainArray, err := sqltype.Array(60001, "_wrapped", domain)
	if err != nil {
		t.Fatal(err)
	}
	whole, err := sqltype.Domain(60002, "whole", "public.whole", array.Usage())
	if err != nil {
		t.Fatal(err)
	}
	nested, err := sqltype.Composite(60003, "nested", "public.nested", 60004, []sqltype.CompositeMember{
		{Attribute: 1, Name: "r", Usage: domain.Usage()},
		{Attribute: 2, Name: "a", Usage: array.Usage()},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []*sqltype.Descriptor{row, domain, domainArray, whole, nested} {
		if !storedArrayElement(typ) {
			t.Fatalf("executable array element %s rejected", typ.Name())
		}
	}
	lookup := sqltype.LookupOnly(60005, "catalog_row", 60006)
	lookupArray := sqltype.LookupArray(60006, "_catalog_row", lookup)
	lookupDomain := sqltype.LookupDomain(60007, "lookup_domain", "public.lookup_domain", row.Usage())
	for _, typ := range []*sqltype.Descriptor{nil, lookup, lookupArray, lookupDomain, testUsage(e, sqltype.Inet).Type, testUsage(e, sqltype.Xid).Type} {
		if storedArrayElement(typ) {
			t.Fatal("unsupported array element admitted", typ)
		}
	}
	for _, id := range []sqltype.ID{sqltype.Int4, sqltype.Text, sqltype.Regclass} {
		if !storedArrayElement(testUsage(e, id).Type) {
			t.Fatal("incumbent stored scalar array rejected", id)
		}
	}
}

// Exercise catalog constructors and publication, not just a hand-built domain.
// A lookup-only associated array used to fail the holder's own-row constructor.
func TestCompositeDomainArrayGenerationOwnership(t *testing.T) {
	e, source, table := typedUnitCatalog(t, false)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.wrapped AS public.pair"))
	before := e.CatalogRead()
	domain := domainUnitType(t, before, "wrapped")
	array, ok := before.LookupArray(domain.typ)
	if !ok || !array.Concrete() || array.Element() != domain.typ || !array.Usage().Valid() {
		t.Fatal("domain did not publish an executable reciprocal array", array)
	}
	holder := domainUnitTable(t, e, op, "CREATE TABLE public.domain_holder(v public.wrapped[])")
	if holder.columns[0].Usage.Type != array {
		t.Fatal("holder borrowed a different domain-array generation")
	}
	original, err := array.Input(context.Background(), `[0:2]={"(7,x)",NULL,"(9,y)"}`, -1)
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, typedRenameStage(t, e, source, table))
	evolved := e.CatalogRead()
	destination := New()
	if _, err := destination.ReplaceCatalog(context.Background(), e, evolved); err != nil {
		t.Fatal(err)
	}
	var evolvedArray *sqltype.Descriptor
	for _, view := range []CatalogRead{evolved, destination.CatalogRead()} {
		row, ok := view.LookupType(source.rowType)
		if !ok || row.CompositeMember(1).Name != "label" || row.CompositeMember(1).Attribute != source.attributes[1] {
			t.Fatal("composite lost renamed stable member")
		}
		wrapped := domainUnitType(t, view, "wrapped")
		current, ok := view.LookupArray(wrapped.typ)
		if !ok || !current.Concrete() || current.Element() != wrapped.typ || wrapped.typ.DomainBase().Type != row || current.ID() != array.ID() || wrapped.typ.ID() != domain.typ.ID() {
			t.Fatal("domain-array generation lost identity or reciprocity", current)
		}
		owned := view.MustRelation(holder.address)
		ownRow, ok := view.LookupType(owned.rowType)
		if !ok || owned.columns[0].Usage.Type != current || ownRow.CompositeMember(0).Usage.Type != current {
			t.Fatal("holder and own-row member borrowed stale domain-array codec")
		}
		if current == array || wrapped.typ == domain.typ || row == domain.typ.DomainBase().Type {
			t.Fatal("new generation reused retained source codecs")
		}
		if evolvedArray == nil {
			evolvedArray = current
		} else if current == evolvedArray {
			t.Fatal("replacement borrowed foreign array codec")
		}
		mapped, err := reownDomainDatum(original, current, nil, nil, nil)
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
	if domain.typ.DomainBase().Type.CompositeMember(1).Name != "note" || array.Element() != domain.typ || holder.columns[0].Usage.Type != array || source.columns[1].Name != "note" {
		t.Fatal("generation reownership mutated retained catalog roots")
	}
	text, err := array.Output(context.Background(), original)
	if err != nil || text != `[0:2]={"(7,x)",NULL,"(9,y)"}` {
		t.Fatal("generation reownership mutated retained payload", text, err)
	}
}
