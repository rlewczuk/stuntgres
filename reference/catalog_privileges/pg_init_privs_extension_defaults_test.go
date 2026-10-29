package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The released script revokes PUBLIC, not configured default grantees. Global
// grant options and schema-specific grants survive that selective revoke.
func TestReferencePGInitPrivsExtensionDefaults(t *testing.T) {
	ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		ipExec(t, p, b, b.DB,
			"CREATE ROLE ip_reader",
			"CREATE ROLE ip_other",
			"CREATE SCHEMA ip_hidden")
		defer func() {
			for _, query := range []string{
				"ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM ip_reader",
				"ALTER DEFAULT PRIVILEGES IN SCHEMA ip_hidden REVOKE EXECUTE ON FUNCTIONS FROM ip_other",
			} {
				if _, err := b.DB.ExecContext(p.Ctx, query); err != nil {
					t.Error("default privilege cleanup", err)
				}
			}
		}()
		ipExec(t, p, b, b.DB,
			"ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO ip_reader WITH GRANT OPTION",
			"ALTER DEFAULT PRIVILEGES IN SCHEMA ip_hidden GRANT EXECUTE ON FUNCTIONS TO ip_other",
			"CREATE EXTENSION amcheck SCHEMA ip_hidden")
		ipExtensionDefaultACLs(t, p, b, true)
		// The same supported SQL revokes every live grant in both inventories,
		// including PostgreSQL's additional, unimplemented gin_index_check.
		ipExec(t, p, b, b.DB, "REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA ip_hidden FROM ip_reader,ip_other")
		ipExtensionDefaultACLs(t, p, b, false)
		// Remove default-ACL dependencies independently of current procedure ACLs.
		// Dump's existing default-privilege rejection must not mask history replay.
		ipExec(t, p, b, b.DB,
			"ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM ip_reader",
			"ALTER DEFAULT PRIVILEGES IN SCHEMA ip_hidden REVOKE EXECUTE ON FUNCTIONS FROM ip_other")
		ipCheck(t, p, b, b.DB, "SELECT defaclrole FROM pg_default_acl WHERE defaclrole=$1", []string{"defaclrole"}, []string{"OID"}, nil, ipOwner(t, p, b))
		ipExtensionDefaultACLs(t, p, b, false)
		ipExtensionDefaultRoleDependency(t, p, b)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(dump.String(), "stuntgres-initial-privileges-v1") {
			t.Fatal("configured initial grants require historical metadata")
		}
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("initprivs/defaults/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		raw := sql.OpenDB(connector)
		defer raw.Close()
		recorded, recorder := fixture.RecordedNativePool(t, connector)
		destination := fixture.Backend{DB: recorded, Recorder: recorder}
		ipExec(t, p, destination, recorded, "CREATE ROLE ip_other", "CREATE ROLE ip_reader", "CREATE TABLE ip_collision(a INT)")
		if ipRole(t, p, destination, "ip_reader") == ipRole(t, p, b, "ip_reader") {
			t.Fatal("destination did not exercise principal remapping")
		}
		if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		ipExtensionDefaultACLs(t, p, destination, false)
		ipExtensionDefaultRoleDependency(t, p, destination)
		ipCheck(t, p, destination, recorded, "SELECT defaclrole FROM pg_default_acl", []string{"defaclrole"}, []string{"OID"}, nil)
		ipExtensionDefaultACLs(t, p, b, false)
	})
}

func ipExtensionDefaultACLs(t *testing.T, p *fixture.Pair, b fixture.Backend, configuredCurrent bool) {
	t.Helper()
	owner := ipOwner(t, p, b)
	reader := ipRole(t, p, b, "ip_reader")
	other := ipRole(t, p, b, "ip_other")
	history := []ipGrant{
		{owner, owner, []string{"EXECUTE"}, false},
		{reader, owner, []string{"EXECUTE"}, true},
		{other, owner, []string{"EXECUTE"}, false},
	}
	current := [][]any{{owner, owner, "EXECUTE", false}}
	if configuredCurrent {
		current = append(current, []any{reader, owner, "EXECUTE", true}, []any{other, owner, "EXECUTE", false})
	}
	for _, signature := range ipAmcheck {
		oid := ipOID(t, p, b, "SELECT $1::regprocedure::oid", "ip_hidden."+signature)
		ipACL(t, p, b, b.DB, 1255, oid, 0, "e", history...)
		ipCheck(t, p, b, b.DB,
			"SELECT x.grantee,x.grantor,x.privilege_type,x.is_grantable FROM pg_proc f CROSS JOIN LATERAL aclexplode(f.proacl) x WHERE f.oid=$1",
			[]string{"grantee", "grantor", "privilege_type", "is_grantable"},
			[]string{"OID", "OID", "TEXT", "BOOL"}, current, oid)
	}
}

func ipExtensionDefaultRoleDependency(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer conn.ExecContext(p.Ctx, "ROLLBACK")
	for _, prepared := range []bool{false, true} {
		// Isolate one supported historical reference. PostgreSQL additionally
		// retains its released gin_index_check snapshot; it is not implemented
		// natively and must not be fabricated. Rollback restores all membership.
		ipExec(t, p, b, conn, "BEGIN")
		for _, signature := range ipAmcheck[1:] {
			ipExec(t, p, b, conn, "ALTER EXTENSION amcheck DROP FUNCTION ip_hidden."+signature)
		}
		oid := ipOID(t, p, b, "SELECT $1::regprocedure::oid", "ip_hidden."+ipAmcheck[0])
		ipCheck(t, p, b, conn,
			"SELECT i.objoid FROM pg_init_privs i JOIN pg_proc f ON f.oid=i.objoid JOIN pg_namespace n ON n.oid=f.pronamespace WHERE i.classoid=1255 AND n.nspname='ip_hidden' AND f.oid=$1",
			[]string{"objoid"}, []string{"OID"}, [][]any{{oid}}, oid)
		s := fixture.ImplicitError("DROP ROLE ip_reader", fixture.Diagnostic{
			Code:    "2BP01",
			Message: `role "ip_reader" cannot be dropped because some objects depend on it`,
			Detail:  "initial privileges for function ip_hidden.bt_index_check(regclass)",
		})
		// REL_18_6 amcheck--1.4--1.5.sql creates gin_index_check(regclass)
		// after the B-tree declarations. pg_shdepend reports OID-ascending
		// dependency descriptions, so this additional initial reference follows.
		oracle := *s.Diagnostic
		oracle.Detail += "\ninitial privileges for function ip_hidden.gin_index_check(regclass)"
		s.PostgresDiagnostic = &oracle
		s.Prepared, s.ErrorStage = prepared, "execute"
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
		ipExec(t, p, b, conn, "ROLLBACK")
	}
	ipExtensionDefaultACLs(t, p, b, false)
}
