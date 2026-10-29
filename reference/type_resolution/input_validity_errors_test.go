package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func ivError(t *testing.T, p *fixture.Pair, c *sql.Conn, query string, want fixture.Diagnostic, analysis bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		var err error
		if prepared {
			var stmt *sql.Stmt
			stmt, err = c.PrepareContext(p.Ctx, query)
			if analysis {
				if stmt != nil {
					stmt.Close()
					t.Fatal("invalid call prepared", query)
				}
			} else {
				if err != nil {
					t.Fatal("runtime failure occurred during Prepare", query, err)
				}
				rows, err = stmt.QueryContext(p.Ctx)
				stmt.Close()
			}
		} else {
			rows, err = c.QueryContext(p.Ctx, query)
		}
		if rows != nil {
			for rows.Next() {
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
		}
		fixture.AssertDiagnostic(t, err, &want)
	}
}
func ivDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}
func ivHardBasics(t *testing.T, p *fixture.Pair, c *sql.Conn) {
	for _, name := range []string{"pg_input_is_valid", "pg_input_error_info"} {
		for _, f := range []struct{ target, code, message string }{
			{"__iv_missing__", "42704", `type "__iv_missing__" does not exist`},
			{"pg_ddl_command", "0A000", "cannot accept a value of type pg_ddl_command"},
			{"numeric(0)", "22023", "NUMERIC precision 0 must be between 1 and 1000"},
			{"int4(4)", "42601", `type modifier is not allowed for type "int4"`},
		} {
			ivError(t, p, c, "SELECT "+name+"('42',"+fixture.RegtypeLiteral(f.target)+")", ivDiagnostic(f.code, f.message), false)
		}
		for _, f := range []struct{ target, position, message string }{{"int4(", "6", "syntax error at end of input"}, {"23", "1", `syntax error at or near "23"`}, {"-", "1", `syntax error at or near "-"`}} {
			want := ivDiagnostic("42601", f.message)
			want.Position = f.position
			want.Context = `invalid type name "` + f.target + `"`
			ivError(t, p, c, "SELECT "+name+"('42',"+fixture.RegtypeLiteral(f.target)+")", want, false)
		}
	}
}
func TestReferenceInputValidityHardErrors(t *testing.T) {
	ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		ivHardBasics(t, p, c)
		for _, q := range []string{"CREATE DOMAIN public.iv_positive AS integer CHECK(VALUE>0)", "CREATE DOMAIN public.iv_broken AS integer CHECK(1/(VALUE-1)>0)", "CREATE DOMAIN public.iv_broken_input AS integer CHECK((VALUE::text || 'oops')::integer > 0)"} {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
		}
		ivBoth(t, p, c, r, ivBool("-1", "public.iv_positive", false))
		ivBoth(t, p, c, r, ivInfo("-1", "public.iv_positive", []any{`value for domain iv_positive violates check constraint "iv_positive_check"`, nil, nil, "23514"}))
		ivBoth(t, p, c, r, ivBool("{1,-1}", "public.iv_positive[]", false))
		ivBoth(t, p, c, r, ivInfo("{1,-1}", "public.iv_positive[]", []any{`value for domain iv_positive violates check constraint "iv_positive_check"`, nil, nil, "23514"}))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path TO pg_catalog", 0))
		ivBoth(t, p, c, r, ivInfo("-1", "public.iv_positive", []any{`value for domain public.iv_positive violates check constraint "iv_positive_check"`, nil, nil, "23514"}))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path TO public", 0))
		ivBoth(t, p, c, r, ivInfo("-1", "public.iv_positive", []any{`value for domain iv_positive violates check constraint "iv_positive_check"`, nil, nil, "23514"}))
		for _, name := range []string{"pg_input_is_valid", "pg_input_error_info"} {
			ivError(t, p, c, "SELECT "+name+"('1','public.iv_broken')", ivDiagnostic("22012", "division by zero"), false)
			ivError(t, p, c, "SELECT "+name+"('1','public.iv_broken_input')", ivDiagnostic("22P02", `invalid input syntax for type integer: "1oops"`), false)
			ivError(t, p, c, "SELECT "+name+"('{1}','public.iv_broken_input[]')", ivDiagnostic("22P02", `invalid input syntax for type integer: "1oops"`), false)
			query := "SELECT " + name + "(NULL,('oops'::integer)::text)"
			want := ivDiagnostic("22P02", `invalid input syntax for type integer: "oops"`)
			want.Position = strconv.Itoa(strings.Index(query, "'oops'") + 1)
			ivError(t, p, c, query, want, true)
		}
		for _, hard := range []bool{false, true} {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("BEGIN", 0))
			if hard {
				_, err := c.ExecContext(p.Ctx, "SELECT pg_input_is_valid('x','pg_ddl_command')")
				want := ivDiagnostic("0A000", "cannot accept a value of type pg_ddl_command")
				fixture.AssertDiagnostic(t, err, &want)
				_, err = c.ExecContext(p.Ctx, "SELECT 1")
				want = ivDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
				fixture.AssertDiagnostic(t, err, &want)
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ROLLBACK", 0))
			} else {
				fixture.RunStep(t, p.Ctx, c, r, ivBool("oops", "integer", false))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT 1 AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
				fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("COMMIT", 0))
			}
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err := c.QueryContext(ctx, "SELECT pg_input_is_valid('42','integer')")
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary cancellation", err)
			}
		}
	})
}

