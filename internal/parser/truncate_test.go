package parser

import "testing"

func TestTruncateGrammar(t *testing.T) {
	for _, prefix := range []string{"TRUNCATE ", "TRUNCATE TABLE "} {
		for _, identity := range []string{"", " CONTINUE IDENTITY"} {
			for _, mode := range []string{"", " RESTRICT", " CASCADE"} {
				q := prefix + `ONLY public."É table", b *, ONLY(c),b` + identity + mode
				statements, err := Parse(q)
				if err != nil || len(statements) != 1 {
					t.Fatalf("%s: %v %v", q, statements, err)
				}
				stmt, ok := statements[0].(*Truncate)
				if !ok || len(stmt.Targets) != 4 || stmt.Pos.Character != 1 {
					t.Fatalf("%s: %#v", q, statements[0])
				}
				if !stmt.Targets[0].Only || stmt.Targets[1].Only || !stmt.Targets[2].Only || stmt.Targets[3].Only || stmt.Targets[0].Name.Parts[1].Text != "É table" {
					t.Fatalf("target decoding: %#v", stmt.Targets)
				}
				want := DropRestrict
				if mode == " CASCADE" {
					want = DropCascade
				}
				if stmt.Mode != want {
					t.Fatalf("mode %v want %v", stmt.Mode, want)
				}
			}
		}
	}
	for _, q := range []string{"TRUNCATE", "TRUNCATE TABLE", "TRUNCATE t,", "TRUNCATE ONLY t *", "TRUNCATE t CASCADE CONTINUE IDENTITY", "TRUNCATE t CONTINUE IDENTITY CONTINUE IDENTITY", "TRUNCATE t RESTART IDENTITY junk"} {
		controlParseError(t, q, "42601", "", "")
	}
	controlParseError(t, "TRUNCATE t RESTART IDENTITY", "0A000", "TRUNCATE RESTART IDENTITY is unsupported", "12")
	for _, q := range []string{"CREATE TABLE truncate(continue INT)", "CREATE TYPE continue AS (truncate INT)", "TRUNCATE truncate CONTINUE IDENTITY"} {
		if _, err := Parse(q); err != nil {
			t.Fatalf("unreserved names %s: %v", q, err)
		}
	}
}
