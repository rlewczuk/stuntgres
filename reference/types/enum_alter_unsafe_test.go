package reference_test

import (
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumAlterUnsafeProducers(t *testing.T) {
	enumAlterScenario(t, func(t *testing.T, f fixture.ControlsReferenceFixture, pg bool) {
		for _, q := range []string{"CREATE TYPE mood AS ENUM ('z','q')", "CREATE DOMAIN alter_wrap AS mood", "CREATE TYPE alter_box AS (v mood)", "CREATE TABLE alter_inputs(v mood)"} {
			enumAlterExec(t, f, f.A, q)
		}
		cases := []struct {
			q, label, pos string
			query         bool
			args          []any
			preparePhase  string
		}{
			{"SELECT 'pending'::mood", "pending", "8", true, nil, "prepare"},
			{"SELECT /*é*/ 'pending'::mood", "pending", "14", true, nil, "prepare"},
			{"INSERT INTO alter_inputs VALUES ('pending')", "pending", "34", false, nil, "prepare"},
			{"SELECT 'pending'::text::mood", "pending", "", true, nil, "execute"},
			{"SELECT $1::mood", "pending", "", true, []any{"pending"}, "execute"},
			{"INSERT INTO alter_inputs VALUES ($1)", "pending", "", false, []any{[]byte("pending")}, "execute"},
			{"SELECT enum_last(NULL::mood)", "pending", "", true, nil, "execute"},
			{"SELECT enum_range(NULL::mood)", "pending", "", true, nil, "execute"},
			{"SELECT enum_range('q'::mood,NULL::mood)", "pending", "", true, nil, "execute"},
			{"SELECT 'pending'::alter_wrap", "pending", "8", true, nil, "prepare"},
			{"SELECT $1::alter_wrap", "pending", "", true, []any{"pending"}, "execute"},
			{"SELECT '(pending)'::alter_box", "pending", "8", true, nil, "prepare"},
			{"SELECT $1::alter_box", "pending", "", true, []any{"(pending)"}, "execute"},
		}
		for i, tc := range cases {
			t.Run(strconv.Itoa(i), func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					enumAlterExec(t, f, f.A, "BEGIN")
					enumAlterExec(t, f, f.A, "ALTER TYPE mood ADD VALUE 'pending'")
					enumAlterExec(t, f, f.A, "SAVEPOINT producer")
					want := enumAlterUnsafe(tc.label, "mood")
					want.Position = tc.pos
					if len(tc.args) > 0 {
						want.Context = "unnamed portal parameter $1 = '...'"
					}
					phase := "execute"
					if prepared {
						phase = tc.preparePhase
					}
					enumAlterError(t, f, f.A, tc.q, prepared, tc.query, phase, want, tc.args...)
					enumAlterExec(t, f, f.A, "ROLLBACK TO producer")
					enumAlterRows(t, f, f.A, "SELECT enum_first(NULL::mood) AS v", "z")
					fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT enum_range('z'::mood,'q'::mood)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"{z,q}"}}))
					fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT enum_range('q'::mood,'z'::mood)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"{}"}}))
					enumAlterExec(t, f, f.A, "ROLLBACK")
				}
			})
		}
		for _, prepared := range []bool{false, true} {
			enumAlterExec(t, f, f.A, "BEGIN")
			enumAlterExec(t, f, f.A, "ALTER TYPE mood ADD VALUE 'first_pending' BEFORE 'z'")
			enumAlterExec(t, f, f.A, "SAVEPOINT first_use")
			enumAlterError(t, f, f.A, "SELECT enum_first(NULL::mood)", prepared, true, "execute", enumAlterUnsafe("first_pending", "mood"))
			enumAlterExec(t, f, f.A, "ROLLBACK TO first_use")
			enumAlterRows(t, f, f.A, "SELECT enum_last(NULL::mood) AS v", "q")
			enumAlterExec(t, f, f.A, "ROLLBACK")
		}
		// A handle admitted before ADD still imports bound inputs at execution.
		bound, err := f.A.PrepareContext(f.Pair.Ctx, "SELECT $1::mood AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer bound.Close()
		enumAlterExec(t, f, f.A, "BEGIN")
		enumAlterExec(t, f, f.A, "ALTER TYPE mood ADD VALUE 'held_pending'")
		enumAlterExec(t, f, f.A, "SAVEPOINT held_use")
		rows, err := bound.QueryContext(f.Pair.Ctx, "held_pending")
		if rows != nil {
			rows.Close()
			t.Error("unsafe held parameter exposed rows")
		}
		if err == nil {
			t.Error("unsafe held parameter succeeded")
		} else {
			got := fixture.ExtractDiagnostic(t, err)
			want := enumAlterUnsafe("held_pending", "mood")
			want.Context = "unnamed portal parameter $1 = '...'"
			if got != want {
				t.Errorf("held parameter got %#v want %#v", got, want)
			}
		}
		enumAlterExec(t, f, f.A, "ROLLBACK TO held_use")
		enumAlterExec(t, f, f.A, "ROLLBACK")
		// New catalog members may be neighbors and rename targets while unsafe.
		enumAlterExec(t, f, f.A, "BEGIN")
		enumAlterExec(t, f, f.A, "ALTER TYPE mood ADD VALUE 'pending'")
		enumAlterExec(t, f, f.A, "ALTER TYPE mood ADD VALUE IF NOT EXISTS 'z' BEFORE 'absent'")
		enumAlterRows(t, f, f.A, "SELECT 'z'::mood AS v", "z")
		enumAlterExec(t, f, f.A, "ALTER TYPE mood ADD VALUE 'middle' BEFORE 'pending'")
		enumAlterExec(t, f, f.A, "ALTER TYPE mood RENAME VALUE 'pending' TO 'renamed'")
		enumAlterExec(t, f, f.A, "SAVEPOINT renamed_use")
		want := enumAlterUnsafe("renamed", "mood")
		want.Position = "8"
		enumAlterError(t, f, f.A, "SELECT 'renamed'::mood", false, true, "execute", want)
		enumAlterExec(t, f, f.A, "ROLLBACK TO renamed_use")
		enumAlterExec(t, f, f.A, "COMMIT")
		enumAlterRows(t, f, f.A, "SELECT 'renamed'::mood AS v", "renamed")
	})
}
