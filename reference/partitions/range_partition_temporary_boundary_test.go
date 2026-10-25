package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangePartitionTemporaryBoundary(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, step) }
		run(fixture.Exec("CREATE TEMP SEQUENCE rp_temp_partition_seed", 0))
		for _, prepared := range []bool{false, true} {
			suffix := "direct"
			if prepared {
				suffix = "prepared"
			}
			for _, tc := range []struct{ name, head string }{
				{"explicit", "CREATE TEMP TABLE "},
				{"ifnotexists", "CREATE TEMP TABLE IF NOT EXISTS "},
				{"qualified", "CREATE TABLE pg_temp."},
			} {
				name := "rp_temp_partition_" + tc.name + "_" + suffix
				query := tc.head + name + "(k INT) PARTITION BY RANGE(k)"
				step := fixture.Exec(query, 0)
				if !b.PG {
					step = referenceRangeError(query, "0A000", "temporary partition definitions are unsupported", fixture.Diagnostic{Position: referenceRangePosition(query, "PARTITION")})
				}
				step.Prepared = prepared
				run(step)
				count := int64(0)
				if b.PG {
					count = 1
				}
				run(fixture.Select("SELECT COUNT(*) FROM pg_class WHERE relname='"+name+"'", []string{"count"}, []string{"INT8"}, [][]any{{count}}))
				if b.PG {
					run(fixture.Exec("DROP TABLE pg_temp."+name, 0))
				}
			}
		}
		// Rejection constructs neither a virtual TEMP member nor a physical heap;
		// it also does not demand the unrelated generator or disturb its live token.
		run(fixture.Select("SELECT nextval('rp_temp_partition_seed')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.Exec("DROP SEQUENCE rp_temp_partition_seed", 0))
	})
}
