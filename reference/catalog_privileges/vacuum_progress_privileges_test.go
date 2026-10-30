package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceVacuumProgressPrivileges(t *testing.T) {
	roles := []string{"vacprog_worker", "vacprog_member", "vacprog_other", "vacprog_effective", "vacprog_effective_member", "vacprog_reader", "vacprog_monitor"}
	fixture.RoleReferenceCleanup(t, roles)
	vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		admin := vacuumConn(t, p, b)
		for _, role := range roles {
			copyProgressExec(t, p.Ctx, admin, "CREATE ROLE "+role+" LOGIN PASSWORD 'role-attributes-fixture'")
			copyProgressExec(t, p.Ctx, admin, "GRANT USAGE ON SCHEMA public TO "+role)
		}
		vacuumProgressWorkload(t, p, b, admin, "vacuum_privileges")
		for _, q := range []string{
			"ALTER TABLE vacuum_privileges OWNER TO vacprog_effective",
			"GRANT vacprog_effective TO vacprog_worker",
			"GRANT vacprog_worker TO vacprog_member",
			"GRANT vacprog_other TO vacprog_member",
			"GRANT vacprog_effective TO vacprog_effective_member",
			"GRANT SELECT, MAINTAIN ON vacuum_privileges TO vacprog_other",
			"GRANT pg_read_all_stats TO vacprog_reader",
			"GRANT pg_monitor TO vacprog_monitor",
		} {
			copyProgressExec(t, p.Ctx, admin, q)
		}
		worker := copyProgressConn(t, p, fixture.RoleAttributesPool(t, p, b, "vacprog_worker"))
		id := copyProgressIdentify(t, p, worker, "vacuum_privileges")
		copyProgressExec(t, p.Ctx, worker, "SET ROLE vacprog_effective")
		// Establish each physical observer and prepare before starting real work.
		type observer struct {
			role string
			conn *sql.Conn
			held fixture.HeldStatement
		}
		var observers []observer
		for _, role := range roles {
			c := copyProgressConn(t, p, fixture.RoleAttributesPool(t, p, b, role))
			observers = append(observers, observer{role, c, vacuumHeld(t, p, c, vacuumProgressQuery)})
		}
		adminHeld := vacuumHeld(t, p, admin, vacuumProgressQuery)
		r := vacuumProgressLaunch(t, p, b, worker, "vacuum_privileges")
		first := vacuumProgressObserve(t, p, adminHeld.Statement, id, r)
		vacuumProgressVisible(t, first, id, b.PG)
		for _, o := range observers {
			restricted := o.role == "vacprog_other" || o.role == "vacprog_effective" || o.role == "vacprog_effective_member"
			assert := func(prepared bool, masked bool) {
				var s vacuumProgressSnapshot
				var ok bool
				if prepared {
					s, ok = vacuumProgressScan(t, o.held.Statement.QueryRowContext(p.Ctx, id.pid))
				} else {
					s, ok = vacuumProgressScan(t, o.conn.QueryRowContext(p.Ctx, vacuumProgressQuery, id.pid))
				}
				if !ok {
					t.Fatal("worker ended before privilege snapshot", o.role)
				}
				select {
				case <-r.finished:
					t.Fatal("privilege snapshot was not live", o.role)
				default:
				}
				if masked {
					vacuumProgressMasked(t, s, id)
				} else {
					vacuumProgressVisible(t, s, id, b.PG)
				}
			}
			assert(false, restricted)
			assert(true, restricted)
			if o.role == "vacprog_member" {
				for _, change := range []struct {
					q      string
					masked bool
				}{{"SET ROLE vacprog_other", true}, {"RESET ROLE", false}} {
					copyProgressExec(t, p.Ctx, o.held.Executor, change.q)
					assert(true, change.masked)
				}
				copyProgressExec(t, p.Ctx, admin, "REVOKE vacprog_worker FROM vacprog_member")
				assert(true, true)
				copyProgressExec(t, p.Ctx, admin, "GRANT vacprog_worker TO vacprog_member")
				assert(true, false)
			}
			if o.role == "vacprog_reader" {
				copyProgressExec(t, p.Ctx, admin, "REVOKE pg_read_all_stats FROM vacprog_reader")
				assert(true, true)
				copyProgressExec(t, p.Ctx, admin, "GRANT pg_read_all_stats TO vacprog_reader")
				assert(true, false)
			}
		}
		copyProgressExec(t, p.Ctx, admin, "SET SESSION AUTHORIZATION vacprog_other")
		masked, ok := vacuumProgressScan(t, adminHeld.Statement.QueryRowContext(p.Ctx, id.pid))
		if !ok {
			t.Fatal("no session-authorization snapshot")
		}
		vacuumProgressMasked(t, masked, id)
		copyProgressExec(t, p.Ctx, admin, "RESET SESSION AUTHORIZATION")
		vacuumProgressVisible(t, vacuumProgressObserve(t, p, adminHeld.Statement, id, r), id, b.PG)
		vacuumProgressCancel(t, p, b, admin, id.pid, "vacuum_privileges", r.cancel, func() error { err := <-r.done; r.joined = true; return err })
		vacuumCount(t, p.Ctx, admin, "vacuum_privileges", 0)
		vacuumProgressVisible(t, first, id, b.PG)
	})
}
