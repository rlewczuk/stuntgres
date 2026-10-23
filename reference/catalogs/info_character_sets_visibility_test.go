package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoCharacterSetsVisibility(t *testing.T) {
	csScenario(t, true, func(c *csCase) {
		c.exec("CREATE ROLE cs_owner", "CREATE ROLE cs_reader", "CREATE SCHEMA cs_visibility AUTHORIZATION cs_owner", "SET SESSION AUTHORIZATION cs_owner", `CREATE COLLATION cs_visibility.cs_hidden_selected_very_long_name FROM pg_catalog."C"`)
		held := c.hold(csQuery)
		c.check(held, "cs_visibility", "cs_hidden_selected_very_long_name")
		c.exec("RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION cs_reader", "SET search_path=pg_catalog,public")
		c.check(held, "cs_visibility", "cs_hidden_selected_very_long_name")
		// The private schema has no reader USAGE grant; search-path visibility
		// still does not filter the winning character-set collation identity.
		c.run(c.conn, fixture.Select(`SELECT pg_collation_is_visible(oid) AS visible FROM pg_catalog.pg_collation WHERE collname='cs_hidden_selected_very_long_name'`, []string{"visible"}, []string{"BOOL"}, [][]any{{false}}))
		c.denied()
		c.check(held, "cs_visibility", "cs_hidden_selected_very_long_name")
		c.exec("RESET SESSION AUTHORIZATION", "DROP COLLATION cs_visibility.cs_hidden_selected_very_long_name", "DROP SCHEMA cs_visibility")
		c.check(held, "pg_catalog", "C")
	})
}

func TestReferenceInfoCharacterSetsDiagnostics(t *testing.T) {
	csScenario(t, false, func(c *csCase) {
		want := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		want.Position = "8"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT missing FROM information_schema.character_sets", want)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			c.run(c.conn, s)
		}
		held := c.hold(csQuery)
		if !c.b.PG {
			ctx, cancel := context.WithCancel(c.p.Ctx)
			cancel()
			for _, e := range []fixture.Executor{c.conn, held} {
				rows, err := e.QueryContext(ctx, csQuery)
				if rows != nil {
					rows.Close()
					t.Fatal("canceled read exposed rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancellation", err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("ordinary cancellation has SQLSTATE", state.SQLState())
				}
			}
		}
		c.check(held, "pg_catalog", "C")
	})
}

func TestReferenceInfoCharacterSetsFeatureSmoke(t *testing.T) {
	csScenario(t, true, func(c *csCase) {
		held := c.hold(csQuery)
		c.run(held, c.projection(csQuery, c.rows("pg_catalog", "C")))
		q := csQuery + " WHERE character_set_name=$1"
		bound := c.hold(q)
		c.run(bound, c.projection(q, c.rows("pg_catalog", "C"), "UTF8"))
		c.run(bound, c.projection(q, nil, nil))
		c.exec(`CREATE COLLATION public.cs_smoke_selected_long_name FROM pg_catalog."C"`)
		c.run(held, c.projection(csQuery, c.rows("public", "cs_smoke_selected_long_name")))
		c.exec("CREATE ROLE cs_reader", "SET SESSION AUTHORIZATION cs_reader")
		c.run(held, c.projection(csQuery, c.rows("public", "cs_smoke_selected_long_name")))
		c.denied()
		c.exec("RESET SESSION AUTHORIZATION", "DROP COLLATION public.cs_smoke_selected_long_name")
		c.run(held, c.projection(csQuery, c.rows("pg_catalog", "C")))
	})
}
