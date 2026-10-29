package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExtensionOwnershipPrerequisites(t *testing.T) {
	t.Run("sealed_old_and_new_cohorts", ownershipPrerequisiteCohorts)
	for _, s := range []string{"eo_cube", "eo_earth", "eo_moved"} {
		fixture.SchemaReferenceCleanup(t, s)
	}
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
			for _, q := range []string{`CREATE SCHEMA eo_cube`, `CREATE SCHEMA eo_earth`, `CREATE SCHEMA eo_moved`, `CREATE DOMAIN public.cube AS integer`} {
				run(fixture.Exec(q, 0))
			}
			want := fixture.ControlsReferenceDiagnostic("42704", `required extension "cube" is not installed`)
			want.Hint = "Use CREATE EXTENSION ... CASCADE to install required extensions too."
			run(fixture.ErrorDiagnostic(`CREATE EXTENSION earthdistance SCHEMA eo_earth`, want))
			run(fixture.Exec(`CREATE EXTENSION cube SCHEMA eo_cube`, 0))
			run(fixture.Exec(`CREATE EXTENSION earthdistance SCHEMA eo_earth`, 0))
			var cube, earth int64
			if err := c.QueryRowContext(p.Ctx, `SELECT 'eo_cube.cube'::regtype::bigint,'eo_earth.earth'::regtype::bigint`).Scan(&cube, &earth); err != nil {
				t.Fatal(err)
			}
			check := func() {
				run(fixture.Select(`SELECT oid::bigint,typbasetype::bigint FROM pg_type WHERE oid=$1`, []string{"oid", "typbasetype"}, []string{"INT8", "INT8"}, [][]any{{earth, cube}}, earth))
				run(fixture.Select(`SELECT d.deptype FROM pg_depend d JOIN pg_extension e ON e.oid=d.objid JOIN pg_extension r ON r.oid=d.refobjid WHERE d.classid='pg_extension'::regclass AND d.refclassid='pg_extension'::regclass AND e.extname='earthdistance' AND r.extname='cube'`, []string{"deptype"}, []string{"CHAR"}, [][]any{{"n"}}))
			}
			check()
			run(fixture.Exec(`ALTER EXTENSION cube SET SCHEMA eo_moved`, 0))
			check()
			run(fixture.Select(`SELECT 'eo_moved.cube'::regtype::bigint AS actual,'public.cube'::regtype='eo_moved.cube'::regtype AS duplicate`, []string{"actual", "duplicate"}, []string{"INT8", "BOOL"}, [][]any{{cube, false}}))
			run(fixture.Select(`SELECT eo_earth.latitude(eo_earth.ll_to_earth(0,0)) AS latitude`, []string{"latitude"}, []string{"FLOAT8"}, [][]any{{float64(0)}}))
			want = fixture.ControlsReferenceDiagnostic("2BP01", `cannot drop extension cube because other objects depend on it`)
			want.Detail = "extension earthdistance depends on extension cube"
			want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
			run(fixture.ErrorDiagnostic(`DROP EXTENSION cube RESTRICT`, want))
			check()
			run(fixture.Exec(`DROP EXTENSION cube CASCADE`, 0))
			run(fixture.Select(`SELECT extname FROM pg_extension WHERE extname IN ('cube','earthdistance') ORDER BY extname`, []string{"extname"}, []string{"NAME"}, [][]any{}))
			run(fixture.Select(`SELECT 'public.cube'::regtype='public.cube'::regtype AS survivor`, []string{"survivor"}, []string{"BOOL"}, [][]any{{true}}))
		})
	}
}
