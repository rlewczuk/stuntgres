package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func conditionalBody(t *testing.T, body string, value int64) {
	t.Helper()
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE FUNCTION conditional_value() RETURNS integer LANGUAGE plpgsql AS $$"+body+"$$")
		plpgsqlValue(t, p, b, c, "SELECT conditional_value() AS v", "INT4", value)
	})
}

func TestReferencePLpgSQLConditionalIf(t *testing.T) {
	t.Run("inputs", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION conditional_if(x boolean) RETURNS integer LANGUAGE plpgsql AS $$BEGIN IF x THEN RETURN 11; ELSE RETURN 22; END IF; END$$")
			q := "SELECT conditional_if($1) AS v"
			for _, tc := range []struct {
				input any
				value int64
			}{{true, 11}, {false, 22}, {nil, 22}} {
				parametersValue(t, p, b, c, q, "v", "INT4", tc.value, tc.input)
			}
			parametersHeld(t, p, b, c, q, "INT4", [][]any{{true}, {false}, {nil}, {true}}, []any{int64(11), int64(22), int64(22), int64(11)})
		})
	})
	for _, tc := range []struct {
		name, body string
		value      int64
	}{
		{"false_no_else", "DECLARE x integer := 7; BEGIN IF false THEN x := 9; END IF; RETURN x; END", 7},
		{"null_no_else", "DECLARE x integer := 7; BEGIN IF NULL THEN x := 9; END IF; RETURN x; END", 7},
		{"ordered_elsif", "DECLARE x integer := 7; BEGIN IF NULL THEN x := 10; ELSIF false THEN x := 11; ELSIF NULL THEN x := 12; ELSIF true THEN x := 20; ELSIF true THEN x := 30; ELSE x := 40; END IF; RETURN x; END", 20},
		{"elseif_alias", "BEGIN IF false THEN RETURN 10; ELSEIF true THEN RETURN 20; ELSE RETURN 30; END IF; END", 20},
		{"skip_missing_elsif", "BEGIN IF true THEN RETURN 11; ELSIF conditional_missing() THEN RETURN 99; END IF; END", 11},
		{"skip_division_elsif", "BEGIN IF true THEN RETURN 11; ELSIF 1/0=0 THEN RETURN 99; END IF; END", 11},
		{"empty_selected", "BEGIN IF true THEN ELSE RETURN 99; END IF; RETURN 7; END", 7},
		{"sql_case", "BEGIN IF (CASE WHEN true THEN 'yes'::text ELSE 'off'::text END) THEN RETURN 11; END IF; RETURN 22; END", 11},
	} {
		t.Run(tc.name, func(t *testing.T) { conditionalBody(t, tc.body, tc.value) })
	}
}

func TestReferencePLpgSQLConditionalSearchedCase(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		value      int64
	}{
		{"ordered", "BEGIN CASE WHEN NULL THEN RETURN 90; WHEN false THEN RETURN 91; WHEN true THEN RETURN 21; WHEN conditional_missing() THEN RETURN 99; ELSE RETURN 29; END CASE; END", 21},
		{"else", "BEGIN CASE WHEN NULL THEN RETURN 90; WHEN false THEN RETURN 91; ELSE RETURN 29; END CASE; END", 29},
		{"empty_else", "BEGIN CASE WHEN false THEN RETURN 90; ELSE END CASE; RETURN 7; END", 7},
		{"matched_empty", "BEGIN CASE WHEN true THEN ELSE RETURN 90; END CASE; RETURN 7; END", 7},
		{"matched_without_else", "BEGIN CASE WHEN true THEN RETURN 23; END CASE; END", 23},
		{"sql_case", "BEGIN CASE WHEN (CASE WHEN true THEN true ELSE false END) THEN RETURN 21; ELSE RETURN 29; END CASE; END", 21},
	} {
		t.Run(tc.name, func(t *testing.T) { conditionalBody(t, tc.body, tc.value) })
	}
	t.Run("absent_else", func(t *testing.T) {
		conditionalRuntimeCase(t, "BEGIN CASE WHEN NULL THEN RETURN 1; WHEN false THEN RETURN 2; END CASE; RETURN 7; END", "20000", "case not found", "CASE")
	})
}

