package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferencePGSequenceDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			for _, tc := range []struct{ q, code, message, position string }{
				{"SELECT oid FROM pg_catalog.pg_sequence", "42703", "column \"oid\" does not exist", "8"},
				{"SELECT last_value FROM pg_catalog.pg_sequence", "42703", "column \"last_value\" does not exist", "8"},
				{"SELECT /*é*/ oid FROM pg_catalog.pg_sequence", "42703", "column \"oid\" does not exist", "14"},
				{"SELECT 'bad'::oid FROM pg_catalog.pg_sequence", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
				{"SELECT 'bad'::oid FROM pg_catalog.pg_sequence WHERE FALSE", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
				{"SELECT '4294967296'::oid FROM pg_catalog.pg_sequence", "22003", "value \"4294967296\" is out of range for type oid", "8"},
			} {
				for _, prepared := range []bool{false, true} {
					want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
					s := referenceErrorDiagnostic(tc.q, want)
					s.selectRows = true
					s.errorStage = "execute"
					if prepared {
						s = referencePrepared(s)
						s.errorStage = "prepare"
					}
					referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				}
			}
			for _, value := range []string{"bad", ""} {
				for _, prepared := range []bool{false, true} {
					portal := "unnamed portal parameter $1 = '...'"
					if value == "" {
						portal = "unnamed portal parameter $1 = ''"
					}
					s := referenceImplicitError(pgSequenceProbe, referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type oid: \"" + value + "\"", Context: portal}, value)
					s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
					referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				}
			}
			pgSequenceExec(t, p, b, b.db, "CREATE SEQUENCE pgs_failed AS INTEGER INCREMENT 7 MINVALUE -20 MAXVALUE 200 START 15 CACHE 9 CYCLE")
			oid := pgDescriptionRelationID(t, p, b, "pgs_failed")
			for _, prepared := range []bool{false, true} {
				s := referenceErrorDiagnostic("ALTER SEQUENCE pgs_failed CACHE 0", referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: "CACHE (0) must be greater than zero"})
				s.prepared, s.errorStage = prepared, "execute"
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				pgSequenceCheck(t, p, b, b.db, oid, pgSequenceCustom(oid))
			}
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err := b.db.QueryContext(canceled, pgSequenceProbe, oid)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled Query published cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("Query cancellation", err)
			}
			stmt, err := b.db.PrepareContext(canceled, pgSequenceProbe)
			if stmt != nil {
				stmt.Close()
				t.Fatal("canceled Prepare published statement")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatal("Prepare cancellation", err)
			}
			stmt, err = b.db.PrepareContext(p.ctx, pgSequenceProbe)
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
			held := referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: pgSequenceProbe}
			pgDescriptionExecutorRows(t, p, b, held, pgSequenceSelect(b, pgSequenceProbe, [][]any{pgSequenceCustom(oid)}, oid))
			pgSequenceCheck(t, p, b, b.db, oid, pgSequenceCustom(oid))
			if !b.pg {
				for _, tc := range []struct{ q, stage string }{{"INSERT INTO pg_catalog.pg_sequence VALUES(1,20,1,1,100,1,1,false)", "execute"}, {"UPDATE pg_catalog.pg_sequence SET seqstart=2", "prepare"}, {"DELETE FROM pg_catalog.pg_sequence", "execute"}} {
					for _, prepared := range []bool{false, true} {
						s := referenceImplicitError(tc.q, referenceDiagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
						s.prepared, s.errorStage = prepared, "execute"
						if prepared {
							s.errorStage = tc.stage
						}
						referenceRunStep(t, p.ctx, b.db, b.recorder, s)
						pgSequenceCheck(t, p, b, b.db, oid, pgSequenceCustom(oid))
					}
				}
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError("SELECT * FROM pg_sequence WHERE FALSE FOR UPDATE", referenceDiagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
					s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
					referenceRunStep(t, p.ctx, b.db, b.recorder, s)
				}
			}
		})
	}
}
