package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceReplicationMessagesDiagnostics(t *testing.T) {
	type sample struct {
		query, code, message, hint string
		position                   int
	}
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	cases := []sample{}
	for _, tc := range []struct{ args, types string }{
		{"", ""}, {"true,'p'", "boolean, unknown"}, {"true,'p',1", "boolean, unknown, integer"}, {"1,'p','x'", "integer, unknown, unknown"},
		{"true,'p','x',false,true", "boolean, unknown, unknown, boolean, boolean"}, {"true,'p','x',1", "boolean, unknown, unknown, integer"},
		{"true,1,'x'", "boolean, integer, unknown"}, {`true,'\x00'::bytea,'x'`, "boolean, bytea, unknown"},
		{"content=>'x',transactional=>true,prefix=>'p'", "content => unknown, transactional => boolean, prefix => unknown"},
		{"message=>'x',prefix=>'p',transactional=>true,bogus=>false", "message => unknown, prefix => unknown, transactional => boolean, bogus => boolean"},
		{`"MESSAGE"=>'x',prefix=>'p',transactional=>true`, "MESSAGE => unknown, prefix => unknown, transactional => boolean"},
	} {
		cases = append(cases, sample{"SELECT pg_logical_emit_message(" + tc.args + ")", "42883", "function pg_logical_emit_message(" + tc.types + ") does not exist", hint, 8})
	}
	cases = append(cases,
		sample{"SELECT pg_sync_replication_slots(NULL)", "42883", "function pg_sync_replication_slots(unknown) does not exist", hint, 8},
		sample{"SELECT pg_logical_emit_message('bad','p','x')", "22P02", `invalid input syntax for type boolean: "bad"`, "", 32},
		sample{"SELECT pg_logical_emit_message('bad','p',NULL::text)", "22P02", `invalid input syntax for type boolean: "bad"`, "", 32},
		sample{"SELECT pg_logical_emit_message(NULL,'p','x','bad')", "22P02", `invalid input syntax for type boolean: "bad"`, "", 45},
	)
	duplicate := "SELECT pg_logical_emit_message(message=>'x',message=>'y',transactional=>true,prefix=>'p')"
	positional := "SELECT pg_logical_emit_message(transactional=>true,'p','x')"
	cases = append(cases, sample{duplicate, "42601", `argument name "message" used more than once`, "", strings.LastIndex(duplicate, "message=>") + 1}, sample{positional, "42601", "positional argument cannot follow named argument", "", strings.Index(positional, "'p'") + 1})
	for _, q := range []string{`SELECT pg_logical_emit_message(true,'p','\xGG'::bytea)`, `SELECT pg_logical_emit_message(NULL,'p','\xGG'::bytea)`, `SELECT pg_logical_emit_message(true,NULL,'\xGG'::bytea)`} {
		cases = append(cases, sample{q, "22023", `invalid hexadecimal digit: "G"`, "", strings.Index(q, `'\xGG'`) + 1})
	}
	cases = append(cases, sample{"SELECT pg_logical_emit_message(" + strings.Repeat("true,", 100) + "true)", "54023", "cannot pass more than 100 arguments to a function", "", 8})
	for _, name := range []string{"pg_logical_emit_message", "pg_sync_replication_slots"} {
		args := ""
		types := ""
		if name == "pg_logical_emit_message" {
			args = "true,'p','x'"
			types = "boolean, unknown, unknown"
		}
		if name == "pg_sync_replication_slots" {
			cases = append(cases, sample{"SELECT " + name + "(*)", "42809", name + "(*) specified, but " + name + " is not an aggregate function", "", 8})
		}
		cases = append(cases,
			sample{"SELECT " + name + "(" + args + ") OVER ()", "42809", "OVER specified, but " + name + " is not a window function nor an aggregate function", "", 8},
			sample{"SELECT " + name + "(" + args + ") FILTER (WHERE true)", "42809", "FILTER specified, but " + name + " is not an aggregate function", "", 8},
			sample{"SELECT public." + name + "(" + args + ")", "42883", "function public." + name + "(" + types + ") does not exist", hint, 8},
			sample{"SELECT missing_replication_messages_schema." + name + "(" + args + ")", "3F000", `schema "missing_replication_messages_schema" does not exist`, "", 8},
		)
	}
	cases = append(cases,
		sample{"SELECT pg_logical_emit_message(DISTINCT true,'p','x')", "42809", "DISTINCT specified, but pg_logical_emit_message is not an aggregate function", "", 8},
		sample{"SELECT pg_logical_emit_message(true,'p','x' ORDER BY 1)", "42809", "ORDER BY specified, but pg_logical_emit_message is not an aggregate function", "", 8},
		sample{"SELECT * FROM pg_logical_emit_message()", "42883", "function pg_logical_emit_message() does not exist", hint, 15},
		sample{"/*é🙂*/ SELECT pg_logical_emit_message()", "42883", "function pg_logical_emit_message() does not exist", hint, 15},
		sample{"SELECT pg_logical_emit_message(true,'p','x') WITHIN GROUP (ORDER BY 1)", "42883", "function pg_logical_emit_message(boolean, unknown, unknown, integer) does not exist", hint, 8},
		sample{"SELECT pg_logical_emit_message(true,'p','x') WITHIN GROUP (ORDER BY false)", "42809", "WITHIN GROUP specified, but pg_logical_emit_message is not an aggregate function", "", 8},
	)
	for i, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			recoveryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: fixture.Position(tc.position)})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
				// The UTF8 prefix contributes seven characters, never its byte length.
				if i == 0 || tc.code == "22023" {
					s := fixture.ImplicitError("/*é🙂*/ "+tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Hint: tc.hint, Position: fixture.Position(tc.position + utf8.RuneCountInString("/*é🙂*/ "))})
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					s.Prepared, s.ErrorStage = true, "prepare"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		})
	}
}
