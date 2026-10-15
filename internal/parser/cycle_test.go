package parser

import (
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unicode/utf8"
)

func TestCTECycleGrammar(t *testing.T) {
	const body = `/*é*/ WITH RECURSIVE r("N") AS (SELECT 1 UNION ALL SELECT "N" FROM r) `
	for _, marks := range []string{"", " TO 'Y' DEFAULT 'N'", " TO numeric(4,2) '1.50' DEFAULT numeric(4,2) '0.00'"} {
		text := body + `CYCLE "N" SET "Mark"` + marks + ` USING "Path" SELECT * FROM r`
		statements, err := Parse(text)
		if err != nil {
			t.Fatal(err)
		}
		clause := statements[0].(*Select).With[0].Cycle
		if clause == nil || len(clause.Keys) != 1 || clause.Keys[0].Text != "N" || clause.Mark.Text != "Mark" || clause.Path.Text != "Path" || clause.Pos.Character != utf8.RuneCountInString(body)+1 {
			t.Fatal(clause)
		}
		if (clause.Value == nil) != (marks == "") {
			t.Fatal("lost mark constants", clause)
		}
	}
	for _, value := range []string{"$1", "1::integer", "-1", "+1", "(1+1)", "'Y' COLLATE \"C\"", "abs(1)"} {
		_, err := Parse(body + "CYCLE \"N\" SET cyc TO " + value + " DEFAULT 0 USING path SELECT * FROM r")
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal(value, err)
		}
	}
	if _, err := Parse("CREATE SEQUENCE cycle CYCLE"); err != nil {
		t.Fatal("sequence CYCLE regressed", err)
	}
}

func TestCTECycleLegality(t *testing.T) {
	for _, tc := range []struct{ body, code, message string }{
		{"(SELECT 1 UNION ALL SELECT 2) UNION ALL SELECT n FROM r", "0A000", "left side of the UNION must be a SELECT"},
		{"SELECT 1 UNION ALL (SELECT n FROM r UNION ALL SELECT 2)", "42601", "right side of the UNION must be a SELECT"},
		{"SELECT 1 UNION ALL SELECT n FROM (SELECT n FROM r) s", "0A000", "must be at the top level"},
		{"SELECT 1 UNION ALL SELECT n FROM r WHERE EXISTS(SELECT n FROM r)", "42P19", "within a subquery"},
		{"WITH x AS (SELECT n FROM r) SELECT 1 UNION ALL SELECT n FROM x", "42P19", "within a subquery"},
	} {
		_, err := Parse("WITH RECURSIVE r(n) AS (" + tc.body + ") CYCLE n SET cyc USING path SELECT * FROM r")
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code || !strings.Contains(diagnostic.Message, tc.message) {
			t.Fatal(tc, err)
		}
	}
	statements, err := Parse("WITH RECURSIVE r(n) AS (SELECT 1) CYCLE n SET cyc USING path SELECT * FROM r")
	if err != nil {
		t.Fatal(err)
	}
	analysis, err := AnalyzeRecursiveCTEs(statements[0])
	if err != nil || analysis.Recursive[0] {
		t.Fatal(analysis, err)
	}
}
