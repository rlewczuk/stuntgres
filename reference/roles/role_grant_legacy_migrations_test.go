package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleGrantCastPreflightMigration(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"cast_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		c, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE TABLE e(i INT4)", "CREATE TABLE populated(i INT4)"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO populated VALUES(1)", 1))
		for _, tc := range []struct {
			expression, code, message string
			bad, good                 any
		}{
			{"int4($1::text)", "22P02", `invalid input syntax for type integer: "oops"`, "oops", "42"},
			{"CAST($1::int8 AS int4)", "22003", "integer out of range", int64(2147483648), int64(42)},
		} {
			for _, from := range []string{"e", "populated WHERE i=0", "populated"} {
				q := "SELECT " + tc.expression + " FROM " + from
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message}, tc.bad)
				s.SelectRows = true
				run(s)
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("expected successful Prepare", err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for i, arg := range []any{tc.bad, tc.good, nil, tc.bad, tc.good} {
					if i == 0 || i == 3 {
						s.Args = []any{arg}
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
						continue
					}
					var want [][]any
					if from == "populated" {
						var v any
						if arg != nil {
							v = int64(42)
						}
						want = [][]any{{v}}
					}
					fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(q, []string{"int4"}, []string{"INT4"}, want, arg))
				}
			}
		}
		for _, q := range []string{"CREATE ROLE cast_reader", "CREATE TABLE secret(i INT4,s TEXT,n INT8)", "CREATE SCHEMA cast_private", "CREATE TABLE cast_private.secret(i INT4,s TEXT,n INT8)"} {
			run(fixture.Exec(q, 0))
		}
		defer b.DB.ExecContext(p.Ctx, "DROP SCHEMA cast_private CASCADE")
		run(fixture.Exec("INSERT INTO secret VALUES(7,'oops',2147483648)", 1))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO cast_reader", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION cast_reader", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		for _, tc := range []struct{ expression, code, message string }{
			{"2147483648::int4", "22003", "integer out of range"},
			{"CAST('oops'::text AS int4)", "22P02", `invalid input syntax for type integer: "oops"`},
			{"CAST('42'::text AS int4)", "42501", "permission denied for table secret"},
			{"7::int4", "42501", "permission denied for table secret"},
			{"s::int4", "42501", "permission denied for table secret"},
			{"CAST(n AS int4)", "42501", "permission denied for table secret"},
		} {
			for _, private := range []bool{false, true} {
				q := "SELECT " + tc.expression + " FROM public.secret"
				want := fixture.Diagnostic{Code: tc.code, Message: tc.message}
				if private {
					q = "SELECT " + tc.expression + " FROM cast_private.secret"
					want = fixture.Diagnostic{Code: "42501", Message: "permission denied for schema cast_private", Position: strconv.Itoa(strings.Index(q, "cast_private") + 1)}
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, want)
					s.SelectRows = true
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if private && prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
				}
			}
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Select("SELECT i,s,n FROM secret", []string{"i", "s", "n"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(7), "oops", int64(2147483648)}}))
	}
}

func TestReferenceRoleGrantLockNegativeMigration(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("CREATE TABLE t(v INT)", 0))
		for _, tc := range []struct{ q, code, message, pos string }{
			{"LOCK TABLE t IN ACCESS EXCLUSIVE MODE", "25P01", "LOCK TABLE can only be used in transaction blocks", ""},
			{"SELECT * FROM missing", "42P01", `relation "missing" does not exist`, "15"},
			{"SELECT * FROM t WHERE v = )", "42601", `syntax error at or near ")"`, "27"},
		} {
			s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.pos})
			if prepared && tc.code == "42601" {
				s.ErrorStage = "prepare"
			}
			run(s)
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.RoleGrantDenied("LOCK TABLE t IN ACCESS EXCLUSIVE MODE", "t"))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
		run(fixture.Exec("ROLLBACK", 0))
	})
}
