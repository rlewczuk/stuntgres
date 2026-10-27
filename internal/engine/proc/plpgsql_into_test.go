package proc

import (
	"reflect"
	"slices"
	"stuntgres/internal/parser"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestPLpgSQLSelectIntoValidation(t *testing.T) {
	const source = `DECLARE r record; a integer; b integer; BEGIN SELECT r.a,r.b INTO a,b; CASE 1 WHEN 1 THEN RETURN 7; END CASE; END`
	original := conditionalUnitDefinition(t, source)
	body := original.PL.Root.Statements[0].Block
	if len(body.Statements[0].Into.Targets) != 2 || body.Statements[1].Case.SearchName != "__Case__Variable_7__" {
		t.Fatal("scalar target ROW or lexical field ledger", body.Statements[1].Case.SearchName)
	}
	before, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
	if err != nil || !reflect.DeepEqual(before, original.PLSyntax) {
		t.Fatal("compilation changed syntax", err)
	}
	for _, name := range []string{"query", "strict", "target", "empty", "span", "separator", "nil_target", "text", "variant", "missing", "ledger"} {
		t.Run(name, func(t *testing.T) {
			candidate := conditionalUnitDefinition(t, source)
			b := candidate.PL.Root.Statements[0].Block
			in := &b.Statements[0]
			switch name {
			case "query":
				in.Into.Query = &parser.Select{}
			case "strict":
				in.Into.Strict = !in.Into.Strict
			case "target":
				in.Into.Targets[0].Slot = candidate.PL.FoundSlot
			case "empty":
				in.Into.Targets = nil
			case "span":
				in.Syntax.Into.TargetsEnd.Character++
			case "separator":
				in.Syntax.Into.Separators[0].Character++
			case "nil_target":
				in.Syntax.Into.Targets[0] = nil
			case "text":
				in.Syntax.Into.Text = "SELECT 1"
			case "variant":
				in.Query = &parser.Select{}
			case "missing":
				in.Into = nil
			case "ledger":
				b.Statements[1].Case.SearchName = "__Case__Variable_6__"
			}
			if err := candidate.PL.ValidateDefinition(candidate); err == nil {
				t.Fatal("accepted forged capture", name)
			}
		})
	}
	copyProgram := *original.PL
	copyProgram.Variables = slices.Clone(original.PL.Variables)
	if !original.PL.SameLayout(&copyProgram) {
		t.Fatal("immutable layout copy differs")
	}
	changed := conditionalUnitDefinition(t, `DECLARE x integer; BEGIN SELECT 1 INTO STRICT x; RETURN x; END`)
	if original.PL.SameLayout(changed.PL) {
		t.Fatal("different capture layout equivalent")
	}
	for _, query := range []string{`SELECT 1 INTO r`, `SELECT 1 INTO a,a`, `SELECT 1 INTO r.a,r.b`, `WITH c AS (SELECT 1 AS i) SELECT i INTO a FROM c`, `INSERT INTO q VALUES(1) RETURNING i INTO a`, `UPDATE q SET i=1 RETURNING i INTO a`, `DELETE FROM q RETURNING i INTO a`} {
		conditionalUnitDefinition(t, `DECLARE r record; a integer; BEGIN `+query+`; RETURN 7; END`)
	}
	t.Run("redacted_newline_source", func(t *testing.T) {
		for _, newline := range []string{"\n", "\r\n"} {
			source := "DECLARE a integer; BEGIN SELECT /*é*/" + newline + " 1 INTO" + newline + " a" + newline + " FROM q; RETURN a; END"
			for _, mutation := range []string{"none", "old_newline_redaction", "coordinate", "outside_trivia"} {
				definition := conditionalUnitDefinition(t, source)
				raw := definition.PL.Root.Statements[0].Block.Statements[0].Syntax.Into
				want := "SELECT /*é*/" + newline + " 1 " + strings.Repeat(" ", 7+2*len(newline)) + "FROM q"
				if raw.Text != want || raw.TargetsEnd.Character != utf8.RuneCountInString(source[:raw.TargetsEnd.Offset])+1 {
					t.Fatalf("incorrect admitted redaction/coordinates: %#v, want %q", raw, want)
				}
				before, err := parser.ParsePLpgSQL(source, parser.PLReturnPolicy{})
				if err != nil || !reflect.DeepEqual(before, definition.PLSyntax) {
					t.Fatal("compilation mutated admitted syntax", err)
				}
				switch mutation {
				case "old_newline_redaction":
					text := []byte(raw.Text)
					for at := raw.IntoStart.Offset; at < raw.TargetsEnd.Offset; at++ {
						if source[at] == '\n' || source[at] == '\r' {
							text[at-raw.Start.Offset] = source[at]
						}
					}
					raw.Text = string(text)
				case "coordinate":
					raw.TargetsEnd.Character++
				case "outside_trivia":
					raw.Text = strings.Replace(raw.Text, "/*é*/", "/*a*/", 1)
				}
				err = definition.PL.ValidateDefinition(definition)
				if mutation == "none" && err != nil || mutation != "none" && err == nil {
					t.Fatalf("source admission %s: %v", mutation, err)
				}
			}
		}
	})
	t.Run("ledger_boundaries", func(t *testing.T) {
		for _, tc := range []struct {
			query      string
			searchName string
		}{
			{`SELECT 1 INTO a`, "__Case__Variable_4__"},
			{`SELECT 1 INTO a /* é中 */`, "__Case__Variable_4__"},
			{`SELECT r.f INTO a`, "__Case__Variable_5__"},
			{`SELECT 1 INTO r.f`, "__Case__Variable_5__"},
			{`SELECT r.f INTO r`, "__Case__Variable_4__"},
			{`SELECT INTO a r.f`, "__Case__Variable_5__"},
			{`SELECT 1 INTO a FROM q WHERE r.f=1`, "__Case__Variable_5__"},
			{`INSERT INTO q VALUES(1) RETURNING r.f INTO a`, "__Case__Variable_5__"},
			{`UPDATE q SET i=1 RETURNING r.f INTO a`, "__Case__Variable_5__"},
			{`DELETE FROM q RETURNING r.f INTO a`, "__Case__Variable_5__"},
		} {
			t.Run(tc.query, func(t *testing.T) {
				definition := conditionalUnitDefinition(t, `DECLARE r record; a integer; BEGIN `+tc.query+`; CASE 1 WHEN 1 THEN RETURN 7; END CASE; END`)
				block := definition.PL.Root.Statements[0].Block
				if got := block.Statements[1].Case.SearchName; got != tc.searchName {
					t.Fatalf("diagnostic ledger name = %q, want %q", got, tc.searchName)
				}
			})
		}
	})
}
