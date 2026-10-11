package parser

import "testing"

func TestDiscardGrammar(t *testing.T) {
	for _, tc := range []struct {
		word     string
		category DiscardCategory
	}{
		{"ALL", DiscardAll}, {"PLANS", DiscardPlans}, {"SEQUENCES", DiscardSequences}, {"TEMP", DiscardTemp}, {"TEMPORARY", DiscardTemp},
	} {
		ast, err := Parse(" /*x*/ DISCARD " + tc.word + ";")
		if err != nil || len(ast) != 1 {
			t.Fatal(tc.word, ast, err)
		}
		d, ok := ast[0].(*Discard)
		if !ok || d.Category != tc.category || d.Pos.Offset != 7 || d.Pos.Character != 8 {
			t.Fatal(tc.word, d)
		}
		for range 8 {
			if _, err := Parse("SELECT discard FROM plans"); err != nil {
				t.Fatal(err)
			}
		}
		if d.Category != tc.category || d.Pos.Offset != 7 {
			t.Fatal("scratch reuse mutated retained AST")
		}
	}
	for _, q := range []string{"DISCARD", "DISCARD p", "DISCARD IF EXISTS TEMP", "DISCARD TEMP p", "DISCARD ALL PLANS", "DISCARD TEMP,PLANS", "DISCARD PLANS SELECT 1", `"DISCARD" ALL`, `DISCARD "TEMP"`} {
		controlParseError(t, q, "42601", "", "")
	}
	for _, q := range []string{"SELECT discard AS discard", "CREATE TABLE discard(discard int)", "SELECT plans,sequences FROM discard"} {
		if _, err := Parse(q); err != nil {
			t.Fatal(q, err)
		}
	}
}
