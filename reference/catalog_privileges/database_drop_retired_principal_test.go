package reference_test

import (
	"context"
	"database/sql/driver"
	"net/url"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceDatabaseDropRetiredPrincipal(t *testing.T) {
	for _, mode := range []string{"direct", "fresh", "held"} {
		for _, authority := range []string{"superuser", "pg_signal_backend"} {
			t.Run(mode+"/"+authority, func(t *testing.T) {
				const target, peer, login = "dd_retired_target", "dd_retired_peer", "dd_retired_login"
				fixture.RoleReferenceCleanup(t, []string{login})
				names := []string{target, peer}
				fixture.DatabaseReferenceCleanup(t, &names)
				p := fixture.NewScenario(t)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						var notices []fixture.Diagnostic
						_, admin, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
						backend.Recorder = record
						c := &databaseCreateCase{t, p, backend, admin, &names}
						query := "DROP DATABASE " + target + " WITH (FORCE)"
						route := databaseDropPrepare(c, mode, query)
						c.create(target, "", false)
						c.create(peer, "", false)
						peerDB, peerRaw, peerRecord := c.pool(peer, "")
						fixture.RunStep(t, p.Ctx, peerDB, peerRecord, fixture.Exec("CREATE TABLE retired_peer_value(v integer)", 0))
						fixture.RunStep(t, p.Ctx, peerDB, peerRecord, fixture.Exec("INSERT INTO retired_peer_value VALUES(22)", 1))
						c.run(fixture.Exec("CREATE ROLE "+login+" LOGIN PASSWORD 'role-core-fixture'", 0))
						var oldOID, replacementOID int64
						if err := admin.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname=$1", login).Scan(&oldOID); err != nil {
							t.Fatal(err)
						}
						// Connect directly, not through a pool: no borrower retry can replace
						// the authenticated physical owner after DROP ROLE.
						var native driver.Conn
						var wire *pgconn.PgConn
						if backend.PG {
							u, err := url.Parse(fixture.DSN)
							if err != nil {
								t.Fatal(err)
							}
							u.Path, u.RawPath = "/"+target, ""
							u.User = url.UserPassword(login, "role-core-fixture")
							wire, err = pgconn.Connect(p.Ctx, u.String())
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() {
								ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
								defer cancel()
								if err := wire.Close(ctx); err != nil {
									t.Error("retired physical owner close", err)
								}
							})
						} else {
							connector, err := p.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(p.NativeDSN, login)
							if err != nil {
								t.Fatal(err)
							}
							native, err = connector.(*stuntgres.Connector).WithDatabase(target).Connect(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() {
								if err := native.Close(); err != nil {
									t.Error("retired physical owner close", err)
								}
							})
						}
						fixture.DatabaseReferenceOwners(t, p, admin, record, target, 1)
						c.run(fixture.Exec("DROP ROLE "+login, 0))
						fixture.DatabaseReferenceOwners(t, p, admin, record, target, 1)
						c.run(fixture.Exec("CREATE ROLE "+login+" LOGIN PASSWORD 'role-core-fixture'", 0))
						if err := admin.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_roles WHERE rolname=$1", login).Scan(&replacementOID); err != nil || oldOID == replacementOID {
							t.Fatal("replacement reused retired physical identity", oldOID, replacementOID, err)
						}
						// Database ownership does not confer authority over the retired login.
						c.run(fixture.Exec("ALTER DATABASE "+target+" OWNER TO "+login, 0))
						c.run(fixture.Exec("SET ROLE "+login, 0))
						denied := databaseCreateDiagnostic("42501", "permission denied to terminate process")
						denied.Detail = `Only roles with privileges of the role whose process is being terminated or with privileges of the "pg_signal_backend" role may terminate this process.`
						route.run(c, fixture.ErrorDiagnostic(query, denied))
						c.run(fixture.Exec("RESET ROLE", 0))
						fixture.DatabaseReferenceOwners(t, p, admin, record, target, 1)
						if authority == "pg_signal_backend" {
							c.run(fixture.Exec("GRANT pg_signal_backend TO "+login, 0))
							c.run(fixture.Exec("SET ROLE "+login, 0))
						}
						route.run(c, fixture.Exec(query, 0))
						c.run(fixture.Exec("RESET ROLE", 0))
						var fatal error
						if wire != nil {
							fatal = wire.ExecParams(p.Ctx, "SELECT 1", nil, nil, nil, nil).Read().Err
						} else {
							_, fatal = native.(driver.ExecerContext).ExecContext(p.Ctx, "SELECT 1", nil)
						}
						want := databaseCreateDiagnostic("57P01", "terminating connection due to administrator command")
						want.Severity, want.SeverityUnlocalized = "FATAL", "FATAL"
						fixture.AssertDiagnostic(t, fatal, &want)
						c.absent(target)
						fixture.RunStep(t, p.Ctx, peerDB, peerRecord, fixture.Select("SELECT v FROM retired_peer_value", []string{"v"}, []string{"INT4"}, [][]any{{int64(22)}}))
						databaseDropClose(t, peerDB, peerRaw)
					})
				}
			})
		}
	}
}
