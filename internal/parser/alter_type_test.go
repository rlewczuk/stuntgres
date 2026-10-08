package parser

import "testing"

func TestAlterTypeSyntax(t *testing.T) {
	for _, target := range []string{"t", "t*", "ONLY t", "ONLY(t)", `"s"."t"`} {
		for _, action := range []string{"ALTER c TYPE bigint", "ALTER COLUMN c SET DATA TYPE varchar(2) COLLATE pg_catalog.\"C\" USING c::varchar(2)", "ALTER c TYPE text USING b, ALTER b TYPE bigint USING c*10, DROP d"} {
			ast, err := Parse("ALTER TABLE " + target + " " + action)
			if err != nil {
				t.Fatal(target, action, err)
			}
			s, ok := ast[0].(*AlterTable)
			if !ok || len(s.Actions) == 0 || s.Actions[0].Column.Text != "c" || s.Actions[0].Pos.Character == 0 {
				t.Fatal(ast)
			}
		}
	}
	for _, q := range []string{"ALTER TABLE t ALTER c TYPE int,", "ALTER TABLE t ALTER c TYPE int USING c USING c", "ALTER TABLE t ALTER c TYPE text COLLATE \"C\" COLLATE \"C\"", "CREATE TABLE prefix(c INT); ALTER TABLE t ALTER c TYPE int garbage"} {
		if ast, err := Parse(q); err == nil || ast != nil {
			t.Fatal("accepted prefix", q, ast, err)
		}
	}
}
