package parser

import (
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestPLpgSQLIntegerForParser(t *testing.T) {
	for _, bounds := range []string{"1..3", "REVERSE 3..1 BY 1", "REVERSE hi..lo BY stride", "REVERSE 3 AS hi..1 AS lo BY 1 AS stride", "lo..hi BY stride", "abs(-1)..abs(-3)", "(1 /* é */ + 0)..(3) BY (1)", `"by".."reverse" BY "loop"`, "1 AS lo..3 AS hi BY 1 AS stride"} {
		t.Run(bounds, func(t *testing.T) {
			source := "BEGIN <<n>> FOR i IN " + bounds + " LOOP CONTINUE n; END LOOP n; END"
			wantLower := strings.SplitN(strings.TrimPrefix(bounds, "REVERSE "), "..", 2)[0]
			block, err := ParsePLpgSQL(source, PLReturnPolicy{})
			if err != nil {
				t.Fatal(err)
			}
			loop := block.Statements[0].IntegerFor
			if loop == nil || len(loop.Targets) != 1 || len(loop.TargetCommas) != 0 || loop.Body == nil || !loop.Body.IsLoop || loop.Body.Label.Text != "n" || loop.Body.EndLabel.Text != "n" || loop.Lower.Expression == nil || loop.Upper.Expression == nil {
				t.Fatal("lost range syntax", loop)
			}
			if got := source[block.Statements[0].IntegerFor.Lower.Start.Offset:block.Statements[0].IntegerFor.Lower.End.Offset]; got != strings.TrimSpace(wantLower) {
				t.Fatalf("lower source got %q want %q", got, wantLower)
			}
			for _, operand := range []PLExpression{loop.Lower, loop.Upper, loop.Step} {
				if operand.Expression == nil {
					if operand != (PLExpression{}) {
						t.Fatal("absent operand retained payload")
					}
					continue
				}
				if operand.Start.Character <= 0 || operand.End.Offset <= operand.Start.Offset || operand.Delimiter.Offset < operand.End.Offset || strings.TrimSpace(source[operand.Start.Offset:operand.End.Offset]) == "" {
					t.Fatal("invalid operand span", operand)
				}
			}
			if source[loop.Lower.Delimiter.Offset:loop.Lower.Delimiter.Offset+2] != ".." || loop.Reverse != strings.HasPrefix(bounds, "REVERSE") {
				t.Fatal("range delimiter/reverse lost")
			}
		})
	}
	t.Run("quoted_declarations", func(t *testing.T) {
		const source = `DECLARE "by" integer:=1; "reverse" integer:=3; "loop" integer:=1; BEGIN FOR i IN "by".."reverse" BY "loop" LOOP END LOOP; RETURN reverse('ab'); END`
		block, err := ParsePLpgSQL(source, PLReturnPolicy{})
		if err != nil {
			t.Fatal("full quoted declaration/range", err)
		}
		for i, name := range []string{"by", "reverse", "loop"} {
			got := block.Declarations[i].Name
			if got.Text != name || got.Pos.Offset != strings.Index(source, `"`+name+`"`) || got.Pos.Character != got.Pos.Offset+1 {
				t.Fatal("quoted declaration identifier/position", got)
			}
		}
		loop := block.Statements[0].IntegerFor
		if loop == nil || loop.Reverse {
			t.Fatal("quoted reverse was an introducer", loop)
		}
		for i, operand := range []PLExpression{loop.Lower, loop.Upper, loop.Step} {
			column, ok := operand.Expression.(ColumnExpr)
			if !ok || len(column.Name.Parts) != 1 || column.Name.Parts[0].Text != []string{"by", "reverse", "loop"}[i] {
				t.Fatal("quoted bound name", operand.Expression)
			}
		}
		call, ok := block.Statements[1].Expression.(FunctionExpr)
		if !ok || len(call.Name.Parts) != 1 || call.Name.Parts[0].Text != "reverse" || len(call.Args) != 1 {
			t.Fatal("ordinary selected function call changed", block.Statements[1].Expression)
		}
		if _, err := Parse("SELECT reverse('ab') AS v"); err != nil {
			t.Fatal("ordinary SQL selected call", err)
		}
	})
	t.Run("reverse_query_diagnostic", func(t *testing.T) {
		const source = `DECLARE r record; BEGIN FOR r IN REVERSE SELECT 1 LOOP END LOOP; END`
		_, err := ParsePLpgSQL(source, PLReturnPolicy{})
		d, ok := err.(*sqlstate.Error)
		if !ok || d.Code != "42601" || d.Message != "cannot specify REVERSE in query FOR loop" {
			t.Fatal("REVERSE query admission", err)
		}
	})
	t.Run("ordered_target_commas", func(t *testing.T) {
		const source = "DECLARE a integer; b integer; c integer; BEGIN FOR a, /* é */ b , c IN 1..3 LOOP END LOOP; END"
		block, err := ParsePLpgSQL(source, PLReturnPolicy{})
		if err != nil {
			t.Fatal(err)
		}
		loop := block.Statements[0].IntegerFor
		if loop == nil || len(loop.Targets) != 3 || len(loop.TargetCommas) != 2 {
			t.Fatal("target-list syntax", loop)
		}
		for i, want := range []string{"a", "b", "c"} {
			column, ok := loop.Targets[i].(ColumnExpr)
			if !ok || len(column.Name.Parts) != 1 || column.Name.Parts[0].Text != want {
				t.Fatal("target order", loop.Targets)
			}
		}
		for _, position := range loop.TargetCommas {
			if source[position.Offset] != ',' || position.Character != utf8.RuneCountInString(source[:position.Offset])+1 {
				t.Fatal("comma coordinates", position)
			}
		}
		if loop.TargetCommas[0].Offset >= loop.Targets[1].SourcePosition().Offset || loop.TargetCommas[1].Offset <= loop.Targets[1].SourcePosition().Offset || loop.TargetCommas[1].Offset >= loop.Targets[2].SourcePosition().Offset {
			t.Fatal("comma/target association", loop.TargetCommas)
		}
	})
	for _, source := range []string{
		"DECLARE c CURSOR(x integer) FOR SELECT x; BEGIN FOR r IN c(1) LOOP END LOOP; END",
		"DECLARE r record; BEGIN FOR r IN SELECT 1 LOOP END LOOP; END",
		"DECLARE r record; BEGIN FOR r IN WITH x AS (SELECT 1 AS i) SELECT i FROM x LOOP END LOOP; END",
	} {
		block, err := ParsePLpgSQL(source, PLReturnPolicy{})
		if err != nil {
			t.Fatal("existing FOR path", source, err)
		}
		if block.Statements[0].IntegerFor != nil || block.Statements[0].Cursor == nil && block.Statements[0].QueryLoop == nil {
			t.Fatal("range classifier captured old FOR")
		}
	}
	for _, source := range []string{"BEGIN FOR i IN ..3 LOOP END LOOP; END", "BEGIN FOR i IN 1.. LOOP END LOOP; END", "BEGIN FOR i IN 1..3 BY LOOP END LOOP; END", "BEGIN FOR i IN REVERSE REVERSE 3..1 LOOP END LOOP; END", "BEGIN FOR i IN 1..3 LOOP END LOOP; END trailing", "BEGIN FOR i IN 1..3 LOOP END LOOP wrong; END"} {
		if _, err := ParsePLpgSQL(source, PLReturnPolicy{}); err == nil {
			t.Fatal("accepted malformed range", source)
		}
	}
	// Classification must preserve scanner failures and suppress duplicate notices.
	calls := 0
	l := &lexer{input: ` i IN "` + strings.Repeat("x", 80) + `"..3 LOOP`, options: Options{Notice: func(_ sqlstate.Error) { calls++ }}}
	integer, _ := l.plIntegerForAhead()
	if !integer || calls != 0 || l.offset != 0 || l.hasLookahead {
		t.Fatal("probe mutated live scanner or emitted notice", integer, calls, l.offset)
	}
	bad := &lexer{input: ` i IN "unterminated`}
	bad.plIntegerForAhead()
	if bad.err == nil {
		t.Fatal("probe hid scanner failure")
	}
}

func TestPLpgSQLIntegerForStatementDiagnostics(t *testing.T) {
	for _, source := range []string{`BEGIN nonsense END`, `BEGIN FOR i IN 1..3 LOOP END LOOP; nonsense END`, `DECLARE x integer; BEGIN SELECT 1 INTO x; nonsense END`, `DECLARE x integer; BEGIN FOR i IN 1..3 LOOP SELECT i INTO x; END LOOP; nonsense END`} {
		_, err := ParsePLpgSQL(source, PLReturnPolicy{})
		d, ok := err.(*sqlstate.Error)
		if !ok || d.Message != "unexpected end of function definition at end of input" {
			t.Fatal("unknown statement must read through EOF", source, err)
		}
	}
	for _, source := range []string{`BEGIN RETURN nonsense + END`, `BEGIN nonsense; END`, `DECLARE nonsense integer; BEGIN nonsense:=1+; END`} {
		_, err := ParsePLpgSQL(source, PLReturnPolicy{})
		if err == nil {
			t.Fatal("invalid source accepted", source)
		}
		if d, ok := err.(*sqlstate.Error); ok && d.Message == "unexpected end of function definition at end of input" {
			t.Fatal("statement provenance leaked into scalar/terminated input", source, err)
		}
	}
	_, err := ParsePLpgSQL(`BEGIN nonsense "unterminated`, PLReturnPolicy{})
	if err == nil || !strings.Contains(err.Error(), "unterminated quoted identifier") {
		t.Fatal("statement probe replaced scanner error", err)
	}
	if _, err := ParsePLpgSQL(`BEGIN <<"return">> FOR i IN 1..3 LOOP RETURN 1; END LOOP return; END`, PLReturnPolicy{}); err != nil {
		t.Fatal("opening RETURN versus closing label", err)
	}
	_, err = ParsePLpgSQL(`BEGIN FOR i IN 1..3 LOOP END LOOP RETURN 1; END`, PLReturnPolicy{})
	if d, ok := err.(*sqlstate.Error); !ok || d.Message != `syntax error at or near "1"` {
		t.Fatal("closing RETURN label before missing semicolon", err)
	}
	_, err = ParsePLpgSQL(`BEGIN FOR i IN 1..3 LOOP END LOOP RETURN; END`, PLReturnPolicy{})
	if d, ok := err.(*sqlstate.Error); !ok || d.Message != `end label "return" specified for unlabeled block` {
		t.Fatal("terminated closing-label diagnosis", err)
	}
	t.Run("capture_boundary_transition", func(t *testing.T) {
		const source = `DECLARE x integer; BEGIN FOR i IN 1..2 LOOP SELECT i INTO x; END LOOP; SELECT x INTO x; RETURN x; END`
		block, err := ParsePLpgSQL(source, PLReturnPolicy{})
		if err != nil {
			t.Fatal("integer/capture statement transition", err)
		}
		loop := block.Statements[0].IntegerFor
		if loop == nil || loop.Body.Statements[0].Into == nil || block.Statements[1].Into == nil || block.Statements[2].Expression == nil {
			t.Fatal("capture or following statement ownership lost")
		}
		_, err = ParsePLpgSQL(`DECLARE x integer; BEGIN SELECT 1 INTO x; nonsense; END`, PLReturnPolicy{})
		if d, ok := err.(*sqlstate.Error); !ok || d.Message != `syntax error at or near "nonsense"` {
			t.Fatal("terminated bare-target diagnosis changed", err)
		}
	})
}
