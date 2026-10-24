package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLLoopDiagnostics(t *testing.T) {
	t.Run("when_projection_alias", func(t *testing.T) {
		loopScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			// This is a scalar projection alias, not a transfer label.
			body := "BEGIN <<x>> LOOP EXIT WHEN true x; END LOOP; END"
			for _, prepared := range []bool{false, true} {
				for _, q := range []string{"CREATE FUNCTION loop_syntax() RETURNS void LANGUAGE plpgsql AS $$" + body + "$$", "DO $$" + body + "$$"} {
					step := fixture.Exec(q, 0)
					if prepared {
						step = fixture.Prepared(step)
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
				}
				plpgsqlValue(t, p, b, c, "SELECT loop_syntax() IS NOT NULL AS v", "BOOL", true)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP FUNCTION loop_syntax()", 0))
			}
		})
	})
	for _, tc := range []struct{ name, body, message, createPosition, doPosition string }{
		{"exit_outside", "BEGIN EXIT; END", "EXIT cannot be used outside a loop, unless it has a label", "72", "12"},
		{"continue_outside", "BEGIN CONTINUE; END", "CONTINUE cannot be used outside a loop", "72", "12"},
		{"unknown_exit", "BEGIN LOOP EXIT absent; END LOOP; END", `there is no label "absent" attached to any block or loop enclosing this statement`, "82", "22"},
		{"unknown_continue", "BEGIN LOOP CONTINUE absent; END LOOP; END", `there is no label "absent" attached to any block or loop enclosing this statement`, "86", "26"},
		{"unknown_false", "BEGIN LOOP EXIT absent WHEN false; END LOOP; END", `there is no label "absent" attached to any block or loop enclosing this statement`, "82", "22"},
		{"unknown_null", "BEGIN LOOP CONTINUE absent WHEN NULL; END LOOP; END", `there is no label "absent" attached to any block or loop enclosing this statement`, "86", "26"},
		{"unknown_unreachable", "BEGIN RETURN; LOOP EXIT absent; END LOOP; END", `there is no label "absent" attached to any block or loop enclosing this statement`, "90", "30"},
		{"sibling", "BEGIN <<old>> LOOP EXIT; END LOOP; LOOP EXIT old; END LOOP; END", `there is no label "old" attached to any block or loop enclosing this statement`, "111", "51"},
		{"block_continue", "BEGIN <<b>> BEGIN LOOP CONTINUE b; END LOOP; END b; END", `block label "b" cannot be used in CONTINUE`, "98", "38"},
		{"nearer_block", "BEGIN <<x>> LOOP <<x>> BEGIN CONTINUE x; END x; END LOOP; END", `block label "x" cannot be used in CONTINUE`, "104", "44"},
		{"end_unlabeled", "BEGIN LOOP EXIT; END LOOP y; END", `end label "y" specified for unlabeled block`, "92", "32"},
		{"end_mismatch", "BEGIN <<x>> LOOP EXIT; END LOOP y; END", `end label "y" differs from block's label "x"`, "98", "38"},
		{"block_end_mismatch", "BEGIN <<x>> BEGIN END y; END", `end label "y" differs from block's label "x"`, "88", "28"},
		{"quoted_mismatch", `BEGIN <<"Mixed">> LOOP EXIT; END LOOP mixed; END`, `end label "mixed" differs from block's label "Mixed"`, "104", "44"},
		{"unicode_multiline", "DECLARE s text:='é';\nBEGIN\n LOOP EXIT absent; END LOOP;\nEND", `there is no label "absent" attached to any block or loop enclosing this statement`, "104", "44"},
		{"missing_end", "BEGIN LOOP END", "syntax error at end of input", "80", "20"},
		{"missing_loop_close", "BEGIN LOOP EXIT; END; END", `syntax error at or near ";"`, "86", "26"},
		{"missing_semicolon", "BEGIN LOOP EXIT END LOOP; END", `syntax error at or near "END"`, "82", "22"},
		{"exit_missing_when", "BEGIN LOOP EXIT WHEN; END LOOP; END", `missing expression at or near ";"`, "86", "26"},
		{"continue_missing_when", "BEGIN LOOP CONTINUE WHEN; END LOOP; END", `missing expression at or near ";"`, "90", "30"},
		{"malformed_when", "BEGIN LOOP EXIT WHEN 1+; END LOOP; END", "syntax error at end of input", "89", "29"},
		{"trailing", "BEGIN LOOP EXIT; END LOOP; END garbage garbage", `end label "garbage" specified for unlabeled block`, "97", "37"},
		{"unreachable_malformed", "DECLARE x integer; BEGIN RETURN; LOOP x:=1+; END LOOP; END", "syntax error at end of input", "109", "49"},
		{"continue_extra_label", "BEGIN <<x>> LOOP CONTINUE x x; END LOOP; END", `syntax error at or near "x"`, "94", "34"},
	} {
		for _, entry := range []string{"create", "do"} {
			t.Run(tc.name+"_"+entry, func(t *testing.T) {
				loopScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					q := "DO $$" + tc.body + "$$"
					if entry == "create" {
						q = "CREATE FUNCTION loop_syntax() RETURNS void LANGUAGE plpgsql AS $$" + tc.body + "$$"
					}
					want := fixture.ControlsReferenceDiagnostic("42601", tc.message)
					want.Position = tc.doPosition
					if entry == "create" {
						want.Position = tc.createPosition
					}
					plpgsqlError(t, p, c, q, false, want)
					if entry == "create" {
						plpgsqlValue(t, p, b, c, "SELECT count(*) AS v FROM pg_proc WHERE proname='loop_syntax'", "INT8", int64(0))
						userOperatorExec(t, p, b, c, "CREATE FUNCTION loop_syntax() RETURNS integer LANGUAGE plpgsql AS $$BEGIN LOOP EXIT; END LOOP; RETURN 7; END$$")
						plpgsqlValue(t, p, b, c, "SELECT loop_syntax() AS v", "INT4", int64(7))
					}
				})
			})
		}
	}
	for _, tc := range []struct{ name, statement, code, message string }{
		{"exit_division", "EXIT WHEN 1/0=0;", "22012", "division by zero"},
		{"continue_division", "CONTINUE WHEN 1/0=0;", "22012", "division by zero"},
		{"exit_bad_bool", "EXIT WHEN 'nope'::text;", "22P02", `invalid input syntax for type boolean: "nope"`},
		{"continue_bad_bool", "CONTINUE WHEN 'nope'::text;", "22P02", `invalid input syntax for type boolean: "nope"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loopScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				userOperatorExec(t, p, b, c, "CREATE FUNCTION loop_error() RETURNS integer LANGUAGE plpgsql AS $$BEGIN LOOP "+tc.statement+" EXIT; END LOOP; RETURN 7; END$$")
				action := "EXIT"
				if tc.name == "continue_division" || tc.name == "continue_bad_bool" {
					action = "CONTINUE"
				}
				want := plpgsqlRuntimeDiagnostic(tc.code, tc.message, "loop_error()", action)
				if tc.code == "22012" {
					want.Context = "PL/pgSQL expression \"1/0=0\"\n" + want.Context
				}
				plpgsqlError(t, p, c, "SELECT loop_error() AS v", true, want)
			})
		})
	}
}

func TestReferencePLpgSQLLoopHiddenLabel(t *testing.T) {
	for _, result := range []string{"scalar", "void", "out"} {
		for _, transfer := range []string{"EXIT loop_hidden;", "LOOP EXIT loop_hidden WHEN true; END LOOP;", "CONTINUE loop_hidden;"} {
			t.Run(result+"/"+transfer, func(t *testing.T) {
				loopScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					declaration, suffix := "() RETURNS integer", "RETURN 99;"
					if result == "void" {
						declaration, suffix = "() RETURNS void", "RETURN;"
					}
					if result == "out" {
						declaration, suffix = "(OUT v integer)", "v:=99; RETURN;"
					}
					q := "CREATE FUNCTION loop_hidden" + declaration + " LANGUAGE plpgsql AS $$DECLARE touched integer; BEGIN " + transfer + "touched:=nextval('loop_hidden_suffix'); " + suffix + " END$$"
					if transfer == "CONTINUE loop_hidden;" {
						want := fixture.ControlsReferenceDiagnostic("42601", `block label "loop_hidden" cannot be used in CONTINUE`)
						want.Position = "106"
						if result == "scalar" {
							want.Position = "109"
						}
						plpgsqlError(t, p, c, q, false, want)
						return
					}
					userOperatorExec(t, p, b, c, "CREATE SEQUENCE loop_hidden_suffix")
					userOperatorExec(t, p, b, c, q)
					want := fixture.ControlsReferenceDiagnostic("2F005", "control reached end of function without RETURN")
					want.Context = "PL/pgSQL function loop_hidden()"
					plpgsqlError(t, p, c, "SELECT loop_hidden() AS v", true, want)
					plpgsqlValue(t, p, b, c, "SELECT CASE WHEN is_called THEN 99 ELSE 0 END AS v FROM loop_hidden_suffix", "INT4", int64(0))
				})
			})
		}
	}
}
