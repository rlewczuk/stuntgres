package functions

import (
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCitextPatternEscapeAdmission(t *testing.T) {
	citext, err := sqltype.Citext(45000, "citext", "public.citext")
	if err != nil {
		t.Fatal(err)
	}
	var text, binary *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		switch d.ID() {
		case sqltype.Text:
			text = d
		case sqltype.Bytea:
			binary = d
		}
	}
	if text == nil || binary == nil {
		t.Fatal("missing builtin types")
	}
	for _, types := range [][2]*sqltype.Descriptor{{citext, citext}, {text, citext}, {citext, text}} {
		args := [2]Argument{{Usage: types[0].Usage()}, {Usage: types[1].Usage()}}
		if !LikeEscapeAllowed(true, false, args, sqltype.Text) {
			t.Fatal("citext escape rejected")
		}
		if LikeEscapeAllowed(false, false, args, sqltype.Text) || LikeEscapeAllowed(true, true, args, sqltype.Text) || LikeEscapeAllowed(true, false, args, sqltype.Bytea) {
			t.Fatal("escape policy widened")
		}
	}
	mixed := [2]Argument{{Usage: citext.Usage()}, {Usage: binary.Usage()}}
	if LikeEscapeAllowed(true, false, mixed, LikeEscapeTarget(mixed)) {
		t.Fatal("mixed binary admitted")
	}
	if PatternTextType(citext) || PatternAllowed(true, citext.Usage(), text.Usage()) {
		t.Fatal("SIMILAR admission widened")
	}
}
