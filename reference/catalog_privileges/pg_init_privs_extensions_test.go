package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGInitPrivsNoPhantoms(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		ipExec(t, p, b, b.DB, "CREATE ROLE ip_reader", "CREATE TABLE ip_plain(a INT,b INT)", "CREATE SEQUENCE ip_seq", "CREATE FUNCTION ip_fn() RETURNS integer LANGUAGE SQL AS 'SELECT 1'", "CREATE TYPE ip_type AS ENUM ('a')", "CREATE SCHEMA ip_hidden", "GRANT SELECT ON ip_plain TO ip_reader", "GRANT UPDATE(b) ON ip_plain TO ip_reader", "GRANT USAGE ON SEQUENCE ip_seq TO ip_reader", "GRANT EXECUTE ON FUNCTION ip_fn() TO ip_reader", "GRANT USAGE ON TYPE ip_type TO ip_reader", "GRANT USAGE ON SCHEMA ip_hidden TO ip_reader", "ALTER DEFAULT PRIVILEGES GRANT SELECT ON TABLES TO ip_reader", "CREATE TABLE ip_default(a INT)", "CREATE EXTENSION pageinspect")
		for _, name := range []string{"ip_plain", "ip_seq", "ip_default"} {
			ipAbsent(t, p, b, b.DB, 1259, ipOID(t, p, b, "SELECT oid FROM pg_class WHERE relname=$1", name))
		}
		for _, name := range []string{"ip_plain", "_ip_plain", "ip_type", "_ip_type"} {
			ipAbsent(t, p, b, b.DB, 1247, ipOID(t, p, b, "SELECT oid FROM pg_type WHERE typname=$1 AND typnamespace='public'::regnamespace", name))
		}
		ipAbsent(t, p, b, b.DB, 1255, ipOID(t, p, b, "SELECT oid FROM pg_proc WHERE proname='ip_fn'"))
		ipAbsent(t, p, b, b.DB, 2615, ipOID(t, p, b, "SELECT oid FROM pg_namespace WHERE nspname='ip_hidden'"))
		ipCheck(t, p, b, b.DB, "SELECT i.objoid FROM pg_init_privs i JOIN pg_depend d ON d.objid=i.objoid AND d.classid=i.classoid JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='pageinspect' AND d.deptype='e'", []string{"objoid"}, []string{"OID"}, nil)
		ipExec(t, p, b, b.DB, "ALTER DEFAULT PRIVILEGES REVOKE SELECT ON TABLES FROM ip_reader")
	})
}

var ipAmcheck = []string{"bt_index_check(regclass)", "bt_index_check(regclass,boolean)", "bt_index_check(regclass,boolean,boolean)", "bt_index_parent_check(regclass)", "bt_index_parent_check(regclass,boolean)", "bt_index_parent_check(regclass,boolean,boolean)", "bt_index_parent_check(regclass,boolean,boolean,boolean)", "verify_heapam(regclass,boolean,boolean,text,bigint,bigint)"}

func ipAmcheckACLs(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	owner := ipOwner(t, p, b)
	for _, signature := range ipAmcheck {
		oid := ipOID(t, p, b, "SELECT $1::regprocedure::oid", signature)
		ipACL(t, p, b, b.DB, 1255, oid, 0, "e", ipGrant{owner, owner, []string{"EXECUTE"}, false})
	}
}

func TestReferencePGInitPrivsExtensions(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		ipExec(t, p, b, b.DB, "CREATE ROLE ip_reader", "CREATE EXTENSION amcheck")
		ipAmcheckACLs(t, p, b)
		ipExec(t, p, b, b.DB, "GRANT EXECUTE ON FUNCTION bt_index_check(regclass) TO ip_reader")
		ipAmcheckACLs(t, p, b)
		ipCheck(t, p, b, b.DB, "SELECT has_function_privilege('ip_reader','bt_index_check(regclass)','EXECUTE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{true}})
		ipExec(t, p, b, b.DB, "DROP EXTENSION amcheck")
	})
}

func TestReferencePGInitPrivsUpdates(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		ipExec(t, p, b, b.DB, "CREATE ROLE ip_reader", "CREATE EXTENSION amcheck", "GRANT EXECUTE ON FUNCTION bt_index_check(regclass) TO ip_reader", "ALTER EXTENSION amcheck UPDATE TO '1.5'")
		ipAmcheckACLs(t, p, b)
		ipCheck(t, p, b, b.DB, "SELECT has_function_privilege('ip_reader','bt_index_check(regclass)','EXECUTE') AS allowed", []string{"allowed"}, []string{"BOOL"}, [][]any{{true}})
		ipExec(t, p, b, b.DB, "ALTER EXTENSION amcheck UPDATE TO '1.5'")
		ipAmcheckACLs(t, p, b)
	})
}

