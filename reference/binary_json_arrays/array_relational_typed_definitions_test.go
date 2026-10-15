package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayRelationalTypedDefinitionClasses(t *testing.T) {
	arrayRelationalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE TYPE ar_def_pair AS (id INT,label TEXT)", "CREATE DOMAIN ar_def_domain AS ar_def_pair"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		// Assert complete independent diagnostics, including absent fields and
		// character positions through both direct and prepared execution.
		for _, prefix := range []string{"", "/*é*/ "} {
			for _, tc := range []struct{ q, token, code, message, hint string }{
				{"SELECT * FROM ROWS FROM (unnest(ARRAY[1],ARRAY['a'])) AS u(x INT,y TEXT)", "x INT", "42601", "ROWS FROM() with multiple functions cannot have a column definition list", "Put a separate column definition list for each function inside ROWS FROM()."},
				{"SELECT * FROM unnest(ARRAY[1],ARRAY['a']) AS u(x INT,y TEXT)", "x INT", "42601", "UNNEST() with multiple arguments cannot have a column definition list", "Use separate UNNEST() calls inside ROWS FROM(), and attach a column definition list to each one."},
				{"SELECT * FROM unnest(NULL::ar_def_pair[]) AS u(id INT,label TEXT)", "id INT", "42601", "a column definition list is redundant for a function returning a named composite type", ""},
				{"SELECT * FROM unnest(NULL::ar_def_domain[]) AS u(id INT,label TEXT)", "id INT", "42601", "a column definition list is redundant for a function returning a named composite type", ""},
				{"SELECT * FROM ROWS FROM (unnest(NULL::ar_def_pair[]) AS (id INT,label TEXT))", "id INT", "42601", "a column definition list is redundant for a function returning a named composite type", ""},
				{"SELECT * FROM ROWS FROM (unnest(NULL::ar_def_domain[]) AS (id INT,label TEXT))", "id INT", "42601", "a column definition list is redundant for a function returning a named composite type", ""},
				{"SELECT * FROM json_each('{}'::json) AS u(k TEXT,v JSON)", "k TEXT", "42601", "a column definition list is redundant for a function with OUT parameters", ""},
				{"SELECT * FROM ROWS FROM (json_each('{}'::json) AS (k TEXT,v JSON))", "k TEXT", "42601", "a column definition list is redundant for a function with OUT parameters", ""},
				{"SELECT * FROM ROWS FROM (json_each_text('{}'::json) AS (k TEXT,v TEXT))", "k TEXT", "42601", "a column definition list is redundant for a function with OUT parameters", ""},
				{"SELECT * FROM unnest(ARRAY[1]) AS u(x INT)", "x INT", "42601", `a column definition list is only allowed for functions returning "record"`, ""},
				{"SELECT * FROM ROWS FROM (unnest(ARRAY[1]) AS (x INT))", "x INT", "42601", `a column definition list is only allowed for functions returning "record"`, ""},
				{"SELECT * FROM ROWS FROM (json_array_elements('[]'::json) AS (x JSON))", "x JSON", "42601", `a column definition list is only allowed for functions returning "record"`, ""},
				{"SELECT * FROM ROWS FROM (string_to_table('a,b',',') AS (x TEXT))", "x TEXT", "42601", `a column definition list is only allowed for functions returning "record"`, ""},
				{"SELECT * FROM ROWS FROM (string_to_table('a,b',','),string_to_table('c,d',',') AS (x TEXT))", "x TEXT", "42601", `a column definition list is only allowed for functions returning "record"`, ""},
				{"SELECT * FROM ROWS FROM (string_to_table('a,b',',') AS (x TEXT),unnest(ARRAY[1]))", "x TEXT", "42601", `a column definition list is only allowed for functions returning "record"`, ""},
				// Definitions must not preempt ordinary function resolution.
				{"SELECT * FROM ROWS FROM (unnest(ARRAY[1],ARRAY['a']) AS (x INT,y TEXT))", "unnest", "42883", "function unnest(integer[], text[]) does not exist", arrayInspectHint},
				{"SELECT * FROM ROWS FROM (unnest(1) AS (x INT))", "unnest", "42883", "function unnest(integer) does not exist", arrayInspectHint},
				{"SELECT * FROM unnest(1) AS u(x INT)", "unnest", "42883", "function unnest(integer) does not exist", arrayInspectHint},
				{"SELECT * FROM ROWS FROM (unnest(NULL) AS (x INT))", "unnest", "42725", "function unnest(unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts."},
				{"SELECT * FROM ROWS FROM (json_each(1) AS (k TEXT,v JSON))", "json_each", "42883", "function json_each(integer) does not exist", arrayInspectHint},
				{"SELECT * FROM ROWS FROM (string_to_table(1,2) AS (x TEXT))", "string_to_table", "42883", "function string_to_table(integer, integer) does not exist", arrayInspectHint},
			} {
				q := prefix + tc.q
				t.Run(q, func(t *testing.T) {
					d := arrayInspectDiagnostic(q, tc.token, tc.code, tc.message, tc.hint)
					arrayRelationalError(t, p, b, c, q, d, false)
				})
			}
		}
	})
}
