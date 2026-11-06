package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgPrewarmDiagnostics(t *testing.T) {
	t.Run("body_and_precedence", func(t *testing.T) {
		pwScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			pwInsert(t, p, b, c, "pw_part.heap", 1000)
			n := pwExtent(t, p, b, c, "pw_part.heap", "main", 6)
			for _, tc := range []struct{ args []any; message, hint string }{
				{[]any{nil, nil, nil, int64(-1), int64(-1)}, "relation cannot be null", ""},
				{[]any{"pw_part.heap", nil, nil, nil, nil}, "prewarm type cannot be null", ""},
				{[]any{int64(4294967294), "bad", "bad", int64(-1), int64(-1)}, "invalid prewarm type", "Valid prewarm types are \"prefetch\", \"read\", and \"buffer\"."},
				{[]any{"pw_part.heap", "BUFFER", "main", nil, nil}, "invalid prewarm type", "Valid prewarm types are \"prefetch\", \"read\", and \"buffer\"."},
				{[]any{"pw_part.heap", "", "main", nil, nil}, "invalid prewarm type", "Valid prewarm types are \"prefetch\", \"read\", and \"buffer\"."},
				{[]any{int64(4294967294), "buffer", nil, nil, nil}, "relation fork cannot be null", ""},
				{[]any{int64(4294967294), "buffer", "bad", int64(-1), int64(-1)}, "invalid fork name", "Valid fork names are \"main\", \"fsm\", \"vm\", and \"init\"."},
				{[]any{"pw_part.heap", "buffer", "MAIN", nil, nil}, "invalid fork name", "Valid fork names are \"main\", \"fsm\", \"vm\", and \"init\"."},
				{[]any{"pw_part.heap", "buffer", "", nil, nil}, "invalid fork name", "Valid fork names are \"main\", \"fsm\", \"vm\", and \"init\"."},
				{[]any{"pw_part.heap", "buffer", "init", int64(-1), int64(-1)}, "fork \"init\" does not exist for this relation", ""},
			} {
				s := pwError(pwCall, "22023", tc.message, tc.args...)
				s.Diagnostic.Hint = tc.hint
				pwBodyError(t, p, b, c, s)
			}
			for _, endpoint := range []int64{-1, n, 9223372036854775807} {
				pwBodyError(t, p, b, c, pwError(pwCall, "22023", fmt.Sprintf("starting block number must be between 0 and %d", n-1), "pw_part.heap", "buffer", "main", endpoint, int64(-1)))
				pwBodyError(t, p, b, c, pwError(pwCall, "22023", fmt.Sprintf("ending block number must be between 0 and %d", n-1), "pw_part.heap", "buffer", "main", nil, endpoint))
			}
			for _, oid := range []int64{0, 4294967294, 4294967295} {
				pwBodyError(t, p, b, c, pwError(pwCall, "XX000", fmt.Sprintf("could not open relation with OID %d", oid), oid, "buffer", "main", int64(-1), int64(-1)))
			}
			bcDDL(t, p, b, c, "CREATE TABLE pw_part.atomic_result(n bigint)")
			s := pwError("INSERT INTO pw_part.atomic_result VALUES(pw_part.pg_prewarm('pw_part.heap')),(pw_part.pg_prewarm(NULL::regclass))", "22023", "relation cannot be null")
			s.SelectRows = false
			pwBodyError(t, p, b, c, s)
			bcBoth(t, p, b, c, fixture.Select("SELECT n FROM pw_part.atomic_result", []string{"n"}, []string{"INT8"}, nil))
		})
	})
	t.Run("storage_kinds", func(t *testing.T) {
		pwScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS pw_fdw CASCADE")
			bcDDL(t, p, b, c, "CREATE VIEW pw_part.no_storage AS SELECT k FROM pw_part.heap", "CREATE TYPE pw_part.composite AS(k integer)", "CREATE TABLE pw_part.parent(k integer) PARTITION BY RANGE(k)", "CREATE FOREIGN DATA WRAPPER pw_fdw", "CREATE SERVER pw_server FOREIGN DATA WRAPPER pw_fdw", "CREATE FOREIGN TABLE pw_part.foreign_heap(k integer) SERVER pw_server")
			for _, tc := range []struct{ name, kind string }{{"no_storage", "views"}, {"composite", "composite types"}, {"parent", "partitioned tables"}, {"foreign_heap", "foreign tables"}} {
				s := pwError(pwCall, "42809", "relation \""+tc.name+"\" does not have storage", "pw_part."+tc.name, "buffer", "init", int64(-1), int64(-1))
				s.Diagnostic.Detail = "This operation is not supported for "+tc.kind+"."
				pwBodyError(t, p, b, c, s)
			}
			// Only this btree constructor is unavailable natively. The native
			// installed-host descriptor/ACL witness lives in TestPgPrewarmCapture.
			if b.PG {
				bcDDL(t, p, b, c, "CREATE INDEX parent_idx ON pw_part.parent(k)")
				s := pwError(pwCall, "42809", "relation \"parent_idx\" does not have storage", "pw_part.parent_idx", "buffer", "init", int64(-1), int64(-1))
				s.Diagnostic.Detail = "This operation is not supported for partitioned indexes."
				pwBodyError(t, p, b, c, s)
			}
		})
	})
	t.Run("binding_and_evaluation", func(t *testing.T) {
		pwScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct{ call, signature string }{
				{"()", "()"}, {"(true)", "(boolean)"}, {"(NULL::regclass,true)", "(regclass, boolean)"},
				{"(NULL::regclass,'buffer','main',0,0,0)", "(regclass, unknown, unknown, integer, integer, integer)"},
				{"(relation=>NULL::regclass)", "(relation => regclass)"}, {"(rel=>NULL::regclass)", "(rel => regclass)"},
			} {
				q := "SELECT pw_part.pg_prewarm"+tc.call
				s := pwError(q, "42883", "function pw_part.pg_prewarm"+tc.signature+" does not exist")
				s.Diagnostic.Position = "8"
				s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				bcFailure(t, p, b, c, s, "prepare")
			}
			q := "SELECT pw_part.pg_prewarm('missing_pw')"
			s := pwError(q, "42P01", "relation \"missing_pw\" does not exist")
			s.Diagnostic.Position = fixture.Position(strings.Index(q, "'missing_pw'")+1)
			bcFailure(t, p, b, c, s, "prepare")
			for _, tc := range []struct{ q, code, message, context string; args []any }{
				{"SELECT pw_part.pg_prewarm($1::regclass)", "42P01", "relation \"missing_pw\" does not exist", "unnamed portal parameter $1 = '...'", []any{"missing_pw"}},
				{"SELECT pw_part.pg_prewarm('pw_part.heap','buffer','main',$1::bigint)", "22P02", "invalid input syntax for type bigint: \"bad\"", "unnamed portal parameter $1 = '...'", []any{"bad"}},
				{"SELECT pw_part.pg_prewarm('pw_part.heap','buffer','main',$1::bigint)", "22003", "value \"9223372036854775808\" is out of range for type bigint", "unnamed portal parameter $1 = '...'", []any{"9223372036854775808"}},
				{"SELECT pw_part.pg_prewarm(NULL::regclass,'buffer','main',1/$1::bigint)", "22012", "division by zero", "", []any{int64(0)}},
				{"SELECT pw_part.pg_prewarm(NULL::regclass,mode=>CASE WHEN 1/$1::integer=0 THEN 'bad' ELSE 'buffer' END)", "22012", "division by zero", "", []any{int64(0)}},
			} {
				s := pwError(tc.q, tc.code, tc.message, tc.args...)
				s.Diagnostic.Context = tc.context
				pwBodyError(t, p, b, c, s)
			}
			q = "SELECT pw_part.pg_prewarm('pw_part.heap','buffer','main',9223372036854775808::bigint)"
			s = pwError(q, "22003", "bigint out of range")
			bcRun(t, p, b, c, s)
			bcRun(t, p, b, c, fixture.Prepared(s))
			for _, tc := range []struct{ q, message, token string }{
				{"SELECT pw_part.pg_prewarm('pw_part.heap',mode=>'buffer',mode=>'read')", "argument name \"mode\" used more than once", "mode=>'read'"},
				{"SELECT pw_part.pg_prewarm('pw_part.heap',mode=>'buffer','main')", "positional argument cannot follow named argument", "'main'"},
			} {
				s := pwError(tc.q, "42601", tc.message)
				s.Diagnostic.Position = fixture.Position(strings.Index(tc.q, tc.token)+1)
				bcFailure(t, p, b, c, s, "prepare")
			}
		})
	})
}
