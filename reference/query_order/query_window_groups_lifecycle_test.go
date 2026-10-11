package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowGroupsLifecycle(t *testing.T) {
	queryWindowGroupsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		queryWindowGroupsSeed(t, p, b)
		queryWindowRun(t, p, b, fixture.Exec("CREATE TABLE groups_sentinel(n BIGINT)", 0))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO groups_sentinel VALUES(99)", 1))
		sentinel := fixture.Select("SELECT n FROM groups_sentinel ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(99)}})
		for _, tc := range []struct{ key, offset, code, message string }{
			{"k", "-1", "22013", "frame starting offset must not be negative"},
			{"k", "NULL", "22004", "frame starting offset must not be null"},
			{"k", "(SELECT 1/0)", "22012", "division by zero"},
			{"1/(id-3)", "1", "22012", "division by zero"},
			{"k", "(9223372036854775807::BIGINT+1)", "22003", "bigint out of range"},
		} {
			q := "INSERT INTO groups_sentinel SELECT count(*) OVER(ORDER BY " + tc.key + " GROUPS " + tc.offset + " PRECEDING) FROM g"
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				queryWindowRun(t, p, b, s)
				queryWindowRoutes(t, p, b, sentinel)
			}
		}
		q := "INSERT INTO groups_sentinel VALUES(1); SELECT count(*) OVER(ORDER BY k GROUPS 1 PRECEDING) FROM g; SELECT ("
		for _, prepared := range []bool{false, true} {
			malformed := q
			stage := "execute"
			if prepared {
				malformed = "SELECT ("
				stage = "prepare"
			}
			s := fixture.ImplicitError(malformed, fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: fixture.Position(len([]rune(malformed)) + 1)})
			s.Prepared = prepared
			s.ErrorStage = stage
			queryWindowRun(t, p, b, s)
			queryWindowRoutes(t, p, b, sentinel)
		}
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if b.PG {
			for _, prepared := range []bool{false, true} {
				fixture.SessionCancelQuery(t, p.Ctx, conn, prepared, "SELECT count(*) OVER(ORDER BY k GROUPS (SELECT 1 FROM (SELECT pg_sleep(600)) AS sleeping) PRECEDING) FROM g", true)
				fixture.SessionCancelQuery(t, p.Ctx, conn, prepared, "INSERT INTO groups_sentinel SELECT count(*) OVER(ORDER BY k GROUPS (SELECT 1 FROM (SELECT pg_sleep(600)) AS sleeping) PRECEDING) FROM g", false)
			}
		} else {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, prepared := range []bool{false, true} {
				q := queryWindowGroupsResult("1 PRECEDING").Query
				stmt, err := conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				var rows *sql.Rows
				if prepared {
					rows, err = stmt.QueryContext(ctx)
				} else {
					rows, err = conn.QueryContext(ctx, q)
				}
				if rows != nil || !errors.Is(err, context.Canceled) {
					t.Fatal(rows, err)
				}
				stmt.Close()
				q = "INSERT INTO groups_sentinel SELECT count(*) OVER(ORDER BY k GROUPS 1 PRECEDING) FROM g"
				stmt, err = conn.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				var result sql.Result
				if prepared {
					result, err = stmt.ExecContext(ctx)
				} else {
					result, err = conn.ExecContext(ctx, q)
				}
				if result != nil || !errors.Is(err, context.Canceled) {
					t.Fatal(result, err)
				}
				stmt.Close()
			}
		}
		queryWindowRoutes(t, p, b, sentinel)
		queryWindowRoutes(t, p, b, queryWindowGroupsResult("1 PRECEDING", 2, 2, 3, 3, 3))
		queryWindowRun(t, p, b, fixture.Exec("INSERT INTO groups_sentinel SELECT count(*) OVER(ORDER BY k GROUPS 1 PRECEDING) FROM g", 5))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT n FROM groups_sentinel ORDER BY n", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}, {int64(2)}, {int64(3)}, {int64(3)}, {int64(3)}, {int64(99)}}))
	})
}

func TestReferenceQueryWindowGroupsAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"groups_reader"})
	queryWindowGroupsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE ROLE groups_reader", 0),
			fixture.Exec("CREATE TABLE groups_secret(id INT,k INT)", 0),
			fixture.Exec("INSERT INTO groups_secret VALUES(1,1)", 1),
		} {
			queryWindowRun(t, p, b, s)
		}
		if b.PG {
			queryWindowRun(t, p, b, fixture.Exec("GRANT USAGE ON SCHEMA public TO groups_reader", 0))
		}
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION groups_reader", 0))
		denied := func(q string) {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table groups_secret"})
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		for _, q := range []string{
			"SELECT count(*) OVER(ORDER BY k GROUPS 1 PRECEDING) FROM public.groups_secret",
			"SELECT count(*) OVER(ORDER BY 1 GROUPS (SELECT k FROM public.groups_secret) PRECEDING)",
			"SELECT 1 WINDOW unused AS(ORDER BY 1 GROUPS (SELECT k FROM public.groups_secret) PRECEDING)",
			"SELECT count(*) OVER(ORDER BY k GROUPS 1 PRECEDING) FROM public.groups_secret WHERE FALSE",
			"SELECT count(*) OVER(ORDER BY k GROUPS 1 PRECEDING) FROM public.groups_secret LIMIT 0",
		} {
			denied(q)
		}
		queryWindowRun(t, p, b, fixture.Exec("GRANT SELECT(id) ON groups_secret TO groups_reader", 0))
		q := "SELECT count(*) OVER(ORDER BY 1 GROUPS CURRENT ROW) FROM public.groups_secret"
		s := fixture.Select(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}})
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		denied("SELECT count(*) OVER(ORDER BY k GROUPS CURRENT ROW) FROM public.groups_secret")
		denied("SELECT id FROM public.groups_secret WINDOW unused AS(ORDER BY k GROUPS CURRENT ROW)")
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{c, stmt, q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		queryWindowRun(t, p, b, fixture.Exec("REVOKE SELECT(id) ON groups_secret FROM groups_reader", 0))
		failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table groups_secret"})
		failure.SelectRows = true
		failure.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		queryWindowRoutes(t, p, b, fixture.Select("SELECT id,k FROM groups_secret", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}}))
	})
}
