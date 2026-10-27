package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLWhileDiagnostics(t *testing.T) {
	// Complete diagnostic records, including character Positions and absent
	// optional fields, observed on pinned PostgreSQL 18.6 in tests-1.md.
	for _, tc := range []struct{ name, body, message, createPosition, doPosition string }{
		{"unknown_exit", "BEGIN WHILE false LOOP EXIT absent; END LOOP; END", `there is no label "absent" attached to any block or loop enclosing this statement`, "95", "34"},
		{"unknown_continue", "BEGIN WHILE false LOOP CONTINUE absent; END LOOP; END", `there is no label "absent" attached to any block or loop enclosing this statement`, "99", "38"},
		{"block_continue", "BEGIN <<b>> BEGIN WHILE false LOOP CONTINUE b; END LOOP; END b; END", `block label "b" cannot be used in CONTINUE`, "111", "50"},
		{"nearer_block", "BEGIN <<x>> WHILE false LOOP <<x>> BEGIN CONTINUE x; END x; END LOOP; END", `block label "x" cannot be used in CONTINUE`, "117", "56"},
		{"end_unlabeled", "BEGIN WHILE false LOOP END LOOP y; END", `end label "y" specified for unlabeled block`, "99", "38"},
		{"end_mismatch", "BEGIN <<x>> WHILE false LOOP END LOOP y; END", `end label "y" differs from block's label "x"`, "105", "44"},
		{"decoded_label", `BEGIN WHILE false LOOP EXIT "é\""label"; END LOOP; END`, `there is no label "é\"label" attached to any block or loop enclosing this statement`, "95", "34"},
		{"unicode_multiline", "DECLARE s text:='é';\nBEGIN\n WHILE false LOOP EXIT absent; END LOOP;\nEND", `there is no label "absent" attached to any block or loop enclosing this statement`, "117", "56"},
		{"missing_condition", "BEGIN WHILE LOOP END LOOP; END", `missing expression at or near "LOOP"`, "79", "18"},
		{"missing_loop", "BEGIN WHILE true; END", `missing "LOOP" at end of SQL expression`, "83", "22"},
		{"missing_end", "BEGIN WHILE false LOOP END", "syntax error at end of input", "93", "32"},
		{"missing_loop_close", "BEGIN WHILE false LOOP END; END", `syntax error at or near ";"`, "93", "32"},
		{"missing_semicolon", "BEGIN WHILE false LOOP EXIT END LOOP; END", `syntax error at or near "END"`, "95", "34"},
		{"trailing", "BEGIN WHILE false LOOP END LOOP; END garbage garbage", `end label "garbage" specified for unlabeled block`, "104", "43"},
		{"unreachable_malformed", "DECLARE x integer; BEGIN WHILE false LOOP x:=1+; END LOOP; END", "syntax error at end of input", "114", "53"},
	} {
		for _, entry := range []string{"create", "do"} {
			t.Run(tc.name+"_"+entry, func(t *testing.T) {
				whileScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					q := "DO $$" + tc.body + "$$"
					if entry == "create" {
						q = "CREATE FUNCTION while_syntax() RETURNS void LANGUAGE plpgsql AS $$" + tc.body + "$$"
					}
					want := fixture.ControlsReferenceDiagnostic("42601", tc.message)
					want.Position = tc.doPosition
					if entry == "create" {
						want.Position = tc.createPosition
					}
					plpgsqlError(t, p, c, q, false, want)
					if entry == "create" {
						plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='while_syntax'", "INT8", int64(0))
						userOperatorExec(t, p, b, c, "CREATE FUNCTION while_syntax() RETURNS integer LANGUAGE plpgsql AS $$BEGIN WHILE false LOOP END LOOP; RETURN 7; END$$")
						plpgsqlValue(t, p, b, c, "SELECT while_syntax() AS v", "INT4", int64(7))
					}
				})
			})
		}
	}
	for _, tc := range []struct{ name, condition, code, message string }{
		{"text", "'nope'::text", "22P02", `invalid input syntax for type boolean: "nope"`},
		{"integer", "2", "22P02", `invalid input syntax for type boolean: "2"`},
		{"division", "1/0=0", "22012", "division by zero"},
		{"unicode_query", "/* é LOOP */ (while_missing())", "42883", "function while_missing() does not exist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			whileScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION while_error() RETURNS integer LANGUAGE plpgsql AS $$BEGIN WHILE "+tc.condition+" LOOP EXIT; END LOOP; RETURN 7; END$$")
				// Runtime contexts and optional-field absence were observed on
				// the same pinned oracle through direct and prepared execution.
				want := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "while_error()", "WHILE")
				if tc.code == "22012" {
					want.Context = "PL/pgSQL expression \"1/0=0\"\n" + want.Context
				}
				if tc.code == "42883" {
					want.Hint = parametersNoMatchHint
					want.InternalPosition, want.InternalQuery = "2", "(while_missing())"
				}
				plpgsqlError(t, p, c, "SELECT while_error() AS v", true, want)
			})
		})
	}
}
