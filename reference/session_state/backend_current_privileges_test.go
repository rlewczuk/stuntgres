package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func backendMasked(t *testing.T, visible, masked []any) {
	t.Helper()
	public := map[int]bool{0: true, 1: true, 2: true, 3: true, 15: true, 16: true}
	for i, v := range masked {
		if public[i] {
			if !reflect.DeepEqual(v, visible[i]) {
				t.Fatal("public activity field", backendActivityNames[i], v, visible[i])
			}
			continue
		}
		if i == 5 {
			if v != "<insufficient privilege>" {
				t.Fatal("exact query sentinel", v)
			}
			continue
		}
		if v != nil {
			t.Fatal("protected activity field", backendActivityNames[i], v)
		}
	}
}
func TestReferenceBackendCurrentPrivileges(t *testing.T) {
	names := []string{"bc_target", "bc_owner", "bc_middle", "bc_transitive", "bc_stats", "bc_other", "bc_noinherit", "bc_setonly", "bc_adminonly", "bc_switch"}
	fixture.RoleReferenceCleanup(t, names)
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		for _, name := range names {
			activityExec(t, p, o, "CREATE ROLE "+name+" LOGIN PASSWORD 'role-attributes-fixture'")
		}
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, name := range backendFunctionNames {
				for _, q := range []string{"GRANT EXECUTE ON FUNCTION " + name + "(integer) TO PUBLIC", "REVOKE EXECUTE ON FUNCTION " + name + "(integer) FROM bc_other"} {
					if _, err := o.ExecContext(ctx, q); err != nil {
						t.Error("backend ACL cleanup", q, err)
					}
				}
			}
		}()
		for _, q := range []string{"CREATE TABLE bc_private(v integer)", "GRANT ALL ON bc_private TO bc_target", "GRANT USAGE ON SCHEMA public TO bc_target", "GRANT bc_target TO bc_owner", "GRANT bc_target TO bc_middle", "GRANT bc_middle TO bc_transitive", "GRANT pg_read_all_stats TO bc_stats", "GRANT bc_target TO bc_noinherit WITH INHERIT FALSE", "GRANT bc_target TO bc_setonly WITH INHERIT FALSE, SET TRUE", "GRANT bc_target TO bc_adminonly WITH ADMIN TRUE, INHERIT FALSE, SET FALSE", "GRANT bc_switch TO bc_target"} {
			activityExec(t, p, o, q)
		}
		pool := fixture.RoleAttributesPool(t, p, b, "bc_target")
		target, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer target.Close()
		pid := statActivityPID(t, p, target)
		activityExec(t, p, target, "BEGIN ISOLATION LEVEL REPEATABLE READ")
		defer target.ExecContext(context.Background(), "ROLLBACK")
		activityExec(t, p, target, "INSERT INTO bc_private VALUES(7)")
		if backendQuery(t, p, b.PG, target, 0, pid)[0][5] == "<insufficient privilege>" {
			t.Fatal("self masked")
		}
		activityExec(t, p, target, "SELECT v FROM bc_private")
		visible := backendQuery(t, p, b.PG, o, 0, pid)[0]
		if visible[15] == nil || visible[16] == nil || visible[8] == nil || visible[9] == nil || visible[10] == nil || visible[11] == nil || visible[4] != "idle in transaction" || visible[5] != "SELECT v FROM bc_private" {
			t.Fatal("positive protected witness", visible)
		}
		for _, name := range []string{"bc_owner", "bc_transitive", "bc_stats", "bc_other", "bc_noinherit", "bc_setonly", "bc_adminonly"} {
			t.Run(name, func(t *testing.T) {
				pool := fixture.RoleAttributesPool(t, p, b, name)
				observer, err := pool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer observer.Close()
				row := backendQuery(t, p, b.PG, observer, 0, pid)[0]
				if name == "bc_owner" || name == "bc_transitive" || name == "bc_stats" {
					for _, i := range []int{4, 5, 8, 9, 10, 11, 17, 18, 25, 27, 28} {
						if !reflect.DeepEqual(row[i], visible[i]) {
							t.Fatal("inherited details", i, row[i], visible[i])
						}
					}
				} else {
					backendMasked(t, visible, row)
				}
				backendIOCheck(t, backendQuery(t, p, b.PG, observer, 1, pid), b.PG)
				backendWALCheck(t, backendQuery(t, p, b.PG, observer, 2, pid), b.PG, true)
				if name == "bc_setonly" {
					activityExec(t, p, observer, "SET ROLE bc_target")
					if backendQuery(t, p, b.PG, observer, 0, pid)[0][5] == "<insufficient privilege>" {
						t.Fatal("current role ignored")
					}
					activityExec(t, p, observer, "RESET ROLE")
					backendMasked(t, visible, backendQuery(t, p, b.PG, observer, 0, pid)[0])
				}
			})
		}
		pool = fixture.RoleAttributesPool(t, p, b, "bc_other")
		observer, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		q := "SELECT * FROM pg_catalog.pg_stat_get_activity($1)"
		held, err := observer.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		e := fixture.HeldStatement{Executor: observer, Statement: held, SQL: q}
		activityExec(t, p, observer, "BEGIN")
		defer observer.ExecContext(context.Background(), "ROLLBACK")
		backendMasked(t, visible, backendQuery(t, p, b.PG, e, 0, pid)[0])
		for _, allow := range []bool{true, false} {
			if allow {
				activityExec(t, p, o, "GRANT bc_target TO bc_other")
			} else {
				activityExec(t, p, o, "REVOKE bc_target FROM bc_other")
			}
			var inherited bool
			if err := observer.QueryRowContext(p.Ctx, "SELECT pg_has_role('bc_target','USAGE') FROM pg_catalog.pg_roles WHERE rolname=current_user").Scan(&inherited); err != nil || inherited != allow {
				t.Fatal("current inherited authority", inherited, err)
			}
			row := backendQuery(t, p, b.PG, e, 0, pid)[0]
			if allow {
				if row[5] != visible[5] {
					t.Fatal("retained raw cut cached mask", row)
				}
			} else {
				backendMasked(t, visible, row)
			}
		}
		activityExec(t, p, observer, "ROLLBACK")
		activityExec(t, p, target, "SET ROLE bc_switch")
		statActivityClear(t, p, o)
		if row := backendQuery(t, p, b.PG, o, 0, pid)[0]; row[2] != visible[2] {
			t.Fatal("target SET ROLE changed authenticated owner", row)
		}
		activityExec(t, p, target, "RESET ROLE")
		for kind, name := range backendFunctionNames {
			q := "SELECT * FROM pg_catalog." + name + "($1)"
			s, err := observer.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			activityExec(t, p, o, "REVOKE EXECUTE ON FUNCTION "+name+"(integer) FROM PUBLIC")
			r, err := s.QueryContext(p.Ctx, pid)
			if r != nil {
				r.Close()
			}
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for function " + name})
			if kind != 0 {
				// RECORD and SRF declarations are excluded from strict scalar
				// planning erasure. Reached NULL calls still require EXECUTE.
				nullRows, err := observer.QueryContext(p.Ctx, "SELECT * FROM "+name+"(NULL)")
				if nullRows != nil {
					nullRows.Close()
				}
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for function " + name})
			}
			activityExec(t, p, o, "GRANT EXECUTE ON FUNCTION "+name+"(integer) TO bc_other")
			r, err = s.QueryContext(p.Ctx, pid)
			if err != nil {
				s.Close()
				t.Fatal(err)
			}
			r.Close()
			s.Close()
			activityExec(t, p, o, "GRANT EXECUTE ON FUNCTION "+name+"(integer) TO PUBLIC")
		}
	})
}
