package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExtensionDependenciesSchemas(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		name := "caller_creation_schema"
		if explicit {
			name = "explicit_schema"
		}
		t.Run(name, func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "deps_addons")
			earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE SCHEMA deps_addons", 0))
				q := "CREATE EXTENSION earthdistance CASCADE"
				if explicit {
					q = "CREATE EXTENSION earthdistance SCHEMA deps_addons CASCADE"
				} else {
					run(fixture.Exec("SET search_path=deps_addons,public", 0))
				}
				before := fixture.OwnershipSnapshot(t, p, c, "SHOW search_path")
				run(fixture.Exec(q, 0))
				fixture.OwnershipUnchanged(t, p, c, "SHOW search_path", before)
				run(fixture.Select("SELECT e.extname,n.nspname FROM pg_extension e JOIN pg_namespace n ON e.extnamespace=n.oid WHERE e.extname IN ('cube','earthdistance') ORDER BY e.extname", []string{"extname", "nspname"}, []string{"NAME", "NAME"}, [][]any{{"cube", "deps_addons"}, {"earthdistance", "deps_addons"}}))
				run(fixture.Exec("SET search_path=deps_addons,public", 0))
				dependencyQualifiedGraph(t, p, b, c, b.Recorder, "deps_addons", "deps_addons")
				run(fixture.Exec("CREATE TABLE public.deps_schema_values(v deps_addons.earth)", 0))
				run(fixture.Exec("INSERT INTO public.deps_schema_values VALUES ('(6378168,0)'),(NULL)", 2))
				run(fixture.EarthSelect(b, "SELECT v FROM public.deps_schema_values WHERE v IS NOT NULL", []string{"v"}, [][]any{{"(6378168, 0)"}}))
			})
		})
	}
	t.Run("existing_genuine_prerequisite_and_hostile_path", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "deps_cube_home", "deps_earth_home", "deps_earth_fail", "deps_hostile")
		earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
				defer cancel()
				for _, q := range []string{"DROP TABLE IF EXISTS pg_temp.earth", "DROP TABLE IF EXISTS pg_temp.cube"} {
					if _, err := c.ExecContext(ctx, q); err != nil {
						t.Error("cleaning TEMP shadows", err)
					}
				}
			}()
			for _, q := range []string{
				"CREATE SCHEMA deps_cube_home", "CREATE SCHEMA deps_earth_home", "CREATE SCHEMA deps_hostile",
				"CREATE SCHEMA deps_earth_fail", "CREATE DOMAIN deps_earth_fail.earth AS integer",
				"CREATE EXTENSION cube SCHEMA deps_cube_home", "SET search_path=deps_cube_home,public,pg_temp",
				"CREATE TABLE public.deps_cube_values(v cube)", "INSERT INTO public.deps_cube_values VALUES ('(1,2)')",
				"CREATE DOMAIN deps_hostile.cube AS integer", "CREATE DOMAIN deps_hostile.earth AS integer",
				"CREATE FUNCTION deps_hostile.earth() RETURNS float8 LANGUAGE SQL RETURN 7::float8",
				"CREATE FUNCTION deps_hostile.cube_is_point(deps_cube_home.cube) RETURNS boolean LANGUAGE SQL RETURN false",
				"CREATE TEMP TABLE cube(v integer)", "CREATE TEMP TABLE earth(v integer)",
			} {
				if q == "INSERT INTO public.deps_cube_values VALUES ('(1,2)')" {
					run(fixture.Exec(q, 1))
				} else {
					run(fixture.Exec(q, 0))
				}
			}
			before := dependencyCaptureCube(t, p, c)
			held := earthHeld(t, p, c, "SELECT v FROM public.deps_cube_values")
			defer held.Statement.Close()
			run(fixture.Exec("SET search_path=deps_hostile,pg_temp,public", 0))
			path := fixture.OwnershipSnapshot(t, p, c, "SHOW search_path")
			dependencyError(t, p, b, c, "CREATE EXTENSION earthdistance SCHEMA deps_earth_fail CASCADE", false, dependencyCollision("deps_cube_home"))
			fixture.OwnershipUnchanged(t, p, c, "SHOW search_path", path)
			dependencyNoEarthExtension(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION earthdistance SCHEMA deps_earth_home CASCADE", 0))
			fixture.OwnershipUnchanged(t, p, c, "SHOW search_path", path)
			run(fixture.Select("SELECT e.extname,n.nspname FROM pg_extension e JOIN pg_namespace n ON e.extnamespace=n.oid WHERE e.extname IN ('cube','earthdistance') ORDER BY e.extname", []string{"extname", "nspname"}, []string{"NAME", "NAME"}, [][]any{{"cube", "deps_cube_home"}, {"earthdistance", "deps_earth_home"}}))
			run(fixture.Select("SELECT typbasetype='deps_cube_home.cube'::regtype AS genuine FROM pg_type WHERE oid='deps_earth_home.earth'::regtype", []string{"genuine"}, []string{"BOOL"}, [][]any{{true}}))
			run(fixture.Exec("CREATE TABLE public.deps_secure_values(v deps_earth_home.earth)", 0))
			run(fixture.Exec("INSERT INTO public.deps_secure_values VALUES ('(6378168,0)')", 1))
			run(fixture.EarthSelect(b, "SELECT v FROM public.deps_secure_values", []string{"v"}, [][]any{{"(6378168, 0)"}}))
			s := fixture.Select("SELECT deps_earth_home.earth() AS radius,deps_earth_home.latitude(deps_earth_home.ll_to_earth(0,0)) AS latitude", []string{"radius", "latitude"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{float64(6378168), float64(0)}})
			run(s)
			run(fixture.Prepared(s))
			dependencyError(t, p, b, c, "CREATE EXTENSION earthdistance", false, fixture.ControlsReferenceDiagnostic("42710", `extension "earthdistance" already exists`))
			fixture.OwnershipUnchanged(t, p, c, "SHOW search_path", path)
			run(fixture.Exec("SET search_path=deps_cube_home,public,pg_temp", 0))
			dependencyQualifiedGraph(t, p, b, c, b.Recorder, "deps_cube_home", "deps_earth_home")
			dependencyUnchangedCube(t, p, c, before)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.CubeSelect(b, held.SQL, []string{"v"}, [][]any{{"(1, 2)"}}))
		})
	})
}

