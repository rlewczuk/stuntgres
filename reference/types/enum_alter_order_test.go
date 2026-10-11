package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumAlterOrder(t *testing.T) {
	enumAlterScenario(t, func(t *testing.T, f fixture.ControlsReferenceFixture, pg bool) {
		enumAlterExec(t, f, f.A, "CREATE TYPE mood AS ENUM ('z','q')")
		enumAlterExec(t, f, f.A, "CREATE TABLE alter_order(v mood)")
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec("INSERT INTO alter_order VALUES ('q'),('z')", 2))
		for _, q := range []string{"ALTER TYPE mood ADD VALUE 'a' BEFORE 'q'", "ALTER TYPE mood ADD VALUE 'first' BEFORE 'z'", "ALTER TYPE mood ADD VALUE 'last'", "ALTER TYPE mood ADD VALUE 'b' AFTER 'a'"} {
			enumAlterExec(t, f, f.A, q)
		}
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO alter_order VALUES ($1),($2),($3),($4)", 4, "first", "a", "b", "last")))
		labels := []any{"first", "z", "a", "b", "q", "last"}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT v FROM alter_order ORDER BY v", []string{"v"}, []string{""}, nil)
			for _, label := range labels {
				s.Rows = append(s.Rows, []any{label})
			}
			s.Prepared = prepared
			s.CheckMetadata = fixture.EnumReferenceMetadata(pg, true)
			fixture.EnumReferenceRunStep(t, f.Pair.Ctx, f.A, f.Recorder, s)
			fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT 'z'::mood='a'::mood AS eq,'z'::mood<>'a'::mood AS ne,'z'::mood<'a'::mood AS lt,'z'::mood<='a'::mood AS le,'z'::mood>'a'::mood AS gt,'z'::mood>='a'::mood AS ge", []string{"eq", "ne", "lt", "le", "gt", "ge"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, true, true, false, false}}))
		}
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT enum_range(NULL::mood)::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"{first,z,a,b,q,last}"}}))
		enumAlterRows(t, f, f.A, "SELECT enum_first(NULL::mood) AS v", "first")
		enumAlterRows(t, f, f.A, "SELECT enum_last(NULL::mood) AS v", "last")
		// Adjacent insertion must never exhaust an ordering slot or move old data.
		for i := range 40 {
			enumAlterExec(t, f, f.A, fmt.Sprintf("ALTER TYPE mood ADD VALUE 'between_%02d' BEFORE 'q'", i))
		}
		enumAlterExec(t, f, f.A, "ALTER TYPE mood RENAME VALUE 'q' TO 'end'")
		enumAlterRows(t, f, f.A, "SELECT 'end'::mood AS v", "end")
		s := fixture.Select("SELECT v FROM alter_order ORDER BY v", []string{"v"}, []string{""}, [][]any{{"first"}, {"z"}, {"a"}, {"b"}, {"end"}, {"last"}})
		s.CheckMetadata = fixture.EnumReferenceMetadata(pg, true)
		fixture.EnumReferenceRunStep(t, f.Pair.Ctx, f.A, f.Recorder, s)
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Select("SELECT 'between_00'::mood<'between_39'::mood AS v", []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
