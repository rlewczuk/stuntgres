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
	"testing"
)

func roleScenarioLoginPool(t *testing.T, d *stuntgres.Driver, pg bool, dsn, name string) *sql.DB {
	t.Helper()
	var db *sql.DB
	if pg {
		u, err := url.Parse(referenceDSN)
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword(name, "role-core-fixture")
		var errOpen error
		db, errOpen = sql.Open("postgres", u.String())
		if errOpen != nil {
			t.Fatal(errOpen)
		}
	} else {
		c, err := d.OpenConnectorAs(dsn, name)
		if err != nil {
			t.Fatal(err)
		}
		db = sql.OpenDB(c)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}

func TestReferenceRoleTrustedConnectorIsolationAndReplacement(t *testing.T) {
	names := []string{"trusted_a", "trusted_b", "trusted_nolog"}
	roleReferenceCleanup(t, names)
	p := newReferenceScenario(t)
	dsn := " exact / roles ? user=no&password=opaque "
	for _, s := range []referenceStep{referenceExec("CREATE USER trusted_a", 0), referenceExec("CREATE USER trusted_b", 0), referenceExec("CREATE ROLE trusted_nolog", 0)} {
		p.step(t, s)
	}
	if p.postgres != nil {
		for _, name := range names[:2] {
			if _, err := referenceAdmin.ExecContext(p.ctx, "ALTER ROLE "+name+" PASSWORD 'role-core-fixture'"); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			d := p.nativeConnector.Driver().(*stuntgres.Driver)
			if !backend.pg {
				d = stuntgres.NewDriver()
				admin := roleScenarioLoginPool(t, d, false, dsn, "postgres")
				backend.db = admin
				for _, q := range []string{"CREATE USER trusted_a", "CREATE USER trusted_b", "CREATE ROLE trusted_nolog"} {
					result, err := admin.ExecContext(p.ctx, q)
					deleteReferenceCount(t, result, err, 0)
				}
			}
			aPool := roleScenarioLoginPool(t, d, backend.pg, dsn, names[0])
			aPool.SetMaxOpenConns(1)
			bPool := roleScenarioLoginPool(t, d, backend.pg, dsn, names[1])
			a, err := aPool.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { a.Close() }()
			b, err := bPool.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			identity := func(c *sql.Conn, name string) {
				t.Helper()
				rows, err := c.QueryContext(p.ctx, "SELECT session_user,current_user,session_user")
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
					if backend.pg {
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
			held, err := a.PrepareContext(p.ctx, "SET SESSION AUTHORIZATION trusted_b")
			if err != nil {
				t.Fatal(err)
			}
			_, err = held.ExecContext(p.ctx)
			held.Close()
			want := controlsReferenceDiagnostic("42501", `permission denied to set session authorization "trusted_b"`)
			referenceAssertDiagnostic(t, err, &want)
			identity(a, names[0])
			identity(b, names[1])
			roleScenarioCommand(t, a, "SET application_name TO retained", 0, false)
			a.Close()
			a, err = aPool.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			var setting string
			if err := a.QueryRowContext(p.ctx, "SHOW application_name").Scan(&setting); err != nil || setting != "retained" {
				t.Fatal(setting, err)
			}
			identity(a, names[0])
			if err := a.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				t.Fatal(err)
			}
			a.Close()
			a, err = aPool.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			identity(a, names[0])
			if err := a.QueryRowContext(p.ctx, "SHOW application_name").Scan(&setting); err != nil || setting != "" {
				t.Fatal(setting, err)
			}
			result, err := backend.db.ExecContext(p.ctx, "DROP ROLE trusted_a")
			deleteReferenceCount(t, result, err, 0)
			_ = a.Raw(func(any) error { return driver.ErrBadConn })
			a.Close()
			if err := aPool.PingContext(p.ctx); err == nil {
				t.Fatal("deleted identity cached")
			}
			result, err = backend.db.ExecContext(p.ctx, "CREATE USER trusted_a")
			deleteReferenceCount(t, result, err, 0)
			if backend.pg {
				if _, err := referenceAdmin.ExecContext(p.ctx, "ALTER ROLE trusted_a PASSWORD 'role-core-fixture'"); err != nil {
					t.Fatal(err)
				}
			}
			if err := aPool.PingContext(p.ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReferenceRoleTrustedConnectorAdmissionAndOpaqueIsolation(t *testing.T) {
	names := []string{"trusted_a", "trusted_b", "trusted_nolog"}
	roleReferenceCleanup(t, names)
	p := newReferenceScenario(t)
	dsn := " exact / roles ? user=no&password=opaque "
	for _, s := range []referenceStep{
		referenceExec("CREATE USER trusted_a", 0),
		referenceExec("CREATE USER trusted_b", 0),
		referenceExec("CREATE ROLE trusted_nolog", 0),
	} {
		p.step(t, s)
	}
	if p.postgres != nil {
		for _, name := range names[:2] {
			if _, err := referenceAdmin.ExecContext(p.ctx, "ALTER ROLE "+name+" PASSWORD 'role-core-fixture'"); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			d := p.nativeConnector.Driver().(*stuntgres.Driver)
			if !backend.pg {
				d = stuntgres.NewDriver()
				admin := roleScenarioLoginPool(t, d, false, dsn, "postgres")
				for _, q := range []string{"CREATE USER trusted_a", "CREATE USER trusted_b", "CREATE ROLE trusted_nolog"} {
					if _, err := admin.ExecContext(p.ctx, q); err != nil {
						t.Fatal(q, err)
					}
				}
			}
			// Valid principals are admitted independently on the same database.
			for _, name := range names[:2] {
				pool := roleScenarioLoginPool(t, d, backend.pg, dsn, name)
				c, err := pool.Conn(p.ctx)
				if err != nil {
					t.Fatal(err)
				}
				roleScenarioIdentity(t, c, name, nil, backend.pg)
				if err := c.Close(); err != nil {
					t.Fatal(err)
				}
			}
			for _, name := range []string{"missing_trusted", "trusted_nolog", "TRUSTED_A"} {
				pool := roleScenarioLoginPool(t, d, backend.pg, dsn, name)
				err := pool.PingContext(p.ctx)
				if err == nil {
					t.Fatal("invalid authenticator admitted", name)
				}
				if !backend.pg {
					var state interface{ SQLState() string }
					if errors.As(err, &state) {
						t.Fatal("host admission SQLSTATE", err)
					}
				}
			}
			if !backend.pg {
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
					raw, err := connector.Connect(p.ctx)
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
					if err := pool.PingContext(p.ctx); err == nil {
						t.Fatal("Driver/DSN identity leaked through original pool admission")
					}
				}
			}
		})
	}
}

func TestReferenceRoleConnectorNoticeConfigurationPreservesAuthentication(t *testing.T) {
	roleReferenceCleanup(t, []string{"notice_login"})
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE USER notice_login", 0))
	if p.postgres != nil {
		if _, err := referenceAdmin.ExecContext(p.ctx, "ALTER ROLE notice_login PASSWORD 'role-core-fixture'; GRANT USAGE,CREATE ON SCHEMA public TO notice_login"); err != nil {
			t.Fatal(err)
		}
	}
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			var base driver.Connector
			if backend.pg {
				u, err := url.Parse(referenceDSN)
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
				c, err := p.nativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs("reference/"+t.Name()[:strings.LastIndex(t.Name(), "/")], "notice_login")
				if err != nil {
					t.Fatal(err)
				}
				base = c
			}
			var notices []referenceDiagnostic
			var configured driver.Connector
			if backend.pg {
				configured = pq.ConnectorWithNoticeHandler(base.(*pq.Connector), func(e *pq.Error) { notices = append(notices, referenceExtractDiagnostic(t, e)) })
			} else {
				configured = base.(*stuntgres.Connector).WithNoticeHandler(func(e stuntgres.Error) { notices = append(notices, referenceExtractDiagnostic(t, &e)) })
			}
			db := sql.OpenDB(configured)
			defer db.Close()
			c, err := db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			var identity string
			if err := c.QueryRowContext(p.ctx, "SELECT current_user").Scan(&identity); err != nil || identity != "notice_login" {
				t.Fatal(identity, err)
			}
			roleScenarioIdentity(t, c, "notice_login", nil, backend.pg)
			roleScenarioCommand(t, c, "CREATE TABLE notice_login_rows(id INT)", 0, false)
			roleScenarioCommand(t, c, "CREATE TABLE IF NOT EXISTS notice_login_rows(id INT,id TEXT)", 0, true)
			want := []referenceDiagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_login_rows" already exists, skipping`}}
			if !reflect.DeepEqual(notices, want) {
				t.Fatal(notices, want)
			}
			plain := sql.OpenDB(base)
			defer plain.Close()
			peer, err := plain.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			roleScenarioIdentity(t, peer, "notice_login", nil, backend.pg)
			roleScenarioCommand(t, peer, "CREATE TABLE IF NOT EXISTS notice_login_rows(id INT)", 0, false)
			if !reflect.DeepEqual(notices, want) {
				t.Fatal("handler leaked", notices)
			}
			if !backend.pg {
				quiet := sql.OpenDB(configured.(*stuntgres.Connector).WithNoticeHandler(nil))
				defer quiet.Close()
				other, err := quiet.Conn(p.ctx)
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
