package parser

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestPLpgSQLConditionalParser(t *testing.T) {
	source := "DECLARE x integer := /*é*/ ((7)); BEGIN IF /* THEN */ ((true)) THEN x := ((8)); ELSIF NULL THEN ELSEIF false THEN ELSE END IF; CASE (CASE WHEN true THEN x ELSE 0 END) WHEN coalesce((SELECT 1),2), (CASE WHEN true THEN 7 ELSE 9 END) THEN ELSE END CASE; RETURN ((x)); END"
	block, err := ParsePLpgSQL(source, PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if len(block.Statements) != 3 {
		t.Fatal("lost statement delimiters", block.Statements)
	}
	conditional := block.Statements[0].If
	if conditional == nil || len(conditional.Arms) != 3 || !conditional.HasElse || len(conditional.Else) != 0 {
		t.Fatal("IF arm order/empty ELSE", conditional)
	}
	caseStatement := block.Statements[1].Case
	if caseStatement == nil || len(caseStatement.Arms) != 1 || len(caseStatement.Arms[0].Conditions) != 2 || !caseStatement.HasElse {
		t.Fatal("CASE search/item boundaries", caseStatement)
	}
	for _, tc := range []struct {
		start, end Position
		want       string
	}{
		{block.Declarations[0].DefaultStart, block.Declarations[0].DefaultEnd, "((7))"},
		{conditional.Arms[0].Conditions[0].Start, conditional.Arms[0].Conditions[0].End, "((true))"},
		{conditional.Arms[0].Statements[0].ExpressionStart, conditional.Arms[0].Statements[0].ExpressionEnd, "((8))"},
		{conditional.Arms[1].Conditions[0].Start, conditional.Arms[1].Conditions[0].End, "NULL"},
		{caseStatement.Search.Start, caseStatement.Search.End, "(CASE WHEN true THEN x ELSE 0 END)"},
		{caseStatement.Arms[0].Conditions[0].Start, caseStatement.Arms[0].Conditions[0].End, "coalesce((SELECT 1),2)"},
		{caseStatement.Arms[0].Conditions[1].Start, caseStatement.Arms[0].Conditions[1].End, "(CASE WHEN true THEN 7 ELSE 9 END)"},
		{block.Statements[2].ExpressionStart, block.Statements[2].ExpressionEnd, "((x))"},
	} {
		if got := PLExpressionSource(source, tc.start, tc.end); got != tc.want {
			t.Fatalf("span got %q want %q", got, tc.want)
		}
		if tc.start.Character != utf8.RuneCountInString(source[:tc.start.Offset])+1 || tc.end.Character != utf8.RuneCountInString(source[:tc.end.Offset])+1 {
			t.Fatal("byte/character span disagreement", tc)
		}
	}
	for _, tc := range []struct {
		source  string
		hasElse bool
	}{
		{"BEGIN IF true THEN END IF; END", false},
		{"BEGIN IF true THEN ELSE END IF; END", true},
		{"BEGIN CASE WHEN true THEN END CASE; END", false},
		{"BEGIN CASE WHEN true THEN ELSE END CASE; END", true},
		{"BEGIN CASE 1 WHEN 1 THEN END CASE; END", false},
		{"BEGIN CASE 1 WHEN 1 THEN ELSE END CASE; END", true},
		{"DECLARE \"THEN\" text := 'WHEN'; \"ELSIF\" boolean := true; BEGIN IF \"ELSIF\" THEN \"THEN\" := $$END IF; THEN$$; ELSEIF false THEN END IF; END", false},
		{"BEGIN IF (CASE WHEN true THEN true ELSE false END) THEN CASE WHEN true THEN RETURN; END CASE; END IF; END", false},
		{"BEGIN IF 'THEN; ELSIF; WHEN' IS NOT NULL /* END IF */ THEN RETURN; END IF; END", false},
		{"DECLARE elsif integer := 1; elseif integer := 2; BEGIN CASE 2 WHEN elsif, elseif THEN RETURN; ELSE END CASE; END", true},
		{"DECLARE elsif integer := 1; copied elsif%TYPE; a ALIAS FOR elsif; BEGIN elsif := 2; CASE a WHEN copied, elsif THEN RETURN; ELSE END CASE; END", true},
		{"DECLARE x elsif; BEGIN END", false},
		{"<<elsif>> BEGIN END elsif", false},
	} {
		t.Run(tc.source, func(t *testing.T) {
			block, err := ParsePLpgSQL(tc.source, PLReturnPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			if len(block.Statements) == 1 {
				statement := block.Statements[0]
				conditional := statement.If
				if conditional == nil {
					conditional = statement.Case
				}
				if conditional == nil || conditional.HasElse != tc.hasElse {
					t.Fatal("absent and empty ELSE conflated")
				}
			}
		})
	}
	for _, source := range []string{
		"BEGIN IF THEN RETURN; END IF; END",
		"BEGIN CASE WHEN THEN RETURN; END CASE; END",
		"BEGIN IF true THEN RETURN; END CASE; END",
		"BEGIN CASE WHEN true THEN RETURN; END IF; END",
		"BEGIN IF true THEN RETURN; END IF; END; junk",
		"BEGIN CASE WHEN CASE WHEN true THEN true ELSE false END THEN RETURN; END CASE; END",
	} {
		if _, err := ParsePLpgSQL(source, PLReturnPolicy{}); err == nil {
			t.Fatal("malformed conditional accepted", source)
		}
	}
	if _, err := Parse("SELECT elsif, elseif FROM t; SELECT CASE WHEN true THEN 1 ELSE 2 END"); err != nil {
		t.Fatal("procedural mode escaped into SQL", err)
	}
	t.Run("return_precedence", func(t *testing.T) {
		source := "DECLARE s text := 'é'; BEGIN IF false THEN RETURN /*ignored*/ 1+; END IF; END"
		policy := PLReturnPolicy{Code: "42804", Message: "RETURN cannot have a parameter in function returning void"}
		_, err := ParsePLpgSQL(source, policy)
		var diagnostic *sqlstate.Error
		index := strings.Index(source, "1+")
		if !errors.As(err, &diagnostic) || diagnostic.Code != policy.Code || diagnostic.Message != policy.Message || diagnostic.Position != strconv.Itoa(utf8.RuneCountInString(source[:index])+1) {
			t.Fatal("RETURN scalar syntax masked legality", err)
		}
	})
	t.Run("expression_eof_position", func(t *testing.T) {
		for _, gap := range []string{"", " ", "\t \n", " /* trailing */", " -- trailing\n"} {
			source := "DECLARE \"é\" integer; BEGIN IF false THEN \"é\" := 1+" + gap + "; END IF; END"
			_, err := ParsePLpgSQL(source, PLReturnPolicy{})
			var diagnostic *sqlstate.Error
			end := strings.Index(source, "1+") + len("1+")
			want := strconv.Itoa(utf8.RuneCountInString(source[:end]) + 1)
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Message != "syntax error at end of input" || diagnostic.Position != want {
				t.Fatalf("expression EOF gap=%q diagnostic=%v want position %s", gap, err, want)
			}
		}
	})
	t.Run("procedural_delimiter_depth", func(t *testing.T) {
		for _, source := range []string{
			"BEGIN IF NOT CASE WHEN true THEN false ELSE true END THEN RETURN; END IF; END",
			"BEGIN IF true AND CASE WHEN true THEN true ELSE false END THEN RETURN; END IF; END",
			"BEGIN IF false THEN RETURN; ELSIF NOT CASE WHEN true THEN false ELSE true END THEN RETURN; END IF; END",
			"BEGIN CASE WHEN NOT CASE WHEN true THEN false ELSE true END THEN RETURN; END CASE; END",
			"BEGIN CASE 1 WHEN 0, NOT CASE WHEN true THEN false ELSE true END THEN RETURN; END CASE; END",
			"BEGIN CASE 1+CASE WHEN true THEN 1 ELSE 2 END WHEN 2 THEN RETURN; END CASE; END",
		} {
			if _, err := ParsePLpgSQL(source, PLReturnPolicy{}); err == nil {
				t.Fatal("depth-zero SQL CASE delimiter consumed as scalar syntax", source)
			}
		}
		for _, source := range []string{
			"BEGIN IF NOT (CASE WHEN true THEN false ELSE true END) THEN RETURN; END IF; END",
			"BEGIN CASE 1+(CASE WHEN true THEN 1 ELSE 2 END) WHEN 0, (CASE WHEN true THEN 2 ELSE 3 END) THEN RETURN; END CASE; END",
			"BEGIN IF ARRAY[CASE WHEN true THEN 1 ELSE 2 END] IS NOT NULL THEN RETURN; END IF; END",
			"BEGIN IF $$THEN WHEN, END CASE$$::text IS NOT NULL THEN RETURN; END IF; END",
		} {
			if _, err := ParsePLpgSQL(source, PLReturnPolicy{}); err != nil {
				t.Fatal("protected or quoted delimiter rejected", source, err)
			}
		}
		if _, err := Parse("SELECT NOT CASE WHEN true THEN false ELSE true END, ARRAY[CASE WHEN true THEN 1 ELSE 2 END]"); err != nil {
			t.Fatal("procedural delimiter mode escaped into SQL", err)
		}
	})
	t.Run("content_end_excludes_only_trailing_trivia", func(t *testing.T) {
		source := `DECLARE "é" text := U&'\0061' UESCAPE '\' /* default tail */; BEGIN IF conditional_missing(/* interior */) /* IF tail */ THEN "é" := E'a\nb' /* assignment tail */; END IF; CASE ("é") /* search tail */ WHEN 'x' /* earlier item */, 'y' -- last item tail
THEN RETURN ("é") /* return tail */; ELSE RETURN 'z'; END CASE; END`
		block, err := ParsePLpgSQL(source, PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		conditional := block.Statements[0].If
		assignment := conditional.Arms[0].Statements[0]
		choice := block.Statements[1].Case
		returned := choice.Arms[0].Statements[0]
		for _, tc := range []struct {
			start, end, delimiter Position
			want                  string
		}{
			{block.Declarations[0].DefaultStart, block.Declarations[0].DefaultEnd, block.Declarations[0].DefaultDelimiter, `U&'\0061' UESCAPE '\'`},
			{conditional.Arms[0].Conditions[0].Start, conditional.Arms[0].Conditions[0].End, conditional.Arms[0].Conditions[0].Delimiter, "conditional_missing(/* interior */)"},
			{assignment.ExpressionStart, assignment.ExpressionEnd, assignment.ExpressionDelimiter, `E'a\nb'`},
			{choice.Search.Start, choice.Search.End, choice.Search.Delimiter, `("é")`},
			{choice.Arms[0].Conditions[0].Start, choice.Arms[0].Conditions[0].End, choice.Arms[0].Conditions[0].Delimiter, "'x'"},
			{choice.Arms[0].Conditions[1].Start, choice.Arms[0].Conditions[1].End, choice.Arms[0].Conditions[1].Delimiter, "'y'"},
			{returned.ExpressionStart, returned.ExpressionEnd, returned.ExpressionDelimiter, `("é")`},
		} {
			if got := PLExpressionSource(source, tc.start, tc.end); got != tc.want || tc.end.Offset >= tc.delimiter.Offset {
				t.Fatalf("token content/delimiter span got=%q want=%q end=%v delimiter=%v", got, tc.want, tc.end, tc.delimiter)
			}
			if tc.end.Character != utf8.RuneCountInString(source[:tc.end.Offset])+1 {
				t.Fatal("raw token byte/character end mismatch", tc.end)
			}
		}
		items := choice.Arms[0].Conditions
		if got := PLExpressionSource(source, items[0].Start, items[1].End); got != "'x' /* earlier item */, 'y'" {
			t.Fatal("interior list comment lost", got)
		}
	})
	t.Run("lexical_compound_names", func(t *testing.T) {
		for _, tc := range []struct{ name, source, want string }{
			{"callee_and_column", "r.absent()+r.absent+s.r.f1", "r|absent;r|absent;s|r|f1"},
			{"trivia_and_quoted_dot", `R /* gap */ . "a.b" + s . r /* gap */ . f1`, "r|a.b;s|r|f1"},
			{"greedy_nonoverlap", "a.b.c.d.e + q.r.* + x.y", "a|b|c;d|e;q|r;x|y"},
			{"parameter_first_only", `r.$2 + $01.f + $1."X"`, "$01|f;$1|X"},
			{"pl_not_sql_keywords", `return.f + elsif.g + int.h + case.i + "case".j + foreach.k + "foreach".l`, "return|f;elsif|g;int|h;case|j;foreach|l"},
			{"unicode_uescape", `U&"r!0065c" UESCAPE '!' . U&"f!0069eld" UESCAPE '!' + U&"case".x`, "rec|field;case|x"},
			{"quoted_case", `R.F + "R"."F"`, "r|f;R|F"},
			{"skip_strings_comments", "r.a + 'not.a' + $$skip.b$$ + E'also.c' + U&'skip!002ed' UESCAPE '!' /* hidden.e */ -- hidden.f\n + t.b", "r|a;t|b"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				const prefix = "outside.before /*é*/ "
				source := prefix + tc.source + " + outside.after"
				start := Position{Offset: len(prefix), Character: utf8.RuneCountInString(prefix) + 1}
				end := Position{Offset: len(prefix) + len(tc.source), Character: utf8.RuneCountInString(prefix+tc.source) + 1}
				var captured [][]Identifier
				var scratch [3]Identifier
				err := VisitPLCompoundNames(source, start, end, &scratch, func(parts []Identifier) error {
					captured = append(captured, append([]Identifier(nil), parts...))
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				var names []string
				for _, parts := range captured {
					var texts []string
					for _, part := range parts {
						if part.Pos.Offset < start.Offset || part.Pos.Offset >= end.Offset || part.Pos.Character != utf8.RuneCountInString(source[:part.Pos.Offset])+1 {
							t.Fatal("compound-name position escaped original bounded source", part)
						}
						texts = append(texts, part.Text)
					}
					names = append(names, strings.Join(texts, "|"))
				}
				if got := strings.Join(names, ";"); got != tc.want {
					t.Fatalf("got %q want %q", got, tc.want)
				}
			})
		}
		var scratch [3]Identifier
		for _, source := range []string{`r."unfinished`, `U&"r!061" UESCAPE '!'.f`} {
			end := Position{Offset: len(source), Character: utf8.RuneCountInString(source) + 1}
			if err := VisitPLCompoundNames(source, Position{Character: 1}, end, &scratch, func([]Identifier) error { return nil }); err == nil {
				t.Fatal("lexical error discarded", source)
			}
		}
		if err := VisitPLCompoundNames("r.f", Position{Character: 1}, Position{Offset: 4, Character: 5}, &scratch, nil); err == nil {
			t.Fatal("out-of-bounds source accepted")
		}
		if err := VisitPLCompoundNames("", Position{}, Position{}, &scratch, nil); err != nil {
			t.Fatal("absent expression rejected", err)
		}
		sentinel := errors.New("stop compound-name scan")
		calls := 0
		err := VisitPLCompoundNames("a.b+c.d", Position{Character: 1}, Position{Offset: 7, Character: 8}, &scratch, func([]Identifier) error { calls++; return sentinel })
		if err != sentinel || calls != 1 {
			t.Fatal("callback error did not stop traversal", err, calls)
		}
	})
}
