package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSystemRowsFeatureSmoke(t *testing.T) {
	srCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, k := range []int64{0, 3, 11} {
			srBoth(t, p, b, c, srSubset("sr", "system_rows", "$1", min(k, int64(10)), k))
		}
		for _, argument := range []string{"'2'", "1.6"} {
			srBoth(t, p, b, c, srSubset("sr", "system_rows", argument, 2))
		}
		for _, table := range []string{"sr", "sr_empty"} {
			srBoth(t, p, b, c, srMetadata(fixture.Select("SELECT id,payload FROM "+table+" TABLESAMPLE system_rows(0)", srNames, srTypes, nil), b.PG))
		}
		h := srHeld(t, p, c, "SELECT count(*) AS n FROM sr TABLESAMPLE system_rows($1)")
		for _, k := range []int64{0, 3, 11} {
			srRun(t, p, b, h, srCount(h.SQL, min(k, int64(10)), k))
		}
		srRun(t, p, b, h, srError(h.SQL, "2202H", "TABLESAMPLE parameter cannot be null", 0, nil))
		srRun(t, p, b, h, srError(h.SQL, "2202H", "sample size must not be negative", 0, int64(-1)))
		srRun(t, p, b, h, srCount(h.SQL, 3, int64(3)))
		srBoth(t, p, b, c, srError("SELECT * FROM sr TABLESAMPLE system_rows(1) REPEATABLE(0)", "0A000", "tablesample method system_rows does not support REPEATABLE", 30))
		srRun(t, p, b, c, fixture.Exec("CREATE SEQUENCE sr_calls START 1", 0))
		for _, prepared := range []bool{false, true} {
			srPipeline(t, p, b, c, prepared)
		}
		srRun(t, p, b, c, fixture.Exec("CREATE VIEW sr_view AS SELECT id,payload FROM sr TABLESAMPLE system_rows(3)", 0))
		srRun(t, p, b, c, fixture.Exec("CREATE VIEW sr_all AS SELECT id,payload FROM sr TABLESAMPLE system_rows(11)", 0))
		srRun(t, p, b, c, fixture.Exec("CREATE VIEW sr_zero_write AS SELECT id,payload FROM sr TABLESAMPLE system_rows(0)", 0))
		srControlPost(t, p, b, c)
		if !b.PG {
			srReplay(t, p, b, c)
			srRun(t, p, b, h, srCount(h.SQL, 3, int64(3)))
		}
	})
}
