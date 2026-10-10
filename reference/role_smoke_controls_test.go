package reference_test

import (
	"bytes"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"stuntgres"
)

// The ordinary creation, authentication, ACL and connector assertions from
// TestRoleSmoke live in TestReferenceRoleCreation/Authorization/AuthenticatedSessions
// and TestReferenceRoleTrustedConnector*. This preserves its remaining controls
// and changed-session pool reuse witnesses without a native-only SQL copy.
func TestReferenceRoleMigratedSmokeControls(t *testing.T) {
	roleReferenceCleanup(t, []string{"role_smoke_owner", "role_smoke_reader", "role_smoke_unsupported"})
	p := newReferenceScenario(t)
	dsn := "reference/" + t.Name()
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			admin, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer admin.Close()
			defer admin.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
			run := func(c referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
			if backend.pg {
				run(admin, referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC", 0))
			}
			run(admin, referenceExec("CREATE ROLE role_smoke_unsupported", 0))
			if backend.pg {
				run(admin, referenceExec("ALTER ROLE role_smoke_unsupported SET application_name TO excluded", 0))
			} else {
				want := controlsReferenceDiagnostic("0A000", "statement is unsupported")
				want.Position = "1"
				run(admin, referenceErrorDiagnostic("ALTER ROLE role_smoke_unsupported SET application_name TO excluded", want))
			}
			run(admin, referenceExec("DROP ROLE role_smoke_unsupported", 0))
			for _, q := range []string{"CREATE USER role_smoke_owner", "CREATE USER role_smoke_reader", "SET SESSION AUTHORIZATION role_smoke_owner", "CREATE TABLE secret(id INT)"} {
				run(admin, referenceExec(q, 0))
			}
			run(admin, referencePrepared(referenceExec("INSERT INTO secret VALUES(7)", 1)))
			check := referenceSelect("SELECT id FROM secret ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}})
			run(admin, check)
			run(admin, referenceExec("RESET SESSION AUTHORIZATION", 0))
			bootstrap := "postgres"
			if backend.pg {
				bootstrap = "stuntgres"
			}
			// A borrowed connection preserves SQL session identity; actual physical
			// replacement returns to its authenticated bootstrap identity.
			backend.db.SetMaxOpenConns(2)
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			run(c, referenceExec("SET SESSION AUTHORIZATION role_smoke_owner", 0))
			c.Close()
			c, err = backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			roleScenarioIdentity(t, c, "role_smoke_owner", nil, backend.pg)
			if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatal(err)
			}
			c.Close()
			c, err = backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			roleScenarioIdentity(t, c, bootstrap, nil, backend.pg)
			run(c, referenceExec("SET application_name TO retained", 0))
			dependency := roleReferenceError("DROP ROLE role_smoke_owner", "2BP01", `role "role_smoke_owner" cannot be dropped because some objects depend on it`, "owner of table secret", false, "execute")
			run(c, dependency)
			var controls *sql.DB
			if !backend.pg {
				connector, err := p.nativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(dsn, "role_smoke_reader")
				if err != nil {
					t.Fatal(err)
				}
				controls = sql.OpenDB(connector)
				defer controls.Close()
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.ctx, controls, &dump); err != nil {
					t.Fatal(err)
				}
				if !strings.HasPrefix(dump.String(), "SET SESSION AUTHORIZATION 'role_smoke_owner';\n") {
					t.Fatal("owner dump", dump.String())
				}
				if err := stuntgres.Reset(p.ctx, controls); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.ctx, controls, strings.NewReader(dump.String())); err != nil {
					t.Fatal(err)
				}
			}
			run(c, referencePrepared(dependency))
			run(c, referenceExec("SET SESSION AUTHORIZATION role_smoke_owner", 0))
			run(c, check)
			run(c, referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"retained"}}))
			if !backend.pg {
				want := controlsReferenceDiagnostic("22023", `role "absent" does not exist`)
				referenceAssertDiagnostic(t, stuntgres.Restore(p.ctx, controls, strings.NewReader("SET SESSION AUTHORIZATION 'absent'; CREATE TABLE bad(id INT);")), &want)
				run(c, check)
			}
			run(c, referenceExec("RESET SESSION AUTHORIZATION", 0))
			if !backend.pg {
				if err := stuntgres.Restore(p.ctx, controls, strings.NewReader("CREATE TABLE legacy(id INT); INSERT INTO legacy VALUES(8);")); err != nil {
					t.Fatal(err)
				}
			} else {
				run(c, referenceExec("DROP TABLE secret", 0))
				run(c, referenceExec("CREATE TABLE legacy(id INT)", 0))
				run(c, referenceExec("INSERT INTO legacy VALUES(8)", 1))
			}
			run(c, referenceSelect("SELECT id FROM legacy", []string{"id"}, []string{"INT4"}, [][]any{{int64(8)}}))
			if !backend.pg {
				if err := stuntgres.Reset(p.ctx, controls); err != nil {
					t.Fatal(err)
				}
			} else {
				run(c, referenceExec("DROP TABLE legacy", 0))
			}
			run(c, referencePrepared(referenceExec("DROP ROLE role_smoke_owner", 0)))
			run(c, roleReferenceError("DROP ROLE role_smoke_owner", "42704", `role "role_smoke_owner" does not exist`, "", false, "execute"))
			if !backend.pg {
				d := p.nativeConnector.Driver().(*stuntgres.Driver)
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
					want := controlsReferenceDiagnostic("22023", `role "role_smoke_reader" does not exist`)
					_, err = pool.ExecContext(p.ctx, "SET SESSION AUTHORIZATION role_smoke_reader")
					referenceAssertDiagnostic(t, err, &want)
					if err := pool.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
		})
	}
}
