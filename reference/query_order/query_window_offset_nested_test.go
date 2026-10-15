package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryWindowOffsetNestedDefaults(t *testing.T) {
	queryWindowOffsetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		queryWindowOffsetSeed(t, p, b)
		for _, fn := range []string{"lead", "lag"} {
			for _, tc := range []struct{ name, value, tail string }{
				{"singleton", "0", ""},
				{"correlated-member", "member.id", " FROM wo member WHERE member.id=wo.id"},
			} {
				t.Run(fn+"/"+tc.name, func(t *testing.T) {
					positive := "(SELECT " + fn + "(" + tc.value + ",1,wo.id) OVER()" + tc.tail + ")"
					negative := "(SELECT " + fn + "(" + tc.value + ",1,-wo.id) OVER()" + tc.tail + ")"
					// Each child has exactly one row, so offset1 always selects its default.
					// Projection is +id; only the nested ORDER default differs, reversing rows.
					queryWindowRoutes(t, p, b, fixture.Select("SELECT wo.id,"+positive+" AS x FROM wo ORDER BY "+negative, []string{"id", "x"}, []string{"INT4", "INT4"}, [][]any{
						{int64(6), int64(6)}, {int64(5), int64(5)}, {int64(4), int64(4)},
						{int64(3), int64(3)}, {int64(2), int64(2)}, {int64(1), int64(1)},
					}))
					// Equal independently analyzed child queries may share the selected target.
					queryWindowRoutes(t, p, b, fixture.Select("SELECT wo.id,"+positive+" AS x FROM wo ORDER BY "+positive, []string{"id", "x"}, []string{"INT4", "INT4"}, [][]any{
						{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)},
						{int64(4), int64(4)}, {int64(5), int64(5)}, {int64(6), int64(6)},
					}))
				})
			}
		}
	})
}
