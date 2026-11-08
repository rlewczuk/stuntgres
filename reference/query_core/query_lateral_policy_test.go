package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryLateralPolicyIdentity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"lat_policy_reader"})
	subqueryReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(context.Background(), "RESET ROLE")
		run := subqueryRun(t, p, b, c)
		for _, q := range []string{
			"CREATE ROLE lat_policy_reader NOSUPERUSER NOBYPASSRLS",
			"GRANT USAGE ON SCHEMA public TO lat_policy_reader",
			"CREATE TABLE lat_policy_target(id INT PRIMARY KEY,v TEXT)",
			"CREATE TABLE lat_policy_helper(id INT PRIMARY KEY)",
			"CREATE TABLE lat_policy_local(id INT PRIMARY KEY)",
			"GRANT SELECT ON lat_policy_target,lat_policy_helper,lat_policy_local TO lat_policy_reader",
			"ALTER TABLE lat_policy_target ENABLE ROW LEVEL SECURITY",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO lat_policy_target VALUES(1,'one'),(2,'two'),(3,'three'),(4,'four')", 4))
		run(fixture.Exec("INSERT INTO lat_policy_helper VALUES(1),(3)", 2))
		run(fixture.Exec("INSERT INTO lat_policy_local VALUES(3)", 1))
		const query = "SELECT id,v FROM lat_policy_target ORDER BY id"
		all := fixture.Select(query, []string{"id", "v"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), "two"}, {int64(3), "three"}, {int64(4), "four"}})
		subqueryBoth(run, all)

		for _, tc := range []struct {
			name      string
			predicate string
			rows      [][]any
		}{
			{
				name: "parent_capture",
				predicate: "EXISTS(SELECT 1 FROM lat_policy_helper h CROSS JOIN LATERAL " +
					"(SELECT (SELECT h.tableoid) AS o) q " +
					"WHERE q.o='lat_policy_helper'::regclass::oid AND h.id=lat_policy_target.id)",
				rows: [][]any{{int64(1), "one"}, {int64(3), "three"}},
			},
			{
				name: "parent_and_scalar_local",
				// The scalar query owns l but captures h. Both identities must
				// survive their respective frame imports exactly once; its
				// missing local row also excludes helper id 1.
				predicate: "EXISTS(SELECT 1 FROM lat_policy_helper h CROSS JOIN LATERAL " +
					"(SELECT (SELECT h.tableoid='lat_policy_helper'::regclass::oid " +
					"AND l.tableoid='lat_policy_local'::regclass::oid " +
					"FROM lat_policy_local l WHERE l.id=h.id) AS identities_match) q " +
					"WHERE q.identities_match AND h.id=lat_policy_target.id)",
				rows: [][]any{{int64(3), "three"}},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				run := subqueryRun(t, p, b, c)
				run(fixture.Exec("CREATE POLICY lat_policy_select ON lat_policy_target FOR SELECT TO lat_policy_reader USING("+tc.predicate+")", 0))
				defer c.ExecContext(context.Background(), "DROP POLICY lat_policy_select ON lat_policy_target")
				defer c.ExecContext(context.Background(), "RESET ROLE")
				run(fixture.Exec("SET ROLE lat_policy_reader", 0))
				subqueryBoth(run, fixture.Select("SELECT current_user::text AS actor,rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user", []string{"actor", "rolsuper", "rolbypassrls"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{"lat_policy_reader", false, false}}))
				want := fixture.Select(query, []string{"id", "v"}, []string{"INT4", "TEXT"}, tc.rows)
				for range 2 {
					subqueryBoth(run, want)
				}

				const heldSQL = "SELECT id,v FROM lat_policy_target WHERE id >= $1 ORDER BY id"
				stmt, err := c.PrepareContext(p.Ctx, heldSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: heldSQL}
				for _, execution := range []struct {
					minimum int64
					rows    [][]any
				}{
					{1, tc.rows},
					{2, [][]any{{int64(3), "three"}}},
					{4, nil},
					{1, tc.rows},
				} {
					step := fixture.Select(heldSQL, []string{"id", "v"}, []string{"INT4", "TEXT"}, execution.rows, execution.minimum)
					subqueryBoth(run, step)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, step)
				}
			})
		}
		// The reader's filtering must not delete or change the hidden rows.
		subqueryBoth(run, all)
	})
}
