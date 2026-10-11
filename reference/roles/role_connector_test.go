package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"github.com/lib/pq"
	"net/url"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleTrustedConnectorIsolationAndReplacement(t *testing.T) {
	names := []string{"trusted_a", "trusted_b", "trusted_nolog"}
	fixture.RoleReferenceCleanup(t, names)
	p := fixture.NewScenario(t)
	dsn := " exact / roles ? user=no&password=opaque "
	for _, s := range []fixture.Step{fixture.Exec("CREATE USER trusted_a", 0), fixture.Exec("CREATE USER trusted_b", 0), fixture.Exec("CREATE ROLE trusted_nolog", 0)} {
		p.Step(t, s)
	}
	if p.Postgres != nil {
		for _, name := range names[:2] {
			if _, err := fixture.Admin.ExecContext(p.Ctx, "ALTER ROLE "+name+" PASSWORD 'role-core-fixture'"); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			d := p.NativeConnector.Driver().(*stuntgres.Driver)
			if !backend.PG {
				d = stuntgres.NewDriver()
				admin := fixture.RoleScenarioLoginPool(t, d, false, dsn, "postgres")
				backend.DB = admin
				for _, q := range []string{"CREATE USER trusted_a", "CREATE USER trusted_b", "CREATE ROLE trusted_nolog"} {
					result, err := admin.ExecContext(p.Ctx, q)
					fixture.DeleteReferenceCount(t, result, err, 0)
				}
			}
			aPool := fixture.RoleScenarioLoginPool(t, d, backend.PG, dsn, names[0])
			aPool.SetMaxOpenConns(1)
			bPool := fixture.RoleScenarioLoginPool(t, d, backend.PG, dsn, names[1])
			a, err := aPool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { a.Close() }()
			b, err := bPool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			identity := func(c *sql.Conn, name string) {
				t.Helper()
				rows, err := c.QueryContext(p.Ctx, "SELECT session_user,current_user,session_user")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				cols, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(cols, []string{"session_user", "current_user", "session_user"}) {
					t.Fatal(cols, err)
				}
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for _, typ := range types {
					scan := reflect.TypeFor[string]()
					if backend.PG {
						scan = reflect.TypeFor[any]()
					}
					if typ.DatabaseTypeName() != "NAME" || typ.ScanType() != scan {
						t.Fatal(typ)
					}
				}
				if !rows.Next() {
					t.Fatal(rows.Err())
				}
				var x, y, z string
				if err := rows.Scan(&x, &y, &z); err != nil || x != name || y != name || z != name {
					t.Fatal(x, y, z, err)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal(rows.Err())
				}
			}
			identity(a, names[0])
			identity(b, names[1])
			held, err := a.PrepareContext(p.Ctx, "SET SESSION AUTHORIZATION trusted_b")
			if err != nil {
				t.Fatal(err)
			}
			_, err = held.ExecContext(p.Ctx)
			held.Close()
			want := fixture.ControlsReferenceDiagnostic("42501", `permission denied to set session authorization "trusted_b"`)
			fixture.AssertDiagnostic(t, err, &want)
			identity(a, names[0])
			identity(b, names[1])
			roleScenarioCommand(t, a, "SET application_name TO retained", 0, false)
			a.Close()
			a, err = aPool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			var setting string
			if err := a.QueryRowContext(p.Ctx, "SHOW application_name").Scan(&setting); err != nil || setting != "retained" {
				t.Fatal(setting, err)
			}
			identity(a, names[0])
			if err := a.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatal(err)
			}
			a.Close()
			a, err = aPool.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			identity(a, names[0])
			if err := a.QueryRowContext(p.Ctx, "SHOW application_name").Scan(&setting); err != nil || setting != "" {
				t.Fatal(setting, err)
			}
			result, err := backend.DB.ExecContext(p.Ctx, "DROP ROLE trusted_a")
			fixture.DeleteReferenceCount(t, result, err, 0)
			_ = a.Raw(func(any) error { return driver.ErrBadConn })
			a.Close()
			if err := aPool.PingContext(p.Ctx); err == nil {
				t.Fatal("deleted identity cached")
			}
			result, err = backend.DB.ExecContext(p.Ctx, "CREATE USER trusted_a")
			fixture.DeleteReferenceCount(t, result, err, 0)
			if backend.PG {
				if _, err := fixture.Admin.ExecContext(p.Ctx, "ALTER ROLE trusted_a PASSWORD 'role-core-fixture'"); err != nil {
					t.Fatal(err)
				}
			}
			if err := aPool.PingContext(p.Ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReferenceRoleTrustedConnectorAdmissionAndOpaqueIsolation(t *testing.T) {
	names := []string{"trusted_a", "trusted_b", "trusted_nolog"}
	fixture.RoleReferenceCleanup(t, names)
	p := fixture.NewScenario(t)
	dsn := " exact / roles ? user=no&password=opaque "
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE USER trusted_a", 0),
		fixture.Exec("CREATE USER trusted_b", 0),
		fixture.Exec("CREATE ROLE trusted_nolog", 0),
	} {
		p.Step(t, s)
	}
	if p.Postgres != nil {
		for _, name := range names[:2] {
			if _, err := fixture.Admin.ExecContext(p.Ctx, "ALTER ROLE "+name+" PASSWORD 'role-core-fixture'"); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			d := p.NativeConnector.Driver().(*stuntgres.Driver)
			if !backend.PG {
				d = stuntgres.NewDriver()
				admin := fixture.RoleScenarioLoginPool(t, d, false, dsn, "postgres")
				for _, q := range []string{"CREATE USER trusted_a", "CREATE USER trusted_b", "CREATE ROLE trusted_nolog"} {
					if _, err := admin.ExecContext(p.Ctx, q); err != nil {
						t.Fatal(q, err)
					}
				}
			}
			// Valid principals are admitted independently on the same database.
			for _, name := range names[:2] {
				pool := fixture.RoleScenarioLoginPool(t, d, backend.PG, dsn, name)
				c, err := pool.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				roleScenarioIdentity(t, c, name, nil, backend.PG)
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"missing_trusted", "trusted_nolog", "TRUSTED_A"} {
				pool := fixture.RoleScenarioLoginPool(t, d, backend.PG, dsn, name)
				err := pool.PingContext(p.Ctx)
				if err == nil {
					t.Fatal("invalid authenticator admitted", name)
				}
				if !backend.PG && name == "trusted_nolog" {
					want := fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "28000", Message: `role "trusted_nolog" is not permitted to log in`}
					fixture.AssertDiagnostic(t, err, &want)
				} else if !backend.PG {
					// Missing and miscased trusted names retain ordinary host errors.
					var state interface{ SQLState() string }
					if errors.As(err, &state) {
						t.Fatal("missing-name host admission SQLSTATE", err)
					}
				}
			}
			if !backend.PG {
				if _, err := d.OpenConnectorAs(dsn, ""); err == nil {
					t.Fatal("empty assertion")
				}
				for _, tc := range []struct {
					d   *stuntgres.Driver
					dsn string
				}{{stuntgres.NewDriver(), dsn}, {d, strings.TrimSpace(dsn)}} {
					connector, err := tc.d.OpenConnectorAs(tc.dsn, names[0])
					if err != nil {
						t.Fatal(err)
					}
					raw, err := connector.Connect(p.Ctx)
					if raw != nil {
						raw.Close()
					}
					if err == nil {
						t.Fatal("Driver/DSN identity leak")
					}
					pool := sql.OpenDB(connector)
					t.Cleanup(func() {
						if err := pool.Close(); err != nil {
							t.Error(err)
						}
					})
					if err := pool.PingContext(p.Ctx); err == nil {
						t.Fatal("Driver/DSN identity leaked through original pool admission")
					}
				}
			}
		})
	}
}

