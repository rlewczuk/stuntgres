package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAnalyzeProgressPrivileges(t *testing.T) {
	roles := []string{"anprog_worker", "anprog_member", "anprog_other", "anprog_effective", "anprog_effective_member", "anprog_reader", "anprog_monitor", "anprog_super"}
	fixture.RoleReferenceCleanup(t, roles)
	analyzeProgressScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		admin := vacuumConn(t, p, b)
		for _, role := range roles {
			copyProgressExec(t, p.Ctx, admin, "CREATE ROLE "+role+" LOGIN PASSWORD 'role-attributes-fixture'")
			copyProgressExec(t, p.Ctx, admin, "GRANT USAGE ON SCHEMA public TO "+role)
		}
		analyzeProgressSetup(t, p, admin, "analyze_privileges")
		for _, q := range []string{
			"ALTER TABLE analyze_privileges OWNER TO anprog_effective",
			"ALTER TABLE analyze_privileges_child OWNER TO anprog_effective",
			"GRANT anprog_effective TO anprog_worker",
			"GRANT anprog_worker TO anprog_member",
			"GRANT anprog_other TO anprog_member",
			"GRANT anprog_effective TO anprog_effective_member",
			"GRANT SELECT,MAINTAIN ON analyze_privileges TO anprog_other",
			"GRANT pg_read_all_stats TO anprog_reader",
			"GRANT pg_monitor TO anprog_monitor",
			"ALTER ROLE anprog_super SUPERUSER",
		} {
			copyProgressExec(t, p.Ctx, admin, q)
		}
		worker := copyProgressConn(t, p, fixture.RoleAttributesPool(t, p, b, "anprog_worker"))
		copyProgressExec(t, p.Ctx, worker, "SET ROLE anprog_effective")
		type observer struct {
			role string
			conn *sql.Conn
			held fixture.HeldStatement
		}
		var observers []observer
		for _, role := range roles {
			c := copyProgressConn(t, p, fixture.RoleAttributesPool(t, p, b, role))
			observers = append(observers, observer{role, c, vacuumHeld(t, p, c, analyzeProgressQuery)})
		}
		adminHeld := vacuumHeld(t, p, admin, analyzeProgressQuery)
		id, holder, run := analyzeProgressHold(t, p, b, worker, "analyze_privileges", true)
		first := analyzeProgressObserve(t, p, adminHeld, id, run)
		analyzeProgressVisible(t, first, id, b.PG)
		var detached *sql.Rows
		if !b.PG {
			var err error
			detached, err = admin.QueryContext(p.Ctx, analyzeProgressQuery, id.pid)
			if err != nil {
				t.Fatal(err)
			}
			defer detached.Close()
		}
		for _, o := range observers {
			masked := o.role == "anprog_other" || o.role == "anprog_effective" || o.role == "anprog_effective_member"
			assert := func(x fixture.Executor, restricted bool) {
				s := analyzeProgressObserve(t, p, x, id, run)
				if restricted {
					analyzeProgressMasked(t, s, id)
				} else {
					analyzeProgressVisible(t, s, id, b.PG)
				}
			}
			assert(o.conn, masked)
			assert(o.held, masked)
			if o.role == "anprog_member" {
				copyProgressExec(t, p.Ctx, o.conn, "SET ROLE anprog_other")
				assert(o.held, true)
				copyProgressExec(t, p.Ctx, o.conn, "RESET ROLE")
				assert(o.held, false)
				copyProgressExec(t, p.Ctx, admin, "REVOKE anprog_worker FROM anprog_member")
				assert(o.held, true)
				copyProgressExec(t, p.Ctx, admin, "GRANT anprog_worker TO anprog_member")
				assert(o.held, false)
			}
			if o.role == "anprog_reader" {
				copyProgressExec(t, p.Ctx, admin, "REVOKE pg_read_all_stats FROM anprog_reader")
				assert(o.held, true)
				copyProgressExec(t, p.Ctx, admin, "GRANT pg_read_all_stats TO anprog_reader")
				assert(o.held, false)
			}
		}
		copyProgressExec(t, p.Ctx, admin, "SET SESSION AUTHORIZATION anprog_other")
		analyzeProgressMasked(t, analyzeProgressObserve(t, p, adminHeld, id, run), id)
		copyProgressExec(t, p.Ctx, admin, "RESET SESSION AUTHORIZATION")
		analyzeProgressVisible(t, analyzeProgressObserve(t, p, adminHeld, id, run), id, b.PG)
		analyzeProgressCancel(t, p, b, admin, id, run)
		copyProgressExec(t, p.Ctx, holder, "ROLLBACK")
		for _, o := range observers {
			analyzeProgressIdle(t, p, o.held, id.pid)
		}
		analyzeProgressVisible(t, first, id, b.PG)
		if !b.PG {
			// Native replacement has no PostgreSQL control analogue. Keep the
			// already detached SQL result values through the public Reset route.
			analyzeProgressReset(t, p, detached, id)
		}
	})
}
