package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/lib/pq"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferencePredefinedRoleIdentities(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor"}, func(s *membershipScenario, db *sql.DB) {
		for _, role := range predefinedRoles {
			s.exec("SET ROLE " + role.name)
			predefinedSelect(s, "SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{role.name}})
			s.exec("RESET ROLE")
			if s.backend.PG {
				predefinedSelect(s, `SELECT oid::int4,rolname,rolinherit,rolcanlogin,rolsuper,rolcreatedb,rolcreaterole,rolreplication,rolbypassrls,rolconnlimit,rolpassword IS NULL AS password_null,rolvaliduntil IS NULL AS expiry_null FROM pg_authid WHERE rolname='`+role.name+`'`,
					[]string{"oid", "rolname", "rolinherit", "rolcanlogin", "rolsuper", "rolcreatedb", "rolcreaterole", "rolreplication", "rolbypassrls", "rolconnlimit", "password_null", "expiry_null"},
					[]string{"INT4", "NAME", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "INT4", "BOOL", "BOOL"},
					[][]any{{role.oid, role.name, true, false, false, false, false, false, false, int64(-1), true, true}})
			}
			if role.name == "pg_database_owner" {
				continue
			}
			s.exec("GRANT " + role.name + " TO pdef_actor")
			s.actor("pdef_actor")
			s.exec("SET ROLE " + role.name)
			predefinedSelect(s, "SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{role.name}})
			s.actor("")
			s.exec("REVOKE " + role.name + " FROM pdef_actor")
			s.actor("pdef_actor")
			predefinedDenied(s, "SET ROLE "+role.name, `permission denied to set role "`+role.name+`"`, false)
			s.actor("")
		}
	})
}

