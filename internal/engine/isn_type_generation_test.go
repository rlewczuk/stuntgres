package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func TestIsnTypeGenerationIdentity(t *testing.T) {
	for _, family := range []string{"ean13", "isbn", "isbn13", "ismn", "ismn13", "issn", "issn13", "upc"} {
		t.Run(family, func(t *testing.T) { testIsnTypeGenerationIdentity(t, family) })
	}
}

func testIsnTypeGenerationIdentity(t *testing.T, family string) {
	e, ext, _ := isnUnitInstalled(t)
	slot, input, output, constructor := 0, "1234567890120!", "123-456789012-8!", sqltype.Ean13
	if family == "isbn" { slot, input, output, constructor = 3, "9780123456780!", "0-12-345678-9!", sqltype.Isbn }
	if family == "isbn13" { slot, input, output, constructor = 6, "9780123456780!", "978-0-12-345678-6!", sqltype.Isbn13 }
	if family == "ismn" { slot, input, output, constructor = 9, "M123456780!", "M-1234-5678-5!", sqltype.Ismn }
	if family == "ismn13" { slot, input, output, constructor = 12, "M123456780!", "979-0-1234-5678-5!", sqltype.Ismn13 }
	if family == "issn" { slot, input, output, constructor = 15, "9771234567898!", "977-1234-567-89-8!", sqltype.Issn }
	if family == "issn13" { slot, input, output, constructor = 18, "9771234567898!", "977-1234-567-89-8!", sqltype.Issn13 }
	if family == "upc" { slot, input, output, constructor = 21, "123456789010!", "123456789012!", sqltype.Upc }
	base := e.CatalogRead().MustType(ext.Members[slot])
	op, done := roleOperation(t, e, "postgres")
	defer done()
	table := domainUnitTable(t, e, op, "CREATE TABLE public.isn_generation(v public."+family+")")
	before := e.CatalogRead()
	value, err := base.SQLType.Input(context.Background(), input, -1)
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
	next.SQLType, err = constructor(base.SQLType.ID(), base.Name(), base.SQLType.SQLName())
	if err != nil {
		t.Fatal(err)
	}
	next.ObjectRevision++
	stage := e.BeginCatalogStage(before)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&next); err != nil {
		t.Fatal(err)
	}
	foreign, err := constructor(base.SQLType.ID(), base.Name(), base.SQLType.SQLName())
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
	if shape.Replacement == nil || shape.Replacement == original {
		t.Fatal("changed generation did not copy its store")
	}
	current := e.stageRead(stage)
	if got := current.MustRelation(table.Address()).Columns[0].Usage.Type; got != next.SQLType {
		t.Fatalf("stale column generation: got %p, want %p", got, next.SQLType)
	}
	if _, err := typedef.ReownDomainDatum(value, next.SQLType, nil, current.State().ScalarImports(), nil); err == nil {
		t.Fatal("staged generation retained transient remint authority")
	}
	for i, row := range shape.Replacement.Versions {
		source, mapped := original.Versions[i].Values[0], row.Values[0]
		if mapped.Type() != next.SQLType || mapped.IsNull() != source.IsNull() || row.Row != original.Versions[i].Row || row.Version != original.Versions[i].Version || row.Born != original.Versions[i].Born {
			t.Fatal("generation changed row identity, NULL framing or destination")
		}
		if !mapped.IsNull() {
			if text, err := next.SQLType.Output(context.Background(), mapped); err != nil || text != output {
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
