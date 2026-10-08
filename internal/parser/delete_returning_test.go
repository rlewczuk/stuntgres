package parser

import "testing"

func TestDeleteReturningGrammar(t *testing.T) {
	for _, q := range []string{"DELETE FROM t RETURNING *", "DELETE FROM t d WHERE d.id=$1 RETURNING *,old.*,new.*,COALESCE(new.id,old.id) AS kept", "DELETE FROM t RETURNING WITH (NEW AS n,OLD AS o) n.*,o.*", `/*é*/ DELETE FROM public.t RETURNING WITH (OLD AS "O") CAST("O".id AS BIGINT),new.id::BIGINT`} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		stmt := statements[0].(*Delete)
		if stmt.Returning == nil || stmt.Returning.Pos.Character <= stmt.Name.Parts[0].Pos.Character || len(stmt.Returning.Targets) == 0 {
			t.Fatal(stmt)
		}
		for _, option := range stmt.Returning.Aliases {
			if option.Pos.Character <= stmt.Returning.Pos.Character || option.Alias.Pos.Character <= option.Pos.Character {
				t.Fatal(option)
			}
		}
	}
	for _, q := range []string{"DELETE FROM t RETURNING", "DELETE FROM t RETURNING WITH () id", "DELETE FROM t RETURNING WITH (OLD o) id", "DELETE FROM t RETURNING WITH (WHATEVER AS o) id", "DELETE FROM t RETURNING * trailing", "DELETE FROM t RETURNING id FROM t", "INSERT INTO t VALUES(1) RETURNING id", "UPDATE t SET id=1 RETURNING id"} {
		if statements, err := Parse(q); err == nil || statements != nil {
			t.Fatal("invalid/excluded suffix admitted", q, statements, err)
		}
	}
}
