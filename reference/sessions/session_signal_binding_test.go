package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func sessionSignalScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, *[]fixture.Diagnostic)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.Recorder = r
			body(t, p, b, c, &notices)
		})
	}
}
func sessionSignalWarning(pid int) fixture.Diagnostic {
	return fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "01000", Message: fmt.Sprintf("PID %d is not a PostgreSQL backend process", pid)}
}
func TestReferenceSessionSignalBinding(t *testing.T) {
	sessionSignalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"SELECT pg_cancel_backend(NULL) AS v",
			"SELECT pg_terminate_backend(NULL) AS v",
			"SELECT pg_terminate_backend(NULL,NULL) AS v",
			"SELECT pg_cancel_backend(NULL::integer) AS v",
			"SELECT pg_terminate_backend(NULL::integer,-1) AS v",
			"SELECT pg_terminate_backend(pg_backend_pid(),NULL::bigint) AS v",
			"SELECT pg_terminate_backend(timeout=>NULL::bigint,pid=>pg_backend_pid()) AS v",
			"SELECT v FROM pg_cancel_backend(NULL::integer) AS v",
			"SELECT v FROM pg_terminate_backend(NULL::integer) AS v",
		} {
			s := signalNull(q)
			s.CheckMetadata = signalMetadata
			signalRoutes(t, p, b, c, s)
			signalNotices(t, n)
		}
		signalRoutes(t, p, b, c, fixture.Select("SELECT COALESCE(pg_cancel_backend(NULL::integer),false) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
		for _, q := range []string{"SELECT pg_cancel_backend(0::smallint) AS v", "SELECT pg_terminate_backend(timeout=>0::integer,pid=>0) AS v", "SELECT pg_terminate_backend(0) AS v"} {
			s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{false}})
			for _, prepared := range []bool{false, true} {
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				signalNotices(t, n, sessionSignalWarning(0))
			}
		}
		for _, fn := range []string{"pg_cancel_backend", "pg_terminate_backend"} {
			for _, args := range []string{"", "true", "1::bigint", "'1'::text", "1,0,0", "pid=>1"} {
				if fn == "pg_terminate_backend" && args == "pid=>1" {
					continue
				}
				signature := map[string]string{"": "", "true": "boolean", "1::bigint": "bigint", "'1'::text": "text", "1,0,0": "integer, integer, integer", "pid=>1": "pid => integer"}[args]
				q := "SELECT " + fn + "(" + args + ")"
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42883", Message: "function " + fn + "(" + signature + ") does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: fixture.Position(8)})
				s.SelectRows = true
				run(s)
				s = fixture.Prepared(s)
				s.ErrorStage = "prepare"
				run(s)
			}
		}
		for _, tc := range []struct{ q, signature string }{
			{"SELECT public.pg_cancel_backend(0)", "public.pg_cancel_backend(integer)"},
			{"SELECT public.pg_terminate_backend(0)", "public.pg_terminate_backend(integer)"},
			{`SELECT "PG_CANCEL_BACKEND"(0)`, `PG_CANCEL_BACKEND(integer)`},
			{`SELECT "PG_TERMINATE_BACKEND"(0)`, `PG_TERMINATE_BACKEND(integer)`},
			{"SELECT pg_terminate_backend(nope=>0)", "pg_terminate_backend(nope => integer)"},
			{"SELECT pg_terminate_backend(0,true)", "pg_terminate_backend(integer, boolean)"},
			{"SELECT pg_cancel_backend(0,0)", "pg_cancel_backend(integer, integer)"},
		} {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: "No function matches the given name and argument types. You might need to add explicit type casts.", Position: fixture.Position(8)})
			s.SelectRows = true
			run(s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			run(s)
		}
		for _, q := range []string{"SELECT pg_cancel_backend($1) AS v", "SELECT pg_terminate_backend($1,$2) AS v"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { stmt.Close() })
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			args := []any{nil}
			if q == "SELECT pg_terminate_backend($1,$2) AS v" {
				args = []any{nil, int64(-1)}
			}
			for range 3 {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, signalNull(q, args...))
				signalNotices(t, n)
			}
			stmt.Close()
		}
		for _, fn := range []string{"pg_cancel_backend", "pg_terminate_backend"} {
			q := "SELECT " + fn + "($1) AS v"
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type integer: "bad"`, Context: "unnamed portal parameter $1 = '...'"}, "bad")
			s.SelectRows = true
			s.Prepared = true
			s.ErrorStage = "execute"
			run(s)
			signalNotices(t, n)
		}
		run(fixture.Select("SELECT oid::integer,proname,proisstrict,provolatile::text,proparallel::text,pronargdefaults::integer,prorettype::integer FROM pg_proc WHERE oid IN (2096,2171) ORDER BY oid", []string{"oid", "proname", "proisstrict", "provolatile", "proparallel", "pronargdefaults", "prorettype"}, []string{"INT4", "NAME", "BOOL", "TEXT", "TEXT", "INT4", "INT4"}, [][]any{{int64(2096), "pg_terminate_backend", true, "v", "s", int64(1), int64(16)}, {int64(2171), "pg_cancel_backend", true, "v", "s", int64(0), int64(16)}}))
		for _, tc := range []struct {
			q, code, message string
			pos              int
		}{
			{"SELECT pg_cancel_backend('bad')", "22P02", `invalid input syntax for type integer: "bad"`, 26},
			{"SELECT pg_terminate_backend('bad')", "22P02", `invalid input syntax for type integer: "bad"`, 29},
			{"SELECT pg_terminate_backend(pid=>0,pid=>0)", "42601", `argument name "pid" used more than once`, 36},
			{"SELECT pg_terminate_backend(pid=>0,0)", "42601", "positional argument cannot follow named argument", 36},
		} {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fixture.Position(tc.pos)})
			s.SelectRows = true
			run(s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			run(s)
		}
		run(fixture.Select("SELECT proargnames::text,pg_get_expr(proargdefaults,0) FROM pg_proc WHERE oid IN (2096,2171) ORDER BY oid", []string{"proargnames", "pg_get_expr"}, []string{"TEXT", "TEXT"}, [][]any{{"{pid,timeout}", "0"}, {nil, nil}}))
		run(fixture.Select("SELECT pg_get_function_arguments(2096)", []string{"pg_get_function_arguments"}, []string{"TEXT"}, [][]any{{"pid integer, timeout bigint DEFAULT 0"}}))
		signalNotices(t, n)
	})
}
func TestReferenceSessionSignalTimeouts(t *testing.T) {
	sessionSignalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, n *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, tc := range []struct {
			input    string
			negative bool
		}{{"-1", true}, {"0", false}, {"2147483647", false}, {"2147483648", true}, {"2147483649", true}, {"4294967295", true}, {"4294967296", false}, {"4294967297", false}, {"-4294967296", false}, {"-9223372036854775808", false}, {"9223372036854775807", true}} {
			q := "SELECT pg_terminate_backend(2147483647,'" + tc.input + "'::bigint) AS v"
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{false}})
				if tc.negative {
					s = signalError(q, "22003", "\"timeout\" must not be negative")
				}
				if prepared {
					s = fixture.Prepared(s)
				}
				run(s)
				if tc.negative {
					signalNotices(t, n)
				} else {
					signalNotices(t, n, sessionSignalWarning(2147483647))
				}
			}
		}
		for _, pid := range []int{0, -1} {
			for _, fn := range []string{"pg_cancel_backend", "pg_terminate_backend"} {
				run(fixture.Select(fmt.Sprintf("SELECT %s(%d) AS v", fn, pid), []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
				signalNotices(t, n, sessionSignalWarning(pid))
			}
		}
		run(fixture.Exec("SET client_min_messages=error", 0))
		run(fixture.Select("SELECT pg_cancel_backend(0) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{false}}))
		signalNotices(t, n)
		run(fixture.Exec("RESET client_min_messages", 0))
	})
}
