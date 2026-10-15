package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSoundexFeatureSmoke(t *testing.T) {
	soundexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		soundexControls(t, p, b, c)
		for _, s := range []fixture.Step{
			soundexSelect(b, "SELECT sx_addon.soundex('BHB')", "soundex", "B100"),
			soundexSelect(b, "SELECT sx_addon.soundex('B1B')", "soundex", "B000"),
			soundexSelect(b, "SELECT sx_addon.soundex('Ashcraft')", "soundex", "A226"),
			soundexSelect(b, "SELECT sx_addon.soundex('Émile')", "soundex", "M400"),
			soundexSelect(b, "SELECT sx_addon.soundex('東京🙂')", "soundex", ""),
			soundexSelect(b, "SELECT sx_addon.difference('','!?')", "difference", int64(4)),
			fixture.Select("SELECT id FROM sx_source WHERE name='Ann' ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
			fixture.Select("SELECT id FROM sx_source WHERE sx_addon.soundex(name)=sx_addon.soundex('Ann') ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
		}
	})
}
