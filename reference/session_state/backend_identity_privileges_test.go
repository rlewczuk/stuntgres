package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBackendIdentityPrivileges(t *testing.T) {
	names := []string{"bi_target", "bi_owner", "bi_middle", "bi_transitive", "bi_stats", "bi_other", "bi_noinherit", "bi_setonly", "bi_adminonly", "bi_switch"}
	fixture.RoleReferenceCleanup(t, names)
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin *sql.Conn) {
		for _, name := range names {
			activityExec(t, p, admin, "CREATE ROLE "+name+" LOGIN PASSWORD 'role-attributes-fixture'")
		}
		for _, q := range []string{
			"GRANT bi_target TO bi_owner", "GRANT bi_target TO bi_middle", "GRANT bi_middle TO bi_transitive", "GRANT pg_read_all_stats TO bi_stats",
			"GRANT bi_target TO bi_noinherit WITH INHERIT FALSE", "GRANT bi_target TO bi_setonly WITH INHERIT FALSE, SET TRUE",
			"GRANT bi_target TO bi_adminonly WITH ADMIN TRUE, INHERIT FALSE, SET FALSE", "GRANT bi_switch TO bi_target",
		} {
			activityExec(t, p, admin, q)
		}
		pool := fixture.RoleAttributesPool(t, p, b, "bi_target")
		target, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer target.Close()
		visible := identityMapped(t, p, b, admin, target)
		id := visible[0]
		identityMasked(t, identityLookup(t, p, b, target, id), visible, true)
		for _, role := range []string{"bi_owner", "bi_transitive", "bi_stats", "bi_other", "bi_noinherit", "bi_setonly", "bi_adminonly"} {
			t.Run(role, func(t *testing.T) {
				pool := fixture.RoleAttributesPool(t, p, b, role)
				o, err := pool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer o.Close()
				s, err := o.PrepareContext(p.Ctx, identityLookupSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				e := fixture.HeldStatement{Executor: o, Statement: s, SQL: identityLookupSQL}
				allowed := role == "bi_owner" || role == "bi_transitive" || role == "bi_stats"
				identityMasked(t, identityLookup(t, p, b, o, id), visible, allowed)
				identityMasked(t, identityLookup(t, p, b, e, id), visible, allowed)
				identityMasked(t, identityForPID(t, identitySet(t, p, b, o), visible[1].(int64)), visible, allowed)
				if role == "bi_setonly" {
					activityExec(t, p, o, "BEGIN")
					defer o.ExecContext(context.Background(), "ROLLBACK")
					identityMasked(t, identityLookup(t, p, b, e, id), visible, false)
					activityExec(t, p, o, "SET ROLE bi_target")
					identityMasked(t, identityLookup(t, p, b, e, id), visible, true)
					activityExec(t, p, o, "RESET ROLE")
					identityMasked(t, identityLookup(t, p, b, e, id), visible, false)
				}
			})
		}
		pool = fixture.RoleAttributesPool(t, p, b, "bi_other")
		o, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer o.Close()
		s, err := o.PrepareContext(p.Ctx, identityLookupSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		e := fixture.HeldStatement{Executor: o, Statement: s, SQL: identityLookupSQL}
		activityExec(t, p, o, "BEGIN")
		defer o.ExecContext(context.Background(), "ROLLBACK")
		identityMasked(t, identityLookup(t, p, b, e, id), visible, false)
		for _, allow := range []bool{true, false} {
			q := "REVOKE bi_target FROM bi_other"
			if allow {
				q = "GRANT bi_target TO bi_other"
			}
			activityExec(t, p, admin, q)
			var inherited bool
			if err := o.QueryRowContext(p.Ctx, "SELECT pg_has_role('bi_target','USAGE') FROM pg_catalog.pg_roles WHERE rolname=current_user").Scan(&inherited); err != nil || inherited != allow {
				t.Fatal("fresh catalog authority publication", inherited, allow, err)
			}
			// No clear/reprepare: same handle and same unmasked activity cut.
			identityMasked(t, identityLookup(t, p, b, e, id), visible, allow)
		}
		activityExec(t, p, target, "SET ROLE bi_switch")
		statActivityClear(t, p, admin)
		identityEqual(t, identityLookup(t, p, b, admin, id), visible)
		identityMasked(t, identityLookup(t, p, b, e, id), visible, false)
		activityExec(t, p, target, "RESET ROLE")
		activityExec(t, p, o, "ROLLBACK")
		identityRoutineAdmission(t, p, b, admin, o, visible)
	})
}

