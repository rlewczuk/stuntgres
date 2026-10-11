package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumAlterMergeViewSafety(t *testing.T) {
	enumAlterScenario(t, func(t *testing.T, f fixture.ControlsReferenceFixture, pg bool) {
		for _, q := range []string{
			"CREATE TYPE closure_write_mood AS ENUM ('z','q')",
			"CREATE TABLE closure_merge_default(id integer,m closure_write_mood DEFAULT enum_last(NULL::closure_write_mood))",
			"CREATE TABLE closure_merge_check(id integer,CONSTRAINT closure_merge_safe CHECK(enum_last(NULL::closure_write_mood) IS NOT NULL))",
			"CREATE TABLE closure_view_default_base(id integer,m closure_write_mood DEFAULT enum_last(NULL::closure_write_mood))",
			"CREATE VIEW closure_view_default_write AS SELECT id,m FROM closure_view_default_base",
			"CREATE TABLE closure_view_check_base(id integer,CONSTRAINT closure_view_base_safe CHECK(enum_last(NULL::closure_write_mood) IS NOT NULL))",
			"CREATE VIEW closure_view_check_write AS SELECT id FROM closure_view_check_base",
		} {
			enumAlterExec(t, f, f.A, q)
		}
		for _, table := range []string{"closure_merge_default", "closure_view_default_base"} {
			fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec("INSERT INTO "+table+"(id) VALUES (0)", 1))
		}
		for _, table := range []string{"closure_merge_check", "closure_view_check_base"} {
			fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec("INSERT INTO "+table+" VALUES (0)", 1))
		}
		steps := []fixture.Step{
			fixture.Exec("MERGE INTO closure_merge_default t USING (VALUES(1)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT(id) VALUES(s.id)", 1),
			fixture.Exec("MERGE INTO closure_merge_default t USING (VALUES(1)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT DEFAULT VALUES", 1),
			fixture.Exec("MERGE INTO closure_merge_check t USING (VALUES(1)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id)", 1),
			fixture.Exec("MERGE INTO closure_merge_check t USING (VALUES(0)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET id=t.id+1", 1),
			fixture.Exec("MERGE INTO closure_merge_check t USING (VALUES(0),(1)) s(id) ON t.id=s.id WHEN MATCHED THEN UPDATE SET id=t.id+2 WHEN NOT MATCHED THEN INSERT VALUES(s.id)", 2),
			fixture.Exec("INSERT INTO closure_view_default_write(id) VALUES (1)", 1),
			fixture.Exec("INSERT INTO closure_view_default_write(id) SELECT 2", 1),
			fixture.Exec("INSERT INTO closure_view_check_write VALUES (1)", 1),
			fixture.Exec("UPDATE closure_view_check_write SET id=id+1 WHERE id=0", 1),
		}
		safe := []fixture.Step{
			fixture.Exec("MERGE INTO closure_merge_default t USING (VALUES(1)) s(id) ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id,'z')", 1),
			fixture.Exec("MERGE INTO closure_merge_default t USING (SELECT 1 AS id WHERE FALSE) s ON FALSE WHEN NOT MATCHED THEN INSERT DEFAULT VALUES", 0),
			fixture.Exec("MERGE INTO closure_merge_default t USING (VALUES(1)) s(id) ON FALSE WHEN NOT MATCHED AND FALSE THEN INSERT(id) VALUES(s.id)", 0),
			fixture.Exec("MERGE INTO closure_merge_check t USING (VALUES(0)) s(id) ON t.id=s.id WHEN MATCHED AND FALSE THEN UPDATE SET id=t.id+1", 0),
			fixture.Exec("MERGE INTO closure_merge_check t USING (SELECT 1 AS id WHERE FALSE) s ON FALSE WHEN NOT MATCHED THEN INSERT VALUES(s.id)", 0),
			fixture.Exec("INSERT INTO closure_view_default_write VALUES (1,'z')", 1),
			fixture.Exec("INSERT INTO closure_view_default_write(id) SELECT 1 WHERE FALSE", 0),
			fixture.Exec("UPDATE closure_view_check_write SET id=id+1 WHERE FALSE", 0),
			fixture.Exec("INSERT INTO closure_view_check_write SELECT 1 WHERE FALSE", 0),
		}
		held := enumAlterClosureHold(t, f, steps)
		heldSafe := enumAlterClosureHold(t, f, safe)
		enumAlterExec(t, f, f.A, "BEGIN")
		t.Cleanup(func() { f.A.ExecContext(f.Pair.Ctx, "ROLLBACK") })
		enumAlterExec(t, f, f.A, "ALTER TYPE closure_write_mood ADD VALUE 'pending'")
		enumAlterClosureFailures(t, f, steps, held, enumAlterReviewUnsafe("pending", "closure_write_mood"))
		for _, table := range []string{"closure_merge_default", "closure_view_default_base"} {
			fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT id,m::text AS m FROM "+table, []string{"id", "m"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "q"}}))
		}
		for _, table := range []string{"closure_merge_check", "closure_view_check_base"} {
			fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT id FROM "+table, []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}}))
		}
		enumAlterClosureSuccesses(t, f, safe, heldSafe, pg)
		enumAlterExec(t, f, f.A, "COMMIT")
		enumAlterExec(t, f, f.A, "BEGIN")
		enumAlterClosureSuccesses(t, f, steps, held, pg)
		enumAlterClosureSuccesses(t, f, safe, heldSafe, pg)
		enumAlterExec(t, f, f.A, "COMMIT")
		// Publish actual held-before-ADD writes and independently inspect their
		// stored rows: affected counts alone cannot prove pq MERGE's effects.
		for _, i := range []int{0, 4, 5, 8} {
			enumAlterClosureStep(t, f, held[i], steps[i], pg)
		}
		for _, table := range []string{"closure_merge_default", "closure_view_default_base"} {
			fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT id,m::text AS m FROM "+table+" ORDER BY id", []string{"id", "m"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), "q"}, {int64(1), "pending"}}))
		}
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT id FROM closure_merge_check ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT id FROM closure_view_check_base", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
	})
}