func TestReferencePLpgSQLConditionalSimpleCase(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		value      int64
	}{
		{"first_list_match", "BEGIN CASE 2 WHEN 1,2 THEN RETURN 12; WHEN 2 THEN RETURN 99; ELSE RETURN 30; END CASE; END", 12},
		{"else", "BEGIN CASE 3 WHEN 1,2 THEN RETURN 12; ELSE RETURN 30; END CASE; END", 30},
		{"null_not_nullsafe", "BEGIN CASE NULL::integer WHEN NULL THEN RETURN 12; ELSE RETURN 30; END CASE; END", 30},
		{"common_type", "BEGIN CASE 2 WHEN 1::bigint,'2' THEN RETURN 12; ELSE RETURN 30; END CASE; END", 12},
		{"typed_null", "BEGIN CASE NULL::varchar(3) WHEN 'x' THEN RETURN 12; ELSE RETURN 30; END CASE; END", 30},
		{"collation", "BEGIN CASE 'a'::text COLLATE \"C\" WHEN 'A' THEN RETURN 99; WHEN 'a' THEN RETURN 12; ELSE RETURN 30; END CASE; END", 12},
		{"skip_missing_when", "BEGIN CASE 2 WHEN 2 THEN RETURN 12; WHEN conditional_missing() THEN RETURN 99; ELSE RETURN 30; END CASE; END", 12},
		{"skip_division_when", "BEGIN CASE 2 WHEN 2 THEN RETURN 12; WHEN 1/0 THEN RETURN 99; ELSE RETURN 30; END CASE; END", 12},
		{"empty_else", "BEGIN CASE 2 WHEN 1 THEN RETURN 99; ELSE END CASE; RETURN 7; END", 7},
		{"matched_empty", "BEGIN CASE 2 WHEN 2 THEN ELSE RETURN 99; END CASE; RETURN 7; END", 7},
		{"sql_case_search_item", "BEGIN CASE (CASE WHEN true THEN 2 ELSE 3 END) WHEN (CASE WHEN false THEN 1 ELSE 2 END),3 THEN RETURN 12; ELSE RETURN 30; END CASE; END", 12},
	} {
		t.Run(tc.name, func(t *testing.T) { conditionalBody(t, tc.body, tc.value) })
	}
	t.Run("absent_else", func(t *testing.T) {
		conditionalRuntimeCase(t, "BEGIN CASE NULL::integer WHEN NULL THEN RETURN 1; END CASE; RETURN 7; END", "20000", "case not found", "CASE")
	})
	t.Run("search_once", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE SEQUENCE conditional_once", "CREATE FUNCTION conditional_once_value() RETURNS integer LANGUAGE plpgsql AS $$BEGIN CASE nextval('conditional_once') WHEN 1 THEN RETURN 10; WHEN 2 THEN RETURN 20; ELSE RETURN 30; END CASE; END$$")
			// Use one held statement: fresh/direct duplication would itself advance the sequence.
			parametersHeld(t, p, b, c, "SELECT conditional_once_value() AS v", "INT4", [][]any{{}, {}}, []any{int64(10), int64(20)})
			plpgsqlValue(t, p, b, c, "SELECT currval('conditional_once') AS v", "INT8", int64(2))
		})
	})
}

func TestReferencePLpgSQLConditionalNested(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		value      int64
	}{
		{"if_case_if", "BEGIN IF true THEN CASE 2 WHEN 2 THEN IF NULL THEN RETURN 99; ELSE RETURN 41; END IF; ELSE RETURN 90; END CASE; END IF; RETURN 1/0; END", 41},
		{"case_if_case", "BEGIN CASE WHEN true THEN IF true THEN CASE 2 WHEN 2 THEN RETURN 41; ELSE RETURN 90; END CASE; END IF; END CASE; RETURN 1/0; END", 41},
		{"branch_same_scope", "DECLARE x integer := 10; BEGIN IF true THEN CASE x WHEN 10 THEN x := 21; END CASE; END IF; RETURN x; END", 21},
		{"block_scope", "DECLARE x integer := 10; BEGIN IF true THEN DECLARE x integer := 20; BEGIN CASE x WHEN 20 THEN x := 21; END CASE; END; END IF; RETURN x; END", 10},
	} {
		t.Run(tc.name, func(t *testing.T) { conditionalBody(t, tc.body, tc.value) })
	}
	t.Run("held_frames", func(t *testing.T) {
		plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			userOperatorExec(t, p, b, c, "CREATE FUNCTION conditional_frames(x integer) RETURNS integer LANGUAGE plpgsql AS $$DECLARE r integer := 7; BEGIN IF x IS NULL THEN RETURN r; ELSE DECLARE r integer := 20; BEGIN CASE x WHEN 1 THEN r := r+1; ELSE r := r+2; END CASE; RETURN r; END; END IF; RETURN 1/0; END$$")
			parametersHeld(t, p, b, c, "SELECT conditional_frames($1) AS v", "INT4", [][]any{{1}, {nil}, {2}, {1}}, []any{int64(21), int64(7), int64(22), int64(21)})
		})
	})
}
