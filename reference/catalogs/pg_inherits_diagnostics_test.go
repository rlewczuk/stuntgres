package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGInheritsDiagnostics(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pi_diag_reader"})
	pgInheritsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, populated := range []bool{false, true} {
			if populated {
				pgClassExec(t, p, b, b.DB, "CREATE TABLE pi_diag_p(a INT)", "CREATE TABLE pi_diag_c() INHERITS(pi_diag_p)")
			}
			for _, tc := range []struct{ query, code, message, position string }{
				{"SELECT missing FROM pg_catalog.pg_inherits", "42703", `column "missing" does not exist`, "8"},
				{"SELECT missing FROM pg_catalog.pg_inherits WHERE FALSE", "42703", `column "missing" does not exist`, "8"},
				{"SELECT /*é*/ missing FROM pg_catalog.pg_inherits", "42703", `column "missing" does not exist`, "14"},
				{"SELECT oid FROM pg_catalog.pg_inherits", "42703", `column "oid" does not exist`, "8"},
				{"SELECT 'x'::oid FROM pg_catalog.pg_inherits", "22P02", `invalid input syntax for type oid: "x"`, "8"},
				{"SELECT '4294967296'::oid FROM pg_catalog.pg_inherits", "22003", `value "4294967296" is out of range for type oid`, "8"},
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
		owner := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		held := pgInheritsHeld(t, p, owner)
		for _, value := range []string{"x", ""} {
			for _, prepared := range []bool{false, true} {
				s := pgCastParameterError(held.SQL, "oid", value, value)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, owner, b.Recorder, s)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgCastParameterError(held.SQL, "oid", value, value))
		}
		pgInheritsHeldRows(t, p, b, held, 4294967295, nil)
		if b.PG {
			pgClassExec(t, p, b, owner, "CREATE ROLE pi_diag_reader", "SET ROLE pi_diag_reader")
			defer owner.ExecContext(p.Ctx, "RESET ROLE")
			for _, q := range []string{"INSERT INTO pg_catalog.pg_inherits(inhrelid,inhparent,inhseqno,inhdetachpending) VALUES(0,0,1,false)", "UPDATE pg_catalog.pg_inherits SET inhseqno=1 WHERE FALSE", "DELETE FROM pg_catalog.pg_inherits WHERE FALSE"} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_inherits"})
					s.Prepared, s.ErrorStage = prepared, "execute"
					fixture.RunStep(t, p.Ctx, owner, b.Recorder, s)
				}
			}
			pgClassExec(t, p, b, owner, "RESET ROLE", "DROP ROLE pi_diag_reader")
		} else {
			for _, tc := range []struct{ query, stage string }{
				{"INSERT INTO pg_catalog.pg_inherits(inhrelid,inhparent,inhseqno,inhdetachpending) VALUES(0,0,1,false)", "execute"},
				{"UPDATE pg_catalog.pg_inherits SET inhseqno=1 WHERE FALSE", "prepare"},
				{"DELETE FROM pg_catalog.pg_inherits WHERE FALSE", "execute"},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared {
						s.ErrorStage = tc.stage
					}
					fixture.RunStep(t, p.Ctx, owner, b.Recorder, s)
				}
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_inherits WHERE FALSE FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, owner, b.Recorder, s)
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := owner.QueryContext(canceled, held.SQL, int64(0))
			if rows != nil {
				rows.Close()
				t.Fatal("canceled rows published")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			stmt, err := owner.PrepareContext(canceled, held.SQL)
			if stmt != nil {
				stmt.Close()
				t.Fatal("canceled statement published")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			rows, err = held.Statement.QueryContext(canceled, int64(0))
			if rows != nil {
				rows.Close()
				t.Fatal("canceled held rows published")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		}
		child, parent := pgInheritsID(t, p, owner, "pi_diag_c"), pgInheritsID(t, p, owner, "pi_diag_p")
		pgInheritsHeldRows(t, p, b, held, child, [][]any{pgInheritsEdge(child, parent, 1)})
	})
}
