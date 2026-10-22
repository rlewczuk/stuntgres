package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceEarthInstallation(t *testing.T) {
	t.Run("prerequisite_and_inventory", func(t *testing.T) {
		earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			want := fixture.ControlsReferenceDiagnostic("42704", `type "earth" does not exist`)
			want.Position = "30"
			cubeError(t, p, b, c, "CREATE TABLE earth_missing(v earth)", false, false, false, want)
			want = fixture.ControlsReferenceDiagnostic("42704", `required extension "cube" is not installed`)
			want.Hint = "Use CREATE EXTENSION ... CASCADE to install required extensions too."
			cubeError(t, p, b, c, "CREATE EXTENSION earthdistance", false, false, false, want)
			earthAbsent(t, p, b, c)
			run(fixture.Exec("CREATE DOMAIN cube AS integer", 0))
			cubeError(t, p, b, c, "CREATE EXTENSION earthdistance", false, false, false, want)
			earthAbsent(t, p, b, c)
			run(fixture.Exec("DROP DOMAIN cube", 0))
			cubeInstall(t, p, b, c)
			run(fixture.Exec("BEGIN", 0))
			run(fixture.Exec("CREATE EXTENSION earthdistance", 0))
			run(fixture.Exec("ROLLBACK", 0))
			earthAbsent(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION earthdistance", 0))
			run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS earthdistance", 0))
			cubeError(t, p, b, c, "CREATE EXTENSION earthdistance", false, false, false, fixture.ControlsReferenceDiagnostic("42710", `extension "earthdistance" already exists`))
			run(fixture.Exec("CREATE TABLE earth_installed(v earth)", 0))
			run(fixture.Select("SELECT extversion,extrelocatable FROM pg_extension WHERE extname='earthdistance'", []string{"extversion", "extrelocatable"}, []string{"TEXT", "BOOL"}, [][]any{{"1.2", true}}))
			run(fixture.Select("SELECT default_version,comment FROM pg_available_extensions WHERE name='earthdistance'", []string{"default_version", "comment"}, []string{"TEXT", "TEXT"}, [][]any{{"1.2", "calculate great-circle distances on the surface of the Earth"}}))
			run(fixture.Select("SELECT version,superuser,trusted,relocatable FROM pg_available_extension_versions WHERE name='earthdistance' AND version='1.2'", []string{"version", "superuser", "trusted", "relocatable"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"1.2", true, false, true}}))
			run(fixture.Select("SELECT d.deptype FROM pg_depend d JOIN pg_extension e ON d.objid=e.oid JOIN pg_extension c ON d.refobjid=c.oid WHERE d.classid='pg_extension'::regclass AND d.refclassid='pg_extension'::regclass AND e.extname='earthdistance' AND c.extname='cube'", []string{"deptype"}, []string{"CHAR"}, [][]any{{"n"}}))
		})
	})
	t.Run("conflict_atomicity", func(t *testing.T) {
		earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			cubeInstall(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN earth AS integer", 0))
			want := fixture.ControlsReferenceDiagnostic("42710", `type "earth" already exists`)
			// Root-observed pinned18.6 full diagnostic, required on both backends.
			want.Context = "SQL statement \"CREATE DOMAIN earth AS public.cube\n  CONSTRAINT not_point CHECK(public.cube_is_point(VALUE))\n  CONSTRAINT not_3d CHECK(public.cube_dim(VALUE) <= 3)\n  CONSTRAINT on_surface CHECK(abs(public.cube_distance(VALUE, '(0)'::public.cube) /\n  earth() - '1'::float8) < '10e-7'::float8)\"\nextension script file \"earthdistance--1.1.sql\", near line 30"
			s := fixture.ErrorDiagnostic("CREATE EXTENSION earthdistance", want)
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT extname FROM pg_extension WHERE extname='earthdistance'", []string{"extname"}, []string{"NAME"}, [][]any{}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT typbasetype='integer'::regtype AS original FROM pg_type WHERE oid='earth'::regtype", []string{"original"}, []string{"BOOL"}, [][]any{{true}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT conname FROM pg_constraint WHERE contypid='earth'::regtype", []string{"conname"}, []string{"NAME"}, [][]any{}))
		})
	})
	t.Run("distinct_schemas_and_hidden_domain", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "earth_cube_s")
		fixture.SchemaReferenceCleanup(t, "earth_domain_s")
		earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, q := range []string{"CREATE SCHEMA earth_cube_s", "CREATE SCHEMA earth_domain_s", "CREATE DOMAIN public.cube AS integer", "CREATE EXTENSION cube SCHEMA earth_cube_s", "CREATE EXTENSION earthdistance SCHEMA earth_domain_s", "CREATE TABLE earth_qualified(v earth_domain_s.earth)"} {
				run(fixture.Exec(q, 0))
			}
			run(fixture.Exec("INSERT INTO earth_qualified VALUES ('(6378168,0)')", 1))
			run(earthSelect(b, "SELECT v FROM earth_qualified", []string{"v"}, [][]any{{"(6378168, 0)"}}))
			run(fixture.Select("SELECT typbasetype='earth_cube_s.cube'::regtype AS actual FROM pg_type WHERE oid='earth_domain_s.earth'::regtype", []string{"actual"}, []string{"BOOL"}, [][]any{{true}}))
			cubeError(t, p, b, c, "SELECT '(0)'::earth_domain_s.earth", false, true, false, earthCheckDiagnostic("earth_domain_s", "on_surface", false))
		})
	})
	t.Run("untrusted_installation", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"earth_install_reader"})
		earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			cubeInstall(t, p, b, c)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE ROLE earth_install_reader", 0))
			run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO earth_install_reader", 0))
			run(fixture.Exec("SET ROLE earth_install_reader", 0))
			defer c.ExecContext(p.Ctx, "RESET ROLE")
			want := fixture.ControlsReferenceDiagnostic("42501", `permission denied to create extension "earthdistance"`)
			want.Hint = "Must be superuser to create this extension."
			cubeError(t, p, b, c, "CREATE EXTENSION earthdistance", false, false, false, want)
			run(fixture.Exec("RESET ROLE", 0))
			earthAbsent(t, p, b, c)
		})
	})
}
