package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbInsertHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := "SELECT jsonb_insert($1,$2,$3,$4) AS changed"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
	good := jsonbSetMetadataStep(jsonbSetSelect(q, `[0, "x", 1, 2]`, "[0,1,2]", "{1}", `"x"`, false), b.PG)
	run(good)
	for _, tc := range []struct {
		source, path, replacement string
		after                     any
		want                      any
	}{
		{"[0,1,2]", "{1}", `"x"`, true, `[0, 1, "x", 2]`},
		{"[0,1,2]", "{-1}", `"x"`, true, `[0, 1, 2, "x"]`},
		{"[0,1,2]", "{-99}", `"x"`, false, `["x", 0, 1, 2]`},
		{"[0,1,2]", "{99}", `"x"`, true, `[0, 1, 2, "x"]`},
		{"{}", "{a}", "9", false, `{"a": 9}`},
		{"[0]", "{0}", "9", nil, nil},
	} {
		run(jsonbSetMetadataStep(jsonbSetSelect(q, tc.want, tc.source, tc.path, tc.replacement, tc.after), b.PG))
	}
	for slot := range 4 {
		args := []any{"[0]", "{0}", "9", false}
		args[slot] = nil
		run(jsonbSetSelect(q, nil, args...))
	}
	run(jsonbSetError(q, jsonbInsertCollision(), `{"a":1}`, "{a}", "9", false))
	run(good)
	run(jsonbSetError(q, fixture.ControlsReferenceDiagnostic("22P02", `path element at position 1 is not an integer: "bad"`), "[0]", "{bad}", "9", true))
	run(good)
	for _, tc := range []struct {
		slot                            int
		value, message, detail, context string
	}{
		{1, "{a,,b}", `malformed array literal: "{a,,b}"`, `Unexpected "," character.`, "unnamed portal parameter $2 = '...'"},
		{3, "bad", `invalid input syntax for type boolean: "bad"`, "", "unnamed portal parameter $4 = '...'"},
		{0, "TRUE", "invalid input syntax for type json", `Token "TRUE" is invalid.`, "JSON data, line 1: TRUE\nunnamed portal parameter $1 = '...'"},
		{2, "TRUE", "invalid input syntax for type json", `Token "TRUE" is invalid.`, "JSON data, line 1: TRUE\nunnamed portal parameter $3 = '...'"},
	} {
		args := []any{"[0]", "{0}", "9", false}
		args[tc.slot] = tc.value
		d := fixture.ControlsReferenceDiagnostic("22P02", tc.message)
		d.Detail, d.Context = tc.detail, tc.context
		run(jsonbSetError(q, d, args...))
		run(good)
	}
	owned, replacement := []byte("[0]"), []byte("9")
	rows, err := stmt.QueryContext(p.Ctx, owned, []byte("{0}"), replacement, false)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing byte-input row", rows.Err())
	}
	var first string
	if err := rows.Scan(&first); err != nil {
		t.Fatal(err)
	}
	for i := range owned {
		owned[i] = 'x'
	}
	replacement[0] = 'x'
	var again string
	if err := rows.Scan(&again); err != nil || first != "[9, 0]" || again != first {
		t.Fatalf("first=%q again=%q err=%v", first, again, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected byte-input tail", rows.Err())
	}
	rows.Close()
	run(good)
	defaultSQL := "SELECT jsonb_insert($1,$2,$3) AS changed"
	defaults, err := c.PrepareContext(p.Ctx, defaultSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer defaults.Close()
	for range 2 {
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: defaults, SQL: defaultSQL}, b.Recorder, jsonbSetSelect(defaultSQL, "[9, 0]", "[0]", "{0}", "9"))
	}
}