func TestReferenceInputValidityBinding(t *testing.T) {
	ivScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		for _, name := range []string{"pg_input_is_valid", "pg_input_error_info", "pg_input_is_valid_missing"} {
			for _, f := range []struct{ args, types string }{{"", ""}, {"'42'", "unknown"}, {"42,'integer'", "integer, unknown"}, {"true,'integer'", "boolean, unknown"}, {"'42'::text,23", "text, integer"}, {"'42','integer','extra'", "unknown, unknown, unknown"}} {
				want := ivDiagnostic("42883", "function "+name+"("+f.types+") does not exist")
				want.Position = "8"
				want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				ivError(t, p, c, "SELECT "+name+"("+f.args+")", want, true)
			}
		}
		wantMissing := ivDiagnostic("42883", "function pg_input_is_valid_missing(unknown, unknown) does not exist")
		wantMissing.Position = "8"
		wantMissing.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		ivError(t, p, c, "SELECT pg_input_is_valid_missing('42','integer')", wantMissing, true)
		for _, args := range []string{"value => 'oops',type_name => 'integer'", "type_name => 'integer',value => 'oops'", "'oops',type_name => 'integer'"} {
			s := ivInfo("oops", "integer", []any{`invalid input syntax for type integer: "oops"`, nil, nil, "22P02"})
			s.Query = "SELECT * FROM pg_input_error_info(" + args + ")"
			ivBoth(t, p, c, r, s)
		}
		ivBoth(t, p, c, r, fixture.Select("SELECT pg_catalog.pg_input_is_valid('42','integer') AS ok", []string{"ok"}, []string{"BOOL"}, [][]any{{true}}))
		unnamed := ivDiagnostic("42883", "function pg_catalog.pg_input_is_valid(value => unknown, type_name => unknown) does not exist")
		unnamed.Position = "8"
		unnamed.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		ivError(t, p, c, "SELECT pg_catalog.pg_input_is_valid(value => '42',type_name => 'integer')", unnamed, true)
		for _, q := range []string{"SELECT pg_input_is_valid(string => '42',type_name => 'integer')", "SELECT /* é🙂 */ pg_input_is_valid(42,'integer')"} {
			want := ivDiagnostic("42883", `function pg_input_is_valid(string => unknown, type_name => unknown) does not exist`)
			want.Position = "8"
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			if q != "SELECT pg_input_is_valid(string => '42',type_name => 'integer')" {
				want.Message = "function pg_input_is_valid(integer, unknown) does not exist"
				want.Position = "17"
			}
			ivError(t, p, c, q, want, true)
		}
		for _, f := range []struct{ query, token, message string }{
			{"SELECT pg_input_is_valid(value => '42',value => 'integer')", "value => 'integer'", `argument name "value" used more than once`},
			{"SELECT pg_input_is_valid(value => '42','integer')", "'integer'", "positional argument cannot follow named argument"},
		} {
			want := ivDiagnostic("42601", f.message)
			want.Position = strconv.Itoa(strings.Index(f.query, f.token) + 1)
			ivError(t, p, c, f.query, want, true)
		}
		ivBoth(t, p, c, r, fixture.Select("SELECT oid::bigint AS oid,proname,proargtypes::text AS args,prorettype::bigint AS result,provolatile::text AS volatility,proisstrict,proparallel::text AS parallel,proretset FROM pg_catalog.pg_proc WHERE oid IN (6210,6211) AND pronamespace=11 ORDER BY oid", []string{"oid", "proname", "args", "result", "volatility", "proisstrict", "parallel", "proretset"}, []string{"INT8", "NAME", "TEXT", "INT8", "TEXT", "BOOL", "TEXT", "BOOL"}, [][]any{{int64(6210), "pg_input_is_valid", "25 25", int64(16), "s", true, "s", false}, {int64(6211), "pg_input_error_info", "25 25", int64(2249), "s", true, "s", false}}))
		ivBoth(t, p, c, r, fixture.Select("SELECT proargnames::text AS names,proallargtypes::text AS alltypes,proargmodes::text AS modes,prosrc FROM pg_catalog.pg_proc WHERE oid IN (6210,6211) AND pronamespace=11 ORDER BY oid", []string{"names", "alltypes", "modes", "prosrc"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{nil, nil, nil, "pg_input_is_valid"}, {"{value,type_name,message,detail,hint,sql_error_code}", "{25,25,25,25,25,25}", "{i,i,o,o,o,o}", "pg_input_error_info"}}))
	})
}
