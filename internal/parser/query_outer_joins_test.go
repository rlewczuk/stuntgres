package parser

import (
	"strings"
	"testing"
)

func TestQueryOuterJoinsKindsAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		word  string
		kind  JoinKind
		token int
	}{{"LEFT", JoinLeft, LEFT}, {"RIGHT", JoinRight, RIGHT}, {"FULL", JoinFull, FULL}} {
		for _, outer := range []string{"", " OUTER"} {
			for _, condition := range []string{" ON a.k=b.k", " USING(k) AS u"} {
				query := "SELECT a.*,b.* FROM a " + tc.word + outer + " JOIN b" + condition
				statements, err := Parse(query)
				if err != nil || len(statements) != 1 {
					t.Fatal(query, statements, err)
				}
				join := statements[0].(*Select).From.(*JoinReference)
				if join.Kind != tc.kind || join.Left.(*TableReference).Name.Parts[0].Text != "a" || join.Right.(*TableReference).Name.Parts[0].Text != "b" {
					t.Fatal(query, join)
				}
				if join.Pos.Character <= 0 {
					t.Fatal("missing source position", join)
				}
			}
			query := "SELECT * FROM a NATURAL " + tc.word + outer + " JOIN b"
			statements, err := Parse(query)
			if err != nil {
				t.Fatal(query, err)
			}
			join := statements[0].(*Select).From.(*JoinReference)
			if join.Kind != tc.kind || !join.Natural {
				t.Fatal(join)
			}
		}
		keyword := lookupKeyword(strings.ToLower(tc.word))
		if keyword == nil || keyword.token != tc.token || keyword.category != TypeFunctionName {
			t.Fatal(keyword)
		}
	}
	for _, query := range []string{"SELECT * FROM a LEFT JOIN b", "SELECT * FROM a RIGHT OUTER JOIN b", "SELECT * FROM a NATURAL FULL JOIN b ON TRUE", "SELECT * FROM a NATURAL LEFT JOIN b USING(k)", "SELECT * FROM a CROSS OUTER JOIN b", "SELECT * FROM a OUTER JOIN b ON TRUE", "SELECT * FROM a FULL OUTER OUTER JOIN b ON TRUE", "SELECT * FROM a LEFT JOIN b ON TRUE ON TRUE"} {
		statements, err := Parse(query)
		if err == nil || len(statements) != 0 {
			t.Fatal("invalid full input admitted", query, statements, err)
		}
	}
	statements, err := Parse("SELECT * FROM a,b LEFT JOIN c ON b.k=c.k")
	if err != nil {
		t.Fatal(err)
	}
	comma := statements[0].(*Select).From.(*JoinReference)
	if comma.Kind != JoinInner || comma.Right.(*JoinReference).Kind != JoinLeft {
		t.Fatal(comma)
	}
}
