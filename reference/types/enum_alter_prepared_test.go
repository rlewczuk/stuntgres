package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumAlterPreparedIdentity(t *testing.T) {
	enumAlterScenario(t, func(t *testing.T, f fixture.ControlsReferenceFixture, pg bool) {
		for _, q := range []string{"CREATE TYPE mood AS ENUM ('z','q')", "CREATE TABLE alter_rows(id integer,m mood)", "INSERT INTO alter_rows VALUES (1,'q')"} {
			count := int64(0)
			if q == "INSERT INTO alter_rows VALUES (1,'q')" {
				count = 1
			}
			fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec(q, count))
		}
		queries := []string{"SELECT 'q'::mood AS v", "INSERT INTO alter_rows VALUES (2,'q')", "SELECT m AS v FROM alter_rows WHERE m='q' ORDER BY id", "SELECT $1::mood AS v", "SELECT enum_last(NULL::mood) AS v"}
		held := make([]fixture.HeldStatement, len(queries))
		for i, q := range queries {
			s, err := f.A.PrepareContext(f.Pair.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			held[i] = fixture.HeldStatement{Executor: f.A, Statement: s, SQL: q}
		}
		for _, phase := range []string{"original", "added", "renamed"} {
			if phase == "added" {
				enumAlterExec(t, f, f.A, "ALTER TYPE mood ADD VALUE 'a' BEFORE 'q'")
			}
			if phase == "renamed" {
				enumAlterExec(t, f, f.A, "ALTER TYPE mood RENAME VALUE 'q' TO 'end'")
			}
			label := "q"
			if phase == "renamed" {
				label = "end"
			}
			enumAlterRows(t, f, held[0], queries[0], label)
			fixture.RunStep(t, f.Pair.Ctx, held[1], f.Recorder, fixture.Exec(queries[1], 1))
			want := []any{label, label}
			if phase == "added" {
				want = append(want, label)
			}
			if phase == "renamed" {
				want = append(want, label, label)
			}
			// The SAME predicate contains the old spelling, not a rebuilt query.
			s := fixture.Select(queries[2], []string{"v"}, []string{""}, nil)
			for _, v := range want {
				s.Rows = append(s.Rows, []any{v})
			}
			s.CheckMetadata = fixture.EnumReferenceMetadata(pg, true)
			fixture.EnumReferenceRunStep(t, f.Pair.Ctx, held[2], f.Recorder, s)
			for _, arg := range []any{label, []byte(label), nil} {
				s := fixture.Select(queries[3], []string{"v"}, []string{""}, [][]any{{arg}}, arg)
				if arg != nil {
					s.Rows = [][]any{{label}}
				}
				s.CheckMetadata = fixture.EnumReferenceMetadata(pg, false)
				fixture.EnumReferenceRunStep(t, f.Pair.Ctx, held[3], f.Recorder, s)
			}
			enumAlterRows(t, f, held[4], queries[4], label)
		}
		old := enumAlterDiagnostic("22P02", `invalid input value for enum mood: "q"`)
		old.Position = "8"
		for _, prepared := range []bool{false, true} {
			stage := "execute"
			if prepared {
				stage = "prepare"
			}
			enumAlterError(t, f, f.A, "SELECT 'q'::mood AS v", prepared, true, stage, old)
		}
		bound := enumAlterDiagnostic("22P02", `invalid input value for enum mood: "q"`)
		bound.Context = "unnamed portal parameter $1 = '...'"
		enumAlterError(t, f, f.A, queries[3], true, true, "execute", bound, "q")
		// Preparing a utility must not publish its label; repeated execution uses
		// the same handle and returns the independently specified duplicate.
		q := "ALTER TYPE mood ADD VALUE 'later'"
		s, err := f.A.PrepareContext(f.Pair.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		d := enumAlterDiagnostic("22P02", `invalid input value for enum mood: "later"`)
		d.Position = "8"
		enumAlterError(t, f, f.A, "SELECT 'later'::mood", false, true, "execute", d)
		fixture.RunStep(t, f.Pair.Ctx, fixture.HeldStatement{Executor: f.A, Statement: s, SQL: q}, f.Recorder, fixture.Exec(q, 0))
		_, err = s.ExecContext(f.Pair.Ctx)
		fixture.EnumReferenceAssertDiagnostic(t, err, enumAlterDiagnostic("42710", `enum label "later" already exists`))
		q = "ALTER TYPE mood RENAME VALUE 'later' TO 'final'"
		rename, err := f.A.PrepareContext(f.Pair.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rename.Close()
		enumAlterRows(t, f, f.A, "SELECT 'later'::mood AS v", "later")
		fixture.RunStep(t, f.Pair.Ctx, fixture.HeldStatement{Executor: f.A, Statement: rename, SQL: q}, f.Recorder, fixture.Select(q, nil, nil, nil))
		enumAlterRows(t, f, f.A, "SELECT 'final'::mood AS v", "final")
		rows, err := rename.QueryContext(f.Pair.Ctx)
		if rows != nil {
			rows.Close()
			t.Error("repeated rename exposed rows")
		}
		fixture.EnumReferenceAssertDiagnostic(t, err, enumAlterDiagnostic("22023", `"later" is not an existing enum label`))
	})
}
