package parser

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestPLpgSQLLoopParser(t *testing.T) {
	const source = "DECLARE n integer:=0; BEGIN <<\"Étage\">> LOOP n:=n+1; CONTINUE \"Étage\" WHEN (CASE WHEN n=2 THEN true ELSE false END) /*tail*/; EXIT WHEN n=3; END LOOP \"Étage\"; RETURN n; END"
	block, err := ParsePLpgSQL(source, PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	loop := block.Statements[0].Loop
	if loop == nil || !loop.IsLoop || loop.Label.Text != "Étage" || loop.EndLabel.Text != "Étage" || len(loop.Declarations) != 0 || len(loop.Statements) != 3 {
		t.Fatal("loop syntax/labels lost", loop)
	}
	transfer := loop.Statements[1]
	if transfer.Transfer == nil || !transfer.Transfer.Continue || transfer.Transfer.Label.Text != "Étage" {
		t.Fatal("transfer target lost")
	}
	start := strings.Index(source, "(CASE")
	end := strings.Index(source, " /*tail*/")
	if transfer.ExpressionStart.Offset != start || transfer.ExpressionStart.Character != utf8.RuneCountInString(source[:start])+1 || transfer.ExpressionEnd.Offset != end || source[transfer.ExpressionDelimiter.Offset] != ';' {
		t.Fatal("condition grouping/comment/Unicode provenance lost", transfer)
	}
	if loop.Statements[2].Transfer == nil || loop.Statements[2].Transfer.Continue || loop.Statements[2].Transfer.Label.Text != "" {
		t.Fatal("default EXIT changed")
	}
	cursor, err := ParsePLpgSQL("DECLARE c CURSOR FOR SELECT 1; BEGIN <<rows_loop>> FOR r IN c LOOP EXIT rows_loop; END LOOP rows_loop; RETURN 7; END", PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	if raw := cursor.Statements[0].Cursor; raw == nil || raw.Loop == nil || !raw.Loop.IsLoop || raw.Loop.Label.Text != "rows_loop" || raw.Loop.EndLabel.Text != "rows_loop" {
		t.Fatal("cursor labels lost")
	}
	for _, source := range []string{
		"BEGIN LOOP END",
		"BEGIN LOOP EXIT END LOOP; END",
		"BEGIN LOOP EXIT WHEN; END LOOP; END",
		"BEGIN LOOP CONTINUE WHEN; END LOOP; END",
		"BEGIN LOOP EXIT WHEN 1+; END LOOP; END",
		"BEGIN <<x>> LOOP EXIT; END LOOP y; END",
		"BEGIN LOOP EXIT; END LOOP y; END",
		"BEGIN RETURN 7; LOOP CONTINUE WHEN 1+; END LOOP; END",
		"BEGIN LOOP EXIT; END LOOP; END garbage garbage",
	} {
		_, err := ParsePLpgSQL(source, PLReturnPolicy{})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal("malformed loop accepted", source, err)
		}
	}
	alias, err := ParsePLpgSQL("BEGIN LOOP EXIT WHEN true x; END LOOP; RETURN 7; END", PLReturnPolicy{})
	if err != nil || alias.Statements[0].Loop.Statements[0].Expression == nil || alias.Statements[0].Loop.Statements[0].Transfer.Alias.Text != "x" {
		t.Fatal("scalar projection alias rejected", err)
	}
	for _, expression := range []string{`true AS "Étage"`, `(true) "Étage"`} {
		parsed, err := ParsePLpgSQL("BEGIN LOOP EXIT WHEN "+expression+"; END LOOP; RETURN 7; END", PLReturnPolicy{})
		if err != nil || parsed.Statements[0].Loop.Statements[0].Transfer.Alias.Text != "Étage" {
			t.Fatal("shared projection alias lost", err)
		}
	}
}
