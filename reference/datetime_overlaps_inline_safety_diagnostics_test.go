package reference_test

import "testing"

// Expected routes follow REL_18_0 clauses.c's duplicated-argument hazards and
// costsize.c's operator/CoerceViaIO costing, independently of native routing.
// Nonvolatile expectations were observed against pinned PostgreSQL 18.0.
// The supported scalar volatile spellings await a fresh isolated observation.
func TestReferenceDatetimeOverlapsInlineSafetyDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			c, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
			for _, q := range []string{"SET TIME ZONE 'UTC'", "SET DateStyle TO 'ISO, MDY'", "SET IntervalStyle TO 'postgres'"} {
				run(referenceExec(q, 0))
			}
			run(referenceExec("CREATE TABLE ov_route(id integer,a timestamp,b timestamp)", 0))
			run(referenceExec("INSERT INTO ov_route VALUES(1,TIMESTAMP '294276-12-31',TIMESTAMP '2000-01-01')", 1))
			nullifs := func(count int) string {
				start := "a"
				for range count {
					start = "NULLIF(" + start + ",b)"
				}
				return start
			}
			zeroAdds := func(start string, count int) string {
				for range count {
					start = "(" + start + "+INTERVAL '0 seconds')"
				}
				return start
			}
			const wrapper = `SQL function "overlaps" statement 1`
			cases := []struct{ name, start, duration, end, context string }{
				// A timestamp column and sentinel comparison column prevent NULLIF folding.
				{"nullif_cost_10", nullifs(10), "INTERVAL '1 day'", "b", ""},
				{"nullif_cost_11", nullifs(11), "INTERVAL '1 day'", "b", wrapper},
				// The existing scalar UUID generator is actually VOLATILE and always
				// non-NULL. CASE therefore preserves deterministic endpoint/duration
				// values while its reached predicate remains a surviving volatile call.
				{"duplicated_volatile_start", "CASE WHEN gen_random_uuid() IS NULL THEN b ELSE a END", "INTERVAL '1 day'", "b", wrapper},
				{"single_use_volatile_duration", "a", "CASE WHEN gen_random_uuid() IS NULL THEN INTERVAL '2 days' ELSE INTERVAL '1 day' END", "b", ""},
				{"single_use_volatile_endpoint", "a", "INTERVAL '1 day'", "CASE WHEN gen_random_uuid() IS NULL THEN a ELSE b END", ""},
				{"duplicated_scalar_subquery_start", "(SELECT a FROM ov_route)", "INTERVAL '1 day'", "b", wrapper},
				// timestamp->date and date->timestamp each use one cast function; the
				// row-dependent zero additions survive simplification and each cost one.
				{"function_cast_cost_10", zeroAdds("((a::date)::timestamp)", 8), "INTERVAL '1 day'", "b", ""},
				{"function_cast_cost_11", zeroAdds("((a::date)::timestamp)", 9), "INTERVAL '1 day'", "b", wrapper},
				// Each CoerceViaIO contributes its output+input pair: two round-trip
				// coercions cost four, plus six/seven surviving timestamp additions.
				{"io_cast_cost_10", zeroAdds("((a::text)::timestamp)", 6), "INTERVAL '1 day'", "b", ""},
				{"io_cast_cost_11", zeroAdds("((a::text)::timestamp)", 7), "INTERVAL '1 day'", "b", wrapper},
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					q := "SELECT (" + tc.start + "," + tc.duration + ") OVERLAPS (b," + tc.end + ") FROM ov_route"
					d := controlsReferenceDiagnostic("22008", "timestamp out of range")
					d.Context = tc.context
					datetimeOverlapsFailure(t, p, b, c, q, d)
					referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT id FROM ov_route ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
					datetimeOverlapsValue(t, p, b, c, "(TIMESTAMP '2000-01-01',INTERVAL '1 day') OVERLAPS (TIMESTAMP '2000-01-01',TIMESTAMP '2000-01-02')", true)
				})
			}
			t.Run("stable_zoned_literal_body", func(t *testing.T) {
				// The zoned duration overload is STABLE: its literal body addition must
				// not ordinarily constant-fold or acquire an immutable wrapper Context.
				q := "SELECT (TIMESTAMPTZ '294276-12-31 00:00+00',INTERVAL '1 day') OVERLAPS (TIMESTAMPTZ '2000-01-01 00:00+00',TIMESTAMPTZ '2000-01-02 00:00+00')"
				d := controlsReferenceDiagnostic("22008", "timestamp out of range")
				datetimeOverlapsFailure(t, p, b, c, q, d)
				datetimeOverlapsValue(t, p, b, c, "(TIMESTAMPTZ '2000-01-01 00:00+00',INTERVAL '1 day') OVERLAPS (TIMESTAMPTZ '2000-01-01 00:00+00',TIMESTAMPTZ '2000-01-02 00:00+00')", true)
			})
		})
	}
}
