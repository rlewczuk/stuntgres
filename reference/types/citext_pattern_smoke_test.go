package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCitextPatternFeatureSmoke(t *testing.T) {
	citextPatternScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		citextPatternStoredSetup(t, p, b, c)
		q := "SELECT id,s ~ p AS re,s LIKE t AS lk,s ~ '(?c)^abc$'::citext AS sensitive FROM citext_pattern_source ORDER BY id"
		s := fixture.Select(q, []string{"id", "re", "lk", "sensitive"}, []string{"INT4", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), true, true, false}, {int64(2), false, false, false}, {int64(3), nil, nil, nil}})
		citextPatternRoutes(t, p, b, c, s)
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		citextPatternRoutes(t, p, b, c, citextPatternBool("SELECT $1::citext LIKE $2::citext ESCAPE $3::citext AS v", true, "A%B", "a!%b", "!"))
		citextPatternHeldRegex(t, p, b, c, "~", "citext", false)
		citextPatternStoredValues(t, p, b, c)
		if !b.PG {
			citextPatternRoundTrip(t, p, b, c)
		}
	})
}