func TestReferenceRoleConnectorNoticeConfigurationPreservesAuthentication(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"notice_login"})
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE USER notice_login", 0))
	if p.Postgres != nil {
		if _, err := fixture.Admin.ExecContext(p.Ctx, "ALTER ROLE notice_login PASSWORD 'role-core-fixture'; GRANT USAGE,CREATE ON SCHEMA public TO notice_login"); err != nil {
			t.Fatal(err)
		}
	}
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var base driver.Connector
			if backend.PG {
				u, err := url.Parse(fixture.DSN)
				if err != nil {
					t.Fatal(err)
				}
				u.User = url.UserPassword("notice_login", "role-core-fixture")
				c, err := pq.NewConnector(u.String())
				if err != nil {
					t.Fatal(err)
				}
				base = c
			} else {
				c, err := p.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs("reference/"+t.Name()[:strings.LastIndex(t.Name(), "/")], "notice_login")
				if err != nil {
					t.Fatal(err)
				}
				base = c
			}
			var notices []fixture.Diagnostic
			var configured driver.Connector
			if backend.PG {
				configured = pq.ConnectorWithNoticeHandler(base.(*pq.Connector), func(e *pq.Error) { notices = append(notices, fixture.ExtractDiagnostic(t, e)) })
			} else {
				configured = base.(*stuntgres.Connector).WithNoticeHandler(func(e stuntgres.Error) { notices = append(notices, fixture.ExtractDiagnostic(t, &e)) })
			}
			db := sql.OpenDB(configured)
			defer db.Close()
			c, err := db.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			var identity string
			if err := c.QueryRowContext(p.Ctx, "SELECT current_user").Scan(&identity); err != nil || identity != "notice_login" {
				t.Fatal(identity, err)
			}
			roleScenarioIdentity(t, c, "notice_login", nil, backend.PG)
			roleScenarioCommand(t, c, "CREATE TABLE notice_login_rows(id INT)", 0, false)
			roleScenarioCommand(t, c, "CREATE TABLE IF NOT EXISTS notice_login_rows(id INT,id TEXT)", 0, true)
			want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_login_rows" already exists, skipping`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatal(notices, want)
			}
			plain := sql.OpenDB(base)
			defer plain.Close()
			peer, err := plain.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			roleScenarioIdentity(t, peer, "notice_login", nil, backend.PG)
			roleScenarioCommand(t, peer, "CREATE TABLE IF NOT EXISTS notice_login_rows(id INT)", 0, false)
			if !reflect.DeepEqual(notices, want) {
				t.Fatal("handler leaked", notices)
			}
			if !backend.PG {
				quiet := sql.OpenDB(configured.(*stuntgres.Connector).WithNoticeHandler(nil))
				defer quiet.Close()
				other, err := quiet.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer other.Close()
				roleScenarioIdentity(t, other, "notice_login", nil, false)
				roleScenarioCommand(t, other, "CREATE TABLE IF NOT EXISTS notice_login_rows(id INT)", 0, false)
				if !reflect.DeepEqual(notices, want) {
					t.Fatal("nil handler leaked", notices)
				}
			}
		})
	}
}
