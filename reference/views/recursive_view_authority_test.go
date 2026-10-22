package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRecursiveViewAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rv_owner", "rv_reader"})
	fixture.SchemaReferenceCleanup(t, "rv_private")
	recursiveViewReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		phase := "setup"
		step := func(x fixture.Executor, route string, s fixture.Step) {
			if !t.Run(phase+"/"+route+"/"+s.Query, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
			}) {
				t.FailNow()
			}
		}
		run := func(s fixture.Step) {
			route := "direct"
			if s.Prepared {
				route = "new-prepare"
			}
			step(c, route, s)
		}
		admin := func(q string) { step(b.DB, "admin", fixture.Exec(q, 0)) }
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE ROLE rv_owner", 0), fixture.Exec("CREATE ROLE rv_reader", 0),
			fixture.Exec("CREATE TABLE rv_acl(seed INT,ceiling INT)", 0), fixture.Exec("INSERT INTO rv_acl VALUES(1,3)", 1),
			fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO rv_owner", 0),
			fixture.Exec("GRANT USAGE ON SCHEMA public TO rv_reader", 0),
			fixture.Exec("CREATE SCHEMA rv_private", 0),
			fixture.Exec("GRANT USAGE ON SCHEMA rv_private TO rv_owner", 0),
			fixture.Exec("SET SESSION AUTHORIZATION rv_owner", 0),
		} {
			run(s)
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		phase = "destination-create-denied"
		createDenied := fixture.ErrorDiagnostic("CREATE RECURSIVE VIEW rv_private.rv_denied(n) AS SELECT 1", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema rv_private"))
		run(createDenied)
		createDenied.Prepared = true
		createDenied.ErrorStage = "execute"
		run(createDenied)
		// Creation needs namespace/destination authority, not eager base SELECT.
		phase = "create-without-base-select"
		run(fixture.Exec("CREATE RECURSIVE VIEW rv_owned(n) AS SELECT seed FROM rv_acl UNION SELECT n+1 FROM rv_owned JOIN rv_acl ON n<ceiling", 0))
		run(fixture.Exec("CREATE RECURSIVE VIEW rv_invoker(n) WITH(security_invoker=true) AS SELECT seed FROM rv_acl UNION SELECT n+1 FROM rv_invoker JOIN rv_acl ON n<ceiling", 0))
		run(fixture.Exec("CREATE VIEW rv_outer AS SELECT n FROM rv_invoker", 0))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		admin("GRANT SELECT ON rv_owned,rv_outer TO rv_reader")
		run(fixture.Exec("SET SESSION AUTHORIZATION rv_reader", 0))
		// ViewHold uses b.DB, which is still the administrative principal. Retain
		// this exact role-switched connection for both preparation and every replay.
		heldSQL := "SELECT n FROM rv_owned"
		stmt, err := c.PrepareContext(p.Ctx, heldSQL)
		if err != nil {
			t.Fatalf("initial-reader/prepare-held/%s: %v", heldSQL, err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: heldSQL}
		denied := func(q, message string) {
			s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", message))
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Prepared(s))
			if q == held.SQL {
				step(held, "same-held", s)
			}
		}
		phase = "owner-without-base-select"
		for _, suffix := range []string{"", " LIMIT 0", " WHERE FALSE"} {
			denied("SELECT n FROM rv_owned"+suffix, "permission denied for table rv_acl")
		}
		phase = "owner-base-granted"
		admin("GRANT SELECT ON rv_acl TO rv_owner")
		run(recursiveViewRows(held.SQL, "INT4", 1, 2, 3))
		step(held, "same-held", recursiveViewRows(held.SQL, "INT4", 1, 2, 3))
		phase = "original-invoker-without-base-select"
		denied("SELECT n FROM rv_outer", "permission denied for table rv_acl")
		phase = "original-invoker-base-granted"
		admin("GRANT SELECT ON rv_acl TO rv_reader")
		run(fixture.Prepared(recursiveViewRows("SELECT n FROM rv_outer", "INT4", 1, 2, 3)))
		phase = "original-invoker-base-revoked"
		admin("REVOKE SELECT ON rv_acl FROM rv_reader")
		denied("SELECT n FROM rv_outer", "permission denied for table rv_acl")
		phase = "owner-base-revoked"
		admin("REVOKE SELECT ON rv_acl FROM rv_owner")
		denied(held.SQL, "permission denied for table rv_acl")
		phase = "empty-base-owner-still-denied"
		step(b.DB, "admin", fixture.Exec("DELETE FROM rv_acl", 1))
		denied(held.SQL, "permission denied for table rv_acl")
		phase = "empty-base-owner-regranted"
		admin("GRANT SELECT ON rv_acl TO rv_owner")
		step(held, "same-held", recursiveViewRows(held.SQL, "INT4"))
		phase = "empty-base-reader-view-revoked"
		admin("REVOKE SELECT ON rv_owned FROM rv_reader")
		denied(held.SQL, "permission denied for view rv_owned")
		phase = "empty-base-reader-view-regranted"
		admin("GRANT SELECT ON rv_owned TO rv_reader")
		step(held, "same-held", recursiveViewRows(held.SQL, "INT4"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}
