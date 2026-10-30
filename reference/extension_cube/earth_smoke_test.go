package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceEarthFeatureSmoke(t *testing.T) {
	earthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		earthInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE earth_smoke(id integer NOT NULL,v earth)", 0))
		run(fixture.Exec("INSERT INTO earth_smoke VALUES (1,'6378168')", 1))
		q := "INSERT INTO earth_smoke VALUES ($1,$2)"
		held := earthHeld(t, p, c, q)
		defer held.Statement.Close()
		input := []byte("(6378168,0)")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 2, input))
		for i := range input {
			input[i] = '9'
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, 3, nil))
		s := fixture.EarthSelect(b, "SELECT id,v FROM earth_smoke ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "(6378168)"}, {int64(2), "(6378168, 0)"}, {int64(3), nil}})
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { cubeMetadata(t, b.PG, rows, s.Columns, []bool{false, true}) }
		run(s)
		cubeError(t, p, b, c, "INSERT INTO earth_smoke VALUES (4,'6378168'),(5,'(6378168,0,0,0)')", false, false, false, earthCheckDiagnostic("public", "not_3d", true))
		run(s)
		bad := fixture.ErrorDiagnostic(q, cubeParameterDiagnostic(earthCheckDiagnostic("public", "not_point", true), "(0),(1)", 2), 4, "(0),(1)")
		bad.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
		bad = fixture.ErrorDiagnostic(q, cubeParameterDiagnostic(earthCheckDiagnostic("public", "on_surface", true), "0", 2), 4, "0")
		bad.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
		run(s)
		if !b.PG {
			// Close the pre-replacement handle before exercising native controls.
			held.Statement.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			run(s)
		}
	})
}