func TestReferencePredefinedDatabaseOwner(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor", "pdef_plain"}, func(s *membershipScenario, db *sql.DB) {
		s.exec("CREATE SCHEMA pdef_owner AUTHORIZATION pg_database_owner")
		s.exec("SET ROLE pg_database_owner")
		s.exec("CREATE TABLE pdef_owner.t(id INT)")
		s.run(fixture.Exec("INSERT INTO pdef_owner.t VALUES(7)", 1))
		s.exec("RESET ROLE")
		predefinedSelect(s, "SELECT id FROM pdef_owner.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		s.exec("ALTER TABLE pdef_owner.t ADD COLUMN v TEXT")
		s.actor("pdef_actor")
		predefinedDenied(s, "SET ROLE pg_database_owner", `permission denied to set role "pg_database_owner"`, false)
		predefinedFailure(s, "SELECT id FROM pdef_owner.t", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_owner", Position: "16"}, true, "prepare")
		s.actor("")
		for _, q := range []string{
			"GRANT pg_database_owner TO pdef_actor",
			"GRANT pdef_plain,pg_database_owner TO pdef_actor",
			"CREATE ROLE pdef_new IN ROLE pg_database_owner",
		} {
			s.fail(q, "0A000", `role "pg_database_owner" cannot have explicit members`, "")
		}
		s.fail("ALTER GROUP pg_database_owner ADD USER pdef_actor", "42939", `role name "pg_database_owner" is reserved`, "Cannot alter reserved roles.")
		for _, q := range []string{
			"GRANT pdef_plain TO pg_database_owner",
			"GRANT pdef_plain TO pdef_actor,pg_database_owner",
			"ALTER GROUP pdef_plain ADD USER pg_database_owner",
			"CREATE ROLE pdef_new ROLE pg_database_owner",
			"CREATE ROLE pdef_new ADMIN pg_database_owner",
		} {
			s.fail(q, "0A000", `role "pg_database_owner" cannot be a member of any role`, "")
		}
		s.fail("DROP ROLE pdef_new", "42704", `role "pdef_new" does not exist`, "")
		s.actor("pdef_actor")
		predefinedDenied(s, "SET ROLE pdef_plain", `permission denied to set role "pdef_plain"`, false)
		s.actor("")
		if s.backend.PG {
			predefinedSelect(s, "SELECT pg_has_role(current_user,'pg_database_owner','MEMBER') AS member,pg_has_role(current_user,'pg_database_owner','USAGE') AS inherited,pg_has_role(current_user,'pg_database_owner','SET') AS settable", []string{"member", "inherited", "settable"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}})
			predefinedSelect(s, "SELECT count(*) FROM pg_auth_members WHERE roleid=6171 OR member=6171", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
		}
		for _, tc := range []struct{ q, role, member string }{
			{"REVOKE pg_database_owner FROM pdef_actor", "pg_database_owner", "pdef_actor"},
			{"REVOKE pdef_plain FROM pg_database_owner", "pdef_plain", "pg_database_owner"},
			{"REVOKE pg_database_owner FROM " + s.bootstrap, "pg_database_owner", s.bootstrap},
		} {
			s.clearNotices()
			s.exec(tc.q)
			s.notice("WARNING", "01000", fmt.Sprintf("role \"%s\" has not been granted membership in role \"%s\" by role \"%s\"", tc.member, tc.role, s.bootstrap))
		}
		s.exec("SET ROLE pg_database_owner")
		predefinedSelect(s, "SELECT id FROM pdef_owner.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		s.exec("RESET ROLE")
		name := ownershipDatabaseName(s)
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			for _, q := range []string{"RESET ROLE", "RESET SESSION AUTHORIZATION", "ALTER DATABASE " + pq.QuoteIdentifier(name) + " OWNER TO " + pq.QuoteIdentifier(s.bootstrap)} {
				if _, err := s.c.ExecContext(ctx, q); err != nil {
					s.t.Error(err)
				}
			}
		}()
		s.exec("ALTER DATABASE " + pq.QuoteIdentifier(name) + " OWNER TO pdef_actor")
		s.actor("pdef_actor")
		s.exec("ALTER TABLE pdef_owner.t ADD COLUMN successor INT")
		s.exec("SET ROLE pg_database_owner")
		predefinedSelect(s, "SELECT id FROM pdef_owner.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		s.exec("RESET ROLE")
		s.actor("")
		s.exec("ALTER DATABASE " + pq.QuoteIdentifier(name) + " OWNER TO pdef_plain")
		s.actor("pdef_actor")
		predefinedDenied(s, "SET ROLE pg_database_owner", `permission denied to set role "pg_database_owner"`, false)
		predefinedFailure(s, "SELECT id FROM pdef_owner.t", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pdef_owner", Position: "16"}, true, "prepare")
		s.actor("pdef_plain")
		s.exec("ALTER TABLE pdef_owner.t ADD COLUMN final_owner INT")
		s.exec("SET ROLE pg_database_owner")
		predefinedSelect(s, "SELECT id FROM pdef_owner.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		s.exec("RESET ROLE")
		s.actor("")
		if s.backend.PG {
			predefinedSelect(s, "SELECT count(*) FROM pg_auth_members WHERE roleid=6171 OR member=6171", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
			predefinedSelect(s, "SELECT pg_has_role('pdef_plain','pg_database_owner','MEMBER') AS member,pg_has_role('pdef_plain','pg_database_owner','USAGE') AS inherited,pg_has_role('pdef_plain','pg_database_owner','SET') AS settable,pg_has_role('pdef_plain','pg_database_owner','MEMBER WITH ADMIN OPTION') AS admin", []string{"member", "inherited", "settable", "admin"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, false}})
		}
		// Database ownership supplies MEMBER/INHERIT/SET, never ADMIN. Give the
		// actor CREATEROLE so this refusal isolates the absent ADMIN authority.
		s.exec("ALTER ROLE pdef_plain CREATEROLE")
		s.actor("pdef_plain")
		detail := "Only roles with the CREATEROLE attribute and the ADMIN option on the target roles may drop roles."
		if s.backend.PG {
			detail = `Only roles with the CREATEROLE attribute and the ADMIN option on role "pg_database_owner" may drop this role.`
		}
		predefinedFailure(s, "DROP ROLE pg_database_owner", fixture.Diagnostic{Code: "42501", Message: "permission denied to drop role", Detail: detail}, false, "execute")
		predefinedSelect(s, "SELECT id FROM pdef_owner.t", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
		s.actor("")
		s.exec("ALTER ROLE pdef_plain NOCREATEROLE")
	})
}

func TestReferencePredefinedDiagnostics(t *testing.T) {
	predefinedCases(t, []string{"pdef_actor", "pdef_victim"}, func(s *membershipScenario, db *sql.DB) {
		s.fail("DROP ROLE "+s.bootstrap, "55006", "current user cannot be dropped", "")
		for _, role := range predefinedRoles {
			s.fail("CREATE ROLE "+role.name, "42939", `role name "`+role.name+`" is reserved`, "Role names starting with \"pg_\" are reserved.")
			s.fail("ALTER ROLE "+role.name+" LOGIN", "42939", `role name "`+role.name+`" is reserved`, "Cannot alter reserved roles.")
			s.fail("DROP ROLE "+role.name, "2BP01", "cannot drop role "+role.name+" because it is required by the database system", "")
		}
		s.fail("DROP ROLE pdef_victim,pg_maintain", "2BP01", "cannot drop role pg_maintain because it is required by the database system", "")
		s.exec("SET ROLE pdef_victim")
		predefinedSelect(s, "SELECT current_user", []string{"current_user"}, []string{"NAME"}, [][]any{{"pdef_victim"}})
		s.exec("RESET ROLE")
		s.fail("DROP ROLE pg_maintain,pdef_missing", "42704", `role "pdef_missing" does not exist`, "")
		s.fail("DROP ROLE pdef_missing,pg_maintain", "42704", `role "pdef_missing" does not exist`, "")
		s.exec("SET ROLE pg_maintain")
		s.fail("DROP ROLE pg_maintain", "42501", "permission denied to drop role", "Only roles with the CREATEROLE attribute and the ADMIN option on the target roles may drop roles.")
		s.exec("RESET ROLE")
		s.actor("pdef_actor")
		s.fail("DROP ROLE pdef_actor", "42501", "permission denied to drop role", "Only roles with the CREATEROLE attribute and the ADMIN option on the target roles may drop roles.")
		s.fail("DROP ROLE pg_maintain", "42501", "permission denied to drop role", "Only roles with the CREATEROLE attribute and the ADMIN option on the target roles may drop roles.")
		s.actor("")
		s.exec("ALTER ROLE pdef_actor CREATEROLE")
		s.actor("pdef_actor")
		if s.backend.PG {
			s.fail("DROP ROLE pdef_actor", "55006", "current user cannot be dropped", "")
		} else {
			s.fail("DROP ROLE pdef_actor", "42501", "permission denied to drop role", "Only roles with the CREATEROLE attribute and the ADMIN option on the target roles may drop roles.")
		}
		s.actor("")
		s.exec("ALTER ROLE pdef_victim CREATEROLE")
		s.exec("GRANT pdef_victim TO pdef_actor")
		s.actor("pdef_actor")
		s.exec("SET ROLE pdef_victim")
		if s.backend.PG {
			s.fail("DROP ROLE pdef_actor", "55006", "session user cannot be dropped", "")
		} else {
			s.fail("DROP ROLE pdef_actor", "42501", "permission denied to drop role", "Only roles with the CREATEROLE attribute and the ADMIN option on the target roles may drop roles.")
		}
		s.actor("")
	})
}
