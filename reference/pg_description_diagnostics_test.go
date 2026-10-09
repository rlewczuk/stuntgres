package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferencePGDescriptionDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			for _, tc := range []struct {
				query, code, message, position string
			}{
				{"SELECT oid FROM pg_catalog.pg_description", "42703", "column \"oid\" does not exist", "8"},
				{"SELECT missing FROM pg_catalog.pg_description", "42703", "column \"missing\" does not exist", "8"},
				{"SELECT /*é*/ missing FROM pg_catalog.pg_description", "42703", "column \"missing\" does not exist", "14"},
				{"SELECT objoid FROM pg_catalog.pg_description WHERE objoid='bad'::oid", "22P02", "invalid input syntax for type oid: \"bad\"", "59"},
			} {
				for _, prepared := range []bool{false, true} {
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
					s := referenceErrorDiagnostic(tc.query, want)
					s.selectRows = true
					s.errorStage = "execute"
					if prepared {
						s = referencePrepared(s)
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
				}
			}
			for _, prepared := range []bool{false, true} {
				failure := referenceImplicitError("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1", referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type oid: \"bad\"", Context: "unnamed portal parameter $1 = '...'"}, "bad")
				failure.selectRows, failure.prepared, failure.errorStage = true, prepared, "execute"
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, failure)
			}
			query := "SELECT * FROM pg_catalog.pg_description WHERE FALSE"
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err := backend.db.QueryContext(canceled, query)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled query published a cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary query cancellation", err)
			}
			stmt, err := backend.db.PrepareContext(canceled, query)
			if stmt != nil {
				stmt.Close()
				t.Fatal("canceled prepare published a statement")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("ordinary prepare cancellation", err)
			}
			stmt, err = backend.db.PrepareContext(p.ctx, query)
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
			pgDescriptionRows(t, p, backend, s)
			pgDescriptionRows(t, p, backend, referencePrepared(s))
			// Native virtual catalogs are readonly; PG's physical superuser
			// catalogs are deliberately not mutated to manufacture parity.
			if !backend.pg {
				for _, tc := range []struct{ query, prepareStage string }{
					{"INSERT INTO pg_catalog.pg_description VALUES(1,1259,0,'forbidden')", "execute"},
					{"UPDATE pg_catalog.pg_description SET description='forbidden'", "prepare"},
					{"DELETE FROM pg_catalog.pg_description", "execute"},
				} {
					for _, prepared := range []bool{false, true} {
						failure := referenceImplicitError(tc.query, referenceDiagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
						failure.errorStage = "execute"
						if prepared {
							failure = referencePrepared(failure)
							failure.errorStage = tc.prepareStage
						}
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, failure)
					}
				}
				for _, prepared := range []bool{false, true} {
					failure := referenceImplicitError("SELECT * FROM pg_catalog.pg_description WHERE FALSE FOR UPDATE", referenceDiagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
					failure.selectRows, failure.prepared, failure.errorStage = true, prepared, "execute"
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, failure)
				}
			}
		})
	}
}
