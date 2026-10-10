package reference_test

import "testing"

func TestReferenceRangePartitionTemporaryBoundary(t *testing.T) {
	referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		conn, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(step referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, step) }
		run(referenceExec("CREATE TEMP SEQUENCE rp_temp_partition_seed", 0))
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
				step := referenceExec(query, 0)
				if !b.pg {
					step = referenceRangeError(query, "0A000", "temporary partition definitions are unsupported", referenceDiagnostic{Position: referenceRangePosition(query, "PARTITION")})
				}
				step.prepared = prepared
				run(step)
				count := int64(0)
				if b.pg {
					count = 1
				}
				run(referenceSelect("SELECT COUNT(*) FROM pg_class WHERE relname='"+name+"'", []string{"count"}, []string{"INT8"}, [][]any{{count}}))
				if b.pg {
					run(referenceExec("DROP TABLE pg_temp."+name, 0))
				}
			}
		}
		// Rejection constructs neither a virtual TEMP member nor a physical heap;
		// it also does not demand the unrelated generator or disturb its live token.
		run(referenceSelect("SELECT nextval('rp_temp_partition_seed')", []string{"nextval"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(referenceExec("DROP SEQUENCE rp_temp_partition_seed", 0))
	})
}