func TestReferenceExtensionDependenciesFixedSchema(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "deps_other")
	earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
			defer cancel()
			for _, q := range []string{"DROP EXTENSION IF EXISTS plpgsql CASCADE", "CREATE EXTENSION plpgsql"} {
				if _, err := c.ExecContext(ctx, q); err != nil {
					t.Error("restoring seeded plpgsql", err)
				}
			}
		}()
		run(fixture.Exec("CREATE SCHEMA deps_other", 0))
		run(fixture.Exec("DROP EXTENSION plpgsql CASCADE", 0))
		for _, prepared := range []bool{false, true} {
			dependencyError(t, p, b, c, "CREATE EXTENSION plpgsql SCHEMA deps_other", prepared, fixture.ControlsReferenceDiagnostic("0A000", `extension "plpgsql" must be installed in schema "pg_catalog"`))
			dependencyError(t, p, b, c, "CREATE EXTENSION plpgsql SCHEMA missing_schema CASCADE", prepared, fixture.ControlsReferenceDiagnostic("3F000", `schema "missing_schema" does not exist`))
			s := fixture.Exec("CREATE EXTENSION plpgsql SCHEMA deps_other CASCADE", 0)
			s.Prepared = prepared
			run(s)
			run(fixture.Select("SELECT n.nspname FROM pg_extension e JOIN pg_namespace n ON e.extnamespace=n.oid WHERE e.extname='plpgsql'", []string{"nspname"}, []string{"NAME"}, [][]any{{"pg_catalog"}}))
			run(fixture.Exec("DROP EXTENSION plpgsql CASCADE", 0))
		}
	})
}
