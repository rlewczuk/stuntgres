package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func sfPrivateSeed(c *sfCase) {
	c.t.Helper()
	c.exec("CREATE ROLE sf_owner", "CREATE ROLE sf_reader", "GRANT USAGE ON SCHEMA public TO sf_reader", "GRANT CREATE ON SCHEMA public TO sf_owner", "SET SESSION AUTHORIZATION sf_owner", "CREATE TABLE public.sf_private(id integer)", "RESET SESSION AUTHORIZATION")
}

func TestReferenceInfoSQLFeaturesVisibility(t *testing.T) {
	sfScenario(t, true, func(c *sfCase) {
		sfPrivateSeed(c)
		held := c.hold(sfQuery)
		c.check(c.conn, sfQuery, c.release)
		for _, actor := range []string{"sf_owner", "sf_reader"} {
			c.exec("SET SESSION AUTHORIZATION "+actor, "SET search_path=pg_catalog,public")
			c.check(held, sfQuery, c.release)
			c.run(c.conn, c.projection(sfQuery, c.release))
			if actor == "sf_reader" {
				c.privateDenied()
				c.check(held, sfQuery, c.release)
			}
			c.exec("RESET SESSION AUTHORIZATION")
		}
		c.exec("CREATE TABLE public.sf_unrelated(id integer)", "CREATE DOMAIN public.sf_unrelated_domain AS varchar", "GRANT sf_owner TO sf_reader", "SET SESSION AUTHORIZATION sf_reader", "SET ROLE sf_owner", "SET search_path=public")
		c.check(held, sfQuery, c.release)
		c.exec("RESET ROLE", "RESET SESSION AUTHORIZATION", "REVOKE sf_owner FROM sf_reader", "DROP DOMAIN public.sf_unrelated_domain", "DROP TABLE public.sf_unrelated", "SET SESSION AUTHORIZATION sf_reader")
		c.privateDenied()
		c.check(held, sfQuery, c.release)
	})
}

func TestReferenceInfoSQLFeaturesDiagnostics(t *testing.T) {
	t.Run("missing column and cancellation", func(t *testing.T) {
		sfScenario(t, false, func(c *sfCase) {
			t := c.t
			want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
			want.Position = "8"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT missing FROM "+sfTable, want)
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				c.run(c.conn, s)
			}
			held := c.hold(sfQuery)
			if !c.b.PG {
				ctx, cancel := context.WithCancel(c.p.Ctx)
				cancel()
				for _, e := range []fixture.Executor{c.conn, held} {
					rows, err := e.QueryContext(ctx, sfQuery)
					if rows != nil {
						rows.Close()
						t.Fatal("canceled query returned rows")
					}
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled query: %v", err)
					}
					var state interface{ SQLState() string }
					if errors.As(err, &state) {
						t.Fatalf("ordinary cancellation has SQLSTATE %s", state.SQLState())
					}
				}
			}
			c.check(held, sfQuery, c.release)
		})
	})
	t.Run("transactional target ACL", func(t *testing.T) {
		sfScenario(t, true, func(c *sfCase) {
			c.exec("CREATE ROLE sf_reader")
			// Catalog-source GRANT/REVOKE SQL is outside native admission scope.
			// Native producer/authorization units independently prove real denial;
			// this required oracle phase proves the PostgreSQL SQL boundary.
			if c.b.PG {
				c.exec("BEGIN")
				full := c.hold(sfQuery)
				emptyQuery := sfQuery + " WHERE FALSE"
				empty := c.hold(emptyQuery)
				c.run(full, c.projection(sfQuery, c.release))
				c.run(empty, c.projection(emptyQuery, nil))
				c.exec("REVOKE SELECT ON information_schema.sql_features FROM PUBLIC", "SET LOCAL ROLE sf_reader")
				for _, target := range []struct {
					q    string
					held fixture.HeldStatement
				}{{sfQuery, full}, {emptyQuery, empty}} {
					for _, route := range []string{"direct", "prepared", "held"} {
						// Each permission error aborts its subtransaction, not the
						// surrounding ACL transaction. Cleanup always rolls back.
						c.exec("SAVEPOINT sf_denial")
						s := fixture.ErrorDiagnostic(target.q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table sql_features"))
						s.SelectRows, s.ErrorStage = true, "execute"
						var executor fixture.Executor = c.conn
						if route == "prepared" {
							s.Prepared = true
						}
						if route == "held" {
							executor = target.held
						}
						c.run(executor, s)
						c.exec("ROLLBACK TO SAVEPOINT sf_denial", "RELEASE SAVEPOINT sf_denial")
					}
				}
				c.exec("ROLLBACK")
			}
			c.exec("SET SESSION AUTHORIZATION sf_reader")
			c.check(c.conn, sfQuery, c.release)
			c.exec("RESET SESSION AUTHORIZATION")
		})
	})
}

func TestReferenceInfoSQLFeaturesFeatureSmoke(t *testing.T) {
	sfScenario(t, true, func(c *sfCase) {
		sfPrivateSeed(c)
		q := sfQuery + " WHERE feature_id=$1"
		held := c.hold(q)
		for _, id := range []any{"B011", "T321", nil, "T616"} {
			c.run(held, c.projection(q, sfFeature(c.release, id), id))
		}
		c.run(c.conn, c.projection(sfQuery, c.release))
		c.exec("SET SESSION AUTHORIZATION sf_reader")
		c.privateDenied()
		c.run(held, c.projection(q, sfFeature(c.release, "T321"), "T321"))
		c.run(c.conn, fixture.Prepared(c.projection(q, sfFeature(c.release, "B011"), "B011")))
		c.exec("RESET SESSION AUTHORIZATION")
		sfCatalogShape(c)
	})
}
