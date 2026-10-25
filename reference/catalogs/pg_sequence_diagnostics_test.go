package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGSequenceDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, tc := range []struct{ q, code, message, position string }{
				{"SELECT oid FROM pg_catalog.pg_sequence", "42703", "column \"oid\" does not exist", "8"},
				{"SELECT last_value FROM pg_catalog.pg_sequence", "42703", "column \"last_value\" does not exist", "8"},
				{"SELECT /*é*/ oid FROM pg_catalog.pg_sequence", "42703", "column \"oid\" does not exist", "14"},
				{"SELECT 'bad'::oid FROM pg_catalog.pg_sequence", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
				{"SELECT 'bad'::oid FROM pg_catalog.pg_sequence WHERE FALSE", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
				{"SELECT '4294967296'::oid FROM pg_catalog.pg_sequence", "22003", "value \"4294967296\" is out of range for type oid", "8"},
			} {
				for _, prepared := range []bool{false, true} {
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
					s := fixture.ErrorDiagnostic(tc.q, want)
					s.SelectRows = true
					s.ErrorStage = "execute"
					if prepared {
						s = fixture.Prepared(s)
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
			for _, value := range []string{"bad", ""} {
				for _, prepared := range []bool{false, true} {
					portal := "unnamed portal parameter $1 = '...'"
					if value == "" {
						portal = "unnamed portal parameter $1 = ''"
					}
					s := fixture.ImplicitError(pgSequenceProbe, fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type oid: \"" + value + "\"", Context: portal}, value)
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
			pgSequenceExec(t, p, b, b.DB, "CREATE SEQUENCE pgs_failed AS INTEGER INCREMENT 7 MINVALUE -20 MAXVALUE 200 START 15 CACHE 9 CYCLE")
			oid := fixture.PGDescriptionRelationID(t, p, b, "pgs_failed")
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("ALTER SEQUENCE pgs_failed CACHE 0", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "CACHE (0) must be greater than zero"})
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				pgSequenceCheck(t, p, b, b.DB, oid, pgSequenceCustom(oid))
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := b.DB.QueryContext(canceled, pgSequenceProbe, oid)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled Query published cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("Query cancellation", err)
			}
			stmt, err := b.DB.PrepareContext(canceled, pgSequenceProbe)
			if stmt != nil {
				stmt.Close()
				t.Fatal("canceled Prepare published statement")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("Prepare cancellation", err)
			}
			stmt, err = b.DB.PrepareContext(p.Ctx, pgSequenceProbe)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err = stmt.QueryContext(canceled, oid)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled Execute published cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("Execute cancellation", err)
			}
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgSequenceProbe}
			fixture.PgDescriptionExecutorRows(t, p, b, held, pgSequenceSelect(b, pgSequenceProbe, [][]any{pgSequenceCustom(oid)}, oid))
			pgSequenceCheck(t, p, b, b.DB, oid, pgSequenceCustom(oid))
			if !b.PG {
				for _, tc := range []struct{ q, stage string }{{"INSERT INTO pg_catalog.pg_sequence VALUES(1,20,1,1,100,1,1,false)", "execute"}, {"UPDATE pg_catalog.pg_sequence SET seqstart=2", "prepare"}, {"DELETE FROM pg_catalog.pg_sequence", "execute"}} {
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
						s.Prepared, s.ErrorStage = prepared, "execute"
						if prepared {
							s.ErrorStage = tc.stage
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
						pgSequenceCheck(t, p, b, b.DB, oid, pgSequenceCustom(oid))
					}
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError("SELECT * FROM pg_sequence WHERE FALSE FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		})
	}
}
