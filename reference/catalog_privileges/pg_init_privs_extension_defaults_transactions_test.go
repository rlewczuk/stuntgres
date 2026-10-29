package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGInitPrivsExtensionDefaultsTransactions(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			ipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				ipExec(t, p, b, b.DB, "CREATE ROLE ip_reader", "CREATE ROLE ip_other", "CREATE SCHEMA ip_hidden")
				owner := ipOwner(t, p, b)
				reader := ipRole(t, p, b, "ip_reader")
				other := ipRole(t, p, b, "ip_other")
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				defer conn.ExecContext(p.Ctx, "ROLLBACK")
				run := func(commands ...string) {
					for _, query := range commands {
						s := fixture.Exec(query, 0)
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
					}
				}
				policy := func(db fixture.Executor, configured bool) {
					var want [][]any
					if configured {
						want = [][]any{
							{int64(0), owner, owner, "EXECUTE", false},
							{int64(0), int64(0), owner, "EXECUTE", false},
							{int64(0), reader, owner, "EXECUTE", true},
							{ipOID(t, p, b, "SELECT oid FROM pg_namespace WHERE nspname='ip_hidden'"), other, owner, "EXECUTE", false},
						}
					}
					ipCheck(t, p, b, db,
						"SELECT d.defaclnamespace,x.grantee,x.grantor,x.privilege_type,x.is_grantable FROM pg_default_acl d CROSS JOIN LATERAL aclexplode(d.defaclacl) x WHERE d.defaclrole=$1 AND d.defaclobjtype='f'",
						[]string{"defaclnamespace", "grantee", "grantor", "privilege_type", "is_grantable"},
						[]string{"OID", "OID", "OID", "TEXT", "BOOL"}, want, owner)
					if !configured {
						ipCheck(t, p, b, db, "SELECT defaclrole FROM pg_default_acl WHERE defaclrole=$1", []string{"defaclrole"}, []string{"OID"}, nil, owner)
					}
				}
				absent := func(db fixture.Executor, oids []int64) {
					ipCheck(t, p, b, db, "SELECT extname FROM pg_extension WHERE extname='amcheck'", []string{"extname"}, []string{"NAME"}, nil)
					ipCheck(t, p, b, db, "SELECT f.oid FROM pg_proc f JOIN pg_namespace n ON n.oid=f.pronamespace WHERE n.nspname='ip_hidden'", []string{"oid"}, []string{"OID"}, nil)
					for _, oid := range oids {
						ipAbsent(t, p, b, db, 1255, oid)
					}
				}
				install := func() []int64 {
					run("CREATE EXTENSION amcheck SCHEMA ip_hidden")
					var oids []int64
					for _, signature := range ipAmcheck {
						var oid int64
						// Discover generated identity from pg_proc's regprocedure
						// resolver in the installing session, never from history.
						if err := conn.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid", "ip_hidden."+signature).Scan(&oid); err != nil {
							t.Fatal(err)
						}
						oids = append(oids, oid)
					}
					return oids
				}
				installed := func(oids []int64, configuredCurrent bool) {
					ipCheck(t, p, b, conn, "SELECT extname FROM pg_extension WHERE extname='amcheck'", []string{"extname"}, []string{"NAME"}, [][]any{{"amcheck"}})
					for _, oid := range oids {
						ipACL(t, p, b, conn, 1255, oid, 0, "e",
							ipGrant{owner, owner, []string{"EXECUTE"}, false},
							ipGrant{reader, owner, []string{"EXECUTE"}, true},
							ipGrant{other, owner, []string{"EXECUTE"}, false})
						current := [][]any{{owner, owner, "EXECUTE", false}}
						if configuredCurrent {
							current = append(current, []any{reader, owner, "EXECUTE", true}, []any{other, owner, "EXECUTE", false})
						}
						ipCheck(t, p, b, conn,
							"SELECT x.grantee,x.grantor,x.privilege_type,x.is_grantable FROM pg_proc f CROSS JOIN LATERAL aclexplode(f.proacl) x WHERE f.oid=$1",
							[]string{"grantee", "grantor", "privilege_type", "is_grantable"}, []string{"OID", "OID", "TEXT", "BOOL"}, current, oid)
						ipCheck(t, p, b, conn,
							"SELECT e.extname FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.objid=$1 AND d.deptype='e'",
							[]string{"extname"}, []string{"NAME"}, [][]any{{"amcheck"}}, oid)
					}
				}

				policy(b.DB, false)
				run("BEGIN",
					"ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO ip_reader WITH GRANT OPTION",
					"ALTER DEFAULT PRIVILEGES IN SCHEMA ip_hidden GRANT EXECUTE ON FUNCTIONS TO ip_other",
					"SAVEPOINT before_install")
				policy(conn, true)
				policy(b.DB, false)
				oids := install()
				installed(oids, true)
				absent(b.DB, oids)
				run("SAVEPOINT installed",
					"ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM ip_reader",
					"ALTER DEFAULT PRIVILEGES IN SCHEMA ip_hidden REVOKE EXECUTE ON FUNCTIONS FROM ip_other",
					"REVOKE EXECUTE ON ALL FUNCTIONS IN SCHEMA ip_hidden FROM ip_reader,ip_other")
				policy(conn, false)
				installed(oids, false)
				run("ROLLBACK TO SAVEPOINT installed")
				policy(conn, true)
				installed(oids, true)
				run("ROLLBACK TO SAVEPOINT before_install")
				policy(conn, true)
				absent(conn, oids)
				// Reinstallation must consume the restored private policy, not a
				// stale installation or a committed-default cache.
				reinstalled := install()
				installed(reinstalled, true)
				run("ROLLBACK")
				policy(conn, false)
				policy(b.DB, false)
				absent(conn, append(oids, reinstalled...))
				absent(b.DB, append(oids, reinstalled...))
			})
		})
	}
}
