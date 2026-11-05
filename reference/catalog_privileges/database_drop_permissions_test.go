package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatabaseDropPermissions(t *testing.T) {
	for _, mode := range []string{"direct", "fresh", "held"} {
		t.Run(mode, func(t *testing.T) {
			const name, actor, other, owner, member, noinherit = "dd_perm_target", "dd_perm_actor", "dd_perm_other", "dd_perm_owner", "dd_perm_member", "dd_perm_noinherit"
			fixture.RoleReferenceCleanup(t, []string{actor, other, owner, member, noinherit})
			names := []string{name}
			fixture.DatabaseReferenceCleanup(t, &names)
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					var notices []fixture.Diagnostic
					_, admin, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
					backend.Recorder = record
					c := &databaseCreateCase{t, p, backend, admin, &names}
					for _, role := range []string{actor, other, member, noinherit} {
						c.run(fixture.Exec("CREATE ROLE "+role+" LOGIN PASSWORD 'role-core-fixture'", 0))
					}
					c.run(fixture.Exec("CREATE ROLE "+owner, 0))
					c.run(fixture.Exec("ALTER ROLE "+noinherit+" NOINHERIT", 0))
					c.run(fixture.Exec("GRANT "+owner+" TO "+member+", "+noinherit, 0))
					query := "DROP DATABASE " + name + " WITH (FORCE)"
					route := databaseDropPrepare(c, mode, query)
					denied := databaseCreateDiagnostic("42501", "permission denied to terminate process")
					denied.Detail = `Only roles with privileges of the role whose process is being terminated or with privileges of the "pg_signal_backend" role may terminate this process.`
					for _, reverse := range []bool{false, true} {
						c.create(name, "OWNER "+actor, false)
						roles := []string{actor, other}
						if reverse {
							roles = []string{other, actor}
						}
						var owners []*sql.Conn
						var pools []*sql.DB
						var records []*fixture.Recorder
						for _, role := range roles {
							db, raw, rec := c.pool(name, role)
							db.SetMaxIdleConns(0)
							pools = append(pools, db, raw)
							physical, err := db.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { physical.Close() })
							owners = append(owners, physical)
							records = append(records, rec)
							// Effective-role changes cannot grant termination rights over a physical
							// login. The other principal remains ineligible in both creation orders.
							if role == other {
								c.run(fixture.Exec("GRANT "+actor+" TO "+other, 0))
								fixture.RunStep(t, p.Ctx, physical, rec, fixture.Exec("SET ROLE "+actor, 0))
							}
						}
						fixture.DatabaseReferenceOwners(t, p, admin, record, name, 2)
						c.run(fixture.Exec("SET ROLE "+actor, 0))
						for range 2 {
							route.run(c, fixture.ErrorDiagnostic(query, denied))
							for i, physical := range owners {
								fixture.RunStep(t, p.Ctx, physical, records[i], fixture.Select("SELECT 7 AS alive", []string{"alive"}, []string{"INT4"}, [][]any{{int64(7)}}))
							}
						}
						c.run(fixture.Exec("RESET ROLE", 0))
						c.run(fixture.Exec("GRANT pg_signal_backend TO "+actor, 0))
						c.run(fixture.Exec("SET ROLE "+actor, 0))
						route.run(c, fixture.Exec(query, 0))
						c.run(fixture.Exec("RESET ROLE", 0))
						for _, physical := range owners {
							if _, err := physical.ExecContext(p.Ctx, "SELECT 1"); err == nil {
								t.Fatal("eligible FORCE owner survived")
							}
							physical.Close()
						}
						databaseDropClose(t, pools...)
						c.absent(name)
						c.run(fixture.Exec("REVOKE pg_signal_backend FROM "+actor, 0))
						c.run(fixture.Exec("REVOKE "+actor+" FROM "+other, 0))
					}
					// pg_signal_backend does not authorize terminating authenticated superusers.
					c.create(name, "OWNER "+actor, false)
					db, raw, rec := c.pool(name, "")
					db.SetMaxIdleConns(0)
					super, err := db.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { super.Close() })
					fixture.RunStep(t, p.Ctx, super, rec, fixture.Exec("SET ROLE "+actor, 0))
					c.run(fixture.Exec("GRANT pg_signal_backend TO "+actor, 0))
					c.run(fixture.Exec("SET ROLE "+actor, 0))
					protected := denied
					protected.Detail = "Only roles with the SUPERUSER attribute may terminate processes of roles with the SUPERUSER attribute."
					route.run(c, fixture.ErrorDiagnostic(query, protected))
					fixture.RunStep(t, p.Ctx, super, rec, fixture.Select("SELECT 8 AS alive", []string{"alive"}, []string{"INT4"}, [][]any{{int64(8)}}))
					c.run(fixture.Exec("RESET ROLE", 0))
					super.Close()
					databaseDropClose(t, db, raw)
					fixture.DatabaseReferenceOwners(t, p, admin, record, name, 0)
					route.run(c, fixture.Exec(query, 0))
					// Membership inheritance authorizes ownership; NOINHERIT does not. The
					// retained raw utility rechecks the owner of each same-name incarnation.
					for _, role := range []string{member, noinherit} {
						c.create(name, "OWNER "+owner, false)
						c.run(fixture.Exec("SET ROLE "+role, 0))
						if role == member {
							route.run(c, fixture.Exec(query, 0))
						} else {
							route.run(c, fixture.ErrorDiagnostic(query, databaseCreateDiagnostic("42501", "must be owner of database "+name)))
						}
						c.run(fixture.Exec("RESET ROLE", 0))
						if role == noinherit {
							route.run(c, fixture.Exec(query, 0))
						}
					}
				})
			}
		})
	}
}
