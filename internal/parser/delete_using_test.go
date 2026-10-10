package parser

import (
	"reflect"
	"testing"
)

func TestDeleteUsingTreeAndTraversal(t *testing.T) {
	q := `/*é*/ DELETE FROM t AS "D" USING s JOIN (SELECT k FROM r) x ON s.k=x.k WHERE "D".id::bigint=s.k RETURNING "D".id,s.k`
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	d := statements[0].(*Delete)
	join, ok := d.Using.(*JoinReference)
	if !ok || join.On == nil || d.Where == nil || d.Returning == nil || d.Alias.Text != "D" || d.Pos.Character != 7 {
		t.Fatal(d)
	}
	var names []string
	err = VisitQuerySources(d, func(*Select) error { return nil }, func(table *TableReference, _ *Select) error {
		names = append(names, table.Name.Parts[0].Text)
		if table.Name.Parts[0].Pos.Character <= d.Pos.Character {
			t.Fatal("missing source position", table)
		}
		return nil
	})
	if err != nil || !reflect.DeepEqual(names, []string{"s", "r"}) {
		t.Fatal(names, err)
	}
}

func TestDeleteUsingGrammar(t *testing.T) {
	for _, q := range []string{
		"DELETE FROM t USING s,r WHERE t.id=s.id",
		"DELETE FROM t d USING (VALUES(1),(1)) x(k) WHERE CAST(d.id AS BIGINT)=x.k",
		"DELETE FROM t d USING s FULL JOIN r USING(k) AS merged WHERE public.s.k=d.id",
		"DELETE FROM t USING s NATURAL LEFT JOIN r RETURNING *",
	} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 || statements[0].(*Delete).Using == nil {
			t.Fatal(q, statements, err)
		}
	}
	for _, q := range []string{
		"DELETE FROM t USING", "DELETE FROM t USING s WHERE", "DELETE FROM t USING s WHERE TRUE USING r",
		"DELETE FROM t USING s CROSS JOIN r ON TRUE", "DELETE FROM t USING s JOIN r USING()",
		"DELETE FROM t USING s; nonsense",
	} {
		if statements, err := Parse(q); err == nil || statements != nil {
			t.Fatal("valid prefix escaped", q, statements, err)
		}
	}
}
