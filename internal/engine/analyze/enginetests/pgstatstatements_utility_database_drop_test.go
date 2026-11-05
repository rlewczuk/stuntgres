package enginetests

import (
	"slices"
	"strings"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
	"testing"
)

func TestPgStatStatementsFingerprintUtilityDatabaseDrop(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		same        bool
	}{
		{"DROP DATABASE pgss_drop", "DROP DATABASE pgss_other", false},
		{"DROP DATABASE pgss_drop", "DROP DATABASE IF EXISTS pgss_drop", false},
		{"DROP DATABASE pgss_drop", "DROP DATABASE pgss_drop (FORCE)", false},
		{"DROP DATABASE pgss_drop (FORCE)", "DROP DATABASE pgss_drop WITH (FORCE)", true},
		{"DROP DATABASE pgss_drop (FORCE)", "DROP DATABASE pgss_drop (FORCE,FORCE)", false},
		{"DROP DATABASE pgss_drop (FORCE,FORCE)", "drop database \"pgss_drop\" with (force,force)", true},
	} {
		left, _ := analyze.JumbleUtility(enginetest.Statement(t, tc.left))
		right, _ := analyze.JumbleUtility(enginetest.Statement(t, tc.right))
		if left.QueryID() == 0 || right.QueryID() == 0 || (left.QueryID() == right.QueryID()) != tc.same {
			t.Fatal("released raw DROP field identity", tc, left.QueryID(), right.QueryID())
		}
	}
	statement := enginetest.Statement(t, "DROP DATABASE IF EXISTS pgss_drop WITH (FORCE,FORCE)").(*parser.DropDatabaseStmt)
	original, _ := analyze.JumbleUtility(statement)
	copied := *statement
	moved := copied
	moved.Pos = parser.Position{Offset: 73, Character: 65}
	moved.Options = slices.Clone(statement.Options)
	for i := range moved.Options {
		moved.Options[i].Position += 73
	}
	value, _ := analyze.JumbleUtility(&copied)
	relocated, anchor := analyze.JumbleUtility(&moved)
	if original.QueryID() != value.QueryID() || original.QueryID() != relocated.QueryID() || anchor != 73 || statement.Pos.Offset != 0 {
		t.Fatal("copied AST or source coordinates changed raw identity")
	}
	empty, _ := analyze.JumbleUtility(nil)
	typed, anchor := analyze.JumbleUtility((*parser.DropDatabaseStmt)(nil))
	if typed.QueryID() != empty.QueryID() || anchor != 0 {
		t.Fatal("typed-nil DROP lacks closed projection")
	}
	query := "DROP /* 雪 */ DATABASE IF EXISTS \"pgss;drop\" WITH (FORCE, FORCE)"
	source := "SELECT ';'; \n\t" + query + " ; SELECT 2"
	statements, err := parser.Parse(source)
	if err != nil || len(statements) != 3 {
		t.Fatal(statements, err)
	}
	jumble, anchor := analyze.JumbleUtility(statements[1])
	if anchor != strings.Index(source, "DROP") {
		t.Fatal("DROP batch anchor", anchor)
	}
	normalized, raw := parser.NormalizeStatementText(source, anchor, jumble.HighestParameter, jumble.Locations, jumble.Lists, jumble.UtilityLocations, parser.Options{StandardConformingStrings: true})
	if normalized != query || raw != query || len(jumble.UtilityLocations) != 0 || len(jumble.Locations) != 0 || len(jumble.Lists) != 0 {
		t.Fatal("DROP utility representative lost raw syntax", normalized, raw)
	}
}
