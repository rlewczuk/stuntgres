package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumAlterRoutedSafety(t *testing.T) {
	enumAlterScenario(t, func(t *testing.T, f fixture.ControlsReferenceFixture, pg bool) {
		for _, q := range []string{
			"CREATE TYPE closure_route_mood AS ENUM ('z','q')",
			"CREATE TABLE closure_route(id integer) PARTITION BY RANGE(id)",
			"CREATE TABLE closure_route_leaf PARTITION OF closure_route(CONSTRAINT closure_leaf_safe CHECK(enum_last(NULL::closure_route_mood) IS NOT NULL)) FOR VALUES FROM (0) TO (10)",
			"CREATE TABLE closure_route_conditional PARTITION OF closure_route(CONSTRAINT closure_leaf_conditional CHECK(CASE WHEN id=11 THEN true ELSE enum_last(NULL::closure_route_mood) IS NOT NULL END)) FOR VALUES FROM (10) TO (20)",
			"CREATE TABLE closure_route_plain PARTITION OF closure_route FOR VALUES FROM (20) TO (30)",
			"CREATE TABLE closure_route_middle PARTITION OF closure_route FOR VALUES FROM (30) TO (40) PARTITION BY RANGE(id)",
			"CREATE TABLE closure_route_deep PARTITION OF closure_route_middle(CONSTRAINT closure_deep_safe CHECK(enum_last(NULL::closure_route_mood) IS NOT NULL)) FOR VALUES FROM (30) TO (40)",
		} {
			enumAlterExec(t, f, f.A, q)
		}
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec("INSERT INTO closure_route VALUES (0)", 1))
		steps := []fixture.Step{
			fixture.Exec("INSERT INTO closure_route VALUES (1)", 1),
			fixture.Exec("INSERT INTO closure_route(id) SELECT 2", 1),
			fixture.Exec("INSERT INTO closure_route_leaf VALUES (3)", 1),
			fixture.Exec("INSERT INTO closure_route VALUES (12)", 1),
			fixture.Exec("INSERT INTO closure_route VALUES (31)", 1),
			fixture.Exec("INSERT INTO closure_route_middle(id) SELECT 32", 1),
			fixture.Exec("INSERT INTO closure_route VALUES (21),(1)", 2),
		}
		safe := []fixture.Step{
			fixture.Exec("INSERT INTO closure_route VALUES (11)", 1),
			fixture.Exec("INSERT INTO closure_route_conditional VALUES (11)", 1),
			fixture.Exec("INSERT INTO closure_route VALUES (21)", 1),
			fixture.Exec("INSERT INTO closure_route(id) SELECT 1 WHERE FALSE", 0),
		}
		held := enumAlterClosureHold(t, f, steps)
		heldSafe := enumAlterClosureHold(t, f, safe)
		enumAlterExec(t, f, f.A, "BEGIN")
		t.Cleanup(func() { f.A.ExecContext(f.Pair.Ctx, "ROLLBACK") })
		enumAlterExec(t, f, f.A, "ALTER TYPE closure_route_mood ADD VALUE 'pending'")
		enumAlterClosureFailures(t, f, steps, held, enumAlterReviewUnsafe("pending", "closure_route_mood"))
		// Includes the mixed safe/unsafe multirow insertion: no physical leaf
		// retains its earlier safe row when a later routed CHECK fails.
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT id FROM closure_route ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}}))
		enumAlterClosureSuccesses(t, f, safe, heldSafe, pg)
		enumAlterExec(t, f, f.A, "COMMIT")
		enumAlterExec(t, f, f.A, "BEGIN")
		enumAlterClosureSuccesses(t, f, steps, held, pg)
		enumAlterClosureSuccesses(t, f, safe, heldSafe, pg)
		enumAlterExec(t, f, f.A, "COMMIT")
		fixture.RunStep(t, f.Pair.Ctx, held[0], f.Recorder, steps[0])
		fixture.RunStep(t, f.Pair.Ctx, held[4], f.Recorder, steps[4])
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT id FROM closure_route_leaf ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}}))
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT id FROM closure_route_deep", []string{"id"}, []string{"INT4"}, [][]any{{int64(31)}}))
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT id FROM closure_route ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(31)}}))
	})
}
