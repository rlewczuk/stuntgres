package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOverexplainFeatureSmoke(t *testing.T) {
	overexplainScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		overexplainSeed(t, p, b, c)
		q := "EXPLAIN (DEBUG, FORMAT JSON) SELECT id,v FROM overexplain_data WHERE id>1 ORDER BY id DESC"
		overexplainError(t, p, b, c, q, overexplainDiagnostic(q, "42601", `unrecognized EXPLAIN option "debug"`, "DEBUG"), "execute", true)
		overexplainExec(t, p, b, c, "LOAD 'pg_overexplain'")
		for _, format := range []string{"TEXT", "XML", "JSON", "YAML"} {
			for _, prepared := range []bool{false, true} {
				q = overexplainSQL(format, "DEBUG, VERBOSE, COSTS FALSE", "SELECT id,v FROM overexplain_data WHERE id>1 ORDER BY id DESC")
				r := overexplainQuery(t, p, c, q, format, prepared)
				overexplainDebug(t, b, r, format, true)
				if format == "TEXT" {
					explainContains(t, []string{r.text}, "overexplain_data", "Sort", "id")
				} else {
					explainJSONFind(t, r.document, "Relation Name", "overexplain_data")
					if !b.PG {
						for _, typ := range []string{"Project", "Sort", "Filter", "Logical Scan"} {
							explainJSONFind(t, r.document, "Node Type", typ)
						}
						overexplainNativeTargets(t, r, "id", "v")
					}
				}
				q = overexplainSQL(format, "DEBUG FALSE, COSTS FALSE", "SELECT id FROM overexplain_data ORDER BY id")
				overexplainDebug(t, b, overexplainQuery(t, p, c, q, format, prepared), format, false)
			}
		}
		q = "EXPLAIN (DEBUG 'bad') UPDATE overexplain_data SET id=99"
		overexplainError(t, p, b, c, q, fixture.ControlsReferenceDiagnostic("42601", "debug requires a Boolean value"), "execute", true)
		overexplainInvariant(t, p, b, c)
	})
}
