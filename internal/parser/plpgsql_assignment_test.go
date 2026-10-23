package parser

import "testing"

func TestPLpgSQLAssignmentParser(t *testing.T) {
	for _, source := range []string{
		"BEGIN a[1].items[2:3] := '{7,8}'::integer[]; RETURN a[1].items::text; END",
		"BEGIN $1[1].items[:2] = '{7,8}'::integer[]; RETURN $1[1].items::text; END",
		"<<scope>> DECLARE a integer[]; x ALIAS FOR a; BEGIN scope.a[1][4] = 7; x[:] := NULL; END",
	} {
		block, err := ParsePLpgSQL(source, PLReturnPolicy{})
		if err != nil || block == nil {
			t.Fatal(source, err)
		}
		if len(block.Statements) == 0 || block.Statements[0].Target == nil {
			t.Fatal("assignment target missing", source)
		}
		if _, err := ParsePLpgSQL(source+" trailing", PLReturnPolicy{}); err == nil {
			t.Fatal("accepted trailing procedural input", source)
		}
	}
	block, err := ParsePLpgSQL("BEGIN a[1].items[2:3] := 7; RETURN a[1].items; END", PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	outer, ok := block.Statements[0].Target.(SubscriptExpr)
	if !ok || len(outer.Subscripts) != 1 || !outer.Subscripts[0].Slice {
		t.Fatal("lost final slice", block.Statements[0].Target)
	}
	field, ok := outer.Base.(FieldExpr)
	if !ok || field.Field.Text != "items" {
		t.Fatal("lost ordered field", outer.Base)
	}
	inner, ok := field.Base.(SubscriptExpr)
	if !ok || len(inner.Subscripts) != 1 || inner.Subscripts[0].Slice {
		t.Fatal("lost leading element", field.Base)
	}
	root, ok := inner.Base.(ColumnExpr)
	if !ok || root.Name.Parts[0].Text != "a" || root.Pos.Character != 7 || inner.Subscripts[0].Pos.Character != 8 || field.Field.Pos.Character != 12 || outer.Subscripts[0].Pos.Character != 17 {
		t.Fatal("lost target source positions", outer)
	}
	read, ok := block.Statements[1].Expression.(FieldExpr)
	if !ok || read.Field.Text != "items" {
		t.Fatal("subscript field read not retained", block.Statements[1].Expression)
	}
	if _, ok := read.Base.(SubscriptExpr); !ok {
		t.Fatal("read lost its subscript base", read.Base)
	}
}
