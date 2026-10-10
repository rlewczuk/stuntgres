package reference_test

import "testing"

// REL_18_0 partbounds.c: check_new_partition_bound uses the deciding lower
// component for an empty range (last finite component on full tuple equality).
// Its ordered-bound search
// selects the partition containing the new lower bound, or the next partition
// in that lower bound's gap, independent of sibling creation order.
// https://raw.githubusercontent.com/postgres/postgres/REL_18_0/src/backend/partitioning/partbounds.c
func TestReferenceRangePartitionOrderedDiagnostics(t *testing.T) {
	cases := []struct {
		name, lower, upper, message, detail, token string
	}{
		{"reversed_second_component", "1,5", "1,3", `empty range bound specified for partition "bad"`, "Specified lower bound (1, 5) is greater than or equal to upper bound (1, 3).", "5) TO"},
		{"equal_empty_last_component", "1,5", "1,5", `empty range bound specified for partition "bad"`, "Specified lower bound (1, 5) is greater than or equal to upper bound (1, 5).", "5) TO"},
		{"spanning_from_gap", "-1,0", "15,0", `partition "bad" would overlap partition "rp_order_low"`, "", "15,0)"},
		{"spanning_from_inside_later_component", "0,2", "15,0", `partition "bad" would overlap partition "rp_order_low"`, "", "2) TO"},
		{"overlap_equal_lower_first_component", "0,0", "15,0", `partition "bad" would overlap partition "rp_order_low"`, "", "0,0) TO"},
	}
	for _, c := range cases {
		for _, route := range []struct {
			name     string
			prepared bool
		}{{"direct", false}, {"prepared", true}} {
			t.Run(c.name+"/"+route.name, func(t *testing.T) {
				referenceRangeScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					run(referenceExec("CREATE TABLE rp_order(a INT,b INT,payload INT) PARTITION BY RANGE(a,b)", 0))
					// Deliberately create the higher range first: creation order
					// must not determine the exposed overlap witness.
					run(referenceExec("CREATE TABLE rp_order_high PARTITION OF rp_order FOR VALUES FROM(10,0) TO(20,0)", 0))
					run(referenceExec("CREATE TABLE rp_order_low PARTITION OF rp_order FOR VALUES FROM(0,0) TO(5,0)", 0))
					run(referenceExec("INSERT INTO rp_order VALUES(1,0,7),(11,0,8)", 2))
					q := "CREATE TABLE bad PARTITION OF rp_order(CONSTRAINT rp_order_bad_unique UNIQUE(payload),CONSTRAINT rp_order_bad_check CHECK(payload>0)) FOR VALUES FROM(" + c.lower + ") TO(" + c.upper + ")"
					step := referenceRangeError(q, "42P17", c.message, referenceDiagnostic{Detail: c.detail, Position: referenceRangePosition(q, c.token)})
					step.prepared = route.prepared
					run(step)
					run(referenceSelect("SELECT COUNT(*) FROM pg_class WHERE relname='bad' OR relname='rp_order_bad_unique'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					for _, missing := range []struct{ sql, name, token string }{
						{"SELECT NULL::bad", "bad", "bad"},
						{`SELECT NULL::"_bad"`, "_bad", `"_bad"`},
					} {
						run(referenceImplicitError(missing.sql, referenceDiagnostic{Code: "42704", Message: `type "` + missing.name + `" does not exist`, Position: referenceRangePosition(missing.sql, missing.token)}))
					}
					run(referenceSelect("SELECT COUNT(*) FROM pg_constraint WHERE conname='rp_order_bad_unique' OR conname='rp_order_bad_check'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(referenceSelect("SELECT relname FROM pg_class WHERE relname='rp_order' OR relname='rp_order_high' OR relname='rp_order_low' ORDER BY relname", []string{"relname"}, []string{"NAME"}, [][]any{{"rp_order"}, {"rp_order_high"}, {"rp_order_low"}}))
					run(referenceSelect("SELECT * FROM rp_order ORDER BY a,b", []string{"a", "b", "payload"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(0), int64(7)}, {int64(11), int64(0), int64(8)}}))
					run(referenceSelect("SELECT * FROM rp_order_low", []string{"a", "b", "payload"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(0), int64(7)}}))
					run(referenceSelect("SELECT * FROM rp_order_high", []string{"a", "b", "payload"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(11), int64(0), int64(8)}}))
					// The old gap remains uncovered, with no failed-child routing.
					run(referenceRangeMiss("INSERT INTO rp_order VALUES(7,0,9)", "rp_order", "a, b", "7, 0"))
					run(referenceExec("INSERT INTO rp_order VALUES(2,0,9),(12,0,10)", 2))
					run(referenceSelect("SELECT * FROM rp_order_low ORDER BY a,b", []string{"a", "b", "payload"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(0), int64(7)}, {int64(2), int64(0), int64(9)}}))
					run(referenceSelect("SELECT * FROM rp_order_high ORDER BY a,b", []string{"a", "b", "payload"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(11), int64(0), int64(8)}, {int64(12), int64(0), int64(10)}}))
				})
			})
		}
	}
}
