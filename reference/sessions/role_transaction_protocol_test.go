//go:build !noservertests

package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// This is actual frontend traffic, not database/sql Prepare relabelled as wire
// coverage. Named statements deliberately survive simple Query messages.
func TestReferenceRoleTransactionalProtocol(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"rtw_group", "rtw_user", "rtw_alias", "rtw_bad", "rtw_flush", "rtw_late", "rtw_commit", "rtw_mark", "rtw_outer", "rtw_reader", "rtw_member"})
	fixture.SchemaReferenceCleanup(t, "rtw_owned", "rtw_bad_schema", "rtw_flush_schema", "rtw_late_schema", "rtw_commit_schema")
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w := serverAuthenticated(t, f.DSNFor(b))
			peer := serverAuthenticated(t, f.DSNFor(b))
			defer func() { serverWantCode(t, w.query(t, "ROLLBACK; RESET SESSION AUTHORIZATION; RESET ROLE"), "") }()
			admin := f.UserFor(b)
			ok := func(q string, ready byte, tags ...string) []serverFrame {
				t.Helper()
				frames := w.query(t, q)
				serverWantCode(t, frames, "")
				serverWantReady(t, frames, ready)
				serverWantCompletions(t, frames, tags...)
				return frames
			}
			absent := func(name string) {
				t.Helper()
				serverScalar(t, peer, "SELECT count(*) FROM pg_roles WHERE rolname='"+name+"'", "0")
			}
			identity := func(want string, ready byte) {
				t.Helper()
				frames := w.query(t, "SELECT current_user, session_user")
				serverWantCode(t, frames, "")
				serverWantReady(t, frames, ready)
				serverWantValues(t, frames, [][]string{{want, want}})
				cols := serverColumns(t, frames)
				if len(cols) != 2 {
					t.Fatalf("identity columns: %v", cols)
				}
				for i, name := range []string{"current_user", "session_user"} {
					if cols[i].name != name || cols[i].oid != 19 || cols[i].length != 64 || cols[i].format != 0 {
						t.Fatalf("NAME identity descriptor: %+v", cols[i])
					}
				}
			}
			utility := func(name, q, tag string, ready byte) {
				t.Helper()
				frames := serverCycle(t, w, serverParse(name, q), serverDescribe('S', name), serverBind(name, name, nil, nil), serverDescribe('P', name), serverExecute(name, 0))
				serverWantCode(t, frames, "")
				wantTags := "1tn2nCZ"
				var statuses []string
				if strings.HasPrefix(q, "SET SESSION AUTHORIZATION ") {
					target := strings.TrimPrefix(q, "SET SESSION AUTHORIZATION ")
					statuses = []string{"session_authorization\x00" + target + "\x00", "is_superuser\x00off\x00"}
					serverWantStatus(t, frames, "session_authorization", target)
					serverWantStatus(t, frames, "is_superuser", "off")
					wantTags = "1tn2nCSSZ"
				} else if q == "RESET SESSION AUTHORIZATION" {
					statuses = []string{"session_authorization\x00" + admin + "\x00", "is_superuser\x00on\x00"}
					serverWantStatus(t, frames, "session_authorization", admin)
					serverWantStatus(t, frames, "is_superuser", "on")
					wantTags = "1tn2nCSSZ"
				}
				serverWantTags(t, frames, wantTags)
				// The exact tag sequence fixes boundary/order/count; validate
				// complete payloads too, including both C-string terminators.
				i := 0
				for _, frame := range frames {
					if frame.tag == 'S' {
						if i >= len(statuses) || string(frame.body) != statuses[i] {
							t.Fatalf("ParameterStatus body %q want %v", frame.body, statuses)
						}
						i++
					}
				}
				serverWantCompletions(t, frames, tag)
				serverWantReady(t, frames, ready)
			}

			// One retained simple message: aliases and initial membership really
			// affect private authority and ownership before the message commits.
			ok("CREATE GROUP rtw_group; CREATE USER rtw_user IN GROUP rtw_group; CREATE ROLE rtw_alias; ALTER USER rtw_user NOLOGIN; ALTER GROUP rtw_group ADD USER rtw_alias; ALTER GROUP rtw_group DROP USER rtw_alias; GRANT rtw_group TO rtw_alias WITH ADMIN TRUE; REVOKE ADMIN OPTION FOR rtw_group FROM rtw_alias; CREATE SCHEMA rtw_owned AUTHORIZATION rtw_group; SET SESSION AUTHORIZATION rtw_user; SET ROLE rtw_group; CREATE TABLE rtw_owned.data(v int); INSERT INTO rtw_owned.data VALUES (7); RESET ROLE; RESET SESSION AUTHORIZATION", 'I',
				"CREATE ROLE", "CREATE ROLE", "CREATE ROLE", "ALTER ROLE", "ALTER ROLE", "ALTER ROLE", "GRANT ROLE", "REVOKE ROLE", "CREATE SCHEMA", "SET", "SET", "CREATE TABLE", "INSERT 0 1", "RESET", "RESET")
			serverScalar(t, peer, "SELECT v FROM rtw_owned.data", "7")
			identity(admin, 'I')
			serverScalar(t, peer, "SELECT current_user", admin)
			ok("DROP SCHEMA rtw_owned CASCADE; DROP USER rtw_user; DROP GROUP rtw_group; DROP ROLE rtw_alias", 'I', "DROP SCHEMA", "DROP ROLE", "DROP ROLE", "DROP ROLE")
			frames := w.query(t, "CREATE ROLE rtw_bad; CREATE SCHEMA rtw_bad_schema AUTHORIZATION rtw_bad; SET ROLE rtw_bad; CREATE TABLE rtw_bad_schema.data(v int); INSERT INTO rtw_bad_schema.data VALUES(8); SELECT 1/0")
			serverNotificationError(t, frames, "22012", "division by zero", "")
			serverWantReady(t, frames, 'I')
			absent("rtw_bad")
			serverScalar(t, peer, "SELECT count(*) FROM pg_namespace WHERE nspname='rtw_bad_schema'", "0")
			identity(admin, 'I')

			// Parse + both Describe forms + Bind are quiet. Flush is not Sync:
			// execution has finished, but neither principal nor object is public.
			frames = serverCycle(t, w, serverParse("flush", "CREATE ROLE rtw_flush"), serverDescribe('S', "flush"), serverBind("flush", "flush", nil, nil), serverDescribe('P', "flush"))
			serverWantTags(t, frames, "1tn2nZ")
			serverWantReady(t, frames, 'I')
			absent("rtw_flush")
			w.send(t, serverBind("flush_run", "flush", nil, nil))
			w.send(t, serverExecute("flush_run", 0))
			w.send(t, serverParse("flush_schema", "CREATE SCHEMA rtw_flush_schema AUTHORIZATION rtw_flush"))
			w.send(t, serverBind("flush_schema", "flush_schema", nil, nil))
			w.send(t, serverExecute("flush_schema", 0))
			w.send(t, serverPacket('H', nil))
			frames = serverThrough(t, w, 'C')
			serverWantTags(t, frames, "2C")
			serverWantCompletions(t, frames, "CREATE ROLE")
			frames = serverThrough(t, w, 'C')
			serverWantTags(t, frames, "12C")
			serverWantCompletions(t, frames, "CREATE SCHEMA")
			absent("rtw_flush")
			serverScalar(t, peer, "SELECT count(*) FROM pg_namespace WHERE nspname='rtw_flush_schema'", "0")
			w.send(t, serverPacket('S', nil))
			frames = w.ready(t)
			serverWantTags(t, frames, "Z")
			serverWantReady(t, frames, 'I')
			serverScalar(t, peer, "SELECT count(*) FROM pg_roles WHERE rolname='rtw_flush'", "1")
			serverScalar(t, peer, "SELECT nspowner= (SELECT oid FROM pg_roles WHERE rolname='rtw_flush') FROM pg_namespace WHERE nspname='rtw_flush_schema'", "t")
			frames = serverCycle(t, w,
				serverParse("late", "CREATE ROLE rtw_late"), serverBind("late", "late", nil, nil), serverExecute("late", 0),
				serverParse("late_object", "CREATE SCHEMA rtw_late_schema AUTHORIZATION rtw_late"), serverBind("late_object", "late_object", nil, nil), serverExecute("late_object", 0),
				serverParse("late_error", "SELECT 1/0"), serverBind("late_error", "late_error", nil, nil), serverExecute("late_error", 0))
			serverNotificationError(t, frames, "22012", "division by zero", "")
			serverWantReady(t, frames, 'I')
			serverWantCompletions(t, frames, "CREATE ROLE", "CREATE SCHEMA")
			absent("rtw_late")
			serverScalar(t, peer, "SELECT count(*) FROM pg_namespace WHERE nspname='rtw_late_schema'", "0")
			ok("DROP SCHEMA rtw_flush_schema; DROP ROLE rtw_flush", 'I', "DROP SCHEMA", "DROP ROLE")

			// BEGIN promotion inside a simple message survives its Ready boundary.
			ok("CREATE ROLE rtw_outer; BEGIN; ALTER ROLE rtw_outer LOGIN", 'T', "CREATE ROLE", "BEGIN", "ALTER ROLE")
			absent("rtw_outer")
			ok("ROLLBACK", 'I', "ROLLBACK")
			absent("rtw_outer")
			ok("BEGIN", 'T', "BEGIN")
			utility("commit_create", "CREATE USER rtw_commit", "CREATE ROLE", 'T')
			utility("commit_alter", "ALTER USER rtw_commit NOLOGIN", "ALTER ROLE", 'T')
			frames = serverCycle(t, w,
				serverParse("quiet_alter", "ALTER USER rtw_commit LOGIN"), serverDescribe('S', "quiet_alter"),
				serverParse("quiet_auth", "SET SESSION AUTHORIZATION rtw_commit"), serverDescribe('S', "quiet_auth"))
			serverWantTags(t, frames, "1tn1tnZ")
			serverWantReady(t, frames, 'T')
			identity(admin, 'T')
			// The unexecuted ALTER must not turn LOGIN back on at commit.
			serverWantTags(t, serverCycle(t, w, serverClose('S', "quiet_alter"), serverClose('S', "quiet_auth")), "33Z")
			absent("rtw_commit")
			ok("CREATE SCHEMA rtw_commit_schema AUTHORIZATION rtw_commit; SET ROLE rtw_commit; CREATE TABLE rtw_commit_schema.data(v int); INSERT INTO rtw_commit_schema.data VALUES(9); RESET ROLE", 'T', "CREATE SCHEMA", "SET", "CREATE TABLE", "INSERT 0 1", "RESET")
			serverScalar(t, w, "SELECT v FROM rtw_commit_schema.data", "9")
			serverScalar(t, peer, "SELECT count(*) FROM pg_namespace WHERE nspname='rtw_commit_schema'", "0")
			ok("SAVEPOINT mark", 'T', "SAVEPOINT")
			utility("mark_create", "CREATE ROLE rtw_mark", "CREATE ROLE", 'T')
			utility("mark_auth", "SET SESSION AUTHORIZATION rtw_commit", "SET", 'T')
			identity("rtw_commit", 'T')
			frames = w.query(t, "SELECT 1/0")
			serverNotificationError(t, frames, "22012", "division by zero", "")
			serverWantReady(t, frames, 'E')
			frames = w.query(t, "SELECT 42")
			serverNotificationError(t, frames, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
			serverWantReady(t, frames, 'E')
			ok("ROLLBACK TO mark", 'T', "ROLLBACK")
			identity(admin, 'T')
			// A second rollback to the same mark must restore the same role root.
			utility("mark_again", "CREATE ROLE rtw_mark", "CREATE ROLE", 'T')
			ok("ROLLBACK TO mark; RELEASE mark; COMMIT", 'I', "ROLLBACK", "RELEASE", "COMMIT")
			absent("rtw_mark")
			serverScalar(t, peer, "SELECT v FROM rtw_commit_schema.data", "9")
			serverScalar(t, peer, "SELECT rolcanlogin FROM pg_roles WHERE rolname='rtw_commit'", "f")
			// Full rollback restores both role roots and session settings even
			// after RELEASE; the previously committed owned table survives.
			ok("BEGIN; SAVEPOINT child", 'T', "BEGIN", "SAVEPOINT")
			utility("outer_create", "CREATE ROLE rtw_outer", "CREATE ROLE", 'T')
			utility("outer_auth", "SET SESSION AUTHORIZATION rtw_commit", "SET", 'T')
			identity("rtw_commit", 'T')
			ok("RELEASE child; ROLLBACK", 'I', "RELEASE", "ROLLBACK")
			identity(admin, 'I')
			absent("rtw_outer")
			ok("BEGIN; DROP SCHEMA rtw_commit_schema CASCADE; DROP USER rtw_commit; ROLLBACK", 'I', "BEGIN", "DROP SCHEMA", "DROP ROLE", "ROLLBACK")
			serverScalar(t, peer, "SELECT v FROM rtw_commit_schema.data", "9")
			ok("DROP SCHEMA rtw_commit_schema CASCADE; DROP USER rtw_commit", 'I', "DROP SCHEMA", "DROP ROLE")

			// A held SELECT and held SET ROLE must rebind against live INHERIT
			// and SET options. No SQL role is mistaken for a transport account.
			// The fixture recreates public without PUBLIC USAGE. Give schema
			// visibility directly to the member, independently of the table
			// privilege whose INHERIT option is under test.
			ok("CREATE ROLE rtw_reader; CREATE USER rtw_member IN ROLE rtw_reader; CREATE TABLE rtw_protected(v int); INSERT INTO rtw_protected VALUES(42); GRANT USAGE ON SCHEMA public TO rtw_member; GRANT SELECT ON rtw_protected TO rtw_reader", 'I',
				"CREATE ROLE", "CREATE ROLE", "CREATE TABLE", "INSERT 0 1", "GRANT", "GRANT")
			serverScalar(t, peer, "SELECT v FROM rtw_protected", "42")
			utility("member_auth", "SET SESSION AUTHORIZATION rtw_member", "SET", 'I')
			identity("rtw_member", 'I')
			frames = serverCycle(t, w, serverParse("held_read", "SELECT v FROM rtw_protected"), serverDescribe('S', "held_read"), serverParse("held_set", "SET ROLE rtw_reader"), serverDescribe('S', "held_set"))
			serverWantTags(t, frames, "1tT1tnZ")
			serverWantReady(t, frames, 'I')
			serverWantValues(t, serverCycle(t, w, serverBind("read_one", "held_read", nil, nil), serverExecute("read_one", 0)), [][]string{{"42"}})
			serverWantCode(t, peer.query(t, "GRANT rtw_reader TO rtw_member WITH INHERIT FALSE, SET FALSE"), "")
			frames = serverCycle(t, w, serverBind("read_denied", "held_read", nil, nil), serverExecute("read_denied", 0))
			serverNotificationError(t, frames, "42501", "permission denied for table rtw_protected", "")
			serverWantReady(t, frames, 'I')
			frames = serverCycle(t, w, serverBind("set_denied", "held_set", nil, nil), serverExecute("set_denied", 0))
			serverNotificationError(t, frames, "42501", `permission denied to set role "rtw_reader"`, "")
			serverWantReady(t, frames, 'I')
			serverWantCode(t, peer.query(t, "GRANT rtw_reader TO rtw_member WITH INHERIT TRUE, SET TRUE"), "")
			serverWantValues(t, serverCycle(t, w, serverBind("read_again", "held_read", nil, nil), serverExecute("read_again", 0)), [][]string{{"42"}})
			frames = serverCycle(t, w, serverBind("set_again", "held_set", nil, nil), serverExecute("set_again", 0))
			serverWantCompletions(t, frames, "SET")
			serverScalar(t, w, "SELECT current_user", "rtw_reader")
			utility("member_reset", "RESET ROLE", "RESET", 'I')
			utility("auth_reset", "RESET SESSION AUTHORIZATION", "RESET", 'I')
			identity(admin, 'I')
			// Explicit option changes apply privately, then rollback restores
			// real permission, not merely the catalog flag.
			ok("BEGIN", 'T', "BEGIN")
			frames = serverCycle(t, w, serverParse("snapshot", "SELECT v FROM rtw_protected"), serverBind("snapshot", "snapshot", nil, nil))
			serverWantCode(t, frames, "")
			ok("SAVEPOINT options", 'T', "SAVEPOINT")
			utility("private_revoke", "REVOKE rtw_reader FROM rtw_member", "REVOKE ROLE", 'T')
			utility("private_auth", "SET SESSION AUTHORIZATION rtw_member", "SET", 'T')
			frames = w.query(t, "SELECT v FROM rtw_protected")
			serverNotificationError(t, frames, "42501", "permission denied for table rtw_protected", "")
			serverWantReady(t, frames, 'E')
			ok("ROLLBACK TO options", 'T', "ROLLBACK")
			identity(admin, 'T')
			serverWantValues(t, serverCycle(t, w, serverExecute("snapshot", 0)), [][]string{{"42"}})
			serverWantTags(t, serverCycle(t, w, serverClose('P', "snapshot")), "3Z")
			utility("restored_auth", "SET SESSION AUTHORIZATION rtw_member", "SET", 'T')
			serverScalar(t, w, "SELECT v FROM rtw_protected", "42")
			ok("ROLLBACK", 'I', "ROLLBACK")
			identity(admin, 'I')
			ok("DROP TABLE rtw_protected; REVOKE USAGE ON SCHEMA public FROM rtw_member; DROP USER rtw_member; DROP ROLE rtw_reader", 'I', "DROP TABLE", "REVOKE", "DROP ROLE", "DROP ROLE")
			serverScalar(t, w, "SELECT 42", "42")
		})
	}
}
