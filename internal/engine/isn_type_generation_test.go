package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func TestIsnTypeGenerationIdentity(t *testing.T) {
	e, _, base := isnUnitInstalled(t)
	op, done := roleOperation(t, e, "postgres")
	defer done()
	table := domainUnitTable(t, e, op, "CREATE TABLE public.isn_generation(v public.ean13)")
	before := e.CatalogRead()
	value, err := base.SQLType.Input(context.Background(), "1234567890120!", -1)
	if err != nil {
		t.Fatal(err)
	}
	original := &storage.Relation{Generation: 1, Versions: []storage.RowVersion{
		{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value}},
		{Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{sqltype.Null(base.SQLType)}},
	}}
	e.mu.Lock()
	e.storage.InstallRelationLocked(table.Address(), original)
	e.visibility.SetCommitSeqLocked(1)
	e.mu.Unlock()

	// Exercise the private generation seam directly, without executing SQL.
	next := *base
	next.SQLType, err = sqltype.Ean13(base.SQLType.ID(), base.Name(), base.SQLType.SQLName())
	if err != nil {
		t.Fatal(err)
	}
	next.ObjectRevision++
	stage := e.BeginCatalogStage(before)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	foreign, err := sqltype.Ean13(base.SQLType.ID(), base.Name(), base.SQLType.SQLName())
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	err = e.reownStagedTypeGenerationsLocked(stage)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	shape := stage.rowShapes[table.Address()]
	if shape.replacement == nil || shape.replacement == original {
		t.Fatal("changed generation did not copy its store")
	}
	current := e.stageRead(stage)
	if got := current.MustRelation(table.Address()).Columns[0].Usage.Type; got != next.SQLType {
		t.Fatalf("stale column generation: got %p, want %p", got, next.SQLType)
	}
	if _, err := typedef.ReownDomainDatum(value, next.SQLType, nil, current.State().ScalarImports(), nil); err == nil {
		t.Fatal("staged generation retained transient remint authority")
	}
	for i, row := range shape.replacement.Versions {
		source, mapped := original.Versions[i].Values[0], row.Values[0]
		if mapped.Type() != next.SQLType || mapped.IsNull() != source.IsNull() || row.Row != original.Versions[i].Row || row.Version != original.Versions[i].Version || row.Born != original.Versions[i].Born {
			t.Fatal("generation changed row identity, NULL framing or destination")
		}
		if !mapped.IsNull() {
			if text, err := next.SQLType.Output(context.Background(), mapped); err != nil || text != "123-456789012-8!" {
				t.Fatal("generation lost marked payload", text, err)
			}
		}
		for _, imports := range []map[*sqltype.Descriptor]*sqltype.Descriptor{
			nil,
			{foreign: next.SQLType},
			{base.SQLType: foreign},
		} {
			if _, err := typedef.ReownDomainDatum(source, next.SQLType, nil, imports, nil); err == nil {
				t.Fatal("missing or foreign exact-pointer authorization accepted")
			}
		}
		remapped, err := typedef.ReownDomainDatum(source, next.SQLType, nil, map[*sqltype.Descriptor]*sqltype.Descriptor{base.SQLType: next.SQLType}, nil)
		if err != nil || remapped.Type() != mapped.Type() || remapped.IsNull() != mapped.IsNull() {
			t.Fatal("exact source/destination authorization rejected", err)
		}
	}
	if before.MustType(base.Address()) != base || original.Versions[0].Values[0].Type() != base.SQLType || e.storage.Relations[table.Address()] != original || e.CatalogRead().State() != before.State() {
		t.Fatal("private generation changed published or retained roots")
	}
	if _, err := value.Retag(next.SQLType); err == nil {
		t.Fatal("staged remint granted ordinary same-OID Retag authority")
	}
}
