package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumAlterRetainedViews(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		t.Run(map[bool]string{false: "visible", true: "qualifiedHidden"}[hidden], func(t *testing.T) {
			enumAlterScenario(t, func(t *testing.T, f fixture.ControlsReferenceFixture, pg bool) {
				typ := "closure_view_mood"
				if hidden {
					enumAlterExec(t, f, f.A, "CREATE SCHEMA closure_hidden")
					typ = "closure_hidden.closure_view_mood"
					t.Cleanup(func() {
						f.A.ExecContext(f.Pair.Ctx, "ROLLBACK")
						if _, err := f.A.ExecContext(f.Pair.Ctx, "DROP SCHEMA closure_hidden CASCADE"); err != nil {
							t.Error(err)
						}
					})
				}
				for _, q := range []string{
					"CREATE TYPE " + typ + " AS ENUM ('z','q')",
					"CREATE TABLE closure_view_rows(id integer)",
					"CREATE TABLE closure_view_empty(id integer)",
					"CREATE VIEW closure_view_last AS SELECT enum_last(NULL::" + typ + ") AS v",
					"CREATE VIEW closure_view_nested AS SELECT v FROM closure_view_last",
					"CREATE VIEW closure_view_deep AS SELECT v FROM closure_view_nested",
					"CREATE VIEW closure_view_qualified AS SELECT id FROM closure_view_rows WHERE enum_last(NULL::" + typ + ") IS NOT NULL",
					"CREATE VIEW closure_view_nested_qualified AS SELECT id FROM closure_view_qualified WHERE id>0",
					"CREATE VIEW closure_view_inner_safe AS SELECT id FROM closure_view_rows WHERE id>0",
					"CREATE VIEW closure_view_outer_qualified AS SELECT id FROM closure_view_inner_safe WHERE enum_last(NULL::" + typ + ") IS NOT NULL",
					"CREATE VIEW closure_view_case AS SELECT CASE WHEN id=1 THEN enum_first(NULL::" + typ + ") ELSE enum_last(NULL::" + typ + ") END AS v FROM closure_view_rows",
					"CREATE VIEW closure_view_zero AS SELECT enum_last(NULL::" + typ + ") AS v FROM closure_view_empty",
				} {
					enumAlterExec(t, f, f.A, q)
				}
				fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec("INSERT INTO closure_view_rows VALUES (1)", 1))
				steps := []fixture.Step{
					fixture.Select("SELECT v::text AS v FROM closure_view_last", []string{"v"}, []string{"TEXT"}, [][]any{{"pending"}}),
					fixture.Select("SELECT v::text AS v FROM closure_view_nested", []string{"v"}, []string{"TEXT"}, [][]any{{"pending"}}),
					fixture.Select("SELECT v::text AS v FROM closure_view_deep", []string{"v"}, []string{"TEXT"}, [][]any{{"pending"}}),
					fixture.Select("SELECT id FROM closure_view_qualified", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
					fixture.Select("SELECT id FROM closure_view_nested_qualified", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
					fixture.Exec("UPDATE closure_view_qualified SET id=id+1 WHERE id=1", 1),
					fixture.Exec("UPDATE closure_view_nested_qualified SET id=id+1 WHERE id=1", 1),
					fixture.Exec("DELETE FROM closure_view_qualified WHERE id=1", 1),
					fixture.Exec("DELETE FROM closure_view_nested_qualified WHERE id=1", 1),
					fixture.Select("SELECT id FROM closure_view_outer_qualified", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}),
					fixture.Exec("UPDATE closure_view_outer_qualified SET id=id+1 WHERE id=1", 1),
					fixture.Exec("DELETE FROM closure_view_outer_qualified WHERE id=1", 1),
				}
				safe := []fixture.Step{
					fixture.Select("SELECT v::text AS v FROM closure_view_case", []string{"v"}, []string{"TEXT"}, [][]any{{"z"}}),
					fixture.Select("SELECT v::text AS v FROM closure_view_zero", []string{"v"}, []string{"TEXT"}, nil),
					fixture.Select("SELECT v::text AS v FROM closure_view_last WHERE FALSE", []string{"v"}, []string{"TEXT"}, nil),
					fixture.Exec("UPDATE closure_view_qualified SET id=id WHERE FALSE", 0),
					// Without CHECK OPTION, INSERT does not demand the retained WHERE.
					fixture.Exec("INSERT INTO closure_view_qualified VALUES (2)", 1),
				}
				held := enumAlterClosureHold(t, f, steps)
				heldSafe := enumAlterClosureHold(t, f, safe)
				selectedCase := []fixture.Step{fixture.Select("SELECT v::text AS v FROM closure_view_case", []string{"v"}, []string{"TEXT"}, [][]any{{"pending"}})}
				heldCase := enumAlterClosureHold(t, f, selectedCase)
				enumAlterExec(t, f, f.A, "BEGIN")
				t.Cleanup(func() { f.A.ExecContext(f.Pair.Ctx, "ROLLBACK") })
				enumAlterExec(t, f, f.A, "ALTER TYPE "+typ+" ADD VALUE 'pending'")
				// Naming is independently qualified when the producer's type is
				// hidden from search_path, even though outer SQL mentions no type.
				enumAlterClosureFailures(t, f, steps, held, enumAlterReviewUnsafe("pending", typ))
				fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT id FROM closure_view_rows", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
				enumAlterClosureSuccesses(t, f, safe, heldSafe, pg)
				// Select the opposite branch through actual row state, not a
				// constant-folded expression or a freshly input unsafe literal.
				enumAlterExec(t, f, f.A, "SAVEPOINT selected_branch")
				fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec("UPDATE closure_view_rows SET id=2", 1))
				enumAlterClosureFailures(t, f, selectedCase, heldCase, enumAlterReviewUnsafe("pending", typ))
				fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT id FROM closure_view_rows", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
				enumAlterExec(t, f, f.A, "ROLLBACK TO selected_branch")
				enumAlterExec(t, f, f.A, "RELEASE selected_branch")
				enumAlterExec(t, f, f.A, "COMMIT")
				enumAlterExec(t, f, f.A, "BEGIN")
				enumAlterClosureSuccesses(t, f, steps, held, pg)
				enumAlterClosureSuccesses(t, f, safe, heldSafe, pg)
				enumAlterExec(t, f, f.A, "COMMIT")
				// A real committed write through the lowered nested qualification.
				fixture.RunStep(t, f.Pair.Ctx, held[6], f.Recorder, steps[6])
				fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT id FROM closure_view_rows", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
				fixture.RunStep(t, f.Pair.Ctx, heldCase[0], f.Recorder, selectedCase[0])
				// A second addition followed by full rollback restores both the
				// held producer's current endpoint and the catalog/provenance root.
				enumAlterExec(t, f, f.A, "BEGIN")
				enumAlterExec(t, f, f.A, "ALTER TYPE "+typ+" ADD VALUE 'aborted'")
				enumAlterClosureFailures(t, f, steps[:1], held[:1], enumAlterReviewUnsafe("aborted", typ))
				enumAlterExec(t, f, f.A, "ROLLBACK")
				absent := enumAlterDiagnostic("22P02", "invalid input value for enum "+typ+": \"aborted\"")
				absent.Position = "8"
				enumAlterError(t, f, f.A, "SELECT 'aborted'::"+typ, false, true, "execute", absent)
				enumAlterError(t, f, f.A, "SELECT 'aborted'::"+typ, true, true, "prepare", absent)
				enumAlterExec(t, f, f.A, "BEGIN")
				enumAlterClosureSuccesses(t, f, steps[:1], held[:1], pg)
				enumAlterExec(t, f, f.A, "COMMIT")
			})
		})
	}
}
