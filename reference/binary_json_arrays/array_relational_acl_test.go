package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayRelationalAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ar_reader"})
	arrayRelationalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{"CREATE ROLE ar_reader", "CREATE TABLE ar_acl(a INT[],n INT,b TEXT[])", "CREATE SCHEMA ar_private", "CREATE TABLE ar_private.hidden(a INT[])"} {
			run(fixture.Exec(q, 0))
		}
		defer func() {
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA ar_private CASCADE"} {
				if _, err := c.ExecContext(p.Ctx, q); err != nil {
					t.Error(err)
				}
			}
			if !b.PG {
				if _, err := c.ExecContext(p.Ctx, "ALTER SCHEMA public OWNER TO CURRENT_USER"); err != nil {
					t.Error(err)
				}
			}
		}()
		run(fixture.Exec("INSERT INTO ar_acl VALUES ('{1,NULL}',1,'{a}')", 1))
		if b.PG {
			run(fixture.Exec("GRANT USAGE ON SCHEMA public TO ar_reader", 0))
		} else {
			run(fixture.Exec("ALTER SCHEMA public OWNER TO ar_reader", 0))
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION ar_reader", 0))
		denied := func(t *testing.T, q string) {
			t.Helper()
			// ACL checks run on execution for table/column access, including held plans.
			d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table ar_acl")
			s := fixture.ErrorDiagnostic(q, d)
			s.SelectRows, s.ErrorStage = true, "execute"
			arrayRelationalBoth(t, p, b, c, s)
		}
		for _, expr := range []string{"array_shuffle(a)", "array_sample(a,n)", "array_sample(NULL::int[],n)", "array_sample(a,NULL::int)", "unnest(a)", "abs(unnest(a))"} {
			for _, suffix := range []string{"", " WHERE FALSE", " LIMIT 0"} {
				q := "SELECT " + expr + " FROM ar_acl" + suffix
				t.Run(q, func(t *testing.T) { denied(t, q) })
			}
		}
		t.Run("private_schema", func(t *testing.T) {
			q := "/*é*/ SELECT array_shuffle(a) FROM ar_private.hidden WHERE FALSE"
			d := arrayInspectDiagnostic(q, "ar_private", "42501", "permission denied for schema ar_private", "")
			arrayRelationalError(t, p, b, c, q, d, false)
		})
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT(a) ON ar_acl TO ar_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ar_reader", 0))
		for _, q := range []string{"SELECT array_sample(a,n) FROM ar_acl", "SELECT array_sample(NULL::int[],n) FROM ar_acl LIMIT 0", "SELECT x,y FROM ar_acl,unnest(a,b) AS u(x,y) WHERE FALSE"} {
			t.Run("column/"+q, func(t *testing.T) { denied(t, q) })
		}
		arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT cardinality(array_shuffle(a)) AS n FROM ar_acl", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT SELECT ON ar_acl TO ar_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ar_reader", 0))
		q := "SELECT x,y FROM ar_acl,unnest(a,b) AS u(x,y) ORDER BY x NULLS LAST"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		good := fixture.Select(q, []string{"x", "y"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {nil, nil}})
		fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("REVOKE SELECT ON ar_acl FROM ar_reader", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION ar_reader", 0))
		bad := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table ar_acl"))
		bad.SelectRows, bad.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
	})
}
