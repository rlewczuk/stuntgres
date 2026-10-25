package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGNamespaceDiagnostics(t *testing.T) {
	pgNamespaceScenario(t, []string{"ns18_diag_denied"}, []string{"ns18_diag_reader"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ q, code, message, position string }{
			{"SELECT missing FROM pg_catalog.pg_namespace", "42703", "column \"missing\" does not exist", "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_namespace", "42703", "column \"missing\" does not exist", "14"},
			{"SELECT oid FROM absent.pg_namespace", "42P01", "relation \"absent.pg_namespace\" does not exist", "17"},
			{"SELECT 'bad'::oid FROM pg_namespace WHERE FALSE", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
			{"SELECT '4294967296'::oid FROM pg_namespace", "22003", "value \"4294967296\" is out of range for type oid", "8"},
		} {
			for _, prepared := range []bool{false, true} {
				for _, plain := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.SelectRows, s.Prepared, s.NonContext, s.ErrorStage = true, prepared, plain, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		}
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		pgNamespaceExec(t, p, b, c, "CREATE ROLE ns18_diag_reader", "CREATE SCHEMA ns18_diag_denied", "SET ROLE ns18_diag_reader")
		for _, prepared := range []bool{false, true} {
			s := fixture.SchemaReferenceError("SELECT oid FROM ns18_diag_denied.pg_namespace", "42501", "permission denied for schema ns18_diag_denied", "", prepared, "execute", "17")
			s.SelectRows = true
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		pgNamespaceExec(t, p, b, c, "RESET ROLE")
		q := "SELECT nspname FROM pg_namespace WHERE oid=$1"
		held := fixture.ViewHold(t, p, b, q)
		for _, value := range []string{"bad", ""} {
			for _, prepared := range []bool{false, true} {
				s := fixture.PGCastParameterError(q, "oid", value, value)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.PGCastParameterError(q, "oid", value, value))
			pgNamespaceCheck(t, p, b, held, fixture.Select(q, []string{"nspname"}, []string{"NAME"}, [][]any{{"pg_catalog"}}, int64(11)))
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.SchemaReferenceError("DROP SCHEMA ns18_diag_absent", "3F000", "schema \"ns18_diag_absent\" does not exist", "", prepared, "execute", "")
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := b.DB.QueryContext(canceled, q, int64(11))
		if rows != nil {
			rows.Close()
			t.Fatal("canceled query published rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		stmt, err := b.DB.PrepareContext(canceled, q)
		if stmt != nil {
			stmt.Close()
			t.Fatal("canceled prepare published statement")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		rows, err = held.Statement.QueryContext(canceled, int64(11))
		if rows != nil {
			rows.Close()
			t.Fatal("canceled prepared query published rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		pgNamespaceCheck(t, p, b, held, fixture.Select(q, []string{"nspname"}, []string{"NAME"}, [][]any{{"pg_catalog"}}, int64(11)))
		if b.PG {
			return
		}
		iteration, stop := context.WithCancel(p.Ctx)
		rows, err = held.Statement.QueryContext(iteration, int64(11))
		if err != nil {
			stop()
			t.Fatal(err)
		}
		defer rows.Close()
		stop()
		if rows.Next() || !errors.Is(rows.Err(), context.Canceled) {
			t.Fatal("canceled eager iteration", rows.Err())
		}
		rows.Close()
		pgNamespaceCheck(t, p, b, held, fixture.Select(q, []string{"nspname"}, []string{"NAME"}, [][]any{{"pg_catalog"}}, int64(11)))
		for _, tc := range []struct{ q, phase string }{
			{"INSERT INTO pg_namespace(oid,nspname,nspowner) VALUES(1,'fake',10)", "execute"},
			{"UPDATE pg_namespace SET nspname='fake' WHERE FALSE", "prepare"},
			{"DELETE FROM pg_namespace WHERE FALSE", "execute"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared {
					s.ErrorStage = tc.phase
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("SELECT * FROM pg_namespace WHERE FALSE FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
	})
}
