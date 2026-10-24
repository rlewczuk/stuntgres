package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoleRevokeTransactions(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		revokeTable(run)
		for _, end := range []string{"ROLLBACK", "COMMIT"} {
			run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0))
			run(fixture.Exec("BEGIN", 0))
			q := "REVOKE SELECT ON t FROM rg_reader CASCADE"
			run(fixture.Exec(q, 0))
			run(fixture.Exec(end, 0))
			revokeRead(run, "rg_reader", end == "ROLLBACK")
		}
		for _, q := range []string{"REVOKE SELECT ON missing_table FROM missing_role CASCADE", "REVOKE GRANT OPTION FOR SELECT(a) ON missing_table FROM missing_role RESTRICT"} {
			var stmt *sql.Stmt
			if prepared {
				var err error
				stmt, err = c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("autocommit Prepare", err)
				}
				defer stmt.Close()
			}
			run(fixture.Exec("BEGIN READ ONLY", 0))
			var source fixture.Executor = c
			if stmt != nil {
				source = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			}
			fixture.RunStep(t, p.Ctx, source, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "25006", Message: "cannot execute REVOKE in a read-only transaction"}))
			run(fixture.Exec("ROLLBACK", 0))
		}
		q := "PREPARE revoke_utility AS REVOKE SELECT ON t FROM rg_reader"
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "REVOKE"`, Position: fixture.Position(strings.Index(q, "REVOKE") + 1)})
		s.ErrorStage = "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		run(s)
	})
}

func TestReferenceRoleRevokeLifecycle(t *testing.T) {
	fixture.RoleGrantScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		q := "REVOKE GRANT OPTION FOR SELECT ON t FROM rg_future GRANTED BY CURRENT_USER CASCADE"
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		utility, err := nc.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("effect-free Prepare before names exist", err)
		}
		defer utility.Close()
		revokeNotices(t, notices, nil)
		run(fixture.Exec("CREATE ROLE rg_future", 0))
		revokeTable(run)
		run(fixture.Exec("GRANT SELECT ON t TO rg_a WITH GRANT OPTION", 0))
		revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_future WITH GRANT OPTION", 0)) })
		revokeActor(run, "rg_future", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0)) })
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("SET SESSION AUTHORIZATION rg_a", 0))
		// The held symbolic actor is resolved at Execute, not pinned to admin
		// at Prepare; only a-origin removal can remove future's delegation.
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: nc, Statement: utility, SQL: q}, recorder, fixture.Exec(q, 0))
		revokeNotices(t, notices, nil)
		fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		revokeRead(run, "rg_future", true)
		revokeRead(run, "rg_reader", false)
		revokeActor(run, "rg_a", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_future WITH GRANT OPTION", 0)) })
		revokeActor(run, "rg_future", func() { run(fixture.Exec("GRANT SELECT ON t TO rg_reader", 0)) })
		run(fixture.Exec("SET SESSION AUTHORIZATION rg_reader", 0))
		selectQ := "SELECT a,b FROM t"
		stmt, err := c.PrepareContext(p.Ctx, selectQ)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: selectQ}
		want := fixture.Select(selectQ, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(9)}})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		old, err := stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		aq := "REVOKE GRANT OPTION FOR SELECT ON t FROM rg_a CASCADE"
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec(aq, 0))
		fixture.DeleteReferenceConsume(t, b.PG, old, nil, want)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.RoleGrantDenied(selectQ, "t"))
		fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("GRANT SELECT ON t TO rg_reader", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, want)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// The native adapter guarantees precanceled execution has no effects.
		// lib/pq may complete a fast utility before observing cancellation.
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			result, err := admin.ExecContext(ctx, "REVOKE SELECT ON t FROM rg_reader CASCADE")
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("canceled revoke", result, err)
			}
		}
		revokeRead(run, "rg_reader", true)
		run(fixture.Exec("REVOKE SELECT ON t FROM rg_reader", 0))
		run(fixture.Exec("GRANT SELECT(a) ON t TO rg_reader WITH GRANT OPTION", 0))
		run(fixture.Exec("ALTER TABLE t RENAME COLUMN a TO renamed", 0))
		run(fixture.Exec("ALTER TABLE t ADD COLUMN a INT DEFAULT 11", 0))
		revokeActor(run, "rg_reader", func() {
			run(fixture.Select("SELECT renamed FROM t", []string{"renamed"}, []string{"INT4"}, [][]any{{int64(7)}}))
			run(fixture.RoleGrantDenied("SELECT a FROM t", "t"))
		})
		run(fixture.Exec("REVOKE GRANT OPTION FOR SELECT(a) ON t FROM rg_reader CASCADE", 0))
		revokeActor(run, "rg_reader", func() { run(fixture.Exec("GRANT SELECT(renamed) ON t TO rg_c", 0)) })
		run(fixture.Exec("REVOKE SELECT(renamed) ON t FROM rg_reader CASCADE", 0))
		revokeActor(run, "rg_c", func() { run(fixture.RoleGrantDenied("SELECT renamed FROM t", "t")) })
		run(fixture.Exec("ALTER TABLE t DROP COLUMN renamed", 0))
		revokeRead(run, "rg_reader", false)
		// Closing a borrower does not roll back the committed ACL change.
		if err := admin.Close(); err != nil {
			t.Fatal(err)
		}
		revokeRead(run, "rg_reader", false)
		if b.PG {
			return
		}
		var out bytes.Buffer
		err = stuntgres.Dump(p.Ctx, p.Native, &out)
		if err == nil || err.Error() != "stuntgres: dump cannot serialize explicit relation or column ACL" || out.Len() != 0 {
			t.Fatal("Dump ACL boundary", out.String(), err)
		}
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE replacement(a INT); REVOKE SELECT ON replacement FROM rg_reader CASCADE"))
		fixture.State(t, err, "0A000")
		run(fixture.Select("SELECT a,b FROM t", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(9)}}))
		run(fixture.Exec("GRANT rg_group TO rg_reader WITH INHERIT TRUE", 0))
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		revokeTable(run)
		revokeRead(run, "rg_reader", false)
		run(fixture.Exec("GRANT SELECT ON t TO rg_group", 0))
		revokeRead(run, "rg_reader", true)
	})
}
