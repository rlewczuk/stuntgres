package reference_test

import (
	"context"
	"database/sql/driver"
	"io"
	"net/url"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceSessionSignalRetiredPrincipal(t *testing.T) {
	for _, mode := range []string{"direct", "fresh", "held"} {
		for _, authority := range []string{"superuser", "pg_signal_backend"} {
			t.Run(mode+"/"+authority, func(t *testing.T) {
				const login = "ss_retired_login"
				fixture.RoleReferenceCleanup(t, []string{login})
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						admin, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer admin.Close()
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, s) }
						run(fixture.Exec("CREATE ROLE "+login+" LOGIN PASSWORD 'role-attributes-fixture'", 0))
						var native driver.Conn
						var wire *pgconn.PgConn
						var pid int
						if b.PG {
							u, err := url.Parse(fixture.DSN)
							if err != nil {
								t.Fatal(err)
							}
							u.User = url.UserPassword(login, "role-attributes-fixture")
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
							identity := wire.ExecParams(p.Ctx, "SELECT pg_backend_pid()", nil, nil, nil, nil).Read()
							if identity.Err != nil || len(identity.Rows) != 1 || len(identity.Rows[0]) != 1 {
								t.Fatal(identity)
							}
							pid, err = strconv.Atoi(string(identity.Rows[0][0]))
							if err != nil || pid <= 0 {
								t.Fatal(pid, err)
							}
						} else {
							connector, err := p.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(p.NativeDSN, login)
							if err != nil {
								t.Fatal(err)
							}
							native, err = connector.Connect(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() {
								if err := native.Close(); err != nil {
									t.Error("retired physical owner close", err)
								}
							})
							rows, err := native.(driver.QueryerContext).QueryContext(p.Ctx, "SELECT pg_backend_pid()", nil)
							if err != nil {
								t.Fatal(err)
							}
							values := make([]driver.Value, 1)
							if err := rows.Next(values); err != nil {
								rows.Close()
								t.Fatal(err)
							}
							id, ok := values[0].(int64)
							if !ok || id <= 0 {
								rows.Close()
								t.Fatal(values)
							}
							pid = int(id)
							if err := rows.Next(values); err != io.EOF {
								rows.Close()
								t.Fatal(err)
							}
							if err := rows.Close(); err != nil {
								t.Fatal(err)
							}
						}
						// Both retained calls precede retirement and membership changes.
						routes := make(map[string]fixture.Executor)
						for _, function := range []string{"pg_cancel_backend", "pg_terminate_backend"} {
							q := "SELECT " + function + "($1) AS v"
							routes[function] = admin
							if mode == "held" {
								stmt, err := admin.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								routes[function] = fixture.HeldStatement{Executor: admin, Statement: stmt, SQL: q}
							}
						}
						signal := func(function string, denied bool) {
							t.Helper()
							q := "SELECT " + function + "($1) AS v"
							s := fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{true}}, pid)
							if denied {
								message := "permission denied to cancel query"
								detail := `Only roles with privileges of the role whose query is being canceled or with privileges of the "pg_signal_backend" role may cancel this query.`
								if function == "pg_terminate_backend" {
									message = "permission denied to terminate process"
									detail = `Only roles with privileges of the role whose process is being terminated or with privileges of the "pg_signal_backend" role may terminate this process.`
								}
								s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: message, Detail: detail}, pid)
								s.SelectRows, s.ErrorStage = true, "execute"
							}
							if mode == "fresh" {
								s = fixture.Prepared(s)
							}
							fixture.RunStep(t, p.Ctx, routes[function], b.Recorder, s)
						}
						run(fixture.Exec("DROP ROLE "+login, 0))
						// A genuinely absent tuple is signalable without a replacement too.
						signal("pg_cancel_backend", false)
						run(fixture.Exec("CREATE ROLE "+login+" LOGIN PASSWORD 'role-attributes-fixture'", 0))
						run(fixture.Exec("SET ROLE "+login, 0))
						signal("pg_cancel_backend", true)
						signal("pg_terminate_backend", true)
						run(fixture.Exec("RESET ROLE", 0))
						if authority == "pg_signal_backend" {
							run(fixture.Exec("GRANT pg_signal_backend TO "+login, 0))
							run(fixture.Exec("SET ROLE "+login, 0))
						}
						signal("pg_cancel_backend", false)
						signal("pg_terminate_backend", false)
						run(fixture.Exec("RESET ROLE", 0))
						var fatal error
						if wire != nil {
							fatal = wire.ExecParams(p.Ctx, "SELECT 1", nil, nil, nil, nil).Read().Err
						} else {
							_, fatal = native.(driver.ExecerContext).ExecContext(p.Ctx, "SELECT 1", nil)
						}
						fixture.AssertDiagnostic(t, fatal, &fixture.Diagnostic{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "57P01", Message: "terminating connection due to administrator command"})
						// The same administrator physical owner remains healthy.
						run(fixture.Select("SELECT 42 AS alive", []string{"alive"}, []string{"INT4"}, [][]any{{int64(42)}}))
					})
				}
			})
		}
	}
}
