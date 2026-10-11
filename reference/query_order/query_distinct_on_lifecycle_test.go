package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryDistinctOnAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"distinct_on_reader"})
	queryDistinctOnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE acl_t(id INT,secret INT)", 0))
		run(fixture.Exec("CREATE TABLE acl_empty(id INT,secret INT)", 0))
		run(fixture.Exec("INSERT INTO acl_t VALUES(1,9),(1,8),(2,8)", 3))
		q := "SELECT DISTINCT ON(secret) id AS secret FROM acl_t ORDER BY secret"
		s := fixture.Select(q, []string{"secret"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		fixture.QueryDistinctBoth(run, s)
		if !b.PG {
			return
		} // Native physical ACL manifests require consumer units, not a fabricated SQL GRANT.
		for _, q := range []string{"CREATE ROLE distinct_on_reader", "GRANT USAGE ON SCHEMA public TO distinct_on_reader", "GRANT SELECT(id) ON acl_t,acl_empty TO distinct_on_reader", "SET SESSION AUTHORIZATION distinct_on_reader"} {
			run(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{c, stmt, q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		for _, table := range []string{"acl_t", "acl_empty"} {
			for _, suffix := range []string{"", " WHERE FALSE", " LIMIT 0"} {
				for _, query := range []string{"SELECT DISTINCT ON(secret) id FROM " + table + suffix, "SELECT DISTINCT ON(" + table + ".secret) id AS secret FROM " + table + suffix} {
					s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table " + table})
					s.SelectRows = true
					s.ErrorStage = "execute"
					fixture.QueryDistinctBoth(run, s)
				}
			}
		}
		if _, err := b.DB.ExecContext(p.Ctx, "REVOKE SELECT(id) ON acl_t FROM distinct_on_reader"); err != nil {
			t.Fatal(err)
		}
		denied := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table acl_t"})
		denied.SelectRows = true
		denied.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, denied)
		if _, err := b.DB.ExecContext(p.Ctx, "GRANT SELECT(id) ON acl_t TO distinct_on_reader"); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	})
}

func TestReferenceQueryDistinctOnCancellation(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		queryDistinctOnEvents(run)
		s := queryDistinctOnHidden()
		stmt, err := c.PrepareContext(p.Ctx, s.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(fixture.Exec("CREATE TABLE cancel_dest(id INT)", 0))
		run(fixture.Exec("INSERT INTO cancel_dest VALUES(17)", 1))
		if b.PG {
			// A running ON query, not a generic sleep-only witness. The existing
			// helper observes exact active SQL, cancels the backend and joins it.
			for _, prepared := range []bool{false, true} {
				fixture.SessionCancelQuery(t, p.Ctx, c, prepared, "SELECT DISTINCT ON(k) id,pg_sleep(600) FROM events ORDER BY k,stamp DESC,id DESC", true)
				fixture.SessionCancelQuery(t, p.Ctx, c, prepared, "INSERT INTO cancel_dest SELECT DISTINCT ON(k) id FROM events WHERE pg_sleep(600) IS NOT NULL ORDER BY k,stamp DESC,id DESC", false)
			}
		} else {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, held := range []bool{false, true} {
				var rows *sql.Rows
				var err error
				if held {
					rows, err = stmt.QueryContext(ctx)
				} else {
					rows, err = c.QueryContext(ctx, s.Query)
				}
				if rows != nil {
					rows.Close()
					t.Fatal("precanceled ON exposed rows")
				}
				var state interface{ SQLState() string }
				if !errors.Is(err, context.Canceled) || errors.As(err, &state) {
					t.Fatal("ordinary context failure", err)
				}
			}
			if result, err := c.ExecContext(ctx, "INSERT INTO cancel_dest SELECT DISTINCT ON(k) id FROM events ORDER BY k,stamp DESC,id DESC"); result != nil || !errors.Is(err, context.Canceled) {
				t.Fatal(result, err)
			}
		}
		run(fixture.Select("SELECT id FROM cancel_dest", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}))
		run(s)
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{c, stmt, s.Query}, b.Recorder, s)
	})
}
