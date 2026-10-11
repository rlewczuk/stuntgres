package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStringSplitDiagnostics(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE dst(v text)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO dst VALUES('sentinel')", 1))
		const signatureHint = "No function matches the given name and argument types. You might need to add explicit type casts."
		const lateralHint = "You might be able to move the set-returning function into a LATERAL FROM item."
		for _, tc := range []struct {
			q, code, message string
			pos              int
			hint, detail     string
			analysis         bool
		}{
			{"SELECT split_part('a', ',', 0)", "22023", "field position must not be zero", 0, "", "", false},
			{"SELECT split_part('', '', 0)", "22023", "field position must not be zero", 0, "", "", false},
			{"SELECT split_part('a', ',', 'bad')", "22P02", `invalid input syntax for type integer: "bad"`, 29, "", "", true},
			{"SELECT split_part('a', ',', '2147483648')", "22003", `value "2147483648" is out of range for type integer`, 29, "", "", true},
			{"SELECT split_part('a', ',', 1::bigint)", "42883", "function split_part(unknown, unknown, bigint) does not exist", 8, signatureHint, "", true},
			{"SELECT split_part('a', ',')", "42883", "function split_part(unknown, unknown) does not exist", 8, signatureHint, "", true},
			{"SELECT string_to_array(1, ',')", "42883", "function string_to_array(integer, unknown) does not exist", 8, signatureHint, "", true},
			{"SELECT string_to_table(1, ',')", "42883", "function string_to_table(integer, unknown) does not exist", 8, signatureHint, "", true},
			{"SELECT 1 WHERE string_to_table('a', ',') = 'a'", "0A000", "set-returning functions are not allowed in WHERE", 16, "", "", true},
			{"SELECT COALESCE(string_to_table('a', ','), 'z')", "0A000", "set-returning functions are not allowed in COALESCE", 17, lateralHint, "", true},
			{"SELECT count(string_to_table('a,b', ','))", "0A000", "aggregate function calls cannot contain set-returning function calls", 14, lateralHint, "", true},
			{"SELECT min(string_to_table('a,b', ',')) OVER ()", "0A000", "window function calls cannot contain set-returning function calls", 12, lateralHint, "", true},
			{"SELECT CASE WHEN false THEN string_to_table('a,b', ',') ELSE 'z' END", "0A000", "set-returning functions are not allowed in CASE", 29, lateralHint, "", true},
			{"SELECT * FROM string_to_table(string_to_table('a:b,c:d', ','), ':')", "0A000", "set-returning functions must appear at top level of FROM", 31, "", "", true},
			{"SELECT * FROM upper(string_to_table('a,b', ','))", "0A000", "set-returning functions must appear at top level of FROM", 21, "", "", true},
			{"SELECT * FROM string_to_table('a,b',',') AS t(v) FOR UPDATE OF t", "0A000", "FOR UPDATE cannot be applied to a function", 64, "", "", true},
			{"SELECT * FROM string_to_table('a,b',',') FOR SHARE OF string_to_table", "0A000", "FOR SHARE cannot be applied to a function", 55, "", "", true},
			{"SELECT * FROM string_to_table('a,b', ',') AS t(v,w)", "42P10", `table "t" has 1 columns available but 2 columns specified`, 0, "", "", true},
			{"SELECT * FROM string_to_table('a,b', ',') WITH ORDINALITY AS t(v,n,extra)", "42P10", `table "t" has 2 columns available but 3 columns specified`, 0, "", "", true},
			{"SELECT * FROM string_to_table('a,b', ',') AS t(v TEXT)", "42601", `a column definition list is only allowed for functions returning "record"`, 48, "", "", true},
			{"SELECT * FROM (VALUES ('a,b')) AS s(v) RIGHT JOIN LATERAL string_to_table(s.v, ',') AS t(x) ON TRUE", "42P10", `invalid reference to FROM-clause entry for table "s"`, 75, "", "The combining JOIN type must be INNER or LEFT for a LATERAL reference.", true},
			{"SELECT * FROM (VALUES ('a,b')) AS s(v) FULL JOIN string_to_table(s.v, ',') AS t(x) ON TRUE", "42P10", `invalid reference to FROM-clause entry for table "s"`, 66, "", "The combining JOIN type must be INNER or LEFT for a LATERAL reference.", true},
			{"VALUES (string_to_table('a,b', ','))", "0A000", "set-returning functions are not allowed in VALUES", 9, "", "", true},
			{"INSERT INTO dst(v) VALUES (string_to_table('a,b', ',')), ('c')", "0A000", "set-returning functions are not allowed in VALUES", 28, "", "", true},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(prefix+tc.q, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Hint = tc.hint
					d.Detail = tc.detail
					if tc.pos != 0 {
						d.Position = fixture.Position(tc.pos + len([]rune(prefix)))
					}
					stringReplaceFailure(t, p, b, c, prefix+tc.q, d, tc.analysis)
				})
			}
		}
		// Placement validation must precede NULL folding, dead predicates and publication.
		for _, tc := range []struct{ q, kind string }{
			{"SELECT 1 WHERE false AND string_to_table(NULL,',')='a'", "WHERE"},
			{"SELECT 1 FROM dst a JOIN dst b ON string_to_table('',',')='a'", "JOIN conditions"},
			{"SELECT count(*) FROM dst HAVING string_to_table('',',')='a'", "HAVING"},
			{"SELECT count(*) FILTER(WHERE string_to_table('',',')='a') FROM dst", "FILTER"},
			{"UPDATE dst SET v=string_to_table('',',')", "UPDATE"},
			{"DELETE FROM dst RETURNING string_to_table('',',')", "RETURNING"},
			{"SELECT 1 LIMIT string_to_table('',',')::int", "LIMIT"},
			{"SELECT 1 OFFSET string_to_table('',',')::int", "OFFSET"},
			{"CREATE TABLE ss_bad_default(v text DEFAULT string_to_table('',','))", "DEFAULT expressions"},
			{"CREATE TABLE ss_bad_check(v text CHECK(string_to_table('',',')='a'))", "check constraints"},
			{"CREATE TABLE ss_bad_generated(v text,g text GENERATED ALWAYS AS(string_to_table(v,',')) STORED)", "column generation expressions"},
			{"CREATE INDEX ss_bad_index ON dst((string_to_table(v,',')))", "index expressions"},
			{"CREATE INDEX ss_bad_pred ON dst(v) WHERE string_to_table('',',')='a'", "index predicates"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("0A000", "set-returning functions are not allowed in "+tc.kind)
				d.Position = fixture.Position(len([]rune(tc.q[:strings.Index(tc.q, "string_to_table")])) + 1)
				// Utility definitions are analyzed when executed, not by Prepare.
				stringReplaceFailure(t, p, b, c, tc.q, d, !strings.HasPrefix(tc.q, "CREATE "))
			})
		}
		for _, tc := range []struct{ q, message, token string }{
			{"SELECT ordinality FROM string_to_table('a,b',',') WITH ORDINALITY AS t(v,n)", `column "ordinality" does not exist`, "ordinality"},
			{"SELECT string_to_table FROM string_to_table('a,b',',') AS t(v)", `column "string_to_table" does not exist`, "string_to_table"},
		} {
			d := fixture.ControlsReferenceDiagnostic("42703", tc.message)
			d.Position = fixture.Position(strings.Index(tc.q, tc.token) + 1)
			stringReplaceFailure(t, p, b, c, tc.q, d, true)
		}
		for _, tc := range []struct{ call, signature string }{
			{"split_part()", "split_part()"}, {"string_to_array('a')", "string_to_array(unknown)"}, {"string_to_table('a',',','x','y')", "string_to_table(unknown, unknown, unknown, unknown)"},
			{"split_part(source => 'a', delimiter => ',', field => 1)", "split_part(source => unknown, delimiter => unknown, field => integer)"},
			{"string_to_array(source => 'a', delimiter => ',')", "string_to_array(source => unknown, delimiter => unknown)"},
			{"string_to_table(source => 'a', delimiter => ',')", "string_to_table(source => unknown, delimiter => unknown)"},
		} {
			d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
			d.Hint = signatureHint
			d.Position = "8"
			stringReplaceFailure(t, p, b, c, "SELECT "+tc.call, d, true)
		}
		// Nonvariadic calls accept the existing explicit argument decoration.
		stringReplaceValue(t, p, b, c, "split_part('a,b',',',VARIADIC 2)", "TEXT", "b")
		stringReplaceValue(t, p, b, c, "string_to_array('a,b',VARIADIC ',')", "_TEXT", `{a,b}`)
		stringReplaceBoth(t, p, b, c, fixture.Select("SELECT string_to_table('a,b',VARIADIC ',') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}}))
		q := "SELECT split_part('a',',',$1) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			arg                    any
			code, message, context string
		}{{0, "22023", "field position must not be zero", ""}, {"bad", "22P02", `invalid input syntax for type integer: "bad"`, "unnamed portal parameter $1 = '...'"}} {
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			d.Context = tc.context
			s := fixture.ErrorDiagnostic(q, d, tc.arg)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}, nil))
		stringSplitAdditionalDiagnostics(t, p, b, c)
		stringReplaceBoth(t, p, b, c, fixture.Select("SELECT v FROM dst", []string{"v"}, []string{"TEXT"}, [][]any{{"sentinel"}}))
	})
}

