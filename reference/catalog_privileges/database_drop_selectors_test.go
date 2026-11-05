package reference_test

import (
	"database/sql/driver"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDatabaseDropSelectors(t *testing.T) {
	for _, mode := range []string{"direct", "fresh", "held"} {
		t.Run(mode, func(t *testing.T) {
			const original, renamed, peer, login = "dd_default_original", "dd_default_renamed", "dd_default_peer", "dd_default_login"
			fixture.RoleReferenceCleanup(t, []string{login})
			names := []string{original, renamed, peer}
			fixture.DatabaseReferenceCleanup(t, &names)
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					d := stuntgres.NewDriver()
					base, err := d.OpenConnector(original)
					if err != nil {
						t.Fatal(err)
					}
					var connector driver.Connector = base
					if backend.PG {
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE DATABASE "+original, 0))
					}
					initial, initialRaw, initialRec := fixture.DatabaseConnectorScenarioPool(t, connector, false)
					if backend.PG {
						initial, initialRaw, initialRec = fixture.DatabaseScenarioPool(t, p, true, original, "")
					}
					fixture.RunStep(t, p.Ctx, initial, initialRec, fixture.Exec("CREATE DATABASE "+peer, 0))
					var peerConnector driver.Connector = base.(*stuntgres.Connector).WithDatabase(peer)
					peerDB, peerRaw, peerRecord := fixture.DatabaseConnectorScenarioPool(t, peerConnector, false)
					if backend.PG {
						peerDB, peerRaw, peerRecord = fixture.DatabaseScenarioPool(t, p, true, peer, "")
					}
					peerDB.SetMaxIdleConns(0)
					physical, err := peerDB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { physical.Close() })
					backend.Recorder = peerRecord
					c := &databaseCreateCase{t, p, backend, physical, &names}
					c.run(fixture.Exec("CREATE TABLE selector_value(v integer)", 0))
					c.run(fixture.Exec("INSERT INTO selector_value VALUES(22)", 1))
					databaseDropClose(t, initial, initialRaw)
					fixture.DatabaseReferenceOwners(t, p, physical, peerRecord, original, 0)
					c.run(fixture.Exec("ALTER DATABASE "+original+" RENAME TO "+renamed, 0))
					query := "DROP DATABASE " + renamed
					route := databaseDropPrepare(c, mode, query)
					oid := c.scalar("SELECT oid::text FROM pg_database WHERE datname=$1", renamed)
					route.run(c, fixture.Exec(query, 0))
					c.absent(renamed)
					c.run(fixture.Exec("CREATE ROLE "+login+" LOGIN PASSWORD 'role-core-fixture'", 0))
					var roleConnector driver.Connector
					if !backend.PG {
						roleConnector, err = d.OpenConnectorAs(original, login)
						if err != nil {
							t.Fatal(err)
						}
						roleConnector = roleConnector.(*stuntgres.Connector).WithDatabase(peer)
					}
					loginDB, loginRaw, loginRecord := peerDB, peerRaw, peerRecord
					if backend.PG {
						loginDB, loginRaw, loginRecord = fixture.DatabaseScenarioPool(t, p, true, peer, login)
					} else {
						loginDB, loginRaw, loginRecord = fixture.DatabaseConnectorScenarioPool(t, roleConnector, false)
					}
					fixture.RunStep(t, p.Ctx, loginDB, loginRecord, fixture.Select("SELECT current_user,current_database()", []string{"current_user", "current_database"}, []string{"NAME", "NAME"}, [][]any{{login, peer}}))
					c.run(fixture.Exec("CREATE DATABASE "+renamed, 0))
					if c.scalar("SELECT oid::text FROM pg_database WHERE datname=$1", renamed) == oid {
						t.Fatal("same-name default recreation reused retired identity")
					}
					var selected driver.Connector = base.(*stuntgres.Connector).WithDatabase(renamed)
					recreated, recreatedRaw, recreatedRecord := fixture.DatabaseConnectorScenarioPool(t, selected, false)
					if backend.PG {
						recreated, recreatedRaw, recreatedRecord = fixture.DatabaseScenarioPool(t, p, true, renamed, "")
					}
					fixture.RunStep(t, p.Ctx, recreated, recreatedRecord, fixture.Select("SELECT current_database()", []string{"current_database"}, []string{"NAME"}, [][]any{{renamed}}))
					if !backend.PG {
						// Default attachment retains its exact retired incarnation and last name,
						// even when a new incarnation now owns that spelling.
						for range 2 {
							failed, err := base.Connect(p.Ctx)
							if failed != nil {
								failed.Close()
								t.Fatal("default adopted replacement")
							}
							want := databaseCreateDiagnostic("3D000", "database \""+renamed+"\" does not exist")
							want.Severity, want.SeverityUnlocalized = "FATAL", "FATAL"
							fixture.AssertDiagnostic(t, err, &want)
						}
						same, err := d.OpenDatabaseConnector(original, peer)
						if err != nil {
							t.Fatal(err)
						}
						healthy, err := same.Connect(p.Ctx)
						if err != nil {
							t.Fatal("opaque cluster authority disappeared", err)
						}
						healthy.Close()
					}
					c.run(fixture.Select("SELECT v FROM selector_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(22)}}))
					c.run(fixture.Select("SELECT pg_stat_reset_shared('bgwriter')", []string{"pg_stat_reset_shared"}, []string{"VOID"}, [][]any{{""}}))
					bootstrap := "postgres"
					if backend.PG {
						bootstrap = "stuntgres"
					}
					c.run(fixture.Select("SELECT datname,usename FROM pg_stat_activity WHERE pid=pg_backend_pid()", []string{"datname", "usename"}, []string{"NAME", "NAME"}, [][]any{{peer, bootstrap}}))
					c.run(fixture.Select("SELECT count(*) FROM pg_stat_database WHERE datid=0", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
					databaseDropClose(t, recreated, recreatedRaw, loginDB, loginRaw)
					if held, ok := route.held.(fixture.HeldStatement); ok {
						if err := held.Statement.Close(); err != nil {
							t.Fatal(err)
						}
					}
					physical.Close()
					databaseDropClose(t, peerDB, peerRaw)
				})
			}
			// Native host-key provenance has no SQL DSN equivalent in PostgreSQL. The
			// common SQL above independently proves retirement; these public host phases
			// prove exact empty/space/URI-like keys and independent Driver authority.
			d := stuntgres.NewDriver()
			for _, key := range []string{"", " ", "postgres://opaque/identity?db=one", "postgres://opaque/identity?db=two"} {
				connector, err := d.OpenConnector(key)
				if err != nil {
					t.Fatal(err)
				}
				db, raw, rec := fixture.DatabaseConnectorScenarioPool(t, connector, false)
				fixture.RunStep(t, p.Ctx, db, rec, fixture.Exec("CREATE DATABASE dd_opaque_child", 0))
				fixture.RunStep(t, p.Ctx, db, rec, fixture.Exec("CREATE DATABASE dd_opaque_peer", 0))
				fixture.RunStep(t, p.Ctx, db, rec, fixture.Exec("DROP DATABASE dd_opaque_child", 0))
				same, err := d.OpenDatabaseConnector(key, "dd_opaque_peer")
				if err != nil {
					t.Fatal(err)
				}
				physical, err := same.Connect(p.Ctx)
				if err != nil {
					t.Fatal("opaque map entry removed", err)
				}
				physical.Close()
				independent, err := stuntgres.NewDriver().OpenDatabaseConnector(key, "dd_opaque_peer")
				if err != nil {
					t.Fatal(err)
				}
				unexpected, err := independent.Connect(p.Ctx)
				if unexpected != nil {
					unexpected.Close()
					t.Fatal("independent Driver adopted peer")
				}
				want := databaseCreateDiagnostic("3D000", `database "dd_opaque_peer" does not exist`)
				want.Severity, want.SeverityUnlocalized = "FATAL", "FATAL"
				fixture.AssertDiagnostic(t, err, &want)
				fixture.RunStep(t, p.Ctx, db, rec, fixture.Select("SELECT datname FROM pg_database WHERE datname=$1", []string{"datname"}, []string{"NAME"}, [][]any{{"dd_opaque_peer"}}, "dd_opaque_peer"))
				databaseDropClose(t, db, raw)
			}
		})
	}
}
