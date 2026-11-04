package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSSLPrivileges(t *testing.T) {
	names := []string{"ssl_target", "ssl_owner_reader", "ssl_middle", "ssl_transitive", "ssl_stats_reader", "ssl_stats_setonly", "ssl_unrelated", "ssl_noinherit", "ssl_setonly", "ssl_adminonly", "ssl_switch", "ssl_effective_reader"}
	fixture.RoleReferenceCleanup(t, names)
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, admin *sql.Conn) {
		for _, name := range names {
			activityExec(t, p, admin, "CREATE ROLE "+name+" LOGIN PASSWORD 'role-attributes-fixture'")
		}
		for _, query := range []string{
			"GRANT ssl_target TO ssl_owner_reader",
			"GRANT ssl_target TO ssl_middle",
			"GRANT ssl_middle TO ssl_transitive",
			"GRANT pg_read_all_stats TO ssl_stats_reader",
			"GRANT pg_read_all_stats TO ssl_stats_setonly WITH INHERIT FALSE, SET TRUE",
			"GRANT ssl_target TO ssl_noinherit WITH INHERIT FALSE",
			"GRANT ssl_target TO ssl_setonly WITH INHERIT FALSE, SET TRUE",
			"GRANT ssl_target TO ssl_adminonly WITH ADMIN TRUE, INHERIT FALSE, SET FALSE",
			"GRANT ssl_switch TO ssl_target",
			"GRANT ssl_switch TO ssl_effective_reader",
		} {
			activityExec(t, p, admin, query)
		}
		login := func(name string) *sql.Conn {
			db := fixture.RoleAttributesPool(t, p, b, name)
			conn, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { conn.Close() })
			return conn
		}
		target := login("ssl_target")
		pid := statActivityPID(t, p, target)
		sslWant(t, p, b, target, pid, true)
		sslWant(t, p, b, admin, pid, true)
		for _, name := range []string{"ssl_target", "ssl_owner_reader", "ssl_transitive", "ssl_stats_reader", "ssl_stats_setonly", "ssl_unrelated", "ssl_noinherit", "ssl_setonly", "ssl_adminonly"} {
			t.Run(name, func(t *testing.T) {
				db := fixture.RoleAttributesPool(t, p, b, name)
				observer, err := db.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer observer.Close()
				self := statActivityPID(t, p, observer)
				sslWant(t, p, b, observer, self, true)
				allowed := name == "ssl_target" || name == "ssl_owner_reader" || name == "ssl_transitive" || name == "ssl_stats_reader"
				held := sslHeld(t, p, observer, sslSQL)
				defer held.Statement.Close()
				activityExec(t, p, observer, "BEGIN")
				defer observer.ExecContext(context.Background(), "ROLLBACK")
				sslWant(t, p, b, held, pid, allowed)
				if name == "ssl_setonly" || name == "ssl_stats_setonly" {
					role := "ssl_target"
					if name == "ssl_stats_setonly" {
						role = "pg_read_all_stats"
					}
					// The same prepared target query and transaction cut must
					// follow current SET-only authority, not the login's grants.
					activityExec(t, p, observer, "SET ROLE "+role)
					sslWant(t, p, b, held, pid, true)
					activityExec(t, p, observer, "RESET ROLE")
					sslWant(t, p, b, held, pid, false)
				}
				activityExec(t, p, observer, "ROLLBACK")
			})
		}
		// Keep the transaction, unmasked cut and prepared definition through
		// committed catalog changes. A fresh relation open receives invalidations;
		// no sleep, clear, transaction restart or reprepare hides stale authority.
		observer := login("ssl_unrelated")
		held := sslHeld(t, p, observer, sslSQL)
		defer held.Statement.Close()
		activityExec(t, p, observer, "BEGIN")
		defer observer.ExecContext(context.Background(), "ROLLBACK")
		authority := func(role string, want bool) {
			var count int
			if err := admin.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_catalog.pg_auth_members WHERE roleid=$1::regrole AND member='ssl_unrelated'::regrole AND inherit_option", role).Scan(&count); err != nil || (count == 1) != want || count > 1 {
				t.Fatal("committed inherited edge", role, count, want, err)
			}
			var current, loginName, isolation string
			var inherited bool
			// Zero parameters deliberately force a new relation open in pq.
			query := "SELECT current_user,session_user,current_setting('transaction_isolation'),pg_has_role('" + role + "','USAGE') FROM pg_catalog.pg_roles WHERE rolname=current_user"
			if err := observer.QueryRowContext(p.Ctx, query).Scan(&current, &loginName, &isolation, &inherited); err != nil || current != "ssl_unrelated" || loginName != "ssl_unrelated" || isolation != "read committed" || inherited != want {
				t.Fatal("current observer authority", role, current, loginName, isolation, inherited, want, err)
			}
		}
		for _, role := range []string{"ssl_target", "pg_read_all_stats"} {
			authority(role, false)
			sslWant(t, p, b, held, pid, false)
			activityExec(t, p, admin, "GRANT "+role+" TO ssl_unrelated")
			authority(role, true)
			sslWant(t, p, b, held, pid, true)
			activityExec(t, p, admin, "REVOKE "+role+" FROM ssl_unrelated")
			authority(role, false)
			sslWant(t, p, b, held, pid, false)
		}
		activityExec(t, p, observer, "ROLLBACK")
		// The target's SET ROLE must not relabel its authenticated login owner.
		originalReader, effectiveReader := login("ssl_owner_reader"), login("ssl_effective_reader")
		originalHeld := sslHeld(t, p, originalReader, sslSQL)
		defer originalHeld.Statement.Close()
		effectiveHeld := sslHeld(t, p, effectiveReader, sslSQL)
		defer effectiveHeld.Statement.Close()
		// Even self visibility follows current authority over the immutable
		// login. Keep its prepared self-PID query and cut across both switches.
		selfHeld := sslHeld(t, p, target, sslSQL)
		defer selfHeld.Statement.Close()
		activityExec(t, p, target, "BEGIN")
		defer target.ExecContext(context.Background(), "ROLLBACK")
		sslWant(t, p, b, selfHeld, pid, true)
		for _, command := range []string{"SET ROLE ssl_switch", "RESET ROLE"} {
			activityExec(t, p, target, command)
			sslWant(t, p, b, selfHeld, pid, command == "RESET ROLE")
			sslWant(t, p, b, originalHeld, pid, true)
			sslWant(t, p, b, effectiveHeld, pid, false)
		}
		activityExec(t, p, target, "ROLLBACK")
	})
}
