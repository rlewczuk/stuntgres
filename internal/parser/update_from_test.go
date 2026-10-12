package parser

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestUpdateFromGrammarRetainsNamespaceBoundaries(t *testing.T) {
	for _, alias := range []string{"AS \"Dé\"", "\"Dé\""} {
		q := `/*é*/ UPDATE public.t ` + alias + ` SET a="Dé".b,b=s.n FROM s AS s(k,n) JOIN r USING(k) AS u WHERE "Dé".id=u.k RETURNING "Dé".*,s.*,old.*,new.*`
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(statements, err)
		}
		u := statements[0].(*Update)
		if u.Alias.Text != "Dé" || len(u.Name.Parts) != 2 || len(u.Assignments) != 2 || u.Where == nil || len(u.Returning.Targets) != 4 {
			t.Fatal(u)
		}
		join, ok := u.From.(*JoinReference)
		if !ok || len(join.Using) != 1 || join.Using[0].Text != "k" || join.UsingAlias == nil || join.UsingAlias.Text != "u" {
			t.Fatal(u.From)
		}
		source, ok := join.Left.(*AliasReference)
		if !ok || source.Name.Text != "s" || len(source.Columns) != 2 {
			t.Fatal(join.Left)
		}
		column := u.Assignments[0].Value.(ColumnExpr)
		if len(column.Name.Parts) != 2 || column.Name.Parts[0].Text != "Dé" {
			t.Fatal(column)
		}
		index := strings.Index(q, `"Dé".b`)
		if column.SourcePosition().Character != utf8.RuneCountInString(q[:index])+1 {
			t.Fatal("lost UTF8 source position", column.SourcePosition())
		}
	}
	for _, q := range []string{"UPDATE t SET a=s.n FROM s CROSS JOIN r", "UPDATE t x SET a=s.n FROM s INNER JOIN r ON s.k=r.k", "UPDATE t SET a=s.n FROM s NATURAL JOIN r", "UPDATE t SET a=j.n FROM (s JOIN r USING(k)) AS j(k,n,z),u", "UPDATE t SET a=t.a WHERE t.id=1", "UPDATE t x SET a=x.a WHERE x.id=1"} {
		statements, err := Parse(q)
		if err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
}

func TestUpdateFromQualifiedDestinationRetainsFirstIdentifier(t *testing.T) {
	statements, err := Parse("UPDATE t x SET x.a=s.n FROM s")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	assignment := statements[0].(*Update).Assignments[0]
	if assignment.Target.Text != "x" || assignment.Target.Pos.Character != 16 || len(assignment.Steps) != 1 || assignment.Steps[0].Field.Text != "a" || assignment.Steps[0].Field.Pos.Character != 18 {
		t.Fatal(assignment)
	}
}
