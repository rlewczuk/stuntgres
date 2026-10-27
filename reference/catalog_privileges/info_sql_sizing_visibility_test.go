package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func szPrivateSeed(c *szCase) {
	c.t.Helper()
	c.exec("CREATE ROLE sz_owner", "CREATE ROLE sz_reader", "GRANT USAGE ON SCHEMA public TO sz_reader", "GRANT CREATE ON SCHEMA public TO sz_owner", "SET SESSION AUTHORIZATION sz_owner", "CREATE TABLE public.sz_private(id integer)", "RESET SESSION AUTHORIZATION")
}

func (c *szCase) privateDenied() {
	c.t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("SELECT id FROM public.sz_private", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table sz_private"))
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		c.run(c.conn, s)
	}
}

func TestReferenceInfoSQLSizingVisibility(t *testing.T) {
	szScenario(t, true, func(c *szCase) {
		szPrivateSeed(c)
		held := c.hold(szQuery)
		c.check(c.conn, szQuery, c.want)
		for _, actor := range []string{"sz_owner", "sz_reader"} {
			c.exec("SET SESSION AUTHORIZATION "+actor, "SET search_path=pg_catalog,public")
			c.check(held, szQuery, c.want)
			c.run(c.conn, c.projection(szQuery, c.want))
			if actor == "sz_reader" {
				c.privateDenied()
				c.check(held, szQuery, c.want)
			}
			c.exec("RESET SESSION AUTHORIZATION")
		}
		c.exec("CREATE TABLE public.sz_unrelated(id integer)", "CREATE DOMAIN public.sz_unrelated_domain AS varchar", "GRANT sz_owner TO sz_reader", "SET SESSION AUTHORIZATION sz_reader", "SET ROLE sz_owner", "SET search_path=public")
		c.check(held, szQuery, c.want)
		c.run(c.conn, c.projection(szQuery, c.want))
		c.exec("RESET ROLE", "RESET SESSION AUTHORIZATION", "REVOKE sz_owner FROM sz_reader", "DROP DOMAIN public.sz_unrelated_domain", "DROP TABLE public.sz_unrelated", "SET SESSION AUTHORIZATION sz_reader")
		c.privateDenied()
		c.check(held, szQuery, c.want)
		c.run(c.conn, c.projection(szQuery, c.want))
	})
}

func TestReferenceInfoSQLSizingDiagnostics(t *testing.T) {
	t.Run("missing column and ordinary cancellation", func(t *testing.T) {
		szScenario(t, false, func(c *szCase) {
			want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
			want.Position = "8"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT missing FROM "+szTable, want)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				c.run(c.conn, s)
			}
			held := c.hold(szQuery)
			if !c.b.PG {
				ctx, cancel := context.WithCancel(c.p.Ctx)
				cancel()
				for _, e := range []fixture.Executor{c.conn, held} {
					rows, err := e.QueryContext(ctx, szQuery)
					if rows != nil {
						rows.Close()
						c.t.Fatal("precanceled query returned rows")
					}
					if !errors.Is(err, context.Canceled) {
						c.t.Fatalf("precanceled query: %v", err)
					}
					var state interface{ SQLState() string }
					if errors.As(err, &state) {
						c.t.Fatalf("ordinary cancellation has SQLSTATE %s", state.SQLState())
					}
				}
			}
			c.check(held, szQuery, c.want)
		})
	})
	for _, schema := range []bool{false, true} {
		name := "relation ACL"
		if schema {
			name = "schema ACL"
		}
		t.Run(name, func(t *testing.T) {
			szScenario(t, true, func(c *szCase) {
				c.exec("CREATE ROLE sz_reader")
				// Source and schema ACL changes remain private until commit.
				{
					c.exec("BEGIN")
					full := c.hold(szQuery)
					emptyQuery := szQuery + " WHERE FALSE"
					empty := c.hold(emptyQuery)
					c.run(full, c.projection(szQuery, c.want))
					c.run(empty, c.projection(emptyQuery, nil))
					message := "permission denied for table sql_sizing"
					if schema {
						c.exec("REVOKE USAGE ON SCHEMA information_schema FROM PUBLIC")
						message = "permission denied for schema information_schema"
					} else {
						c.exec("REVOKE SELECT ON information_schema.sql_sizing FROM PUBLIC")
					}
					c.exec("SET LOCAL ROLE sz_reader")
					for _, target := range []struct {
						q    string
						held fixture.HeldStatement
					}{{szQuery, full}, {emptyQuery, empty}} {
						for _, route := range []string{"direct", "prepared", "held"} {
							c.exec("SAVEPOINT sz_denial")
							want := fixture.ControlsReferenceDiagnostic("42501", message)
							if schema {
								// Both queries qualify the FROM relation at character 60.
								want.Position = "60"
							}
							s := fixture.ErrorDiagnostic(target.q, want)
							s.SelectRows, s.ErrorStage = true, "execute"
							var executor fixture.Executor = c.conn
							if route == "prepared" {
								s.Prepared = true
								if schema {
									s.ErrorStage = "prepare"
								}
							}
							if route == "held" {
								executor = target.held
							}
							c.run(executor, s)
							c.exec("ROLLBACK TO SAVEPOINT sz_denial", "RELEASE SAVEPOINT sz_denial")
						}
					}
					c.exec("ROLLBACK")
				}
				c.exec("SET SESSION AUTHORIZATION sz_reader")
				c.check(c.conn, szQuery, c.want)
				c.exec("RESET SESSION AUTHORIZATION")
			})
		})
	}
}