func TestReferenceStringSplitAuthorization(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{fixture.Exec("CREATE ROLE ss_denied", 0), fixture.Exec("CREATE TABLE split_secret(s text)", 0), fixture.Exec("CREATE SCHEMA ss_denied_schema", 0)} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		// Native nonpublic schemas start private; PostgreSQL fixture grants need
		// explicit setup. The SQL witnesses below use the same denied authority.
		if b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT USAGE ON SCHEMA public TO ss_denied", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("REVOKE USAGE ON SCHEMA ss_denied_schema FROM PUBLIC", 0))
		}
		for _, populated := range []bool{false, true} {
			if populated {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO split_secret VALUES('x,x'),(''),(NULL)", 3))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE ss_denied", 0))
			for _, expr := range []string{"split_part(s,',',1)", "string_to_array(s,',')", "string_to_table(s,',')"} {
				for _, suffix := range []string{"", " WHERE false"} {
					d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table split_secret")
					stringReplaceFailure(t, p, b, c, "SELECT "+expr+" FROM public.split_secret"+suffix, d, false)
				}
			}
			for _, call := range []string{"split_part('a',',',1)", "string_to_array('a',',')", "string_to_table('a',',')"} {
				d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema ss_denied_schema")
				d.Position = "8"
				stringReplaceFailure(t, p, b, c, "SELECT ss_denied_schema."+call, d, true)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT SELECT ON split_secret TO ss_denied", 0))
		for _, expr := range []string{"split_part(s,',',1)", "string_to_array(s,',')", "string_to_table(s,',')"} {
			q := "SELECT " + expr + " FROM public.split_secret WHERE false"
			st, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("REVOKE SELECT ON split_secret FROM ss_denied", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE ss_denied", 0))
			d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table split_secret")
			s := fixture.ErrorDiagnostic(q, d)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: st, SQL: q}, b.Recorder, s)
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT SELECT ON split_secret TO ss_denied", 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE split_secret", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP SCHEMA ss_denied_schema", 0))
		if b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("REVOKE USAGE ON SCHEMA public FROM ss_denied", 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP ROLE ss_denied", 0))
	})
}
