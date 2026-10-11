package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceErrorFieldsEngine(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE diagnostic_rows (id INT)", 0))
			for _, tc := range []struct{ query, code, message, position string }{
				{"SELECT * FROM missing", "42P01", `relation "missing" does not exist`, "15"},
				{"SELECT missing FROM diagnostic_rows", "42703", `column "missing" does not exist`, "8"},
				{"SELECT * FROM diagnostic_rows WHERE missing=1", "42703", `column "missing" does not exist`, "37"},
				{"SELECT * FROM diagnostic_rows ORDER BY missing", "42703", `column "missing" does not exist`, "40"},
				{"CREATE TABLE absent.t (id INT)", "3F000", `schema "absent" does not exist`, "14"},
				{"CREATE TABLE pg_catalog.t (id INT)", "0A000", "system schema creation is unsupported", "14"},
			} {
				for _, route := range []string{"exec", "query", "prepare"} {
					t.Run(route+"/"+tc.query, func(t *testing.T) {
						if backend.PG && tc.code == "0A000" {
							want := fixture.ControlsReferenceDiagnostic("42501", `permission denied to create "pg_catalog.t"`)
							want.Detail = "System catalog modifications are currently disallowed."
							s := fixture.ErrorDiagnostic(tc.query, want)
							s.SelectRows, s.Prepared, s.ErrorStage = route == "query", route == "prepare", "execute"
							run(s)
							return
						}
						want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
						want.Position = tc.position
						s := fixture.ErrorDiagnostic(tc.query, want)
						s.SelectRows = route == "query"
						s.Prepared = route == "prepare"
						if route == "prepare" && tc.code != "3F000" && tc.code != "0A000" {
							s.ErrorStage = "prepare"
						} else {
							s.ErrorStage = "execute"
						}
						run(s)
					})
				}
			}
			want := fixture.ControlsReferenceDiagnostic("42P07", `relation "diagnostic_rows" already exists`)
			for _, route := range []string{"exec", "query", "prepared"} {
				s := fixture.ErrorDiagnostic("CREATE TABLE diagnostic_rows (id INT)", want)
				s.SelectRows, s.Prepared = route == "query", route == "prepared"
				s.ErrorStage = "execute"
				run(s)
			}
			// Wrapping must retain the complete SQL diagnostic, not merely its code.
			_, err := backend.DB.QueryContext(p.Ctx, "SELECT missing FROM diagnostic_rows")
			want = fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
			want.Position = "8"
			fixture.AssertDiagnostic(t, fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", err)), &want)
		})
	}
}

func TestReferenceErrorFieldsNotNull(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE diagnostic_rows (id INT NOT NULL,note TEXT)", 0))
	want := fixture.ControlsReferenceDiagnostic("23502", `null value in column "id" of relation "diagnostic_rows" violates not-null constraint`)
	want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (null, bad).", "public", "diagnostic_rows", "id"
	for _, s := range []fixture.Step{
		fixture.ErrorDiagnostic("INSERT INTO diagnostic_rows VALUES (NULL,'bad')", want),
		fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO diagnostic_rows VALUES ($1,$2)", want, nil, "bad")),
		fixture.ErrorDiagnostic("INSERT INTO diagnostic_rows VALUES (1,'good'),(NULL,'bad')", want),
	} {
		s.ErrorStage = "execute"
		p.Step(t, s)
		for _, backend := range p.Backends() {
			empty := fixture.Select("SELECT id,note FROM diagnostic_rows ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, nil)
			empty.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				fixture.DriverBasicMetadata(t, rows, backend.PG, empty.Types, []bool{false, true})
			}
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, empty)
		}
	}
}
