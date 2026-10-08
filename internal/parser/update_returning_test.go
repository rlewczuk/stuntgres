package parser

import "testing"

func TestUpdateReturningGrammar(t *testing.T) {
	for _, q := range []string{"UPDATE t SET a=b,b=a RETURNING *,old.*,new.*,t.*,public.t.*", `/*é*/ UPDATE public.t SET id=$1 WHERE id=$2 RETURNING WITH (NEW AS n,OLD AS "O") CAST("O".id AS BIGINT),n.id AS "after"`} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
		s, ok := statements[0].(*Update)
		if !ok || s.Returning == nil || len(s.Returning.Targets) == 0 || s.Returning.Pos.Character < 1 {
			t.Fatal(s)
		}
		for _, a := range s.Returning.Aliases {
			if a.Pos.Character < 1 || a.Alias.Pos.Character < 1 {
				t.Fatal(a)
			}
		}
	}
	for _, q := range []string{"UPDATE t SET id=1 RETURNING", "UPDATE t SET id=1 RETURNING WITH () id", "UPDATE t SET id=1 RETURNING WITH (OLD o) id", "UPDATE t SET id=1 RETURNING WITH (OTHER AS o) id", "UPDATE t SET id=1 RETURNING id FROM t", "UPDATE t AS x SET id=1 RETURNING x.id", "UPDATE t SET id=old.id RETURNING id", "UPDATE t SET id=1 WHERE old.id=1 RETURNING id", "INSERT INTO t VALUES(1) RETURNING id"} {
		s, err := Parse(q)
		if err == nil || len(s) != 0 {
			t.Fatal("excluded or malformed input admitted", q, s, err)
		}
	}
}
