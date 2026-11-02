package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExtensionDependenciesDiagnostics(t *testing.T) {
	t.Run("options_and_missing_prerequisites", func(t *testing.T) {
		earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct {
					q    string
					want fixture.Diagnostic
				}{
					{"CREATE EXTENSION earthdistance", dependencyMissing()},
					{"CREATE EXTENSION earthdistance VERSION 'missing' CASCADE", fixture.ControlsReferenceDiagnostic("22023", `extension "earthdistance" has no installation script nor update path for version "missing"`)},
					{"CREATE EXTENSION earthdistance SCHEMA missing_schema CASCADE", fixture.ControlsReferenceDiagnostic("3F000", `schema "missing_schema" does not exist`)},
				} {
					dependencyError(t, p, b, c, tc.q, prepared, tc.want)
					dependencyAbsent(t, p, b, c)
				}
				want := fixture.ControlsReferenceDiagnostic("42601", "conflicting or redundant options")
				want.Position = "40"
				dependencyError(t, p, b, c, "CREATE EXTENSION earthdistance CASCADE CASCADE", prepared, want)
				// Ordered interleaving retains the second option's source position.
				want.Position = "59"
				dependencyError(t, p, b, c, "CREATE EXTENSION earthdistance WITH CASCADE VERSION '1.2' CASCADE", prepared, want)
				want = fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "trailing"`)
				want.Position = "40"
				dependencyError(t, p, b, c, "CREATE EXTENSION earthdistance CASCADE trailing", prepared, want)
				dependencyAbsent(t, p, b, c)
				run(fixture.Exec("CREATE DOMAIN cube AS integer", 0))
				dependencyError(t, p, b, c, "CREATE EXTENSION earthdistance", prepared, dependencyMissing())
				run(fixture.Select("SELECT typbasetype='integer'::regtype AS original FROM pg_type WHERE oid='cube'::regtype", []string{"original"}, []string{"BOOL"}, [][]any{{true}}))
				run(fixture.Exec("DROP DOMAIN cube", 0))
				run(fixture.Exec("SET search_path=''", 0))
				dependencyError(t, p, b, c, "CREATE EXTENSION earthdistance CASCADE", prepared, fixture.ControlsReferenceDiagnostic("3F000", "no schema has been selected to create in"))
				run(fixture.Select("SELECT current_setting('search_path')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{`""`}}))
				run(fixture.Exec("SET search_path=public", 0))
				dependencyAbsent(t, p, b, c)
			}
		})
	})
	t.Run("late_collision", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "deps_collision_cube")
		earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE DOMAIN earth AS integer", 0))
			for _, prepared := range []bool{false, true} {
				dependencyError(t, p, b, c, "CREATE EXTENSION earthdistance CASCADE", prepared, dependencyCollision("public"))
				dependencyAbsent(t, p, b, c)
			}
			run(fixture.Exec("CREATE SCHEMA deps_collision_cube", 0))
			run(fixture.Exec("CREATE EXTENSION cube SCHEMA deps_collision_cube", 0))
			for _, prepared := range []bool{false, true} {
				dependencyError(t, p, b, c, "CREATE EXTENSION earthdistance SCHEMA public CASCADE", prepared, dependencyCollision("deps_collision_cube"))
				dependencyNoEarthExtension(t, p, b, c)
			}
		})
	})
	for _, rights := range []bool{false, true} {
		name := "no_target_rights"
		if rights {
			name = "target_rights"
		}
		t.Run(name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"deps_installer"})
			fixture.SchemaReferenceCleanup(t, "deps_target")
			earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE ROLE deps_installer", 0))
				run(fixture.Exec("CREATE SCHEMA deps_target", 0))
				var database string
				if err := c.QueryRowContext(p.Ctx, "SELECT current_database()").Scan(&database); err != nil {
					t.Fatal(err)
				}
				grant := fixture.ExtensionTempIdentifier(database)
				run(fixture.Exec("GRANT CREATE ON DATABASE "+grant+" TO deps_installer", 0))
				defer func() {
					ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
					defer cancel()
					for _, q := range []string{"RESET ROLE", "REVOKE CREATE ON DATABASE " + grant + " FROM deps_installer"} {
						if _, err := c.ExecContext(ctx, q); err != nil {
							t.Error(err)
						}
					}
				}()
				if rights {
					run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA deps_target TO deps_installer", 0))
				}
				run(fixture.Exec("SET ROLE deps_installer", 0))
				for _, prepared := range []bool{false, true} {
					dependencyError(t, p, b, c, "CREATE EXTENSION earthdistance SCHEMA deps_target", prepared, dependencyMissing())
					dependencyError(t, p, b, c, "CREATE EXTENSION earthdistance SCHEMA deps_target CASCADE", prepared, dependencyDenied())
				}
				run(fixture.Exec("RESET ROLE", 0))
				dependencyAbsent(t, p, b, c)
				run(fixture.Select("SELECT typname FROM pg_type WHERE typnamespace='deps_target'::regnamespace ORDER BY typname", []string{"typname"}, []string{"NAME"}, [][]any{}))
				run(fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='deps_target'::regnamespace ORDER BY proname", []string{"proname"}, []string{"NAME"}, [][]any{}))
				run(fixture.Select("SELECT conname FROM pg_constraint WHERE connamespace='deps_target'::regnamespace ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{}))
			})
		})
	}
}
