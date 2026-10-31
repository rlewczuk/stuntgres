package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGShdependDiagnostics(t *testing.T) {
	p := shScenario(t)
	for _, b := range p.Backends() {
		shExec(t, p, b, b.DB, "CREATE ROLE sh_reader")
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			defer conn.ExecContext(p.Ctx, "GRANT SELECT ON pg_catalog.pg_shdepend TO PUBLIC")
			defer conn.ExecContext(p.Ctx, "RESET ROLE")
			shExec(t, p, b, conn, "REVOKE SELECT ON pg_catalog.pg_shdepend FROM PUBLIC", "SET ROLE sh_reader")
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_shdepend", fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_shdepend"})
				s.SelectRows = true
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
			}
		}()
		for _, tc := range []struct{ q, msg, pos string }{
			{"SELECT oid FROM pg_catalog.pg_shdepend", "column \"oid\" does not exist", "8"},
			{"SELECT missing FROM pg_catalog.pg_shdepend", "column \"missing\" does not exist", "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_shdepend", "column \"missing\" does not exist", "14"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.q, fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42703", Message: tc.msg, Position: tc.pos})
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		for _, tc := range []struct{ input, message, context string }{
			{"bad", "invalid input syntax for type oid: \"bad\"", "unnamed portal parameter $1 = '...'"},
			{"", "invalid input syntax for type oid: \"\"", "unnamed portal parameter $1 = ''"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_shdepend WHERE objid=$1", fixture.Diagnostic{Code: "22P02", Message: tc.message, Context: tc.context}, tc.input)
				s.SelectRows = true
				s.Prepared = prepared
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		q := "SELECT * FROM pg_catalog.pg_shdepend WHERE FALSE"
		rows, err := b.DB.QueryContext(canceled, q)
		if rows != nil {
			rows.Close()
			if !b.PG {
				t.Fatal("canceled query cursor")
			}
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		stmt, err := b.DB.PrepareContext(canceled, q)
		if stmt != nil {
			stmt.Close()
			if !b.PG {
				t.Fatal("canceled statement")
			}
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		stmt, err = b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		rows, err = stmt.QueryContext(canceled)
		if rows != nil {
			rows.Close()
			if !b.PG {
				t.Fatal("canceled held cursor")
			}
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, shSelect(q, [][]any{}))
		if !b.PG { // Never mutate physical PG superuser catalogs for parity.
			for _, tc := range []struct{ q, stage string }{{"INSERT INTO pg_catalog.pg_shdepend VALUES(1,1259,1,0,1260,10,'o')", "execute"}, {"UPDATE pg_catalog.pg_shdepend SET deptype='o'", "prepare"}, {"DELETE FROM pg_catalog.pg_shdepend", "execute"}} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = tc.stage
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
			s := fixture.ImplicitError(q+" FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
	}
}
