package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

func TestReferenceQueryDistinctOnAuthority(t *testing.T) {
	roleReferenceCleanup(t, []string{"distinct_on_reader"})
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE acl_t(id INT,secret INT)", 0))
		run(referenceExec("CREATE TABLE acl_empty(id INT,secret INT)", 0))
		run(referenceExec("INSERT INTO acl_t VALUES(1,9),(1,8),(2,8)", 3))
		q := "SELECT DISTINCT ON(secret) id AS secret FROM acl_t ORDER BY secret"
		s := referenceSelect(q, []string{"secret"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		queryDistinctBoth(run, s)
		if !b.pg {
			return
		} // Native physical ACL manifests require consumer units, not a fabricated SQL GRANT.
		for _, q := range []string{"CREATE ROLE distinct_on_reader", "GRANT USAGE ON SCHEMA public TO distinct_on_reader", "GRANT SELECT(id) ON acl_t,acl_empty TO distinct_on_reader", "SET SESSION AUTHORIZATION distinct_on_reader"} {
			run(referenceExec(q, 0))
		}
		defer c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{c, stmt, q}
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		for _, table := range []string{"acl_t", "acl_empty"} {
			for _, suffix := range []string{"", " WHERE FALSE", " LIMIT 0"} {
				for _, query := range []string{"SELECT DISTINCT ON(secret) id FROM " + table + suffix, "SELECT DISTINCT ON(" + table + ".secret) id AS secret FROM " + table + suffix} {
					s := referenceImplicitError(query, referenceDiagnostic{Code: "42501", Message: "permission denied for table " + table})
					s.selectRows = true
					s.errorStage = "execute"
					queryDistinctBoth(run, s)
				}
			}
		}
		if _, err := b.db.ExecContext(p.ctx, "REVOKE SELECT(id) ON acl_t FROM distinct_on_reader"); err != nil {
			t.Fatal(err)
		}
		denied := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table acl_t"})
		denied.selectRows = true
		denied.errorStage = "execute"
		referenceRunStep(t, p.ctx, held, b.recorder, denied)
		if _, err := b.db.ExecContext(p.ctx, "GRANT SELECT(id) ON acl_t TO distinct_on_reader"); err != nil {
			t.Fatal(err)
		}
		referenceRunStep(t, p.ctx, held, b.recorder, s)
	})
}

func TestReferenceQueryDistinctOnCancellation(t *testing.T) {
	queryDistinctOnScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		queryDistinctOnEvents(run)
		s := queryDistinctOnHidden()
		stmt, err := c.PrepareContext(p.ctx, s.query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(referenceExec("CREATE TABLE cancel_dest(id INT)", 0))
		run(referenceExec("INSERT INTO cancel_dest VALUES(17)", 1))
		if b.pg {
			// A running ON query, not a generic sleep-only witness. The existing
			// helper observes exact active SQL, cancels the backend and joins it.
			for _, prepared := range []bool{false, true} {
				referenceSessionCancelQuery(t, p.ctx, c, prepared, "SELECT DISTINCT ON(k) id,pg_sleep(600) FROM events ORDER BY k,stamp DESC,id DESC", true)
				referenceSessionCancelQuery(t, p.ctx, c, prepared, "INSERT INTO cancel_dest SELECT DISTINCT ON(k) id FROM events WHERE pg_sleep(600) IS NOT NULL ORDER BY k,stamp DESC,id DESC", false)
			}
		} else {
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			for _, held := range []bool{false, true} {
				var rows *sql.Rows
				var err error
				if held {
					rows, err = stmt.QueryContext(ctx)
				} else {
					rows, err = c.QueryContext(ctx, s.query)
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
		run(referenceSelect("SELECT id FROM cancel_dest", []string{"id"}, []string{"INT4"}, [][]any{{int64(17)}}))
		run(s)
		referenceRunStep(t, p.ctx, referenceHeldStatement{c, stmt, s.query}, b.recorder, s)
	})
}
