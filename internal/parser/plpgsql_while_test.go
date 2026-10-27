package parser

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func TestPLpgSQLWhileParser(t *testing.T) {
	const source = `DECLARE "while" integer:=0; BEGIN <<"Étage">> WHILE /* head */ (CASE WHEN "while"<3 THEN true ELSE false END) admitted /* tail */ LOOP "while":="while"+1; CONTINUE "Étage"; END LOOP "Étage"; RETURN "while"; END`
	raw, err := ParsePLpgSQL(source, PLReturnPolicy{})
	if err != nil {
		t.Fatal(err)
	}
	in := raw.Statements[0]
	if in.While == nil || in.Loop != nil || in.Expression != nil || in.Pos.Offset != strings.Index(source, "WHILE") {
		t.Fatal("WHILE variant or statement origin lost")
	}
	loop, condition := in.While.Body, in.While.Condition
	if loop == nil || !loop.IsLoop || loop.Label.Text != "Étage" || loop.EndLabel.Text != "Étage" || len(loop.Declarations) != 0 || len(loop.Statements) != 2 {
		t.Fatal("WHILE body/label ownership lost")
	}
	start, end := strings.Index(source, "(CASE"), strings.Index(source, " /* tail */")
	if condition.Expression == nil || condition.Alias.Text != "admitted" || condition.Start.Offset != start || condition.Start.Character != utf8.RuneCountInString(source[:start])+1 || condition.End.Offset != end || condition.End.Character != utf8.RuneCountInString(source[:end])+1 || condition.Delimiter != loop.Pos || source[loop.Pos.Offset:loop.Pos.Offset+4] != "LOOP" {
		t.Fatal("WHILE token-exclusive grouped/alias/Unicode span lost", condition)
	}
	for _, source := range []string{"BEGIN WHILE false LOOP END LOOP; RETURN 7; END", "BEGIN <<x>> WHILE false LOOP END LOOP; RETURN 7; END", `BEGIN WHILE (true) AS "Étage" LOOP EXIT; END LOOP; RETURN 7; END`} {
		if _, err := ParsePLpgSQL(source, PLReturnPolicy{}); err != nil {
			t.Fatal("valid WHILE rejected", source, err)
		}
	}
	if _, err := Parse("SELECT while FROM (SELECT 3 AS while) AS named"); err != nil {
		t.Fatal("ordinary SQL identifier changed", err)
	}
	for _, source := range []string{
		"BEGIN WHILE LOOP END LOOP; END",
		"BEGIN WHILE true; END",
		"BEGIN WHILE false LOOP END",
		"BEGIN WHILE false LOOP END; END",
		"BEGIN WHILE false LOOP EXIT END LOOP; END",
		"BEGIN <<x>> WHILE false LOOP END LOOP y; END",
		"BEGIN WHILE false LOOP END LOOP y; END",
		"BEGIN WHILE false LOOP END LOOP; END garbage garbage",
		"DECLARE x integer; BEGIN WHILE false LOOP x:=1+; END LOOP; END",
	} {
		_, err := ParsePLpgSQL(source, PLReturnPolicy{})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal("malformed WHILE accepted", source, err)
		}
	}
}
