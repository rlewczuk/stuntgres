package parser

import (
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unicode/utf8"
)

func TestCTESearchGrammar(t *testing.T) {
	const body = `/*é*/ WITH RECURSIVE r("N",k) AS (SELECT 1,2 UNION ALL SELECT "N",k FROM r) `
	for _, mode := range []string{"BREADTH", "DEPTH"} {
		statements, err := Parse(body + `SEARCH ` + mode + ` FIRST BY k,"N" SET "Seq" CYCLE "N" SET cyc USING path SELECT * FROM r`)
		if err != nil {
			t.Fatal(err)
		}
		d := statements[0].(*Select).With[0]
		s := d.Search
		if s == nil || s.Breadth != (mode == "BREADTH") || len(s.Keys) != 2 || s.Keys[0].Text != "k" || s.Keys[1].Text != "N" || s.Sequence.Text != "Seq" || s.Pos.Character != utf8.RuneCountInString(body)+1 || d.Cycle == nil {
			t.Fatal(d)
		}
	}
	for _, suffix := range []string{"SEARCH DEPTH BY k SET seq", "SEARCH DEPTH FIRST k SET seq", "SEARCH DEPTH FIRST BY k seq", "SEARCH DEPTH FIRST BY k+1 SET seq", "SEARCH DEPTH FIRST BY r.k SET seq", "SEARCH DEPTH FIRST BY k DESC SET seq", "SEARCH DEPTH FIRST BY k SET seq SEARCH DEPTH FIRST BY k SET other"} {
		_, err := Parse(body + suffix + " SELECT * FROM r")
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal(suffix, err)
		}
	}
	for _, text := range []string{"SELECT search,breadth,depth,first FROM search", "SELECT 1 ORDER BY 1 NULLS FIRST", "SELECT 1 FETCH FIRST 1 ROW ONLY", "FETCH FIRST FROM c"} {
		if _, err := Parse(text); err != nil {
			t.Fatal(text, err)
		}
	}
}
func TestCTESearchLegality(t *testing.T) {
	for _, tc := range []struct{ body, code, message string }{
		{"(SELECT 1 UNION ALL SELECT 2) UNION ALL SELECT n FROM r", "0A000", "left side of the UNION must be a SELECT"},
		{"SELECT 1 UNION ALL (SELECT n FROM r UNION ALL SELECT 2)", "42601", "right side of the UNION must be a SELECT"},
		{"SELECT 1 UNION ALL SELECT n FROM (SELECT n FROM r) s", "0A000", "must be at the top level"},
		{"SELECT 1 UNION ALL SELECT n FROM r WHERE EXISTS(SELECT n FROM r)", "42P19", "within a subquery"},
	} {
		for _, clause := range []string{"", " CYCLE n SET cyc USING path", " SEARCH DEPTH FIRST BY n SET seq", " SEARCH BREADTH FIRST BY n SET seq CYCLE n SET cyc USING path"} {
			statements, err := Parse("WITH RECURSIVE r(n) AS (" + tc.body + ")" + clause + " SELECT * FROM r")
			early := tc.code == "42P19" || strings.HasPrefix(clause, " CYCLE")
			var diagnostic *sqlstate.Error
			if early {
				if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || !strings.Contains(diagnostic.Message, tc.message) {
					t.Fatal(tc, clause, err)
				}
				continue
			}
			if err != nil {
				t.Fatal(tc, clause, err)
			}
			body := statements[0].(*Select).With[0].Query.(*Select)
			err = CheckRecursiveCTESelectArms(body)
			if err == nil {
				err = CheckRecursiveCTETopLevel(body, Identifier{Text: "r"})
			}
			if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || !strings.Contains(diagnostic.Message, tc.message) {
				t.Fatal(tc, clause, err)
			}
		}
	}
	for _, mode := range []string{"DEPTH", "BREADTH"} {
		statements, err := Parse("WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT r.n FROM r JOIN (WITH r(n) AS (SELECT 2) SELECT n FROM r) s ON true) SEARCH " + mode + " FIRST BY n SET seq SELECT * FROM r")
		if err != nil {
			t.Fatal(err)
		}
		definition := statements[0].(*Select).With[0]
		if err := CheckRecursiveCTETopLevel(definition.Query.(*Select), definition.Name); err != nil {
			t.Fatal("nested same-name CTE is not the recursive parent", err)
		}
	}
}
