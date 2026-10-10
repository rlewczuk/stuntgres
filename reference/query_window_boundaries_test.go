package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceQueryWindowCapabilityBoundaries(t *testing.T) {
	queryWindowScenario(t, func(p *referencePair, b referenceBackend) {
		queryWindowSeed(t, p, b)
		for _, tc := range []struct {
			q, name, typ, message, marker string
			v                             []int64
		}{
			{"SELECT count(id) OVER() AS n FROM t ORDER BY id", "n", "INT8", "window function provider is unsupported", "count", []int64{7, 7, 7, 7, 7, 7, 7}},
			{"SELECT count(*) FILTER(WHERE id>2) OVER() AS n FROM t ORDER BY id", "n", "INT8", "window aggregate FILTER is unsupported", "FILTER", []int64{5, 5, 5, 5, 5, 5, 5}},
			{"SELECT count(*) OVER(ORDER BY id ROWS UNBOUNDED PRECEDING) AS n FROM t ORDER BY id", "n", "INT8", "explicit window frames are unsupported", "ROWS", []int64{1, 2, 3, 4, 5, 6, 7}},
			{"SELECT sum(id) OVER() AS n FROM t ORDER BY id", "n", "INT8", "window function provider is unsupported", "sum", []int64{28, 28, 28, 28, 28, 28, 28}},
		} {
			rows := make([][]any, len(tc.v))
			for i, v := range tc.v {
				rows[i] = []any{v}
			}
			s := referenceSelect(tc.q, []string{tc.name}, []string{tc.typ}, rows)
			if !b.pg {
				s = referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: tc.message, Position: referencePosition(strings.Index(tc.q, tc.marker) + 1)})
				s.selectRows = true
			}
			queryWindowRoutes(t, p, b, s)
		}
		// Malformed window syntax must reject the entire input before any prefix writes.
		for _, prepared := range []bool{false, true} {
			q := "INSERT INTO empty_t VALUES(9,'x',1);SELECT count(*) OVER(ORDER BY) FROM t"
			s := referenceImplicitError(q, referenceDiagnostic{Code: "42601", Message: `syntax error at or near ")"`, Position: referencePosition(strings.Index(q, "BY)") + 3)})
			s.prepared = prepared
			if prepared {
				// lib/pq rejects multi-command preparation before parser errors; use the
				// actual single malformed statement for its Prepare analysis companion.
				s.query = "SELECT count(*) OVER(ORDER BY) FROM t"
				s.diagnostic.Position = "30"
				s.errorStage = "prepare"
			}
			queryWindowRun(t, p, b, s)
			queryWindowRun(t, p, b, referenceSelect("SELECT id FROM empty_t", []string{"id"}, []string{"INT4"}, nil))
		}
	})
}
