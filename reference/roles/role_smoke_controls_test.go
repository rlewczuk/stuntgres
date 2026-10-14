package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// The ordinary creation, authentication, ACL and connector assertions from
// TestRoleSmoke live in TestReferenceRoleCreation/Authorization/AuthenticatedSessions
// and TestReferenceRoleTrustedConnector*. This preserves its remaining controls
// and changed-session pool reuse witnesses without a native-only SQL copy.
func TestReferenceRoleMigratedSmokeControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"role_smoke_owner", "role_smoke_reader", "role_smoke_unsupported"})
	p := fixture.NewScenario(t)
	dsn := "reference/" + t.Name()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			admin, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Close()
			defer admin.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			run := func(c fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
			run(admin, fixture.Exec("CREATE ROLE role_smoke_unsupported", 0))
			if backend.PG {
				run(admin, fixture.Exec("ALTER ROLE role_smoke_unsupported SET application_name TO excluded", 0))
			} else {
				want := fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported")
				want.Position = "1"
				run(admin, fixture.ErrorDiagnostic("ALTER ROLE role_smoke_unsupported SET application_name TO excluded", want))
			}
			run(admin, fixture.Exec("DROP ROLE role_smoke_unsupported", 0))
			for _, q := range []string{"CREATE USER role_smoke_owner", "CREATE USER role_smoke_reader", "SET SESSION AUTHORIZATION role_smoke_owner", "CREATE TABLE secret(id INT)"} {
				if q == "CREATE TABLE secret(id INT)" {
					fixture.WithPublicSchemaOwner(t, p, backend, "role_smoke_owner", func() { run(admin, fixture.Exec(q, 0)) })
				} else {
					run(admin, fixture.Exec(q, 0))
				}
			}
			run(admin, fixture.Prepared(fixture.Exec("INSERT INTO secret VALUES(7)", 1)))
			check := fixture.Select("SELECT id FROM secret ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
			run(admin, check)
			run(admin, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			bootstrap := "postgres"
			if backend.PG {
				bootstrap = "stuntgres"
			}
			// A borrowed connection preserves SQL session identity; actual physical
			// replacement returns to its authenticated bootstrap identity.
			backend.DB.SetMaxOpenConns(2)
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			run(c, fixture.Exec("SET SESSION AUTHORIZATION role_smoke_owner", 0))
			c.Close()
			c, err = backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			roleScenarioIdentity(t, c, "role_smoke_owner", nil, backend.PG)
			if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatal(err)
			}
			c.Close()
			c, err = backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			roleScenarioIdentity(t, c, bootstrap, nil, backend.PG)
			run(c, fixture.Exec("SET application_name TO retained", 0))
			dependency := fixture.RoleReferenceError("DROP ROLE role_smoke_owner", "2BP01", `role "role_smoke_owner" cannot be dropped because some objects depend on it`, "owner of table secret", false, "execute")
			run(c, dependency)
			var controls *sql.DB
			if !backend.PG {
				connector, err := p.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(dsn, "role_smoke_reader")
				if err != nil {
					t.Fatal(err)
				}
				controls = sql.OpenDB(connector)
				defer controls.Close()
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, controls, &dump); err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(dump.String(), "SET SESSION AUTHORIZATION 'role_smoke_owner';\n") {
					t.Fatal("owner dump", dump.String())
				}
				if err := stuntgres.Reset(p.Ctx, controls); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, controls, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
			}
			run(c, fixture.Prepared(dependency))
			run(c, fixture.Exec("SET SESSION AUTHORIZATION role_smoke_owner", 0))
			run(c, check)
			run(c, fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"retained"}}))
			if !backend.PG {
				want := fixture.ControlsReferenceDiagnostic("22023", `role "absent" does not exist`)
				fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, controls, strings.NewReader("SET SESSION AUTHORIZATION 'absent'; CREATE TABLE bad(id INT);")), &want)
				run(c, check)
			}
			run(c, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			if !backend.PG {
				if err := stuntgres.Restore(p.Ctx, controls, strings.NewReader("CREATE TABLE legacy(id INT); INSERT INTO legacy VALUES(8);")); err != nil {
					t.Fatal(err)
				}
			} else {
				run(c, fixture.Exec("DROP TABLE secret", 0))
				run(c, fixture.Exec("CREATE TABLE legacy(id INT)", 0))
				run(c, fixture.Exec("INSERT INTO legacy VALUES(8)", 1))
			}
			run(c, fixture.Select("SELECT id FROM legacy", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}))
			if !backend.PG {
				if err := stuntgres.Reset(p.Ctx, controls); err != nil {
					t.Fatal(err)
				}
			} else {
				run(c, fixture.Exec("DROP TABLE legacy", 0))
			}
			run(c, fixture.Prepared(fixture.Exec("DROP ROLE role_smoke_owner", 0)))
			run(c, fixture.RoleReferenceError("DROP ROLE role_smoke_owner", "42704", `role "role_smoke_owner" does not exist`, "", false, "execute"))
			if !backend.PG {
				d := p.NativeConnector.Driver().(*stuntgres.Driver)
				for _, isolated := range []struct {
					driver *stuntgres.Driver
					dsn    string
				}{
					{stuntgres.NewDriver(), dsn},
					{d, dsn + " "},
				} {
					connector, err := isolated.driver.OpenConnector(isolated.dsn)
					if err != nil {
						t.Fatal(err)
					}
					pool := sql.OpenDB(connector)
					want := fixture.ControlsReferenceDiagnostic("22023", `role "role_smoke_reader" does not exist`)
					_, err = pool.ExecContext(p.Ctx, "SET SESSION AUTHORIZATION role_smoke_reader")
					fixture.AssertDiagnostic(t, err, &want)
					if err := pool.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}
