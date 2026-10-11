package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumAlterRetainedSafety(t *testing.T) {
	enumAlterScenario(t, func(t *testing.T, f fixture.ControlsReferenceFixture, pg bool) {
		for _, q := range []string{
			"CREATE TYPE review_mood AS ENUM ('z','q')",
			"CREATE TABLE review_default(id integer,m review_mood DEFAULT enum_last(NULL::review_mood))",
			"CREATE TABLE review_nested(id integer,m review_mood DEFAULT COALESCE(enum_last(NULL::review_mood),'z'::review_mood))",
			"CREATE TABLE review_safe(id integer,m review_mood DEFAULT COALESCE('z'::review_mood,enum_last(NULL::review_mood)))",
			"CREATE TABLE review_first(id integer,m review_mood DEFAULT enum_first(NULL::review_mood))",
			"CREATE TABLE review_check(id integer,m review_mood,CHECK(m<=enum_last(NULL::review_mood)))",
			"CREATE TABLE review_integer_check(id integer,CONSTRAINT review_integer_safe CHECK(enum_last(NULL::review_mood) IS NOT NULL))",
			"CREATE TABLE review_conditional(id integer,CHECK(CASE WHEN id=7 THEN enum_last(NULL::review_mood) IS NOT NULL ELSE true END))",
			"CREATE TABLE review_case(id integer,m review_mood DEFAULT CASE WHEN enum_first(NULL::review_mood)='z'::review_mood THEN 'z'::review_mood ELSE enum_last(NULL::review_mood) END)",
			"CREATE TABLE review_range(id integer,v text DEFAULT enum_range(NULL::review_mood)::text)",
			"CREATE TABLE review_range_safe(id integer,v text DEFAULT enum_range('z'::review_mood,'q'::review_mood)::text)",
		} {
			enumAlterExec(t, f, f.A, q)
		}
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec("INSERT INTO review_default(id) VALUES (0)", 1))
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec("INSERT INTO review_check VALUES (0,'z')", 1))
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec("INSERT INTO review_integer_check VALUES (0)", 1))
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec("INSERT INTO review_conditional VALUES (0)", 1))
		// Analyze real retained write handles before the enum has a new member.
		var held []*sql.Stmt
		for _, q := range []string{"INSERT INTO review_default(id) VALUES (10)", "INSERT INTO review_default(id) SELECT 11", "INSERT INTO review_default DEFAULT VALUES", "UPDATE review_default SET m=DEFAULT WHERE id=0", "INSERT INTO review_nested(id) VALUES (10)", "INSERT INTO review_integer_check VALUES (10)", "UPDATE review_integer_check SET id=0 WHERE id=0", "INSERT INTO review_conditional VALUES (7)", "INSERT INTO review_range(id) VALUES (10)"} {
			if !pg && strings.Contains(q, "SET m=DEFAULT") {
				// Existing native capability boundary, not an enum producer path.
				boundary := enumAlterDiagnostic("0A000", "UPDATE DEFAULT is unsupported")
				boundary.Position = strconv.Itoa(strings.Index(q, "DEFAULT") + 1)
				enumAlterError(t, f, f.A, q, false, false, "execute", boundary)
				enumAlterError(t, f, f.A, q, true, false, "prepare", boundary)
				continue
			}
			stmt, err := f.A.PrepareContext(f.Pair.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held = append(held, stmt)
		}
		enumAlterExec(t, f, f.A, "BEGIN")
		t.Cleanup(func() { f.A.ExecContext(f.Pair.Ctx, "ROLLBACK") })
		enumAlterExec(t, f, f.A, "ALTER TYPE review_mood ADD VALUE 'pending'")
		want := enumAlterReviewUnsafe("pending", "review_mood")
		for _, q := range []string{
			"INSERT INTO review_default(id) VALUES (1)",
			"INSERT INTO review_default(id) SELECT 2",
			"INSERT INTO review_default DEFAULT VALUES",
			"UPDATE review_default SET m=DEFAULT WHERE id=0",
			"INSERT INTO review_range(id) VALUES (1)",
			"INSERT INTO review_nested(id) VALUES (1)",
			"INSERT INTO review_nested(id) SELECT 2",
			"UPDATE review_check SET id=1 WHERE id=0",
			"INSERT INTO review_check SELECT 2,m FROM review_check WHERE id=0",
			"INSERT INTO review_check VALUES (3,'z')",
			"INSERT INTO review_integer_check VALUES (1)",
			"INSERT INTO review_integer_check SELECT 2",
			"UPDATE review_integer_check SET id=1 WHERE id=0",
			"UPDATE review_conditional SET id=7 WHERE id=0",
		} {
			if !pg && strings.Contains(q, "SET m=DEFAULT") {
				continue
			} // Boundary asserted before BEGIN.
			for _, prepared := range []bool{false, true} {
				enumAlterExec(t, f, f.A, "SAVEPOINT producer")
				enumAlterError(t, f, f.A, q, prepared, false, "execute", want)
				enumAlterExec(t, f, f.A, "ROLLBACK TO producer")
				enumAlterExec(t, f, f.A, "RELEASE producer")
			}
		}
		for _, stmt := range held {
			enumAlterExec(t, f, f.A, "SAVEPOINT held_producer")
			result, err := stmt.ExecContext(f.Pair.Ctx)
			if result != nil || err == nil {
				t.Errorf("unsafe held producer exposed result or succeeded: %v", err)
			} else if got := fixture.ExtractDiagnostic(t, err); got != want {
				t.Errorf("held producer got %#v want %#v", got, want)
			}
			enumAlterExec(t, f, f.A, "ROLLBACK TO held_producer")
			enumAlterExec(t, f, f.A, "RELEASE held_producer")
		}
		// Failed producers insert no rows and preserve old stored values.
		for _, q := range []string{"SELECT id,m::text AS m FROM review_default", "SELECT id,m::text AS m FROM review_check"} {
			value := "z"
			if q == "SELECT id,m::text AS m FROM review_default" {
				value = "q"
			}
			fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select(q, []string{"id", "m"}, []string{"INT4", "TEXT"}, [][]any{{int64(0), value}}))
		}
		for _, table := range []string{"review_integer_check", "review_conditional"} {
			fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT id FROM "+table, []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}}))
		}
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT id FROM review_nested", []string{"id"}, []string{"INT4"}, nil))
		// Supplying a column bypasses its default; safe/unused producers remain legal.
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"INSERT INTO review_default VALUES (4,'z')", "INSERT INTO review_safe(id) VALUES (1)", "INSERT INTO review_first(id) SELECT 1", "INSERT INTO review_case(id) VALUES (1)", "INSERT INTO review_range_safe(id) VALUES (1)", "INSERT INTO review_conditional VALUES (6)", "UPDATE review_default SET id=0 WHERE id=0"} {
				s := fixture.Exec(q, 1)
				s.Prepared = prepared
				fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, s)
			}
		}
		for _, table := range []string{"review_safe", "review_first", "review_case"} {
			fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT m::text AS v FROM "+table, []string{"v"}, []string{"TEXT"}, [][]any{{"z"}, {"z"}}))
		}
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT v FROM review_range_safe", []string{"v"}, []string{"TEXT"}, [][]any{{"{z,q}"}, {"{z,q}"}}))
		enumAlterExec(t, f, f.A, "COMMIT")
		for _, stmt := range held {
			result, err := stmt.ExecContext(f.Pair.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LockAffected(t, result, 1)
		}
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"INSERT INTO review_default(id) VALUES (8)", "INSERT INTO review_default(id) SELECT 9", "INSERT INTO review_nested(id) VALUES (8)", "UPDATE review_default SET m=DEFAULT WHERE id=0", "UPDATE review_check SET id=0 WHERE id=0", "UPDATE review_conditional SET id=0 WHERE id=0"} {
				if !pg && strings.Contains(q, "SET m=DEFAULT") {
					continue
				}
				s := fixture.Exec(q, 1)
				s.Prepared = prepared
				fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, s)
			}
		}
		oldDefault := "q"
		if pg {
			oldDefault = "pending"
		} // Native excludes UPDATE DEFAULT.
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT m::text AS v FROM review_default WHERE id=0", []string{"v"}, []string{"TEXT"}, [][]any{{oldDefault}}))
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT m::text AS v FROM review_nested ORDER BY id", []string{"v"}, []string{"TEXT"}, [][]any{{"pending"}, {"pending"}, {"pending"}}))
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT m::text AS v FROM review_default WHERE id=10 OR id=11 ORDER BY id", []string{"v"}, []string{"TEXT"}, [][]any{{"pending"}, {"pending"}}))
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT v FROM review_range", []string{"v"}, []string{"TEXT"}, [][]any{{"{z,q,pending}"}}))
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT m::text AS v FROM review_default WHERE id IS NULL", []string{"v"}, []string{"TEXT"}, [][]any{{"pending"}}))
	})
}
