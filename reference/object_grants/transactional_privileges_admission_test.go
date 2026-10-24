package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/ngrok/sqlmw"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func transactionalPrivilegesLanguageDeclaration(t *testing.T) {
	for _, goTx := range []bool{false, true} {
		for _, held := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("language-declaration/goTx=%v/held=%v/commit=%v", goTx, held, commit), func(t *testing.T) {
					objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), _ bool) {
						defer func() {
							ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
							defer cancel()
							c.ExecContext(ctx, "ROLLBACK")
							c.ExecContext(ctx, "RESET ROLE")
							c.ExecContext(ctx, "GRANT USAGE ON LANGUAGE sql TO PUBLIC")
							c.ExecContext(ctx, "REVOKE ALL ON LANGUAGE sql FROM og_object_reader CASCADE")
						}()
						run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO og_object_reader", 0))
						run(fixture.Exec("REVOKE USAGE ON LANGUAGE sql FROM PUBLIC", 0))
						q := "GRANT USAGE ON LANGUAGE sql TO og_object_reader"
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
						var e fixture.Executor = c
						finish := func() { run(fixture.Exec(map[bool]string{true: "COMMIT", false: "ROLLBACK"}[commit], 0)) }
						if goTx {
							tx, err := c.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							e = tx
							finish = func() {
								var err error
								if commit {
									err = tx.Commit()
								} else {
									err = tx.Rollback()
								}
								if err != nil {
									t.Fatal(err)
								}
							}
						} else {
							run(fixture.Exec("BEGIN", 0))
						}
						if held {
							e = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}
						}
						fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0))
						finish()
						run(fixture.Exec("SET ROLE og_object_reader", 0))
						create := "CREATE FUNCTION tx_language_function() RETURNS INT LANGUAGE SQL RETURN 7"
						if commit {
							run(fixture.Exec(create, 0))
							run(fixture.Select("SELECT tx_language_function() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
						} else {
							run(fixture.ImplicitError(create, fixture.Diagnostic{Code: "42501", Message: "permission denied for language sql"}))
						}
						run(fixture.Exec("RESET ROLE", 0))
						if commit {
							run(fixture.Exec("BEGIN", 0))
							run(fixture.Exec("REVOKE USAGE ON LANGUAGE sql FROM og_object_reader", 0))
							run(fixture.Exec("COMMIT", 0))
							run(fixture.Exec("SET ROLE og_object_reader", 0))
							// EXECUTE on an existing declaration is independent of LANGUAGE USAGE.
							run(fixture.Select("SELECT tx_language_function() AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
							run(fixture.ImplicitError("CREATE FUNCTION tx_language_later() RETURNS INT LANGUAGE SQL RETURN 9", fixture.Diagnostic{Code: "42501", Message: "permission denied for language sql"}))
							run(fixture.Exec("RESET ROLE", 0))
						}
					})
				})
			}
		}
	}
}

func transactionalPrivilegesConnect(t *testing.T) {
	for _, control := range []string{"autocommit", "sql", "go"} {
		for _, held := range []bool{false, true} {
			t.Run(fmt.Sprintf("connect/%s/held=%v", control, held), func(t *testing.T) {
				objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), _ bool) {
					_, database := fixture.RoleGrantEnvironment(t, p, c)
					quoted := pq.QuoteIdentifier(database)
					public := objectGrantDatabasePublic(t, p.Ctx, c)
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer cancel()
						c.ExecContext(ctx, "ROLLBACK")
						objectGrantDatabaseCleanup(t, ctx, b.DB, database, []string{"og_object_reader"}, public)
					}()
					run(fixture.Exec("ALTER ROLE og_object_reader LOGIN PASSWORD 'tx-role-fixture'", 0))
					connector := func() driver.Connector {
						if b.PG {
							u, err := url.Parse(fixture.DSN)
							if err != nil {
								t.Fatal(err)
							}
							u.User = url.UserPassword("og_object_reader", "tx-role-fixture")
							x, err := pq.NewConnector(u.String())
							if err != nil {
								t.Fatal(err)
							}
							return x
						}
						x, err := p.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(p.NativeDSN, "og_object_reader")
						if err != nil {
							t.Fatal(err)
						}
						return x
					}
					fresh := func(allowed bool) {
						pool := sql.OpenDB(connector())
						defer pool.Close()
						err := pool.PingContext(p.Ctx)
						if allowed {
							if err != nil {
								t.Fatal("fresh CONNECT", err)
							}
							return
						}
						want := fixture.ControlsReferenceDiagnostic("42501", `permission denied for database "`+database+`"`)
						want.Detail = "User does not have CONNECT privilege."
						if b.PG {
							want.Severity, want.SeverityUnlocalized = "FATAL", "FATAL"
						}
						fixture.AssertDiagnostic(t, err, &want)
					}
					// Keep a genuinely admitted physical session across committed revocation.
					wrapped := sqlmw.Driver(fixture.NoticeReferenceDriver{Connector: connector()}, fixture.Interceptor{})
					recorded, err := wrapped.(driver.DriverContext).OpenConnector("")
					if err != nil {
						t.Fatal(err)
					}
					pool := sql.OpenDB(recorded)
					defer pool.Close()
					admitted, err := pool.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer admitted.Close()
					record := &fixture.Recorder{RawBackend: b.Recorder.RawBackend}
					healthy := func() {
						fixture.RunStep(t, p.Ctx, admitted, record, fixture.Select("SELECT 1 AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}}))
					}
					run(fixture.Exec("REVOKE CONNECT ON DATABASE "+quoted+" FROM PUBLIC", 0))
					fresh(false)
					healthy()
					grant := "GRANT CONNECT ON DATABASE " + quoted + " TO og_object_reader"
					revoke := "REVOKE CONNECT ON DATABASE " + quoted + " FROM og_object_reader"
					gs, err := c.PrepareContext(p.Ctx, grant)
					if err != nil {
						t.Fatal(err)
					}
					defer gs.Close()
					rs, err := c.PrepareContext(p.Ctx, revoke)
					if err != nil {
						t.Fatal(err)
					}
					defer rs.Close()
					change := func(q string, stmt *sql.Stmt, commit bool) {
						var e fixture.Executor = c
						finish := func() {}
						if control != "autocommit" {
							if control == "go" {
								tx, err := c.BeginTx(p.Ctx, nil)
								if err != nil {
									t.Fatal(err)
								}
								defer tx.Rollback()
								e = tx
								finish = func() {
									var err error
									if commit {
										err = tx.Commit()
									} else {
										err = tx.Rollback()
									}
									if err != nil {
										t.Fatal(err)
									}
								}
							} else {
								run(fixture.Exec("BEGIN", 0))
								finish = func() { run(fixture.Exec(map[bool]string{true: "COMMIT", false: "ROLLBACK"}[commit], 0)) }
							}
						}
						if held {
							e = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: q}
						}
						fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(q, 0))
						if control != "autocommit" {
							fresh(strings.HasPrefix(q, "REVOKE"))
							healthy()
						}
						finish()
					}
					if control != "autocommit" {
						change(grant, gs, false)
						fresh(false)
					}
					change(grant, gs, true)
					fresh(true)
					healthy()
					if control != "autocommit" {
						change(revoke, rs, false)
						fresh(true)
					}
					change(revoke, rs, true)
					fresh(false)
					healthy()
				})
			})
		}
	}
}
