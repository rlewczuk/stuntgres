package parser

import "testing"

func TestLikeSyntax(t *testing.T) {
	for _, q := range []string{
		"SELECT v LIKE 'a%' FROM t", "SELECT v NOT LIKE 'a%' ESCAPE '!' FROM t",
		"SELECT v ILIKE 'a%' FROM t", "SELECT v NOT ILIKE 'a%' ESCAPE '!' FROM t",
		"SELECT v OPERATOR(pg_catalog.~~*) 'a%' FROM t",
		"DELETE FROM t WHERE v LIKE 'a%' ESCAPE '!'",
		"SELECT v FROM t WHERE v LIKE $1",
		"SELECT v FROM t WHERE v NOT LIKE 'x' ESCAPE '!'",
		"UPDATE t SET v='changed' WHERE v LIKE $1",
		"UPDATE t SET v='changed' WHERE v ILIKE p ESCAPE e",
		"UPDATE t SET v='changed' WHERE v NOT LIKE 'x' ESCAPE '!'",
		"SELECT b LIKE $1::bytea ESCAPE $2::bytea FROM t",
		"SELECT b LIKE '\\x0025'::bytea ESCAPE '\\x'::bytea FROM t",
		"SELECT x ilike FROM t",
	} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
	if _, err := Parse("SELECT v ~~ 'a%' ESCAPE '!' FROM t"); err == nil {
		t.Fatal("symbolic ESCAPE accepted")
	}
}

func TestLikeContextualProjectionLabels(t *testing.T) {
	for _, word := range []string{"like", "ilike"} {
		for _, expression := range []string{"x", "x+1", "CAST(x AS TEXT)", "(x LIKE 'a%')", "(x BETWEEN 'a' AND 'z')"} {
			orderLabel(t, "SELECT "+expression+" "+word+" FROM t", word)
		}
		s := orderParse(t, "SELECT x "+word+",y FROM t")
		if len(s.Labels) != 2 || s.Labels[0].Text != word {
			t.Fatal("keyword label before comma", s.Labels)
		}
	}
	for _, q := range []string{
		"SELECT x FROM t WHERE x ILIKE FROM t",
		"SELECT x ILIKE 'a%' FROM t trailing extra",
		"SELECT x BETWEEN 'a' AND 'z' LIKE FROM t",
		"SELECT x BETWEEN 'a' AND 'z' ILIKE FROM t",
		"SELECT x='a' LIKE FROM t",
		"SELECT x LIKE 'a' LIKE FROM t",
	} {
		if _, err := Parse(q); err == nil {
			t.Fatal("incomplete predicate or trailing input accepted", q)
		}
	}
}
