package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceEnumAlterDependents(t *testing.T) {
	enumAlterScenario(t, func(t *testing.T, f fixture.ControlsReferenceFixture, pg bool) {
		for _, q := range []string{
			"CREATE TYPE mood AS ENUM ('z','q')",
			"CREATE DOMAIN alter_domain AS mood",
			"CREATE TYPE alter_composite AS (v mood)",
			"CREATE TABLE alter_dependents(id integer,m mood DEFAULT 'q'::mood,CHECK (m<='q'::mood))",
			"CREATE TABLE alter_nested(id integer,d alter_domain,c alter_composite)",
			"CREATE VIEW alter_stored AS SELECT id,m AS v FROM alter_dependents WHERE m='q'::mood",
			"CREATE VIEW alter_provider AS SELECT enum_last(NULL::mood) AS v",
		} {
			enumAlterExec(t, f, f.A, q)
		}
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec("INSERT INTO alter_dependents(id) VALUES (1)", 1))
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec("INSERT INTO alter_nested VALUES (1,'q','(q)')", 1))
		enumAlterExec(t, f, f.A, "ALTER TYPE mood ADD VALUE 'a' BEFORE 'q'")
		enumAlterExec(t, f, f.A, "ALTER TYPE mood RENAME VALUE 'q' TO 'end'")
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"SELECT v FROM alter_stored ORDER BY id", "SELECT v FROM alter_provider", "SELECT d::mood AS v FROM alter_nested"} {
				s := fixture.Select(q, []string{"v"}, []string{""}, [][]any{{"end"}})
				s.Prepared = prepared
				fixture.EnumReferenceRunStep(t, f.Pair.Ctx, f.A, f.Recorder, s)
			}
			composite := fixture.Select("SELECT c::text AS v FROM alter_nested", []string{"v"}, []string{"TEXT"}, [][]any{{"(end)"}})
			composite.Prepared = prepared
			fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, composite)
		}
		// Retained DEFAULT and CHECK constants carry members, not stale spelling.
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Exec("INSERT INTO alter_dependents(id) VALUES (2)", 1))
		fixture.RunStep(t, f.Pair.Ctx, f.A, f.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO alter_dependents VALUES ($1,$2)", 1, 3, "a")))
		s := fixture.Select("SELECT m AS v FROM alter_dependents ORDER BY m,id", []string{"v"}, []string{""}, [][]any{{"a"}, {"end"}, {"end"}})
		s.CheckMetadata = fixture.EnumReferenceMetadata(pg, true)
		fixture.EnumReferenceRunStep(t, f.Pair.Ctx, f.A, f.Recorder, s)
		enumAlterExec(t, f, f.A, "ALTER TYPE mood ADD VALUE 'last'")
		want := enumAlterDiagnostic("23514", `new row for relation "alter_dependents" violates check constraint "alter_dependents_m_check"`)
		want.Detail = "Failing row contains (4, last)."
		want.Schema = "public"
		want.Table = "alter_dependents"
		want.Constraint = "alter_dependents_m_check"
		for _, prepared := range []bool{false, true} {
			enumAlterError(t, f, f.A, "INSERT INTO alter_dependents VALUES (4,'last')", prepared, false, "execute", want)
		}
		provider := fixture.Select("SELECT v FROM alter_provider", []string{"v"}, []string{""}, [][]any{{"last"}})
		provider.CheckMetadata = fixture.EnumReferenceMetadata(pg, true)
		fixture.EnumReferenceRunStep(t, f.Pair.Ctx, f.A, f.Recorder, provider)
	})
}
