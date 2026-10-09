package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceErrorFieldsEngine(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE diagnostic_rows (id INT)", 0))
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
						if backend.pg && tc.code == "0A000" {
							want := controlsReferenceDiagnostic("42501", `permission denied to create "pg_catalog.t"`)
							want.Detail = "System catalog modifications are currently disallowed."
							s := referenceErrorDiagnostic(tc.query, want)
							s.selectRows, s.prepared, s.errorStage = route == "query", route == "prepare", "execute"
							run(s)
							return
						}
						want := controlsReferenceDiagnostic(tc.code, tc.message)
						want.Position = tc.position
						s := referenceErrorDiagnostic(tc.query, want)
						s.selectRows = route == "query"
						s.prepared = route == "prepare"
						if route == "prepare" && tc.code != "3F000" && tc.code != "0A000" {
							s.errorStage = "prepare"
						} else {
							s.errorStage = "execute"
						}
						run(s)
					})
				}
			}
			want := controlsReferenceDiagnostic("42P07", `relation "diagnostic_rows" already exists`)
			for _, route := range []string{"exec", "query", "prepared"} {
				s := referenceErrorDiagnostic("CREATE TABLE diagnostic_rows (id INT)", want)
				s.selectRows, s.prepared = route == "query", route == "prepared"
				s.errorStage = "execute"
				run(s)
			}
			// Wrapping must retain the complete SQL diagnostic, not merely its code.
			_, err := backend.db.QueryContext(p.ctx, "SELECT missing FROM diagnostic_rows")
			want = controlsReferenceDiagnostic("42703", `column "missing" does not exist`)
			want.Position = "8"
			referenceAssertDiagnostic(t, fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", err)), &want)
		})
	}
}

func TestReferenceErrorFieldsNotNull(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE diagnostic_rows (id INT NOT NULL,note TEXT)", 0))
	want := controlsReferenceDiagnostic("23502", `null value in column "id" of relation "diagnostic_rows" violates not-null constraint`)
	want.Detail, want.Schema, want.Table, want.Column = "Failing row contains (null, bad).", "public", "diagnostic_rows", "id"
	for _, s := range []referenceStep{
		referenceErrorDiagnostic("INSERT INTO diagnostic_rows VALUES (NULL,'bad')", want),
		referencePrepared(referenceErrorDiagnostic("INSERT INTO diagnostic_rows VALUES ($1,$2)", want, nil, "bad")),
		referenceErrorDiagnostic("INSERT INTO diagnostic_rows VALUES (1,'good'),(NULL,'bad')", want),
	} {
		s.errorStage = "execute"
		p.step(t, s)
		for _, backend := range p.backends() {
			empty := referenceSelect("SELECT id,note FROM diagnostic_rows ORDER BY id", []string{"id", "note"}, []string{"INT4", "TEXT"}, nil)
			empty.checkMetadata = func(t *testing.T, rows *sql.Rows) {
				driverBasicMetadata(t, rows, backend.pg, empty.types, []bool{false, true})
			}
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, empty)
		}
	}
}
