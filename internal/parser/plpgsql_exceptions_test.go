package parser

import (
	"errors"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestPLExceptionParsing(t *testing.T) {
	source := `DECLARE "raise" integer; BEGIN "raise" := 1; BEGIN RETURN 7; EXCEPTION WHEN division_by_zero OR SQLSTATE '22000' THEN RAISE; WHEN OTHERS THEN RETURN 9; END; EXCEPTION WHEN SQLSTATE 'ZX987' THEN RETURN 8; END`
	block, err := ParsePLpgSQL(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(block.Handlers) != 1 || len(block.Statements) != 2 || block.Handlers[0].Conditions[0].Code != "ZX987" {
		t.Fatal("protected block association", block)
	}
	inner := block.Statements[1].Block
	if len(inner.Handlers) != 2 || len(inner.Handlers[0].Conditions) != 2 || inner.Handlers[0].Conditions[1].Code != "22000" || !inner.Handlers[0].Statements[0].Rethrow {
		t.Fatal("ordered alternatives/rethrow", inner)
	}
	if inner.Handlers[0].Statements[0].Return || inner.Handlers[0].Statements[0].Expression != nil {
		t.Fatal("rethrow confused with RETURN")
	}
	for _, source := range []string{
		"BEGIN RETURN 7; EXCEPTION WHEN division_by_zero THEN RETURN 1+; END",
		"BEGIN RETURN 7; EXCEPTION WHEN THEN RETURN 1; END",
		"BEGIN RETURN 7; EXCEPTION WHEN OTHERS RETURN 1; END",
		"BEGIN RAISE 'constructed'; END",
		"BEGIN RAISE; END; junk",
	} {
		if _, err := ParsePLpgSQL(source); err == nil {
			t.Fatalf("accepted malformed/unreachable suffix %q", source)
		}
	}
	for _, code := range []string{"2201", "22p02", "22012x", "22_12"} {
		_, err := ParsePLpgSQL("BEGIN RETURN 7; EXCEPTION WHEN SQLSTATE '" + code + "' THEN RETURN 1; END")
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "42601" {
			t.Fatalf("invalid SQLSTATE %q: %v", code, err)
		}
	}
	// Procedural contextual tokens must not reserve ordinary SQL identifiers.
	if _, err := Parse("SELECT exception, raise, sqlstate FROM t"); err != nil {
		t.Fatal(err)
	}
	if _, err := ParsePLpgSQL("DECLARE sqlstate text := 'user'; BEGIN RETURN sqlstate; END"); err != nil {
		t.Fatal(err)
	}
}
