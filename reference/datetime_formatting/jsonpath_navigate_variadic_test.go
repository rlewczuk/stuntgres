package reference_test

import (
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathNavigateVariadic(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, args := range []string{
				`'1',VARIADIC '$'`,
				`'1','$','{}',VARIADIC false`,
				`target=>'1',VARIADIC path=>'$'`,
				`'1',VARIADIC path=>'$'`,
				`'1','$',VARIADIC vars=>'{}'`,
				`target=>'1',path=>'$',vars=>'{}',VARIADIC silent=>false`,
			} {
				t.Run(args, func(t *testing.T) {
					fixture.JSONPathNavigateBoth(t, p, b, c, jsonpathNavigateSelect(`SELECT jsonb_path_query_array(`+args+`) AS changed`, `[1]`))
				})
			}

			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE jpn_variadic(doc jsonb,path jsonpath)`, 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jpn_variadic VALUES('[1,2]','$[*]'),('[1,2]','$[*]'),('[3]','$[*]')`, 3))
			for _, tc := range []struct{ selected, grouped string }{
				{`doc,VARIADIC path`, `doc,path`},
				{`doc,path`, `doc,VARIADIC path`},
			} {
				t.Run("group_"+tc.selected, func(t *testing.T) {
					q := `SELECT jsonb_path_query_array(` + tc.selected + `) AS changed,count(*) AS n FROM jpn_variadic GROUP BY jsonb_path_query_array(` + tc.grouped + `) ORDER BY changed`
					fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(q, []string{"changed", "n"}, []string{"JSONB", "INT8"}, [][]any{{`[3]`, int64(1)}, {`[1, 2]`, int64(2)}}))
				})
			}

			// With named arguments, the last written formal must also be the
			// last supplied ordinal. Positional acceptance cannot be generalized
			// to reordered names or to a named silent argument skipping vars.
			for _, tc := range []struct{ args, signature string }{
				{`path=>'$',VARIADIC target=>'1'`, `jsonb_path_query_array(path => unknown, target => unknown)`},
				{`'1','$',VARIADIC silent=>false`, `jsonb_path_query_array(unknown, unknown, silent => boolean)`},
			} {
				t.Run("rejected_"+tc.args, func(t *testing.T) {
					q := `SELECT jsonb_path_query_array(` + tc.args + `)`
					d := fixture.ControlsReferenceDiagnostic("42883", "function "+tc.signature+" does not exist")
					d.Position = "8"
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
					s := fixture.ErrorDiagnostic(q, d)
					s.SelectRows, s.ErrorStage = true, "execute"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					s = fixture.Prepared(s)
					s.ErrorStage = "prepare"
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				})
			}
			q := `SELECT jsonb_path_query_array(target=>'1',VARIADIC '$')`
			d := fixture.ControlsReferenceDiagnostic("42601", "positional argument cannot follow named argument")
			d.Position = fixture.Position(strings.Index(q, `VARIADIC '$'`) + len("VARIADIC ") + 1)
			s := fixture.ErrorDiagnostic(q, d)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			s = fixture.Prepared(s)
			s.ErrorStage = "prepare"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}