func TestReferenceJSONBInsertTypingAndParameters(t *testing.T) {
	jsonbInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE DOMAIN jbi_doc AS jsonb", "CREATE DOMAIN jbi_path AS text[]", "CREATE DOMAIN jbi_flag AS boolean"} {
			run(fixture.Exec(q, 0))
		}
		for _, call := range []string{
			`pg_catalog.jsonb_insert('[0]','{0}','9')`, `"jsonb_insert"('[0]','{0}','9')`, `JSONB_INSERT('[0]','{0}','9',false)`,
			`jsonb_insert('[0]'::json::jsonb,'{0}'::varchar[],'9')`,
			`jsonb_insert('[0]'::jbi_doc,'{0}'::jbi_path,'9'::jbi_doc,false::jbi_flag)`,
			`jsonb_insert(replacement => '9',path => '{0}',jsonb_in => '[0]')`,
			`jsonb_insert('[0]',replacement := '9',path := '{0}')`,
			`jsonb_insert('[0]','{0}',VARIADIC '9'::jsonb)`,
		} {
			t.Run(call, func(t *testing.T) { jsonbSetBoth(t, p, b, c, jsonbSetSelect("SELECT "+call+" AS changed", "[9, 0]")) })
		}
		for _, call := range []string{`jsonb_insert(insert_after => true,replacement => '9',path => '{0}',jsonb_in => '[0]')`, `jsonb_insert('[0]','{0}','9',VARIADIC true)`} {
			t.Run(call, func(t *testing.T) { jsonbSetBoth(t, p, b, c, jsonbSetSelect("SELECT "+call+" AS changed", "[0, 9]")) })
		}
		for _, tc := range []struct{ call, sig string }{
			{`jsonb_insert()`, `jsonb_insert()`}, {`jsonb_insert('{}','{a}')`, `jsonb_insert(unknown, unknown)`},
			{`jsonb_insert('{}','{a}','2',true,true)`, `jsonb_insert(unknown, unknown, unknown, boolean, boolean)`},
			{`jsonb_insert(1,'{a}','2')`, `jsonb_insert(integer, unknown, unknown)`},
			{`jsonb_insert('{}'::json,'{a}','2')`, `jsonb_insert(json, unknown, unknown)`},
			{`jsonb_insert('{}'::text,'{a}','2')`, `jsonb_insert(text, unknown, unknown)`},
			{`jsonb_insert('{}','{a}','2'::text)`, `jsonb_insert(unknown, unknown, text)`},
			{`jsonb_insert('{}',ARRAY[1],'2')`, `jsonb_insert(unknown, integer[], unknown)`},
			{`jsonb_insert('{}','{a}','2',1)`, `jsonb_insert(unknown, unknown, unknown, integer)`},
			{`"JSONB_INSERT"('{}','{a}','2')`, `JSONB_INSERT(unknown, unknown, unknown)`},
			{`jsonb_insert(target => '{}',path => '{a}',replacement => '2')`, `jsonb_insert(target => unknown, path => unknown, replacement => unknown)`},
			{`jsonb_insert('{}','{a}',VARIADIC ARRAY['2']::jsonb[])`, `jsonb_insert(unknown, unknown, jsonb[])`},
			{`jsonb_insert(*)`, `jsonb_insert()`},
		} {
			t.Run(tc.call, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.sig+" does not exist")
				d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				d.Position = fixture.Position(14)
				s := jsonbSetError("/*é*/ SELECT "+tc.call, d)
				run(s)
				s.Prepared, s.ErrorStage = true, "prepare"
				run(s)
			})
		}
		for _, tc := range []struct{ call, code, msg, token string }{
			{`jsonb_insert(jsonb_in => '{}',path => '{a}',path => '{b}',replacement => '2')`, "42601", `argument name "path" used more than once`, "path => '{b}'"},
			{`jsonb_insert(jsonb_in => '{}','{a}','2')`, "42601", "positional argument cannot follow named argument", "'{a}'"},
			{`jsonb_insert(DISTINCT '{}','{a}','2')`, "42809", "DISTINCT specified, but jsonb_insert is not an aggregate function", "jsonb_insert"},
			{`jsonb_insert('{}','{a}','2' ORDER BY 1)`, "42809", "ORDER BY specified, but jsonb_insert is not an aggregate function", "jsonb_insert"},
			{`jsonb_insert('{}','{a}','2') FILTER (WHERE true)`, "42809", "FILTER specified, but jsonb_insert is not an aggregate function", "jsonb_insert"},
			{`jsonb_insert('{}','{a}','2') OVER ()`, "42809", "OVER specified, but jsonb_insert is not a window function nor an aggregate function", "jsonb_insert"},
			{`jsonb_insert(NULL,'{a}','2',missing)`, "42703", `column "missing" does not exist`, "missing"},
		} {
			t.Run(tc.call, func(t *testing.T) {
				q := "/*é*/ SELECT " + tc.call
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)
				d.Position = fixture.Position(len([]rune(q[:strings.Index(q, tc.token)])) + 1)
				s := jsonbSetError(q, d)
				run(s)
				s.Prepared, s.ErrorStage = true, "prepare"
				run(s)
			})
		}
		for _, tc := range []struct{ args, token, message, detail, context string }{
			{`'{}','{a,,b}','2'`, `'{a,,b}'`, `malformed array literal: "{a,,b}"`, `Unexpected "," character.`, ""},
			{`NULL,'{a}','2','bad'`, `'bad'`, `invalid input syntax for type boolean: "bad"`, "", ""},
			{`'{}','{a}','TRUE'`, `'TRUE'`, "invalid input syntax for type json", `Token "TRUE" is invalid.`, "JSON data, line 1: TRUE"},
		} {
			t.Run(tc.args, func(t *testing.T) {
				q := "/*é*/ SELECT jsonb_insert(" + tc.args + ")"
				d := fixture.ControlsReferenceDiagnostic("22P02", tc.message)
				d.Detail, d.Context = tc.detail, tc.context
				d.Position = fixture.Position(len([]rune(q[:strings.Index(q, tc.token)])) + 1)
				s := jsonbSetError(q, d)
				run(s)
				s.Prepared, s.ErrorStage = true, "prepare"
				run(s)
			})
		}
		run(fixture.Exec("CREATE TABLE jbi_demand(v jsonb,p text[],m text)", 0))
		jsonbSetBoth(t, p, b, c, fixture.Select("SELECT jsonb_insert(v,p,'9') AS changed FROM jbi_demand", []string{"changed"}, []string{"JSONB"}, nil))
		run(fixture.Exec("INSERT INTO jbi_demand VALUES('[0]','{bad}','bad')", 1))
		jsonbSetBoth(t, p, b, c, jsonbSetError("SELECT jsonb_insert(v,p,'9') FROM jbi_demand", fixture.ControlsReferenceDiagnostic("22P02", `path element at position 1 is not an integer: "bad"`)))
		run(fixture.Exec("UPDATE jbi_demand SET v=NULL", 1))
		// Both target and ordinary child remain row-dependent, avoiding
		// PostgreSQL's constant strict-NULL folding.
		jsonbSetBoth(t, p, b, c, jsonbSetError("SELECT jsonb_insert(v,p,'9',(m::integer)::boolean) FROM jbi_demand", fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type integer: "bad"`)))
		jsonbInsertHeld(t, p, b, c)
	})
}
