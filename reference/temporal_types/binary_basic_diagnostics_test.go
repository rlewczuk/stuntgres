package reference_test

import (
	"context"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceBinaryBasicDiagnostics(t *testing.T) {
	for route, nonContext := range []bool{false, true} {
		t.Run([]string{"context", "consumer"}[route], func(t *testing.T) {
			p := fixture.NewScenario(t)
			apply := func(t *testing.T, s fixture.Step) { s.NonContext = nonContext; p.Step(t, s) }
			apply(t, fixture.Exec("CREATE TABLE t(v BYTEA,s BYTEA)", 0))
			f := "No function matches the given name and argument types. You might need to add explicit type casts."
			o := "No operator matches the given name and argument types. You might need to add explicit type casts."
			cases := []struct {
				q, code, msg string
				pos          int
				hint         string
			}{
				{"SELECT length(1)", "42883", "function length(integer) does not exist", 8, f},
				{"SELECT length()", "42883", "function length() does not exist", 8, f},
				{"SELECT bit_length(1)", "42883", "function bit_length(integer) does not exist", 8, f},
				{"SELECT octet_length(TRUE)", "42883", "function octet_length(boolean) does not exist", 8, f},
				{"SELECT length(v,1) FROM t", "42883", "function length(bytea, integer) does not exist", 8, f},
				{"SELECT v || 1 FROM t", "42883", "operator does not exist: bytea || integer", 10, o},
				{"SELECT 1 || v FROM t", "42883", "operator does not exist: integer || bytea", 10, o},
				{`SELECT btrim(v,'\q') FROM t`, "22P02", "invalid input syntax for type bytea", 16, ""},
				{`SELECT btrim(v,'\x0') FROM t`, "22023", "invalid hexadecimal data: odd number of digits", 16, ""},
				{`SELECT btrim(v,'\xGG') FROM t`, "22023", `invalid hexadecimal digit: "G"`, 16, ""},
				{"SELECT public.length(v) FROM t", "42883", "function public.length(bytea) does not exist", 8, f},
				{"SELECT missing.length(v) FROM t", "3F000", `schema "missing" does not exist`, 8, ""},
			}
			for _, name := range []string{"btrim", "ltrim", "rtrim"} {
				cases = append(cases, struct {
					q, code, msg string
					pos          int
					hint         string
				}{"SELECT " + name + "(v) FROM t", "42883", "function " + name + "(bytea) does not exist", 8, f}, struct {
					q, code, msg string
					pos          int
					hint         string
				}{"SELECT " + name + "(v,1) FROM t", "42883", "function " + name + "(bytea, integer) does not exist", 8, f}, struct {
					q, code, msg string
					pos          int
					hint         string
				}{"SELECT " + name + "(v,s,s) FROM t", "42883", "function " + name + "(bytea, bytea, bytea) does not exist", 8, f})
			}
			for _, populated := range []bool{false, true} {
				if populated {
					apply(t, fixture.Exec("INSERT INTO t VALUES(NULL,NULL)", 1))
				}
				for _, tc := range cases {
					for _, prefix := range []string{"", "/*é*/ "} {
						pos := tc.pos
						if prefix != "" {
							pos += 6
						}
						s := fixture.ImplicitError(prefix+tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.msg, Position: strconv.Itoa(pos), Hint: tc.hint})
						s.ErrorStage = "execute"
						apply(t, s)
						s.SelectRows = true
						apply(t, s)
						s.ErrorStage = "prepare"
						apply(t, fixture.Prepared(s))
					}
				}
			}
		})
	}
}

func TestReferenceBinaryBasicAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"binary_reader"})
	t.Cleanup(func() {
		if !fixture.Started {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := fixture.Admin.ExecContext(ctx, "DROP SCHEMA IF EXISTS binary_private CASCADE"); err != nil {
			t.Error(err)
		}
	})
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(p.Ctx, "RESET ROLE")
			step := func(s fixture.Step) { t.Helper(); fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			for _, q := range []string{"CREATE ROLE binary_reader", "CREATE TABLE t(v BYTEA,s BYTEA)", `INSERT INTO t VALUES('\xff','\x00')`, "CREATE SCHEMA binary_private", "CREATE TABLE binary_private.t(v BYTEA,s BYTEA)"} {
				count := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					count = 1
				}
				step(fixture.Exec(q, count))
			}
			// Fixture recreation clears PostgreSQL public's initdb ACL; native
			// public already grants USAGE through the existing namespace contract.
			if backend.PG {
				step(fixture.Exec("GRANT USAGE ON SCHEMA public TO binary_reader", 0))
			}
			step(fixture.Exec("SET ROLE binary_reader", 0))
			q := "SELECT length(v),btrim(v,s),v||s FROM public.t"
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
			s.SelectRows = true
			s.ErrorStage = "execute"
			step(s)
			step(fixture.Prepared(s))
			q = "SELECT length(v),btrim(v,s),v||s FROM binary_private.t"
			s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema binary_private", Position: strconv.Itoa(strings.Index(q, "binary_private") + 1)})
			s.SelectRows = true
			s.ErrorStage = "execute"
			step(s)
			s.ErrorStage = "prepare"
			step(fixture.Prepared(s))
			step(fixture.Exec("RESET ROLE", 0))
			step(fixture.Select("SELECT length(v) AS n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
			var n int64
			if err := conn.QueryRowContext(p.Ctx, "SELECT length(v) FROM t").Scan(&n); err != nil || n != 1 {
				t.Fatal(n, err)
			}
		})
	}
}
