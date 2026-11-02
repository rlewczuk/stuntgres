package enginetests

import (
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/parser"
)

func TestPgStatStatementsFingerprintUtilityRangeFunction(t *testing.T) {
	// REL_18_6 parsenodes.h RangeFunction selects lateral, ordinality,
	// is_rowsfrom, the ordered function/coldef pairs, alias and coldeflist.
	// These are raw CREATE VIEW children, not analyzed function sources.
	for _, tc := range []struct {
		name, left, right string
		same              bool
	}{
		{"quoted names", "string_to_table('a,b',',') AS t(v)", "\"string_to_table\"('a,b',',') AS \"t\"(\"v\")", true},
		{"location", "string_to_table('a,b',',')", "/* 雪 */ string_to_table('a,b',',')", true},
		{"argument", "string_to_table('a,b',',')", "string_to_table('c,d',',')", false},
		{"function", "generate_series(1,2)", "generate_subscripts(ARRAY[1,2],1)", false},
		{"lateral", "string_to_table('a,b',',')", "LATERAL string_to_table('a,b',',')", false},
		{"ordinality", "string_to_table('a,b',',')", "string_to_table('a,b',',') WITH ORDINALITY", false},
		{"rows from", "string_to_table('a,b',',')", "ROWS FROM(string_to_table('a,b',','))", false},
		{"regexp rows from", "regexp_split_to_table('a,b',',')", "ROWS FROM(regexp_split_to_table('a,b',','))", false},
		{"regexp location", "regexp_split_to_table('a,b',',')", "/* 雪 */ regexp_split_to_table('a,b',',')", true},
		{"regexp lateral", "regexp_split_to_table('a,b',',')", "LATERAL regexp_split_to_table('a,b',',')", false},
		{"regexp ordinality", "regexp_split_to_table('a,b',',')", "regexp_split_to_table('a,b',',') WITH ORDINALITY", false},
		{"regexp member column", "ROWS FROM(regexp_split_to_table('a,b',',') AS (v text))", "ROWS FROM(regexp_split_to_table('a,b',',') AS (w text))", false},
		{"member order", "ROWS FROM(generate_series(1,2),string_to_table('a,b',','))", "ROWS FROM(string_to_table('a,b',','),generate_series(1,2))", false},
		{"member column name", "ROWS FROM(f() AS (v integer))", "ROWS FROM(f() AS (w integer))", false},
		{"member column type", "ROWS FROM(f() AS (v integer))", "ROWS FROM(f() AS (v bigint))", false},
		{"member column collation", "ROWS FROM(f() AS (v text))", "ROWS FROM(f() AS (v text COLLATE \"C\"))", false},
		{"member column order", "ROWS FROM(f() AS (v integer,w text))", "ROWS FROM(f() AS (w text,v integer))", false},
		{"alias", "string_to_table('a,b',',') AS t(v)", "string_to_table('a,b',',') AS u(v)", false},
		{"column alias", "string_to_table('a,b',',') AS t(v)", "string_to_table('a,b',',') AS t(w)", false},
		{"top column name", "f() AS t(v integer)", "f() AS t(w integer)", false},
		{"top column type", "f() AS t(v integer)", "f() AS t(v bigint)", false},
		{"top column collation", "f() AS t(v text)", "f() AS t(v text COLLATE \"C\")", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			left, _ := analyze.JumbleUtility(enginetest.Statement(t, "CREATE VIEW pgss_raw_function AS SELECT * FROM "+tc.left))
			right, _ := analyze.JumbleUtility(enginetest.Statement(t, "CREATE VIEW pgss_raw_function AS SELECT * FROM "+tc.right))
			if left.QueryID() == 0 || right.QueryID() == 0 || (left.QueryID() == right.QueryID()) != tc.same {
				t.Fatalf("raw RangeFunction identity: same=%v, want %v", left.QueryID() == right.QueryID(), tc.same)
			}
		})
	}
}

func TestPgStatStatementsFingerprintUtilityRangeFunctionProvenance(t *testing.T) {
	statement := enginetest.Statement(t, "CREATE VIEW pgss_raw_function AS SELECT * FROM LATERAL ROWS FROM(f('a') AS (v text COLLATE \"C\")) WITH ORDINALITY AS t").(*parser.CreateView)
	source := statement.Query.From.(*parser.AliasReference).Input.(*parser.FunctionTable)
	first, _ := analyze.JumbleUtility(statement)
	call := &source.Calls[0]
	// Native diagnostic/capture provenance is not a released raw selected field.
	// In contrast, the call and each actual coldef remain selected above.
	source.Pos = parser.Position{Offset: 3, Character: 4}
	call.Pos = parser.Position{Offset: 7, Character: 8}
	call.WithinPos = parser.Position{Offset: 11, Character: 12}
	call.ProcedureCall = true
	call.ResolutionOnly = true
	call.CompositeCandidate = true
	call.StoredSQLRoutine = true
	call.StoredProviderRoutine = true
	call.StoredProviderSymbol = "different_capture"
	call.All = true // Explicit/default ALL is not a FuncCall field.
	call.Name.Parts[0].Pos = parser.Position{Offset: 15, Character: 16}
	call.ColumnDefinitions[0].Name.Pos = parser.Position{Offset: 19, Character: 20}
	call.ColumnDefinitions[0].Type.Pos = parser.Position{Offset: 23, Character: 24}
	call.ColumnDefinitions[0].Type.Quoted = true
	second, _ := analyze.JumbleUtility(statement)
	if first.QueryID() == 0 || first.QueryID() != second.QueryID() {
		t.Fatal("native provenance entered raw RangeFunction identity", first.QueryID(), second.QueryID())
	}

	absent, _ := analyze.JumbleUtility(&parser.Select{From: (*parser.FunctionTable)(nil)})
	empty, _ := analyze.JumbleUtility(&parser.Select{From: &parser.FunctionTable{}})
	if absent.QueryID() == 0 || empty.QueryID() == 0 || absent.QueryID() == empty.QueryID() {
		t.Fatal("nil raw function source collapsed with an empty present source")
	}
}

func TestPgStatStatementsFingerprintUtilityRegexpRangeFunctionProvenance(t *testing.T) {
	statement := enginetest.Statement(t, "CREATE VIEW pgss_raw_function AS SELECT * FROM ROWS FROM(regexp_split_to_table('a,b',','))").(*parser.CreateView)
	source := statement.Query.From.(*parser.FunctionFrom)
	if !source.RowsFrom || len(source.Calls) != 1 {
		t.Fatal("single regexp member lost explicit ROWS FROM provenance")
	}
	first, _ := analyze.JumbleUtility(statement)
	source.RowsFrom = false
	plain, _ := analyze.JumbleUtility(statement)
	if first.QueryID() == plain.QueryID() {
		t.Fatal("selected is_rowsfrom collapsed with ordinary function FROM")
	}
	source.RowsFrom = true
	source.Pos = parser.Position{Offset: 3, Character: 4}
	source.Calls[0].Pos = parser.Position{Offset: 7, Character: 8}
	source.Calls[0].ResolutionOnly = true
	source.Calls[0].StoredProviderRoutine = true
	source.Calls[0].StoredProviderSymbol = "different_capture"
	second, _ := analyze.JumbleUtility(statement)
	if first.QueryID() == 0 || first.QueryID() != second.QueryID() {
		t.Fatal("native regexp provenance entered raw RangeFunction identity")
	}
}
