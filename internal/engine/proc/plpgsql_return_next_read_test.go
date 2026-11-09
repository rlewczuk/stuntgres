package proc

import (
	"stuntgres/internal/parser"
	"testing"
)

func TestPLpgSQLReturnNextReadSlots(t *testing.T) {
	for _, operand := range []string{"$1", "input", "next_unit.input", "a"} {
		source := "DECLARE a ALIAS FOR $1; BEGIN RETURN NEXT " + operand + "; END"
		d, err := returnNextUnit(t, "BEGIN RETURN; END", true)
		if err != nil {
			t.Fatal(err)
		}
		d.Source = source
		if err = d.SetFormals([]Formal{{Mode: "i", Name: "input", Usage: d.Result}}); err != nil {
			t.Fatal(err)
		}
		d.PLSyntax, err = parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		d.PL, err = CompilePL(d, d.PLSyntax, false, plUnitCollation(t), plUnitResolver(t, d.Result))
		if err != nil {
			t.Fatal(err)
		}
		if err = d.PL.ValidateDefinition(d); err != nil {
			t.Fatal(err)
		}
		if slot := d.PL.Root.Statements[0].Block.Statements[0].ReturnNext.Slot; slot != 0 {
			t.Fatal("read did not resolve original formal", operand, slot)
		}
	}
	d, err := returnNextUnit(t, "DECLARE r record; BEGIN RETURN NEXT r; CASE 1 WHEN 1 THEN RETURN; END CASE; END", true)
	if err != nil {
		t.Fatal(err)
	}
	if err = d.PL.ValidateDefinition(d); err != nil {
		t.Fatal(err)
	}
	d.PL.Root.Statements[0].Block.Statements[1].Case.SearchName = "__Case__Variable_0__"
	if d.PL.ValidateDefinition(d) == nil {
		t.Fatal("forged diagnostic datum ledger accepted")
	}
}
