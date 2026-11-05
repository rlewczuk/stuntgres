package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBackendStatePrivileges(t *testing.T) {
	names := []string{"bs_target", "bs_owner", "bs_middle", "bs_transitive", "bs_stats", "bs_other", "bs_noinherit", "bs_setonly", "bs_adminonly", "bs_switch"}
	fixture.RoleReferenceCleanup(t, names)
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin *sql.Conn) {
		for _, name := range names {
			activityExec(t, p, admin, "CREATE ROLE "+name+" LOGIN PASSWORD 'role-attributes-fixture'")
		}
		for _, q := range []string{"GRANT bs_target TO bs_owner", "GRANT bs_target TO bs_middle", "GRANT bs_middle TO bs_transitive", "GRANT pg_read_all_stats TO bs_stats", "GRANT bs_target TO bs_noinherit WITH INHERIT FALSE", "GRANT bs_target TO bs_setonly WITH INHERIT FALSE, SET TRUE", "GRANT bs_target TO bs_adminonly WITH ADMIN TRUE, INHERIT FALSE, SET FALSE", "GRANT bs_switch TO bs_target"} {
			activityExec(t, p, admin, q)
		}
		activityExec(t, p, admin, "CREATE TABLE bs_authority(v integer)")
		activityExec(t, p, admin, "GRANT ALL ON bs_authority TO bs_target,bs_switch")
		activityExec(t, p, admin, "CREATE TABLE bs_authority_wait(v integer)")
		activityExec(t, p, admin, "GRANT ALL ON bs_authority_wait TO bs_target,bs_switch")
		activityExec(t, p, admin, "GRANT USAGE ON SCHEMA public TO bs_target,bs_switch")
		pool := fixture.RoleAttributesPool(t, p, b, "bs_target")
		target, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer target.Close()
		id := stateMapped(t, p, b, admin, target)[0]
		activityExec(t, p, target, "BEGIN")
		defer target.ExecContext(context.Background(), "ROLLBACK")
		activityExec(t, p, target, "SAVEPOINT child")
		activityExec(t, p, target, "INSERT INTO bs_authority VALUES(1)")
		self := stateRead(t, p, b, target, id)
		if self[5] != "(1,f)" || self[1] == nil || self[2] == nil || self[0] == stateDenied {
			t.Fatal("self authority", self)
		}
		// Activity ownership remains authenticated bs_target after SET ROLE.
		activityExec(t, p, target, "SET ROLE bs_switch")
		stateLockWitness(t, p, b, admin, target, id, "bs_authority_wait", func(visible []any) {
			if visible[5] != "(1,f)" || visible[1] == nil || visible[2] == nil {
				t.Fatal("positive protected facts", visible)
			}
			for _, role := range []string{"bs_owner", "bs_transitive", "bs_stats", "bs_other", "bs_noinherit", "bs_setonly", "bs_adminonly"} {
				t.Run(role, func(t *testing.T) {
					pool := fixture.RoleAttributesPool(t, p, b, role)
					o, err := pool.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer o.Close()
					held := statePrepare(t, p, o)
					allow := role == "bs_owner" || role == "bs_transitive" || role == "bs_stats"
					stateMask(t, stateRead(t, p, b, o, id), visible, allow)
					stateMask(t, stateRead(t, p, b, held, id), visible, allow)
					if role == "bs_setonly" {
						activityExec(t, p, o, "BEGIN")
						defer o.ExecContext(context.Background(), "ROLLBACK")
						stateMask(t, stateRead(t, p, b, held, id), visible, false)
						activityExec(t, p, o, "SET ROLE bs_target")
						stateMask(t, stateRead(t, p, b, held, id), visible, true)
						activityExec(t, p, o, "RESET ROLE")
						stateMask(t, stateRead(t, p, b, held, id), visible, false)
					}
				})
			}
			pool := fixture.RoleAttributesPool(t, p, b, "bs_other")
			o, err := pool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer o.Close()
			held := statePrepare(t, p, o)
			activityExec(t, p, o, "BEGIN")
			defer o.ExecContext(context.Background(), "ROLLBACK")
			stateMask(t, stateRead(t, p, b, held, id), visible, false)
			for _, allow := range []bool{true, false} {
				q := "REVOKE bs_target FROM bs_other"
				if allow {
					q = "GRANT bs_target TO bs_other"
				}
				// admin is in the retained activity transaction; use a separate
				// committed publisher so real catalog invalidations are visible.
				publisher := statActivityConn(t, p, b)
				activityExec(t, p, publisher, q)
				var inherited bool
				if err := o.QueryRowContext(p.Ctx, "SELECT pg_has_role('bs_target','USAGE') FROM pg_catalog.pg_roles WHERE rolname=current_user").Scan(&inherited); err != nil || inherited != allow {
					t.Fatal("catalog invalidation barrier", inherited, allow, err)
				}
				stateMask(t, stateRead(t, p, b, held, id), visible, allow)
			}
			activityExec(t, p, o, "ROLLBACK")
		})
		activityExec(t, p, target, "ROLLBACK")
		activityExec(t, p, target, "RESET ROLE")
		stateRoutineAdmission(t, p, b, admin, id)
	})
}

func stateRoutineAdmission(t *testing.T, p *fixture.Pair, b fixture.Backend, admin *sql.Conn, id any) {
	t.Helper()
	activityExec(t, p, admin, "CREATE TABLE bs_acl_null(v integer)")
	activityExec(t, p, admin, "INSERT INTO bs_acl_null VALUES(NULL)")
	activityExec(t, p, admin, "GRANT SELECT ON bs_acl_null TO bs_other")
	activityExec(t, p, admin, "GRANT USAGE ON SCHEMA public TO bs_other")
	pool := fixture.RoleAttributesPool(t, p, b, "bs_other")
	o, err := pool.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	for i, name := range stateFunctions {
		func() {
			sig := name + "(integer)"
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				for _, q := range []string{"GRANT EXECUTE ON FUNCTION " + sig + " TO PUBLIC", "REVOKE EXECUTE ON FUNCTION " + sig + " FROM bs_other"} {
					if _, err := admin.ExecContext(ctx, q); err != nil {
						t.Error("builtin ACL cleanup", err)
					}
				}
			}()
			q := "SELECT " + name + "(v) AS value FROM bs_acl_null"
			s, err := o.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			activityExec(t, p, admin, "REVOKE EXECUTE ON FUNCTION "+sig+" FROM PUBLIC")
			d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for function " + name}
			step := fixture.ErrorDiagnostic(q, d)
			step.SelectRows = true
			step.ErrorStage = "execute"
			stateBoth(t, p, b, o, step)
			r, err := s.QueryContext(p.Ctx)
			if r != nil {
				r.Close()
				t.Fatal("denied prepared rows")
			}
			fixture.AssertDiagnostic(t, err, &d)
			if i < 5 {
				stateBoth(t, p, b, o, fixture.Select("SELECT "+name+"(NULL) AS value", []string{"value"}, []string{stateTypes[i]}, [][]any{{nil}}))
			}
			activityExec(t, p, admin, "GRANT EXECUTE ON FUNCTION "+sig+" TO bs_other")
			r, err = s.QueryContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			got := stateRows(t, r, b.PG, []string{"value"}, []string{stateTypes[i]})
			if len(got) != 1 {
				t.Fatal(got)
			}
			stateEqual(t, got[0], []any{nil})
		}()
	}
}
