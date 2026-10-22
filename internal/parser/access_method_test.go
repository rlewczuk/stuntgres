package parser

import "testing"

func TestAccessMethodGrammar(t *testing.T) {
	statements, err := Parse(`CREATE ACCESS METHOD "Am é" TYPE INDEX HANDLER pg_catalog.bthandler; CREATE ACCESS METHOD AM_HEAP TYPE TABLE HANDLER heap_tableam_handler; DROP ACCESS METHOD IF EXISTS "Am é" CASCADE; DROP ACCESS METHOD am_heap RESTRICT`)
	if err != nil || len(statements) != 4 {
		t.Fatal(statements, err)
	}
	index, ok := statements[0].(*CreateAccessMethod)
	if !ok || index.Name.Text != "Am é" || index.MethodKind != "index" || len(index.Handler.Parts) != 2 || index.Handler.Parts[0].Text != "pg_catalog" || index.Handler.Parts[1].Text != "bthandler" || index.Name.Pos.Character != 22 || index.Pos.Character != 1 {
		t.Fatal("index declaration", index)
	}
	heap, ok := statements[1].(*CreateAccessMethod)
	if !ok || heap.Name.Text != "am_heap" || heap.MethodKind != "table" || len(heap.Handler.Parts) != 1 {
		t.Fatal("table declaration", heap)
	}
	drop, ok := statements[2].(*DropAccessMethod)
	if !ok || !drop.IfExists || drop.Name.Text != "Am é" || drop.Mode != DropCascade {
		t.Fatal("drop declaration", drop)
	}
	for _, query := range []string{
		`CREATE ACCESS METHOD public.am TYPE INDEX HANDLER bthandler`,
		`CREATE ACCESS METHOD am TYPE BOGUS HANDLER bthandler`,
		`CREATE ACCESS METHOD am TYPE INDEX HANDLER bthandler(internal)`,
		`CREATE ACCESS METHOD am TYPE INDEX HANDLER bthandler GARBAGE`,
		`CREATE ACCESS METHOD IF NOT EXISTS am TYPE INDEX HANDLER bthandler`,
		`DROP ACCESS METHOD am,another`,
		`DROP ACCESS METHOD am GARBAGE`,
	} {
		if statements, err := Parse(query); err == nil || statements != nil {
			t.Fatal("invalid complete input admitted", query, statements, err)
		}
	}
}