func identityRoutineAdmission(t *testing.T, p *fixture.Pair, b fixture.Backend, admin, observer *sql.Conn, visible []any) {
	t.Helper()
	activityExec(t, p, admin, "CREATE TABLE bi_acl_null(v integer)")
	activityExec(t, p, admin, "INSERT INTO bi_acl_null VALUES(NULL)")
	activityExec(t, p, admin, "GRANT SELECT ON bi_acl_null TO bi_other")
	activityExec(t, p, admin, "GRANT USAGE ON SCHEMA public TO bi_other")
	names := append([]string{"pg_stat_get_backend_idset"}, identityFunctions...)
	for i, name := range names {
		signature := name + "(integer)"
		query := "SELECT " + name + "($1) AS value"
		args := []any{visible[0]}
		typ := "INT4"
		if i == 0 {
			signature = name + "()"
			query = "SELECT " + name + "() AS value"
			args = nil
		} else {
			typ = identityTypes[i-1]
		}
		func() {
			// Restore global builtin ACLs even if a diagnostic assertion aborts.
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				for _, q := range []string{"GRANT EXECUTE ON FUNCTION " + signature + " TO PUBLIC", "REVOKE EXECUTE ON FUNCTION " + signature + " FROM bi_other"} {
					if _, err := admin.ExecContext(ctx, q); err != nil {
						t.Error("identity routine ACL cleanup", q, err)
					}
				}
			}()
			s, err := observer.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			activityExec(t, p, admin, "REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC")
			r, err := s.QueryContext(p.Ctx, args...)
			if r != nil {
				r.Close()
				t.Fatal("denied identity routine exposed rows")
			}
			// Independently specified full permission contract, confirmed by the
			// root's pinned PostgreSQL18.6 oracle discovery.
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for function " + name})
			if i != 0 {
				// A table-sourced NULL survives planning and still needs ordinary
				// routine admission; literal STRICT folding is a different route.
				step := fixture.ErrorDiagnostic("SELECT "+name+"(v) FROM bi_acl_null", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for function " + name})
				step.SelectRows = true
				step.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, observer, b.Recorder, step)
				fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Prepared(step))
				literal := fixture.Select("SELECT "+name+"(NULL) AS value", []string{"value"}, []string{typ}, [][]any{{nil}})
				fixture.RunStep(t, p.Ctx, observer, b.Recorder, literal)
				fixture.RunStep(t, p.Ctx, observer, b.Recorder, fixture.Prepared(literal))
			}
			activityExec(t, p, admin, "GRANT EXECUTE ON FUNCTION "+signature+" TO bi_other")
			r, err = s.QueryContext(p.Ctx, args...)
			if err != nil {
				t.Fatal("same handle after EXECUTE grant", err)
			}
			got := identityRows(t, r, b.PG, []string{"value"}, []string{typ})
			if i == 0 {
				found := false
				for _, row := range got {
					found = found || row[0] == visible[0]
				}
				if !found {
					t.Fatal("admitted public idset", got)
				}
			} else {
				want := visible[i]
				if i >= 4 {
					want = nil
				}
				if len(got) != 1 || fixture.Normalize(got[0][0]) != fixture.Normalize(want) {
					t.Fatal("admitted scalar identity", got, want)
				}
			}
		}()
	}
}
