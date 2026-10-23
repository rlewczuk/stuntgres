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

func TestTypeLifecycleSyntax(t *testing.T) {
	for _, tc := range []struct {
		q, target string
		moving    bool
	}{
		{`ALTER TYPE "s"."t" RENAME TO "é"`, "é", false},
		{`ALTER TYPE "s"."t" SET SCHEMA destination`, "destination", true},
	} {
		ast, err := Parse(tc.q)
		if err != nil {
			t.Fatal(err)
		}
		s, ok := ast[0].(*AlterType)
		if !ok || len(ast) != 1 || len(s.Name.Parts) != 2 || s.Name.Parts[0].Text != "s" || s.Name.Parts[1].Text != "t" || s.Target.Text != tc.target || s.SetSchema != tc.moving || s.Pos.Character != 1 || s.Target.Pos.Character == 0 {
			t.Fatalf("identity AST=%#v", ast)
		}
	}
	ast, err := Parse("DROP TYPE IF EXISTS s.e,s.d,s.e CASCADE")
	if err != nil {
		t.Fatal(err)
	}
	d, ok := ast[0].(*DropType)
	if !ok || !d.IfExists || d.Mode != DropCascade || len(d.Names) != 3 || d.Pos.Character != 1 {
		t.Fatalf("drop AST=%#v", ast)
	}
	for _, q := range []string{`ALTER TYPE t RENAME TO "é" extra`, "ALTER TYPE t SET SCHEMA s trailing", "DROP TYPE t CASCADE trailing", "CREATE TYPE prefix AS ENUM (); ALTER TYPE t RENAME TO"} {
		if ast, err := Parse(q); err == nil || ast != nil {
			t.Fatal("accepted incomplete or trailing lifecycle input", q, ast, err)
		}
	}
}
