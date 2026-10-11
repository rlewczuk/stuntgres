package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGCastFeatureSmoke(t *testing.T) {
	pgCastScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgCastShape(t, p, b)
		// Permanent compact witnesses distinguish function, binary and I/O
		// metadata, all three admission contexts, and genuine typmod identity.
		fixture.PgCastCheckFacts(t, p, b, b.DB, [][]any{
			{int64(16), int64(25), int64(2971), "a", "f"},
			{int64(20), int64(23), int64(480), "a", "f"},
			{int64(23), int64(16), int64(2557), "e", "f"},
			{int64(23), int64(20), int64(481), "i", "f"},
			{int64(23), int64(26), int64(0), "i", "b"},
			{int64(26), int64(23), int64(0), "a", "b"},
			{int64(114), int64(3802), int64(0), "a", "i"},
			{int64(1043), int64(1043), int64(669), "i", "f"},
		}, false)
		pgCastHeld(t, p, b, pgCastIdentities(t, p, b))
		pgCastCoercion(t, p, b)
		pgCastMissing(t, p, b)
	})
	pgCastControlsScenario(t)
}
