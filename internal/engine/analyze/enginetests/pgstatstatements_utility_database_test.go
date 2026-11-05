package enginetests

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
)

func TestPgStatStatementsFingerprintUtilityDatabaseFields(t *testing.T) {
	// REL_18_6 parsenodes.h:3866-3877 and gram.y:11403-11434 select
	// dbname and the ordered DefElem list, or the distinct refresh node.
	// RenameStmt selects both names (gram.y:9369-9379). SET TABLESPACE
	// produces the same one-element option list as its general spelling.
	for _, tc := range []struct {
		name, left, right string
		same              bool
	}{
		{"rename_source", "ALTER DATABASE pgss_db RENAME TO pgss_next", "ALTER DATABASE pgss_other RENAME TO pgss_next", false},
		{"rename_target", "ALTER DATABASE pgss_db RENAME TO pgss_next", "ALTER DATABASE pgss_db RENAME TO pgss_other", false},
		{"rename_quotes", "ALTER DATABASE pgss_db RENAME TO pgss_next", "alter database \"pgss_db\" rename to \"pgss_next\"", true},
		{"rename_quoted_case", "ALTER DATABASE pgss_db RENAME TO pgss_next", "ALTER DATABASE pgss_db RENAME TO \"PGSS_NEXT\"", false},
		{"option_source", "ALTER DATABASE pgss_db CONNECTION LIMIT 3", "ALTER DATABASE pgss_other CONNECTION LIMIT 3", false},
		{"option_value", "ALTER DATABASE pgss_db CONNECTION LIMIT 3", "ALTER DATABASE pgss_db CONNECTION LIMIT 4", false},
		{"option_numeric_kind", "ALTER DATABASE pgss_db CONNECTION LIMIT 3", "ALTER DATABASE pgss_db CONNECTION LIMIT '3'", false},
		{"option_boolean_string", "ALTER DATABASE pgss_db ALLOW_CONNECTIONS true", "ALTER DATABASE pgss_db WITH allow_connections='true'", true},
		{"option_boolean_numeric_kind", "ALTER DATABASE pgss_db ALLOW_CONNECTIONS 1", "ALTER DATABASE pgss_db ALLOW_CONNECTIONS true", false},
		{"option_boolean_value", "ALTER DATABASE pgss_db ALLOW_CONNECTIONS true", "ALTER DATABASE pgss_db ALLOW_CONNECTIONS false", false},
		{"option_name", "ALTER DATABASE pgss_db IS_TEMPLATE false", "ALTER DATABASE pgss_db ALLOW_CONNECTIONS false", false},
		{"option_default_kind", "ALTER DATABASE pgss_db CONNECTION LIMIT DEFAULT", "ALTER DATABASE pgss_db CONNECTION LIMIT 'default'", false},
		{"option_order", "ALTER DATABASE pgss_db IS_TEMPLATE false ALLOW_CONNECTIONS true", "ALTER DATABASE pgss_db ALLOW_CONNECTIONS true IS_TEMPLATE false", false},
		{"option_duplicate", "ALTER DATABASE pgss_db CONNECTION LIMIT 3", "ALTER DATABASE pgss_db CONNECTION LIMIT 3 CONNECTION LIMIT 3", false},
		{"option_spelling", "ALTER DATABASE pgss_db CONNECTION LIMIT 3", " \n alter /* 雪 */ database \"pgss_db\" with connection_limit=3", true},
		{"tablespace_source", "ALTER DATABASE pgss_db SET TABLESPACE pg_default", "ALTER DATABASE pgss_other SET TABLESPACE pg_default", false},
		{"tablespace_target", "ALTER DATABASE pgss_db SET TABLESPACE pg_default", "ALTER DATABASE pgss_db SET TABLESPACE pg_global", false},
		{"tablespace_option_spelling", "ALTER DATABASE pgss_db SET TABLESPACE pg_default", "ALTER DATABASE pgss_db WITH TABLESPACE = pg_default", true},
		{"tablespace_option_string", "ALTER DATABASE pgss_db SET TABLESPACE pg_default", "ALTER DATABASE pgss_db TABLESPACE 'pg_default'", true},
		{"refresh_source", "ALTER DATABASE pgss_db REFRESH COLLATION VERSION", "ALTER DATABASE pgss_other REFRESH COLLATION VERSION", false},
		{"refresh_spelling", "ALTER DATABASE pgss_db REFRESH COLLATION VERSION", " \n alter /* 雪 */ database \"pgss_db\" refresh collation version", true},
		{"distinct_refresh_family", "ALTER DATABASE pgss_db REFRESH COLLATION VERSION", "ALTER DATABASE pgss_db", false},
		{"distinct_rename_family", "ALTER DATABASE pgss_db RENAME TO pg_default", "ALTER DATABASE pgss_db SET TABLESPACE pg_default", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			left := enginetest.Statement(t, tc.left)
			before := enginetest.Statement(t, tc.left)
			first, _ := analyze.JumbleUtility(left)
			second, _ := analyze.JumbleUtility(enginetest.Statement(t, tc.right))
			if first.QueryID() == 0 || second.QueryID() == 0 || (first.QueryID() == second.QueryID()) != tc.same {
				t.Fatalf("selected database fields: same=%v, want %v", first.QueryID() == second.QueryID(), tc.same)
			}
			if !reflect.DeepEqual(left, before) {
				t.Fatal("utility identity mutated retained syntax")
			}
		})
	}
}

