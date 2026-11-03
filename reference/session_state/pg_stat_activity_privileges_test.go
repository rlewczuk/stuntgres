package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGStatActivityPrivileges(t *testing.T) {
	names := []string{"activity_target", "activity_owner_reader", "activity_middle", "activity_transitive", "activity_stats_reader", "activity_unrelated", "activity_noinherit", "activity_setonly", "activity_adminonly", "activity_switch"}
	fixture.RoleReferenceCleanup(t, names)
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, o *sql.Conn) {
		for _, name := range names {
			activityExec(t, p, o, "CREATE ROLE "+name+" LOGIN PASSWORD 'role-attributes-fixture'")
		}
		activityExec(t, p, o, "CREATE TABLE activity_private(v integer)")
		activityExec(t, p, o, "GRANT ALL ON activity_private TO activity_target")
		activityExec(t, p, o, "GRANT USAGE ON SCHEMA public TO activity_target")
		for _, q := range []string{
			"GRANT activity_target TO activity_owner_reader",
			"GRANT activity_target TO activity_middle",
			"GRANT activity_middle TO activity_transitive",
			"GRANT pg_read_all_stats TO activity_stats_reader",
			"GRANT activity_target TO activity_noinherit WITH INHERIT FALSE",
			"GRANT activity_target TO activity_setonly WITH INHERIT FALSE, SET TRUE",
			"GRANT activity_target TO activity_adminonly WITH ADMIN TRUE, INHERIT FALSE, SET FALSE",
			"GRANT activity_switch TO activity_target",
		} {
			activityExec(t, p, o, q)
		}
		pool := fixture.RoleAttributesPool(t, p, b, "activity_target")
		target, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer target.Close()
		pid := statActivityPID(t, p, target)
		activityExec(t, p, target, "SET application_name='protected target'")
		activityExec(t, p, target, "BEGIN ISOLATION LEVEL REPEATABLE READ")
		defer target.ExecContext(context.Background(), "ROLLBACK")
		activityExec(t, p, target, "INSERT INTO activity_private VALUES(7)")
		self := statActivityGet(t, p, target, pid, true)
		if self.text(20) == "<insufficient privilege>" {
			t.Fatal("self redacted")
		}
		activityExec(t, p, target, "SELECT v FROM activity_private")
		visible := statActivityGet(t, p, o, pid, true)
		if visible[17] == nil || visible[18] == nil || visible[10] == nil || visible[11] == nil || visible[12] == nil || visible[13] == nil || visible.text(16) != "idle in transaction" || visible.text(20) != "SELECT v FROM activity_private" || visible.text(21) != "client backend" {
			t.Fatal("missing positive privilege witness", visible)
		}
		for _, name := range []string{"activity_owner_reader", "activity_transitive", "activity_stats_reader", "activity_unrelated", "activity_noinherit", "activity_setonly", "activity_adminonly"} {
			t.Run(name, func(t *testing.T) {
				db := fixture.RoleAttributesPool(t, p, b, name)
				observer, err := db.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer observer.Close()
				r := statActivityGet(t, p, observer, pid, true)
				if name == "activity_owner_reader" || name == "activity_transitive" || name == "activity_stats_reader" {
					for _, i := range []int{10, 11, 12, 13, 16, 20, 21} {
						if r.text(i) != visible.text(i) {
							t.Fatalf("inherited %s %s=%v want %v", name, statActivityNames[i], r[i], visible[i])
						}
					}
				} else {
					statActivityDenied(t, visible, r)
				}
				if name == "activity_setonly" {
					activityExec(t, p, observer, "SET ROLE activity_target")
					if r := statActivityGet(t, p, observer, pid, true); r.text(20) == "<insufficient privilege>" {
						t.Fatal("current role did not authorize")
					}
					activityExec(t, p, observer, "RESET ROLE")
					statActivityDenied(t, visible, statActivityGet(t, p, observer, pid, true))
				}
			})
		}
		// Keep one prepared SELECT and its activity cut through current GRANT/REVOKE.
		db := fixture.RoleAttributesPool(t, p, b, "activity_unrelated")
		observer, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer observer.Close()
		held, err := observer.PrepareContext(p.Ctx, statActivitySelect)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		activityExec(t, p, observer, "BEGIN")
		defer observer.ExecContext(context.Background(), "ROLLBACK")
		membership := func(want int) {
			var count int
			if err := o.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_catalog.pg_auth_members WHERE roleid='activity_target'::regrole AND member='activity_unrelated'::regrole AND inherit_option").Scan(&count); err != nil || count != want {
				t.Fatal("committed inherited membership", count, want, err)
			}
		}
		authority := func(want bool) {
			// A freshly parsed relation open receives PostgreSQL shared invalidations.
			// This does not clear the activity cut or replace the held SELECT/transaction.
			var current, login, isolation string
			var inherited bool
			query := "SELECT current_user,session_user,current_setting('transaction_isolation'),pg_has_role('activity_target','USAGE') FROM pg_catalog.pg_roles WHERE rolname=current_user"
			if err := observer.QueryRowContext(p.Ctx, query).Scan(&current, &login, &isolation, &inherited); err != nil {
				t.Fatal("observer authority publication", err)
			}
			if current != "activity_unrelated" || login != "activity_unrelated" || isolation != "read committed" || inherited != want {
				t.Fatal("observer current authority", current, login, isolation, inherited, "want", want)
			}
		}
		membership(0)
		authority(false)
		read := func() statActivityRow {
			rows, err := held.QueryContext(p.Ctx, pid)
			if err != nil {
				t.Fatal(err)
			}
			r, ok := statActivityRead(t, rows)
			if !ok {
				t.Fatal("lost cached row")
			}
			return r
		}
		statActivityDenied(t, visible, read())
		activityExec(t, p, o, "GRANT activity_target TO activity_unrelated")
		membership(1)
		authority(true)
		if r := read(); r.text(20) != visible.text(20) {
			t.Fatal("prepared capture cached old mask", r)
		}
		activityExec(t, p, o, "REVOKE activity_target FROM activity_unrelated")
		membership(0)
		authority(false)
		statActivityDenied(t, visible, read())
		activityExec(t, p, target, "SET ROLE activity_switch")
		switched := statActivityGet(t, p, o, pid, true)
		if switched.text(4) != visible.text(4) || switched.text(5) != "activity_target" {
			t.Fatal("target SET ROLE relabeled login", switched)
		}
		activityExec(t, p, target, "RESET ROLE")
	})
}