func TestReferencePGInitPrivsMembership(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		oid, owner, reader := ipMemberSetup(t, p, b)
		ipAbsent(t, p, b, b.DB, 1259, oid)
		ipExec(t, p, b, b.DB, "ALTER EXTENSION pageinspect ADD TABLE ip_member")
		ipTableACL(t, p, b, b.DB, oid, owner, reader, "SELECT")
		ipACL(t, p, b, b.DB, 1259, oid, 2, "e", ipGrant{reader, owner, []string{"UPDATE"}, false})
		ipExec(t, p, b, b.DB, "REVOKE SELECT ON ip_member FROM ip_reader", "GRANT INSERT ON ip_member TO ip_reader", "ALTER TABLE ip_member OWNER TO ip_other", "ALTER TABLE ip_member RENAME TO ip_renamed_table", "CREATE SCHEMA ip_moved", "ALTER TABLE ip_renamed_table SET SCHEMA ip_moved")
		ipTableACL(t, p, b, b.DB, oid, owner, reader, "SELECT")
		ipExec(t, p, b, b.DB, "ALTER EXTENSION pageinspect DROP TABLE ip_moved.ip_renamed_table")
		ipAbsent(t, p, b, b.DB, 1259, oid)
		ipExec(t, p, b, b.DB, "ALTER TABLE ip_moved.ip_renamed_table OWNER TO CURRENT_USER", "ALTER EXTENSION pageinspect ADD TABLE ip_moved.ip_renamed_table")
		ipTableACL(t, p, b, b.DB, oid, owner, reader, "INSERT")
		ipExec(t, p, b, b.DB, "ALTER TABLE ip_moved.ip_renamed_table DROP COLUMN a")
		ipACL(t, p, b, b.DB, 1259, oid, 2, "e", ipGrant{reader, owner, []string{"UPDATE"}, false})
		ipExec(t, p, b, b.DB, "ALTER TABLE ip_moved.ip_renamed_table DROP COLUMN b")
		ipCheck(t, p, b, b.DB, "SELECT objsubid FROM pg_init_privs WHERE classoid=1259 AND objoid=$1", []string{"objsubid"}, []string{"INT4"}, [][]any{{int64(0)}}, oid)
		ipExec(t, p, b, b.DB, "ALTER EXTENSION pageinspect DROP TABLE ip_moved.ip_renamed_table", "DROP TABLE ip_moved.ip_renamed_table")
		ipAbsent(t, p, b, b.DB, 1259, oid)
		ipMembershipClasses(t, p, b, owner, reader)
		ipGrantorWitness(t, p, b, owner, reader)
		ipHistoricalOwnerWitness(t, p, b)
	})
}

func ipMembershipClasses(t *testing.T, p *fixture.Pair, b fixture.Backend, owner, reader int64) {
	t.Helper()
	for _, tc := range []struct {
		create, grant, object, lookup           string
		class                                   int64
		ownerRights, publicRights, readerRights []string
	}{
		{"CREATE SEQUENCE ip_seq", "GRANT USAGE ON SEQUENCE ip_seq TO ip_reader", "SEQUENCE ip_seq", "SELECT oid FROM pg_class WHERE relname='ip_seq'", 1259, []string{"SELECT", "UPDATE", "USAGE"}, nil, []string{"USAGE"}},
		{"CREATE FUNCTION ip_fn() RETURNS integer LANGUAGE SQL AS 'SELECT 1'", "GRANT EXECUTE ON FUNCTION ip_fn() TO ip_reader", "FUNCTION ip_fn()", "SELECT oid FROM pg_proc WHERE proname='ip_fn'", 1255, []string{"EXECUTE"}, []string{"EXECUTE"}, []string{"EXECUTE"}},
		{"CREATE TYPE ip_type AS ENUM ('a')", "GRANT USAGE ON TYPE ip_type TO ip_reader", "TYPE ip_type", "SELECT oid FROM pg_type WHERE typname='ip_type'", 1247, []string{"USAGE"}, []string{"USAGE"}, []string{"USAGE"}},
		{"CREATE SCHEMA ip_hidden", "GRANT USAGE ON SCHEMA ip_hidden TO ip_reader", "SCHEMA ip_hidden", "SELECT oid FROM pg_namespace WHERE nspname='ip_hidden'", 2615, []string{"USAGE", "CREATE"}, nil, []string{"USAGE"}},
		{"CREATE FOREIGN DATA WRAPPER ip_fdw", "GRANT USAGE ON FOREIGN DATA WRAPPER ip_fdw TO ip_reader", "FOREIGN DATA WRAPPER ip_fdw", "SELECT oid FROM pg_foreign_data_wrapper WHERE fdwname='ip_fdw'", 2328, []string{"USAGE"}, nil, []string{"USAGE"}},
		{"CREATE SERVER ip_server FOREIGN DATA WRAPPER ip_fdw", "GRANT USAGE ON FOREIGN SERVER ip_server TO ip_reader", "SERVER ip_server", "SELECT oid FROM pg_foreign_server WHERE srvname='ip_server'", 1417, []string{"USAGE"}, nil, []string{"USAGE"}},
	} {
		ipExec(t, p, b, b.DB, tc.create, tc.grant, "ALTER EXTENSION pageinspect ADD "+tc.object)
		oid := ipOID(t, p, b, tc.lookup)
		grants := []ipGrant{{owner, owner, tc.ownerRights, false}, {reader, owner, tc.readerRights, false}}
		if tc.publicRights != nil {
			grants = append(grants, ipGrant{0, owner, tc.publicRights, false})
		}
		ipACL(t, p, b, b.DB, tc.class, oid, 0, "e", grants...)
		ipExec(t, p, b, b.DB, "ALTER EXTENSION pageinspect DROP "+tc.object)
		ipAbsent(t, p, b, b.DB, tc.class, oid)
	}
	ipExec(t, p, b, b.DB, "DROP SERVER ip_server", "DROP FOREIGN DATA WRAPPER ip_fdw")
}
