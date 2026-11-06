package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgPrewarmRanges(t *testing.T) {
	for _, rows := range []int{0, 1, 185, 186, 1000} {
		t.Run(fmt.Sprintf("rows_%d", rows), func(t *testing.T) {
			pwScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				if rows > 0 { pwInsert(t, p, b, c, "pw_part.heap", rows) }
				// Each integer tuple is 40 allocation bytes plus a four-byte line
				// pointer. Compact payload capacity is 8168, independent of PG pages.
				native := (int64(rows)*44 + 8167) / 8168
				n := pwExtent(t, p, b, c, "pw_part.heap", "main", native)
				if (rows == 0 && n != 0) || (rows > 0 && n < 1) || (rows == 1000 && n < 3) { t.Fatal("independent extent", rows, n) }
				for _, mode := range []string{"buffer", "read", "prefetch"} {
					pwRoutes(t, p, b, c, pwCount(pwCall, n, "pw_part.heap", mode, "main", nil, nil))
					pwRoutes(t, p, b, c, pwCount(pwCall, n, "pw_part.heap", mode, "main", nil, nil))
					if n == 0 {
						for _, endpoints := range [][2]any{{int64(0), nil}, {nil, int64(0)}, {int64(0), int64(0)}} {
							which := "starting"
							if endpoints[0] == nil { which = "ending" }
							pwBodyError(t, p, b, c, pwError(pwCall, "22023", which+" block number must be between 0 and -1", "pw_part.heap", mode, "main", endpoints[0], endpoints[1]))
						}
						continue
					}
					for _, tc := range []struct{ first, last any; count int64 }{{nil, int64(0), 1}, {int64(0), int64(0), 1}, {int64(0), nil, n}, {nil, n-1, n}, {n-1, nil, 1}} {
						pwRoutes(t, p, b, c, pwCount(pwCall, tc.count, "pw_part.heap", mode, "main", tc.first, tc.last))
					}
					if n >= 3 {
						pwRoutes(t, p, b, c, pwCount(pwCall, 2, "pw_part.heap", mode, "main", int64(1), int64(2)))
						pwRoutes(t, p, b, c, pwCount(pwCall, 0, "pw_part.heap", mode, "main", int64(2), int64(1)))
					}
				}
			})
		})
	}
}
