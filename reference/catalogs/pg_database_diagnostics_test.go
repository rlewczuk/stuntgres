package reference_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func pgDatabaseMissing(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError("SELECT missing FROM pg_catalog.pg_database", fixture.Diagnostic{Code: "42703", Message: "column \"missing\" does not exist", Position: "8"})
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		if prepared {
			s.ErrorStage = "prepare"
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func TestReferencePGDatabaseDiagnostics(t *testing.T) {
	pgDatabaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgDatabaseMissing(t, p, b)
		for _, tc := range []struct{ q, code, message, position string }{
			{"SELECT missing FROM pg_catalog.pg_database WHERE FALSE", "42703", "column \"missing\" does not exist", "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_database", "42703", "column \"missing\" does not exist", "14"},
			{"SELECT datname FROM pg_catalog.pg_database_missing", "42P01", "relation \"pg_catalog.pg_database_missing\" does not exist", "21"},
			{"SELECT 'bad'::oid FROM pg_database WHERE FALSE", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
			{"SELECT '4294967296'::oid FROM pg_database", "22003", "value \"4294967296\" is out of range for type oid", "8"},
		} {
			for _, plain := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.SelectRows, s.Prepared, s.NonContext, s.ErrorStage = true, prepared, plain, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		}
		q := "SELECT datname=current_database() AS same_name FROM pg_database WHERE oid=$1"
		held := fixture.ViewHold(t, p, b, q)
		var oid int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_database WHERE datname=current_database()").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{"bad", ""} {
			for _, prepared := range []bool{false, true} {
				s := fixture.PGCastParameterError(q, "oid", value, value)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.PGCastParameterError(q, "oid", value, value))
			fixture.PGDatabaseCheck(t, p, b, held, fixture.Select(q, []string{"same_name"}, []string{"BOOL"}, [][]any{{true}}, oid))
		}
		fixture.PGDatabaseCheck(t, p, b, held, fixture.Select(q, []string{"same_name"}, []string{"BOOL"}, nil, nil))
		if b.PG {
			return
		}
		// Native catalog-source boundaries are not destructive PG catalog mutations.
		for _, tc := range []struct{ q, stage string }{{"INSERT INTO pg_database(oid,datname) VALUES(1,'fake')", "execute"}, {"UPDATE pg_database SET datname='fake' WHERE FALSE", "prepare"}, {"DELETE FROM pg_database WHERE FALSE", "execute"}} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared {
					s.ErrorStage = tc.stage
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("SELECT * FROM pg_database WHERE FALSE FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := b.DB.QueryContext(canceled, q, oid)
		if rows != nil {
			rows.Close()
			t.Fatal("canceled query published Rows")
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
		rows, err = held.Statement.QueryContext(canceled, oid)
		if rows != nil {
			rows.Close()
			t.Fatal("canceled held query published Rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		fixture.PGDatabaseCheck(t, p, b, held, fixture.Select(q, []string{"same_name"}, []string{"BOOL"}, [][]any{{true}}, oid))
	})
}
