package parser

import "testing"

func TestInheritanceMaintSyntax(t *testing.T) {
	for _, q := range []string{
		`ALTER TABLE ONLY "schema"."子" INHERIT "schema"."π",NO INHERIT "other"`,
		`ALTER TABLE IF EXISTS "schema"."子" * INHERIT "schema"."π",NO INHERIT "other"`,
	} {
		statements, err := Parse(q)
		if err != nil {
			t.Fatal(q, err)
		}
		if len(statements) != 1 {
			t.Fatal("unexpected statement count", len(statements))
		}
		raw, ok := statements[0].(*AlterTable)
		if !ok || len(raw.Actions) != 2 || raw.Actions[0].Action != InheritTable || raw.Actions[1].Action != NoInheritTable || len(raw.Actions[0].Parent.Parts) != 2 || raw.Actions[0].Parent.Parts[1].Text != "π" || raw.Actions[1].Parent.Parts[0].Text != "other" || raw.Actions[0].Parent.Parts[0].Pos.Character <= 0 || raw.Actions[1].Pos.Character <= raw.Actions[0].Pos.Character {
			t.Fatal("lost relationship payload or source positions", raw)
		}
	}
	for _, q := range []string{"ALTER TABLE c INHERIT p garbage", "ALTER TABLE c INHERIT p,", "ALTER TABLE c NO INHERIT"} {
		if _, err := Parse(q); err == nil {
			t.Fatal("incomplete/trailing relationship syntax accepted", q)
		}
	}
}
