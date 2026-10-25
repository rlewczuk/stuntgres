package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePgClassDiagnostics(t *testing.T) {
	pgClassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ query, code, message, position string }{
			{"SELECT missing FROM pg_catalog.pg_class", "42703", "column \"missing\" does not exist", "8"},
			{"SELECT missing FROM pg_catalog.pg_class WHERE FALSE", "42703", "column \"missing\" does not exist", "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_class", "42703", "column \"missing\" does not exist", "14"},
			{"SELECT relname FROM pc18_missing_class", "42P01", "relation \"pc18_missing_class\" does not exist", "21"},
			{"SELECT 'bad'::oid FROM pg_class WHERE FALSE", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
			{"SELECT '4294967296'::oid FROM pg_class", "22003", "value \"4294967296\" is out of range for type oid", "8"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		q := "SELECT relname FROM pg_class WHERE oid=$1"
		owner := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		stmt, err := owner.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: owner, Statement: stmt, SQL: q}
		for _, value := range []string{"bad", ""} {
			for _, prepared := range []bool{false, true} {
				s := fixture.PGCastParameterError(q, "oid", value, value)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			s := fixture.PGCastParameterError(q, "oid", value, value)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		pgClassCheck(t, p, b, held, fixture.Select(q, []string{"relname"}, []string{"NAME"}, [][]any{{"pg_class"}}, int64(1259)))
		if !b.PG {
			for _, tc := range []struct{ query, stage string }{{"INSERT INTO pg_class(oid,relname) VALUES(1,'pc18_fake')", "execute"}, {"UPDATE pg_class SET relname='pc18_fake' WHERE FALSE", "prepare"}, {"DELETE FROM pg_class WHERE FALSE", "execute"}} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared {
						s.ErrorStage = tc.stage
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT * FROM pg_class WHERE FALSE FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := b.DB.QueryContext(canceled, q, int64(1259))
			if rows != nil {
				rows.Close()
				t.Fatal("canceled Query published rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			stmt, err := b.DB.PrepareContext(canceled, q)
			if stmt != nil {
				stmt.Close()
				t.Fatal("canceled Prepare published statement")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			rows, err = held.Statement.QueryContext(canceled, int64(1259))
			if rows != nil {
				rows.Close()
				t.Fatal("canceled held execution published rows")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
		pgClassCheck(t, p, b, held, fixture.Select(q, []string{"relname"}, []string{"NAME"}, [][]any{{"pg_class"}}, int64(1259)))
	})
}
