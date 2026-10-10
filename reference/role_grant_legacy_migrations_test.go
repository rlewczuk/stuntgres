package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
)

func TestReferenceRoleGrantCastPreflightMigration(t *testing.T) {
	roleReferenceCleanup(t, []string{"cast_reader"})
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		c, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		for _, q := range []string{"CREATE TABLE e(i INT4)", "CREATE TABLE populated(i INT4)"} {
			run(referenceExec(q, 0))
		}
		run(referenceExec("INSERT INTO populated VALUES(1)", 1))
		for _, tc := range []struct {
			expression, code, message string
			bad, good                 any
		}{
			{"int4($1::text)", "22P02", `invalid input syntax for type integer: "oops"`, "oops", "42"},
			{"CAST($1::int8 AS int4)", "22003", "integer out of range", int64(2147483648), int64(42)},
		} {
			for _, from := range []string{"e", "populated WHERE i=0", "populated"} {
				q := "SELECT " + tc.expression + " FROM " + from
				s := referenceImplicitError(q, referenceDiagnostic{Code: tc.code, Message: tc.message}, tc.bad)
				s.selectRows = true
				run(s)
				stmt, err := c.PrepareContext(p.ctx, q)
				if err != nil {
					t.Fatal("expected successful Prepare", err)
				}
				defer stmt.Close()
				held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
				for i, arg := range []any{tc.bad, tc.good, nil, tc.bad, tc.good} {
					if i == 0 || i == 3 {
						s.args = []any{arg}
						referenceRunStep(t, p.ctx, held, b.recorder, s)
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
					referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"int4"}, []string{"INT4"}, want, arg))
				}
			}
		}
		for _, q := range []string{"CREATE ROLE cast_reader", "CREATE TABLE secret(i INT4,s TEXT,n INT8)", "CREATE SCHEMA cast_private", "CREATE TABLE cast_private.secret(i INT4,s TEXT,n INT8)"} {
			run(referenceExec(q, 0))
		}
		defer b.db.ExecContext(p.ctx, "DROP SCHEMA cast_private CASCADE")
		run(referenceExec("INSERT INTO secret VALUES(7,'oops',2147483648)", 1))
		if b.pg {
			run(referenceExec("GRANT USAGE ON SCHEMA public TO cast_reader", 0))
		}
		run(referenceExec("SET SESSION AUTHORIZATION cast_reader", 0))
		defer c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
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
				want := referenceDiagnostic{Code: tc.code, Message: tc.message}
				if private {
					q = "SELECT " + tc.expression + " FROM cast_private.secret"
					want = referenceDiagnostic{Code: "42501", Message: "permission denied for schema cast_private", Position: strconv.Itoa(strings.Index(q, "cast_private") + 1)}
				}
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(q, want)
					s.selectRows = true
					s.prepared = prepared
					s.errorStage = "execute"
					if private && prepared {
						s.errorStage = "prepare"
					}
					run(s)
				}
			}
		}
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceSelect("SELECT i,s,n FROM secret", []string{"i", "s", "n"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(7), "oops", int64(2147483648)}}))
	}
}

func TestReferenceRoleGrantLockNegativeMigration(t *testing.T) {
	roleGrantScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep), prepared bool) {
		run(referenceExec("CREATE TABLE t(v INT)", 0))
		for _, tc := range []struct{ q, code, message, pos string }{
			{"LOCK TABLE t IN ACCESS EXCLUSIVE MODE", "25P01", "LOCK TABLE can only be used in transaction blocks", ""},
			{"SELECT * FROM missing", "42P01", `relation "missing" does not exist`, "15"},
			{"SELECT * FROM t WHERE v = )", "42601", `syntax error at or near ")"`, "27"},
		} {
			s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.pos})
			if prepared && tc.code == "42601" {
				s.errorStage = "prepare"
			}
			run(s)
		}
		run(referenceExec("SET SESSION AUTHORIZATION rg_reader", 0))
		run(referenceExec("BEGIN", 0))
		run(roleGrantDenied("LOCK TABLE t IN ACCESS EXCLUSIVE MODE", "t"))
		run(referenceExec("ROLLBACK", 0))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("BEGIN", 0))
		run(referenceExec("LOCK TABLE t IN ACCESS EXCLUSIVE MODE NOWAIT", 0))
		run(referenceExec("ROLLBACK", 0))
	})
}
