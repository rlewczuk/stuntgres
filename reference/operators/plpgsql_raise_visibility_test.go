package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLRaiseDiagnosticVisibility(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "raise_visible")
	fixture.SchemaReferenceCleanup(t, "raise_shadow")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil { t.Fatal(err) }
			defer c.Close()
			userOperatorExec(t, p, b, c,
				"CREATE SCHEMA raise_visible",
				"CREATE SCHEMA raise_shadow",
				"CREATE FUNCTION raise_visible.hidden_first() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'hidden first'; END$$",
				"CREATE FUNCTION raise_visible.pick(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'target'; END$$",
				"SET search_path TO raise_visible,public",
				"CREATE FUNCTION raise_visible.visible_first() RETURNS integer LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'visible first'; END$$",
				"SET search_path TO raise_shadow,raise_visible,public",
				"CREATE FUNCTION raise_shadow.pick(x integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'shadow'; END$$",
				"SET search_path TO public")
			hiddenQuery := "SELECT raise_visible.hidden_first() AS v"
			visibleQuery := "SELECT raise_visible.visible_first() AS v"
			hidden, err := c.PrepareContext(p.Ctx, hiddenQuery)
			if err != nil { t.Fatal(err) }
			defer hidden.Close()
			visible, err := c.PrepareContext(p.Ctx, visibleQuery)
			if err != nil { t.Fatal(err) }
			defer visible.Close()
			// CREATE validation compiles these bodies under their creation paths.
			// Neither preparing nor calling after path changes may relabel that
			// cached diagnostic signature.
			userOperatorExec(t, p, b, c, "SET search_path TO public")
			wantHidden := plpgsqlRuntimeDiagnostic("P0001", "hidden first", "raise_visible.hidden_first()", "RAISE")
			rows, err := hidden.QueryContext(p.Ctx)
			if rows != nil { rows.Close(); t.Fatal("RAISE exposed rows") }
			fixture.AssertDiagnostic(t, err, &wantHidden)
			userOperatorExec(t, p, b, c, "SET search_path TO raise_visible,public")
			rows, err = hidden.QueryContext(p.Ctx)
			if rows != nil { rows.Close(); t.Fatal("RAISE exposed rows") }
			fixture.AssertDiagnostic(t, err, &wantHidden)
			plpgsqlError(t, p, c, hiddenQuery, true, wantHidden)
			wantVisible := plpgsqlRuntimeDiagnostic("P0001", "visible first", "visible_first()", "RAISE")
			rows, err = visible.QueryContext(p.Ctx)
			if rows != nil { rows.Close(); t.Fatal("RAISE exposed rows") }
			fixture.AssertDiagnostic(t, err, &wantVisible)
			userOperatorExec(t, p, b, c, "SET search_path TO public")
			rows, err = visible.QueryContext(p.Ctx)
			if rows != nil { rows.Close(); t.Fatal("RAISE exposed rows") }
			fixture.AssertDiagnostic(t, err, &wantVisible)
			plpgsqlError(t, p, c, visibleQuery, true, wantVisible)
			// Membership in the path is insufficient: the earlier exact signature
			// shadows the target even though its namespace is also searchable.
			userOperatorExec(t, p, b, c, "SET search_path TO raise_shadow,raise_visible,public")
			wantTarget := plpgsqlRuntimeDiagnostic("P0001", "target", "raise_visible.pick(integer)", "RAISE")
			plpgsqlError(t, p, c, "SELECT raise_visible.pick(1) AS v", true, wantTarget)
			unqualified := "SELECT pick(1) AS v"
			shadow, err := c.PrepareContext(p.Ctx, unqualified)
			if err != nil { t.Fatal(err) }
			defer shadow.Close()
			wantShadow := plpgsqlRuntimeDiagnostic("P0001", "shadow", "pick(integer)", "RAISE")
			rows, err = shadow.QueryContext(p.Ctx)
			if rows != nil { rows.Close(); t.Fatal("RAISE exposed rows") }
			fixture.AssertDiagnostic(t, err, &wantShadow)
			userOperatorExec(t, p, b, c, "SET search_path TO raise_visible,raise_shadow,public")
			rows, err = shadow.QueryContext(p.Ctx)
			if rows != nil { rows.Close(); t.Fatal("RAISE exposed rows") }
			fixture.AssertDiagnostic(t, err, &wantTarget)
			plpgsqlError(t, p, c, "SELECT raise_shadow.pick(1) AS v", true, wantShadow)
			userOperatorExec(t, p, b, c, "RESET search_path")
		})
	}
}
