package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbSetTypingEdges(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, tc := range []struct{ call, sig string }{
		{`jsonb_set('{}','{a}',1)`, `jsonb_set(unknown, unknown, integer)`},
		{`jsonb_set('{}','{a}'::text,'2')`, `jsonb_set(unknown, text, unknown)`},
		{`jsonb_set_lax('{}','{a}',NULL,true,true)`, `jsonb_set_lax(unknown, unknown, unknown, boolean, boolean)`},
		{`jsonb_set_lax('{}','{a}',NULL,true,'delete_key',true)`, `jsonb_set_lax(unknown, unknown, unknown, boolean, unknown, boolean)`},
		{`jsonb_set(jsonb_in => '{}',replacement => '2')`, `jsonb_set(jsonb_in => unknown, replacement => unknown)`},
		{`jsonb_set('{}','{a}','2',jsonb_in => '{}')`, `jsonb_set(unknown, unknown, unknown, jsonb_in => unknown)`},
	} {
		t.Run(tc.call, func(t *testing.T) {
			d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.sig+" does not exist")
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			d.Position = "8"
			s := jsonbSetError("SELECT "+tc.call, d)
			run(s)
			s.Prepared = true
			s.ErrorStage = "prepare"
			run(s)
		})
	}
	q := `SELECT jbs_hidden.jsonb_set('{}','{a}','2')`
	d := fixture.ControlsReferenceDiagnostic("3F000", `schema "jbs_hidden" does not exist`)
	d.Position = "8"
	s := jsonbSetError(q, d)
	run(s)
	s.Prepared = true
	s.ErrorStage = "prepare"
	run(s)
	for _, tc := range []struct{ args, token, msg, detail, context string }{
		{`'{}','{a,,b}','2'`, `'{a,,b}'`, `malformed array literal: "{a,,b}"`, `Unexpected "," character.`, ""},
		{`NULL,'{a}','2','bad'`, `'bad'`, `invalid input syntax for type boolean: "bad"`, "", ""},
		{`'{}','{a}','TRUE'`, `'TRUE'`, `invalid input syntax for type json`, `Token "TRUE" is invalid.`, `JSON data, line 1: TRUE`},
	} {
		t.Run(tc.args, func(t *testing.T) {
			q := "/*é*/ SELECT jsonb_set(" + tc.args + ")"
			d := fixture.ControlsReferenceDiagnostic("22P02", tc.msg)
			d.Detail = tc.detail
			d.Context = tc.context
			d.Position = fixture.Position(len([]rune(q[:strings.Index(q, tc.token)])) + 1)
			s := jsonbSetError(q, d)
			run(s)
			s.Prepared = true
			s.ErrorStage = "prepare"
			run(s)
		})
	}
	// Immutable literal calls are evaluated by both preflight paths, even
	// where a runtime column call would not be demanded.
	for _, suffix := range []string{" WHERE false", " FROM (SELECT 1 AS id) AS x WHERE false"} {
		jsonbSetBoth(t, p, b, c, jsonbSetError("SELECT jsonb_set('[1]','{x}','2')"+suffix, fixture.ControlsReferenceDiagnostic("22P02", `path element at position 1 is not an integer: "x"`)))
	}
}
