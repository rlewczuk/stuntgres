package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGDescriptionDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			for _, tc := range []struct {
				query, code, message, position string
			}{
				{"SELECT oid FROM pg_catalog.pg_description", "42703", "column \"oid\" does not exist", "8"},
				{"SELECT missing FROM pg_catalog.pg_description", "42703", "column \"missing\" does not exist", "8"},
				{"SELECT /*é*/ missing FROM pg_catalog.pg_description", "42703", "column \"missing\" does not exist", "14"},
				{"SELECT objoid FROM pg_catalog.pg_description WHERE objoid='bad'::oid", "22P02", "invalid input syntax for type oid: \"bad\"", "59"},
			} {
				for _, prepared := range []bool{false, true} {
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
					s := fixture.ErrorDiagnostic(tc.query, want)
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
				}
			}
			for _, prepared := range []bool{false, true} {
				failure := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1", fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type oid: \"bad\"", Context: "unnamed portal parameter $1 = '...'"}, "bad")
				failure.SelectRows, failure.Prepared, failure.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, failure)
			}
			query := "SELECT * FROM pg_catalog.pg_description WHERE FALSE"
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := backend.DB.QueryContext(canceled, query)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query published a cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary query cancellation", err)
			}
			stmt, err := backend.DB.PrepareContext(canceled, query)
			if stmt != nil {
				stmt.Close()
				t.Fatal("canceled prepare published a statement")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary prepare cancellation", err)
			}
			stmt, err = backend.DB.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err = stmt.QueryContext(canceled)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled prepared query published a cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary prepared cancellation", err)
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
			s := pgDescriptionSelect(query, [][]any{})
			fixture.PgDescriptionRows(t, p, backend, s)
			fixture.PgDescriptionRows(t, p, backend, fixture.Prepared(s))
			// Native virtual catalogs are readonly; PG's physical superuser
			// catalogs are deliberately not mutated to manufacture parity.
			if !backend.PG {
				for _, tc := range []struct{ query, prepareStage string }{
					{"INSERT INTO pg_catalog.pg_description VALUES(1,1259,0,'forbidden')", "execute"},
					{"UPDATE pg_catalog.pg_description SET description='forbidden'", "prepare"},
					{"DELETE FROM pg_catalog.pg_description", "execute"},
				} {
					for _, prepared := range []bool{false, true} {
						failure := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
						failure.ErrorStage = "execute"
						if prepared {
							failure = fixture.Prepared(failure)
							failure.ErrorStage = tc.prepareStage
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, failure)
					}
				}
				for _, prepared := range []bool{false, true} {
					failure := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_description WHERE FALSE FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
					failure.SelectRows, failure.Prepared, failure.ErrorStage = true, prepared, "execute"
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, failure)
				}
			}
		})
	}
}
