package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGConstraintDiagnostics(t *testing.T) {
	pgConstraintScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, populated := range []bool{false, true} {
			if populated {
				pgClassExec(t, p, b, b.DB, "CREATE TABLE pc_diag(a INT UNIQUE)")
			}
			for _, tc := range []struct{ query, code, message, position string }{
				{"SELECT missing FROM pg_catalog.pg_constraint", "42703", `column "missing" does not exist`, "8"},
				{"SELECT missing FROM pg_catalog.pg_constraint WHERE FALSE", "42703", `column "missing" does not exist`, "8"},
				{"SELECT /*é*/ missing FROM pg_catalog.pg_constraint", "42703", `column "missing" does not exist`, "14"},
				{"SELECT 'x'::oid FROM pg_constraint", "22P02", `invalid input syntax for type oid: "x"`, "8"},
				{"SELECT '4294967296'::oid FROM pg_constraint", "22003", `value "4294967296" is out of range for type oid`, "8"},
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
		}
		q := "SELECT conname FROM pg_constraint WHERE oid=$1"
		owner := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		stmt, err := owner.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: owner, Statement: stmt, SQL: q}
		for _, value := range []string{"x", ""} {
			for _, prepared := range []bool{false, true} {
				s := pgCastParameterError(q, "oid", value, value)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgCastParameterError(q, "oid", value, value))
		}
		pgConstraintCheck(t, p, b, held, fixture.Select(q, []string{"conname"}, []string{"NAME"}, nil, int64(4294967295)))
		if !b.PG {
			for _, tc := range []struct{ query, stage string }{{"INSERT INTO pg_constraint(oid,conname) VALUES(1,'fake')", "execute"}, {"UPDATE pg_constraint SET conname='fake' WHERE FALSE", "prepare"}, {"DELETE FROM pg_constraint WHERE FALSE", "execute"}} {
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
				s := fixture.ImplicitError("SELECT * FROM pg_constraint WHERE FALSE FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := b.DB.QueryContext(canceled, q, int64(1))
			if rows != nil {
				rows.Close()
				t.Fatal("canceled rows published")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			statement, err := b.DB.PrepareContext(canceled, q)
			if statement != nil {
				statement.Close()
				t.Fatal("canceled statement published")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			rows, err = stmt.QueryContext(canceled, int64(1))
			if rows != nil {
				rows.Close()
				t.Fatal("canceled held rows published")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
	})
}
