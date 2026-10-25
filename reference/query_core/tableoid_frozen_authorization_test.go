package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryTableOIDFrozenPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"toi_frozen_owner", "toi_frozen_caller"})
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		routineExec(t, p, b, c,
			"CREATE ROLE toi_frozen_owner",
			"CREATE ROLE toi_frozen_caller",
			"GRANT USAGE ON SCHEMA public TO toi_frozen_owner,toi_frozen_caller",
			"CREATE TABLE toi_frozen_base(a INT)")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO toi_frozen_base VALUES(7)", 1))
		physical := tableoidCell(b, tableoidIdentity(t, p, c, "toi_frozen_base"))
		routineExec(t, p, b, c,
			"GRANT SELECT ON toi_frozen_base TO toi_frozen_owner,toi_frozen_caller",
			"GRANT SELECT(a) ON toi_frozen_base TO toi_frozen_owner,toi_frozen_caller",
			"CREATE VIEW toi_frozen_system AS SELECT q.tableoid AS physical FROM toi_frozen_base q",
			"CREATE VIEW toi_frozen_ordinary AS SELECT a FROM toi_frozen_base",
			"ALTER VIEW toi_frozen_system OWNER TO toi_frozen_owner",
			"ALTER VIEW toi_frozen_ordinary OWNER TO toi_frozen_owner",
			"GRANT SELECT ON toi_frozen_system,toi_frozen_ordinary TO toi_frozen_caller",
			"CREATE FUNCTION toi_frozen_direct() RETURNS oid LANGUAGE SQL BEGIN ATOMIC SELECT q.tableoid FROM toi_frozen_base q; END",
			"CREATE FUNCTION toi_frozen_via_view() RETURNS oid LANGUAGE SQL BEGIN ATOMIC SELECT physical FROM toi_frozen_system; END",
			"ALTER FUNCTION toi_frozen_direct() OWNER TO toi_frozen_owner",
			"ALTER FUNCTION toi_frozen_via_view() OWNER TO toi_frozen_owner",
			"GRANT EXECUTE ON FUNCTION toi_frozen_direct() TO toi_frozen_caller",
			"GRANT EXECUTE ON FUNCTION toi_frozen_via_view() TO toi_frozen_caller",
			"SET SESSION AUTHORIZATION toi_frozen_caller")

		cases := []struct {
			query, routine string
			want           fixture.Step
		}{
			{"SELECT q.tableoid AS physical FROM toi_frozen_base q", "", fixture.Step{}},
			{"SELECT physical FROM toi_frozen_system", "", fixture.Step{}},
			{"SELECT toi_frozen_direct() AS physical", "toi_frozen_direct", fixture.Step{}},
			{"SELECT toi_frozen_via_view() AS physical", "toi_frozen_via_view", fixture.Step{}},
			{"SELECT a FROM toi_frozen_ordinary", "", fixture.Step{}},
		}
		held := make([]fixture.HeldStatement, len(cases))
		for i := range cases {
			cases[i].want = fixture.Select(cases[i].query, []string{"physical"}, []string{"OID"}, [][]any{{physical}})
			if i == 4 {
				cases[i].want = fixture.Select(cases[i].query, []string{"a"}, []string{"INT4"}, [][]any{{int64(7)}})
			}
			stmt, err := c.PrepareContext(p.Ctx, cases[i].query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: cases[i].query}
		}
		check := func(index int, allowed bool) {
			t.Helper()
			tc := cases[index]
			s := tc.want
			if !allowed {
				want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table toi_frozen_base")
				if tc.routine != "" {
					// A scalar SQL body reading a relation is not the simple
					// expression-only inlining case. The oracle will check this
					// independently seeded, complete routine context.
					want.Context = "SQL function \"" + tc.routine + "\" statement 1"
				}
				s = fixture.ErrorDiagnostic(tc.query, want)
				s.SelectRows, s.ErrorStage = true, "execute"
			}
			tableoidRoutes(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held[index], b.Recorder, s)
		}
		for i := range cases {
			check(i, true)
		}

		// The caller can read the base, but cannot lend that authority to a
		// definer view whose owner retains only an ordinary-column grant.
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE SELECT ON toi_frozen_base FROM toi_frozen_owner", "GRANT SELECT(a) ON toi_frozen_base TO toi_frozen_owner", "SET SESSION AUTHORIZATION toi_frozen_caller")
		check(0, true)
		check(1, false)
		check(2, true)
		check(3, false)
		check(4, true)

		// Reverse the grants. Stored view reads use its owner; the standard
		// direct-body routine is SECURITY INVOKER despite its different owner.
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON toi_frozen_base TO toi_frozen_owner", "REVOKE SELECT ON toi_frozen_base FROM toi_frozen_caller", "GRANT SELECT(a) ON toi_frozen_base TO toi_frozen_caller", "SET SESSION AUTHORIZATION toi_frozen_caller")
		check(0, false)
		check(1, true)
		check(2, false)
		check(3, true)
		check(4, true)

		// Revalidate the same retained statements after source-owner revocation
		// and regrant, not just on their first successful execution.
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "REVOKE SELECT ON toi_frozen_base FROM toi_frozen_owner", "GRANT SELECT(a) ON toi_frozen_base TO toi_frozen_owner", "SET SESSION AUTHORIZATION toi_frozen_caller")
		check(1, false)
		check(3, false)
		check(4, true)
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "GRANT SELECT ON toi_frozen_base TO toi_frozen_owner", "GRANT SELECT ON toi_frozen_base TO toi_frozen_caller", "SET SESSION AUTHORIZATION toi_frozen_caller")
		for i := range cases {
			check(i, true)
		}
		routineExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
	})
}
