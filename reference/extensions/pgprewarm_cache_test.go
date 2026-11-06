package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgPrewarmCache(t *testing.T) {
	for _, mode := range []string{"buffer", "read", "prefetch"} {
		t.Run(mode, func(t *testing.T) {
			pwScenario(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				pwCacheInstall(t, p, b, c)
				bcDDL(t, p, b, c, "CREATE TABLE pw_part.unrequested(k integer)")
				pwInsert(t, p, b, c, "pw_part.heap", 1000)
				pwInsert(t, p, b, c, "pw_part.unrequested", 1)
				extent := pwExtent(t, p, b, c, "pw_part.heap", "main", 6)
				if extent < 3 { t.Fatal("fixture lacks requested independent blocks", extent) }
				before := pwCoordinates(t, p, b, c, "pw_part.heap")
				other := pwCoordinates(t, p, b, c, "pw_part.unrequested")
				if !b.PG && len(before) != 0 { t.Fatal("DML warmed unread native heap", before) }
				for iteration := int64(1); iteration <= 7; iteration++ {
					// Exactly one actual invocation per iteration: usage is not
					// guessed from a direct/fresh/held helper's call multiplier.
					bcRun(t, p, b, c, pwCount(pwCall, 2, "pw_part.heap", mode, "main", int64(1), int64(2)))
					if b.PG && mode != "buffer" { continue }
					after := pwCoordinates(t, p, b, c, "pw_part.heap")
					pwResident(t, p, b, c, "pw_part.heap", 0, 1, 2)
					if !b.PG {
						if len(after) != 2 || after[[2]int64{0, 0}] != nil { t.Fatal("range admitted unrequested coordinates", after) }
						want := min(iteration, int64(5))
						for key, row := range after {
							if row[7] != want || row[6] != false { t.Fatal("logical admission usage/dirty", key, row, want) }
							if old := before[key]; old != nil && old[0] != row[0] { t.Fatal("resident repeat changed slot", old, row) }
						}
						pwSameCoordinates(t, other, pwCoordinates(t, p, b, c, "pw_part.unrequested"))
					}
					before = after
				}
				// Reversed but individually valid bounds are not errors and do
				// not touch residency/usage. Invalid requests likewise do no work.
				before = pwCoordinates(t, p, b, c, "pw_part.heap")
				bcRun(t, p, b, c, pwCount(pwCall, 0, "pw_part.heap", mode, "main", int64(2), int64(1)))
				pwBodyError(t, p, b, c, pwError(pwCall, "22023", fmt.Sprintf("starting block number must be between 0 and %d", extent-1), "pw_part.heap", mode, "main", int64(-1), nil))
				if !b.PG { pwSameCoordinates(t, before, pwCoordinates(t, p, b, c, "pw_part.heap")) }
			})
		})
	}
}
