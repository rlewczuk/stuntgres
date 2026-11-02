package enginetests

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
)

func TestPgStatStatementsFingerprintUtilityTablespaceFields(t *testing.T) {
	// REL_18_6 parsenodes.h:3615-3627 and gram.y:9842-9851 map
	// TABLESPACE rename to RenameStmt.subname/newname. Both names are selected;
	// the remaining RenameStmt fields are fixed by this grammar alternative.
	// DropTableSpaceStmt selects tablespacename/missing_ok (parsenodes.h:
	// 2877-2882, gram.y:5174-5190). No name is a replacement location.
	for _, tc := range []struct {
		name, left, right string
		same              bool
	}{
		{"rename_source", "ALTER TABLESPACE pg_default RENAME TO pgss_space", "ALTER TABLESPACE pg_global RENAME TO pgss_space", false},
		{"rename_target", "ALTER TABLESPACE pg_default RENAME TO pgss_space", "ALTER TABLESPACE pg_default RENAME TO pgss_other", false},
		{"rename_quote_provenance", "ALTER TABLESPACE pg_default RENAME TO pgss_space", "ALTER TABLESPACE \"pg_default\" RENAME TO \"pgss_space\"", true},
		{"rename_quoted_case", "ALTER TABLESPACE pg_default RENAME TO pgss_space", "ALTER TABLESPACE pg_default RENAME TO \"PGSS_SPACE\"", false},
		{"rename_spelling_and_positions", "ALTER TABLESPACE pg_default RENAME TO pgss_space", " \n alter /* 雪 */ tablespace pg_default rename to pgss_space", true},
		{"drop_name", "DROP TABLESPACE IF EXISTS pgss_missing", "DROP TABLESPACE IF EXISTS pgss_other", false},
		{"drop_missing_ok", "DROP TABLESPACE pgss_missing", "DROP TABLESPACE IF EXISTS pgss_missing", false},
		{"drop_quote_provenance", "DROP TABLESPACE IF EXISTS pgss_missing", "DROP TABLESPACE IF EXISTS \"pgss_missing\"", true},
		{"drop_quoted_case", "DROP TABLESPACE IF EXISTS pgss_missing", "DROP TABLESPACE IF EXISTS \"PGSS_MISSING\"", false},
		{"drop_spelling_and_positions", "DROP TABLESPACE IF EXISTS pgss_missing", " \n drop /* 雪 */ tablespace if exists pgss_missing", true},
		{"distinct_statement_families", "ALTER TABLESPACE pg_default RENAME TO pgss_space", "DROP TABLESPACE IF EXISTS pg_default", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			left, _ := analyze.JumbleUtility(enginetest.Statement(t, tc.left))
			right, _ := analyze.JumbleUtility(enginetest.Statement(t, tc.right))
			if left.QueryID() == 0 || right.QueryID() == 0 {
				t.Fatal("empty tablespace utility identity")
			}
			if (left.QueryID() == right.QueryID()) != tc.same {
				t.Fatalf("selected tablespace fields: same=%v, want %v", left.QueryID() == right.QueryID(), tc.same)
			}
		})
	}
}

func TestPgStatStatementsFingerprintUtilityTablespacePositions(t *testing.T) {
	for _, source := range []string{
		"ALTER TABLESPACE pg_default RENAME TO pgss_space",
		"DROP TABLESPACE IF EXISTS pgss_missing",
	} {
		t.Run(source, func(t *testing.T) {
			statement := enginetest.Statement(t, source)
			var copied, relocated parser.Statement
			switch s := statement.(type) {
			case *parser.AlterTablespaceRename:
				if s.Name.Text != "pg_default" || s.NewName.Text != "pgss_space" {
					t.Fatal("parsed rename lost its selected names", s)
				}
				value := *s
				copied = &value
				moved := value
				moved.Pos = parser.Position{Offset: 73, Character: 65}
				moved.Name.Pos = parser.Position{Offset: 90, Character: 82}
				moved.NewName.Pos = parser.Position{Offset: 115, Character: 107}
				relocated = &moved
			case *parser.DropTablespace:
				if s.Name.Text != "pgss_missing" || !s.IfExists {
					t.Fatal("parsed drop lost its selected name/missing_ok", s)
				}
				value := *s
				copied = &value
				moved := value
				moved.Pos = parser.Position{Offset: 73, Character: 65}
				moved.Name.Pos = parser.Position{Offset: 99, Character: 91}
				relocated = &moved
			default:
				t.Fatalf("expected executable tablespace AST, got %T", statement)
			}
			original, _ := analyze.JumbleUtility(statement)
			// Statement has pointer-only methods: copy the actual parsed value
			// and pass its address, without reflection or an inspection export.
			value, _ := analyze.JumbleUtility(copied)
			moved, anchor := analyze.JumbleUtility(relocated)
			if original.QueryID() == 0 || value.QueryID() != original.QueryID() || moved.QueryID() != original.QueryID() {
				t.Fatal("copied value or ignored statement/identifier positions changed identity")
			}
			if anchor != 73 {
				t.Fatalf("relocated statement anchor = %d, want 73", anchor)
			}
			if len(moved.Locations) != 0 || len(moved.Lists) != 0 || len(moved.UtilityLocations) != 0 || moved.HighestParameter != 0 {
				t.Fatal("selected tablespace names became normalization replacements", moved)
			}
		})
	}
}

