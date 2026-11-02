package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSystemRowsPipeline(t *testing.T) {
	srCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		srRun(t, p, b, c, fixture.Exec("CREATE SEQUENCE sr_calls START 1", 0))
		for _, indexed := range []bool{false, true} {
			if indexed {
				srRun(t, p, b, c, fixture.Exec("CREATE INDEX sr_id_idx ON sr(id)", 0))
			}
			for _, prepared := range []bool{false, true} {
				t.Run(fmt.Sprintf("indexed_%t_prepared_%t", indexed, prepared), func(t *testing.T) {
					srPipeline(t, p, b, c, prepared)
					// Every sampled row fails this indexed condition. A sampler
					// must not refill from matches or run WHERE over the full heap.
					s := srCount("SELECT count(*) AS n FROM sr TABLESAMPLE system_rows(3) WHERE id<0", 0)
					s.Prepared = prepared
					srRun(t, p, b, c, s)
				})
			}
		}
		// Alias-before-sampling, ONLY and independent source occurrences keep
		// their ordinary FROM semantics; sorting happens after sampling.
		srBoth(t, p, b, c, srCount("SELECT count(*) AS n FROM ONLY sr AS sampled TABLESAMPLE system_rows(3)", 3))
		srBoth(t, p, b, c, srCount("SELECT count(*) AS n FROM sr AS a TABLESAMPLE system_rows(3) CROSS JOIN sr AS z TABLESAMPLE system_rows(0)", 0))
		srBoth(t, p, b, c, srSubset("sr AS sampled", "system_rows", "3", 3))
	})
}

func srPipeline(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, prepared bool) {
	t.Helper()
	srRun(t, p, b, c, fixture.Exec("ALTER SEQUENCE sr_calls RESTART WITH 1", 0))
	s := srCount("SELECT count(*) AS n FROM sr TABLESAMPLE system_rows(3) WHERE nextval('sr_calls')<0", 0)
	s.Prepared = prepared
	srRun(t, p, b, c, s)
	srRun(t, p, b, c, fixture.Select("SELECT last_value FROM sr_calls", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(3)}}))
}
