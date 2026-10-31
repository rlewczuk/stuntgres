package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWExplainAnalyzeFeatureSmoke(t *testing.T) {
	fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		want := fdwAdvancedLive(t, p, b, r, run)
		for _, child := range []string{
			"SELECT * FROM fdw_data",
			"INSERT INTO fdw_data VALUES(99,'never',true)",
			"UPDATE fdw_data SET label='never' WHERE id=1",
			"DELETE FROM fdw_data WHERE id=2",
		} {
			fdwExplainRoutes(t, p, c, "EXPLAIN (VERBOSE,COSTS false) "+child, func(lines []string) {
				text := strings.Join(lines, "\n")
				property := "Remote SQL:"
				if !b.PG && !strings.HasPrefix(child, "SELECT") {
					property = "Remote operation:"
				}
				if !strings.Contains(text, property) || !strings.Contains(text, "fdw_source") {
					t.Fatal("missing provider plan", text)
				}
				if strings.HasPrefix(child, "SELECT") && !strings.Contains(text, "Foreign Scan") {
					t.Fatal(text)
				}
				if !b.PG && strings.HasPrefix(child, "SELECT") && !strings.Contains(text, "chosen second") {
					t.Fatal("wrong actual selected path", text)
				}
				fdwAssertSource(t, p, b, r, want)
			})
		}
		run(fixture.Exec("ANALYZE fdw_data", 0))
		fdwPopulation(run, 20)
		fdwWantEstimate(t, p, c, "label='common'", 14)
		fdwWantEstimate(t, p, c, "label='rare'", 2)
		fdwWantEstimate(t, p, c, "label IS NULL", 4)
		fdwAssertSource(t, p, b, r, want)
		if !b.PG {
			fdwInformationControls(t, p, b, c, r, run)
			fdwAssertSource(t, p, b, r, want)
		}
		fdwSourceExec(t, p, b, r, run, "DELETE FROM fdw_source.data", 20)
		fdwAnalyzeScaledCounts(t, p, b, c, r, run)
	})
}