func TestPgStatStatementsFingerprintUtilityDatabasePositions(t *testing.T) {
	for _, query := range []string{
		"ALTER DATABASE pgss_db RENAME TO pgss_next",
		"ALTER DATABASE pgss_db IS_TEMPLATE false CONNECTION LIMIT 3",
		"ALTER DATABASE pgss_db SET TABLESPACE pg_default",
		"ALTER DATABASE pgss_db REFRESH COLLATION VERSION",
	} {
		t.Run(query, func(t *testing.T) {
			source := " \n/* 雪 */ " + query
			statement := enginetest.Statement(t, source).(*parser.AlterDatabaseStmt)
			offset := strings.Index(source, "ALTER")
			if statement.Pos.Offset != offset || statement.Pos.Character != utf8.RuneCountInString(source[:offset])+1 {
				t.Fatal("ALTER lost its real top-level byte/character position", statement.Pos)
			}
			copied := *statement
			moved := copied
			moved.Pos = parser.Position{Offset: 73, Character: 65}
			moved.Options = slices.Clone(statement.Options)
			for i := range moved.Options {
				moved.Options[i].Position += 73
			}
			original, _ := analyze.JumbleUtility(statement)
			value, _ := analyze.JumbleUtility(&copied)
			relocated, anchor := analyze.JumbleUtility(&moved)
			if original.QueryID() == 0 || value.QueryID() != original.QueryID() || relocated.QueryID() != original.QueryID() || anchor != 73 {
				t.Fatal("copy/source coordinates changed identity or lost the real anchor", anchor)
			}
			if relocated.HighestParameter != 0 || len(relocated.Locations) != 0 || len(relocated.Lists) != 0 || len(relocated.UtilityLocations) != 0 {
				t.Fatal("selected database names/literals became normalization replacements", relocated)
			}
		})
	}
}

func TestPgStatStatementsFingerprintUtilityDatabaseRepresentatives(t *testing.T) {
	for _, query := range []string{
		"ALTER /* 雪 */ DATABASE \"pgss db\" RENAME TO \"pgss next\"",
		"ALTER DATABASE pgss_db WITH IS_TEMPLATE 'false' CONNECTION LIMIT 3",
		"ALTER DATABASE pgss_db SET TABLESPACE \"pg_default\"",
		"ALTER DATABASE pgss_db REFRESH COLLATION VERSION",
	} {
		t.Run(query, func(t *testing.T) {
			// The preceding quoted semicolon is not a batch separator. The
			// following SELECT must not enter the ALTER representative either.
			source := "SELECT '; 雪'; \n\t" + query + " ; SELECT 2"
			statements, err := parser.Parse(source)
			if err != nil || len(statements) != 3 {
				t.Fatal("batch parse", statements, err)
			}
			jumble, anchor := analyze.JumbleUtility(statements[1])
			if anchor != strings.Index(source, "ALTER") || jumble.HighestParameter != 0 || len(jumble.Locations) != 0 || len(jumble.Lists) != 0 || len(jumble.UtilityLocations) != 0 {
				t.Fatal("batch anchor or literal replacement set is incorrect", anchor, jumble)
			}
			normalized, raw := parser.NormalizeStatementText(source, anchor, jumble.HighestParameter, jumble.Locations, jumble.Lists, jumble.UtilityLocations, parser.Options{StandardConformingStrings: true})
			if normalized != query || raw != query {
				t.Fatalf("representatives normalized=%q raw=%q, want both %q", normalized, raw, query)
			}
		})
	}
}

func TestPgStatStatementsFingerprintUtilityDatabaseTypedNil(t *testing.T) {
	empty, _ := analyze.JumbleUtility(nil)
	jumble, anchor := analyze.JumbleUtility((*parser.AlterDatabaseStmt)(nil))
	if jumble.QueryID() == 0 || jumble.QueryID() != empty.QueryID() || anchor != 0 || jumble.HighestParameter != 0 || len(jumble.Locations) != 0 || len(jumble.Lists) != 0 || len(jumble.UtilityLocations) != 0 {
		t.Fatal("typed-nil database statement lacks the explicit empty projection", jumble, anchor)
	}
}