func TestPgStatStatementsFingerprintUtilityTablespaceTypedNil(t *testing.T) {
	empty, _ := analyze.JumbleUtility(nil)
	for _, tc := range []struct {
		name   string
		node   parser.Statement
		source string
	}{
		{"rename", (*parser.AlterTablespaceRename)(nil), "ALTER TABLESPACE pg_default RENAME TO pgss_space"},
		{"drop", (*parser.DropTablespace)(nil), "DROP TABLESPACE IF EXISTS pgss_missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jumble, anchor := analyze.JumbleUtility(tc.node)
			if jumble.QueryID() == 0 || jumble.QueryID() != empty.QueryID() {
				t.Fatal("typed-nil statement lacks the explicit empty projection")
			}
			if anchor != 0 || jumble.HighestParameter != 0 || len(jumble.Locations) != 0 || len(jumble.Lists) != 0 || len(jumble.UtilityLocations) != 0 {
				t.Fatal("typed-nil statement retained source state", jumble, anchor)
			}
			present, _ := analyze.JumbleUtility(enginetest.Statement(t, tc.source))
			if present.QueryID() == jumble.QueryID() {
				t.Fatal("parsed nonnil statement received the empty projection")
			}
		})
	}
}

func TestPgStatStatementsFingerprintUtilityTablespaceRepresentatives(t *testing.T) {
	for _, tc := range []struct{ source, want string }{
		{"ALTER TABLESPACE pg_default RENAME TO pgss_space", "ALTER TABLESPACE pg_default RENAME TO pgss_space"},
		{" \tALTER /* 雪 */ TABLESPACE \"pg_default\" RENAME TO \"pgss space\"; \n", "ALTER /* 雪 */ TABLESPACE \"pg_default\" RENAME TO \"pgss space\""},
		{"alter tablespace pg_default rename to \"pgss \"\"雪\";", "alter tablespace pg_default rename to \"pgss \"\"雪\""},
		{"DROP TABLESPACE pgss_missing", "DROP TABLESPACE pgss_missing"},
		{"DROP TABLESPACE IF EXISTS pgss_missing", "DROP TABLESPACE IF EXISTS pgss_missing"},
		{" \tdrop /* keep 雪 */ tablespace if exists \"pgss missing\"; \n", "drop /* keep 雪 */ tablespace if exists \"pgss missing\""},
	} {
		t.Run(tc.source, func(t *testing.T) {
			jumble, anchor := analyze.JumbleUtility(enginetest.Statement(t, tc.source))
			if jumble.HighestParameter != 0 || len(jumble.Locations) != 0 || len(jumble.Lists) != 0 || len(jumble.UtilityLocations) != 0 {
				t.Fatal("selected names must remain in the representative", jumble)
			}
			normalized, raw := parser.NormalizeStatementText(tc.source, anchor, jumble.HighestParameter, jumble.Locations, jumble.Lists, jumble.UtilityLocations, parser.Options{StandardConformingStrings: true})
			if normalized != tc.want || raw != tc.want {
				t.Fatalf("representatives normalized=%q raw=%q, want both %q", normalized, raw, tc.want)
			}
		})
	}
}
