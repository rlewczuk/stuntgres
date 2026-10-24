package reference_test

import (
	"testing"

	"github.com/lib/pq"
	"stuntgres/reference/fixture"
)

func TestReferenceRoleOwnershipTransactionalDatabase(t *testing.T) {
	for _, route := range ownershipTransactionRoutes {
		t.Run(route, func(t *testing.T) {
			names := []string{"tx_own_remote_direct", "tx_own_remote_held"}
			fixture.DatabaseReferenceCleanup(t, &names)
			ownershipProviderCases(t, func(s *membershipScenario, database string) {
				remoteName := "tx_own_remote_direct"
				if s.prepared {
					remoteName = "tx_own_remote_held"
				}
				s.exec("CREATE DATABASE " + remoteName + " OWNER own_other")
				remote, _, rec := fixture.DatabaseScenarioPool(s.t, s.p, s.backend.PG, remoteName, "")
				defer func() {
					remote.ExecContext(s.p.Ctx, "DROP TABLE IF EXISTS tx_remote_owned CASCADE")
					remote.ExecContext(s.p.Ctx, "ALTER DATABASE "+remoteName+" OWNER TO "+pq.QuoteIdentifier(s.bootstrap))
					remote.Close()
					if s.backend.PG {
						s.c.ExecContext(s.p.Ctx, "DROP DATABASE "+remoteName)
					}
				}()
				remoteRun := func(step fixture.Step) { fixture.RunStep(s.t, s.p.Ctx, remote, rec, step) }
				remoteRun(fixture.Exec("CREATE TABLE tx_remote_owned(id INT)", 0))
				remoteRun(fixture.Exec("INSERT INTO tx_remote_owned VALUES(7)", 1))
				remoteRun(fixture.Exec("ALTER TABLE tx_remote_owned OWNER TO own_source", 0))
				prefix := "ALTER DATABASE " + pq.QuoteIdentifier(database) + " OWNER TO "
				s.exec(prefix + "own_source")
				s.exec("CREATE TABLE tx_database_local(id INT)")
				s.run(fixture.Exec("INSERT INTO tx_database_local VALUES(7)", 1))
				s.exec("ALTER TABLE tx_database_local OWNER TO own_source")
				s.exec("ALTER ROLE own_actor CREATEDB")
				s.exec("GRANT own_source,own_target TO own_actor WITH INHERIT TRUE,SET TRUE")
				peer, err := s.backend.DB.Conn(s.p.Ctx)
				if err != nil {
					s.t.Fatal(err)
				}
				defer peer.Close()
				ownerQ := "SELECT r.rolname FROM pg_database d JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname=" + pq.QuoteLiteral(database)
				check := func(e fixture.Executor, owner string) {
					s.t.Helper()
					fixture.RunStep(s.t, s.p.Ctx, e, s.record, fixture.Select(ownerQ, []string{"rolname"}, []string{"NAME"}, [][]any{{owner}}))
				}
				implicit := func(x *ownershipTransaction, source, target bool) {
					x.run(fixture.Select("SELECT pg_has_role('own_source','pg_database_owner','MEMBER') AS source_member,pg_has_role('own_source','pg_database_owner','USAGE') AS source_usage,pg_has_role('own_source','pg_database_owner','SET') AS source_set,pg_has_role('own_target','pg_database_owner','MEMBER') AS target_member,pg_has_role('own_target','pg_database_owner','USAGE') AS target_usage,pg_has_role('own_target','pg_database_owner','SET') AS target_set", []string{"source_member", "source_usage", "source_set", "target_member", "target_usage", "target_set"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{source, source, source, target, target, target}}))
					x.run(fixture.Select("SELECT pg_has_role('own_source','pg_database_owner','MEMBER WITH ADMIN OPTION') AS source_admin,pg_has_role('own_target','pg_database_owner','MEMBER WITH ADMIN OPTION') AS target_admin", []string{"source_admin", "target_admin"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}}))
				}
				peerImplicit := func(x *ownershipTransaction, source, target bool) {
					view := *x
					view.e = peer
					view.held = nil
					implicit(&view, source, target)
				}
				for _, commit := range []bool{false, true} {
					if route == "auto" && !commit {
						continue
					}
					s.actor("own_actor") // Select the ordinary session actor before either transaction API.
					x := ownershipBegin(s, route, prefix+"own_target", prefix+"own_source", "REASSIGN OWNED BY own_source TO own_target")
					defer x.abort()
					implicit(x, true, false)
					x.exec(prefix + "own_target")
					implicit(x, false, true)
					check(x.e, "own_target")
					if route != "auto" {
						check(peer, "own_source")
						check(remote, "own_source")
						peerImplicit(x, true, false)
						x.exec("SAVEPOINT database_owner")
						x.exec(prefix + "own_source")
						implicit(x, true, false)
						x.exec("ROLLBACK TO database_owner")
						implicit(x, false, true)
						x.exec("ROLLBACK TO database_owner")
						implicit(x, false, true)
						x.exec("RELEASE database_owner")
					}
					// Private implicit owner authorizes a real namespace creation.
					x.exec("SET ROLE own_target")
					x.exec("SET ROLE pg_database_owner")
					x.run(fixture.Select("SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"pg_database_owner"}}))
					x.exec("RESET ROLE")
					x.exec("SET ROLE own_target")
					x.exec("CREATE SCHEMA own_schema")
					x.exec("RESET ROLE")
					if route != "auto" {
						x.exec("SAVEPOINT former_database_owner")
					}
					x.exec("SET ROLE own_source")
					denied := fixture.ImplicitError(prefix+"own_source", fixture.Diagnostic{Code: "42501", Message: "must be owner of database " + database})
					denied.ErrorStage = "execute"
					x.run(denied)
					if route != "auto" {
						x.exec("ROLLBACK TO former_database_owner")
						x.exec("RELEASE former_database_owner")
					} else {
						x.exec("RESET ROLE")
					}
					if route != "auto" {
						for _, q := range []string{"SET SESSION AUTHORIZATION own_source", "SET ROLE pg_database_owner", "RESET ROLE", "RESET SESSION AUTHORIZATION"} {
							fixture.RunStep(s.t, s.p.Ctx, peer, s.record, fixture.Exec(q, 0))
						}
					}
					x.exec("REASSIGN OWNED BY own_source TO own_target")
					// Projection must merge the other target's latest committed root.
					remoteRun(fixture.Exec("CREATE TABLE tx_remote_unrelated(id INT)", 0))
					remoteRun(fixture.Exec("INSERT INTO tx_remote_unrelated VALUES(9)", 1))
					x.end(commit)
					s.actor("")
					if commit {
						check(peer, "own_target")
						check(remote, "own_target")
					} else {
						check(peer, "own_source")
						check(remote, "own_source")
					}
					peerImplicit(x, !commit, commit)
					remoteRun(fixture.Select("SELECT id FROM tx_remote_unrelated", []string{"id"}, []string{"INT4"}, [][]any{{int64(9)}}))
					remoteRun(fixture.Select("SELECT r.rolname FROM pg_class c JOIN pg_roles r ON r.oid=c.relowner WHERE c.relname='tx_remote_owned'", []string{"rolname"}, []string{"NAME"}, [][]any{{"own_source"}}))
					remoteRun(fixture.Exec("DROP TABLE tx_remote_unrelated", 0))
					if !commit {
						ownershipOwner(s, "tx_database_local", "own_source")
					}
				}
				s.exec("DROP OWNED BY own_source")
				check(s.c, "own_target") // DROP OWNED never deletes the shared database.
				ownershipRoleDependency(s, "own_source", "1 object in database "+remoteName)
				remoteRun(fixture.Exec("DROP OWNED BY own_source", 0))
				s.exec("DROP ROLE own_source")
				ownershipRows(s, "tx_database_local")
			})
		})
	}
}
