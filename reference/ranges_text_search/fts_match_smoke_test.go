package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSMatchFeatureSmoke(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer ftsConvertSetting(t, p, c)()
		ftsMatchTyped(t, p, b, []ftsMatchCase{
			{"fat cat rat", "cat & !cow", true}, {"fat cat rat", "cat & !rat", false},
			{"wa:1D wb:2A", "w:*D<->w:*A", true}, {"wa:1A wb:2D", "w:*D<->w:*A", false},
			{"a:1 b:3", "a<2>b", true}, {"a:1 b:3", "a<->b", false},
			{"a b", "a<->b", false}, {"a b", "!(a<->b)", true}, {"cat", "", false},
		})
		run := func(s fixture.Step) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
		for _, tc := range []struct {
			expression, label string
			want              bool
		}{
			{"'Rats'::text @@ 'rat'::tsquery", "?column?", true},
			{"'Rats'::tsvector @@ 'rat'::tsquery", "?column?", false},
			{"'cat'::text @@ 'cat | rat'::text", "?column?", false},
			{"'cat'::text @@ 'cat | rat'::tsquery", "?column?", true},
			{"ts_match_vq('cat'::tsvector,'cat'::tsquery)", "ts_match_vq", true},
			{"ts_match_tt('Rats'::text,'rat'::text)", "ts_match_tt", true},
		} {
			run(ftsMatchStep("SELECT "+tc.expression, tc.label, [][]any{{tc.want}}))
		}
		for _, signature := range ftsMatchSignatures {
			query := "SELECT " + signature.expression(false)
			run(ftsMatchStep(query, "?column?", [][]any{{nil}}, nil, "cat"))
			run(ftsMatchStep(query+" WHERE false", "?column?", nil, "cat", "cat"))
		}
		ftsMatchDefaults(t, p, b, c)
		d := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: integer @@ integer")
		d.Position = "10"
		d.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		ftsOpsDiagnosticRoutes(t, p, b, "SELECT 1 @@ 2", d, true)
	})
}
