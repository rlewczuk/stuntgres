package reference_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func pgCastParameterError(query, typ, value string, args ...any) referenceStep {
	portal := "unnamed portal parameter $1 = '...'"
	if value == "" {
		portal = "unnamed portal parameter $1 = ''"
	}
	s := referenceImplicitError(query, referenceDiagnostic{Code: "22P02", Message: "invalid input syntax for type " + typ + ": \"" + value + "\"", Context: portal}, args...)
	s.selectRows, s.errorStage = true, "execute"
	return s
}

func pgCastMissing(t *testing.T, p *referencePair, b referenceBackend) {
	t.Helper()
	s := referenceImplicitError("SELECT /*é*/ missing FROM pg_catalog.pg_cast", referenceDiagnostic{Code: "42703", Message: "column \"missing\" does not exist", Position: "14"})
	s.selectRows, s.errorStage = true, "execute"
	referenceRunStep(t, p.ctx, b.db, b.recorder, s)
	s.prepared, s.errorStage = true, "prepare"
	referenceRunStep(t, p.ctx, b.db, b.recorder, s)
}

func TestReferencePGCastDiagnostics(t *testing.T) {
	pgCastScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		pgCastMissing(t, p, b)
		for _, tc := range []struct{ query, code, message, position string }{
			{"SELECT missing FROM pg_catalog.pg_cast", "42703", "column \"missing\" does not exist", "8"},
			{"SELECT 'bad'::oid FROM pg_catalog.pg_cast WHERE FALSE", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
			{"SELECT '4294967296'::oid FROM pg_catalog.pg_cast", "22003", "value \"4294967296\" is out of range for type oid", "8"},
			{"SELECT '1'::oid::smallint FROM pg_catalog.pg_cast", "42846", "cannot cast type oid to smallint", ""},
		} {
			if tc.position == "" {
				tc.position = strconv.Itoa(strings.LastIndex(tc.query, "::") + 1)
			}
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError(tc.query, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
				s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
				if prepared {
					s.errorStage = "prepare"
				}
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			}
		}
		for _, value := range []string{"bad", ""} {
			for _, prepared := range []bool{false, true} {
				s := pgCastParameterError(pgCastProbe, "oid", value, value, int64(20))
				s.prepared = prepared
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			}
		}
		ids := pgCastIdentities(t, p, b)
		pgCastHeld(t, p, b, ids)
		if !b.pg {
			for _, tc := range []struct{ query, stage string }{
				{"INSERT INTO pg_catalog.pg_cast VALUES(1,23,20,481,'i','f')", "execute"},
				{"UPDATE pg_catalog.pg_cast SET castfunc=0", "prepare"},
				{"DELETE FROM pg_catalog.pg_cast", "execute"},
			} {
				for _, prepared := range []bool{false, true} {
					s := referenceImplicitError(tc.query, referenceDiagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
					s.prepared, s.errorStage = prepared, "execute"
					if prepared {
						s.errorStage = tc.stage
					}
					referenceRunStep(t, p.ctx, b.db, b.recorder, s)
					pgCastCheckFacts(t, p, b, b.db, pgCastFacts(), true)
				}
			}
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("SELECT * FROM pg_catalog.pg_cast WHERE FALSE FOR UPDATE", referenceDiagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
				s.selectRows, s.prepared, s.errorStage = true, prepared, "execute"
				referenceRunStep(t, p.ctx, b.db, b.recorder, s)
			}
			pgCastCanceled(t, p, b, ids)
		}
		pgCastCheckFacts(t, p, b, b.db, pgCastFacts(), true)
	})
}

func pgCastCanceled(t *testing.T, p *referencePair, b referenceBackend, ids map[[2]int64]int64) {
	t.Helper()
	canceled, cancel := context.WithCancel(p.ctx)
	cancel()
	rows, err := b.db.QueryContext(canceled, pgCastProbe, 23, 20)
	if rows != nil {
		rows.Close()
		t.Fatal("canceled Query published cursor")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("Query cancellation", err)
	}
	stmt, err := b.db.PrepareContext(canceled, pgCastProbe)
	if stmt != nil {
		stmt.Close()
		t.Fatal("canceled Prepare published statement")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("Prepare cancellation", err)
	}
	stmt, err = b.db.PrepareContext(p.ctx, pgCastProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err = stmt.QueryContext(canceled, 23, 20)
	if rows != nil {
		rows.Close()
		t.Fatal("canceled Execute published cursor")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("Execute cancellation", err)
	}
	held := referenceHeldStatement{referenceExecutor: b.db, statement: stmt, query: pgCastProbe}
	pgDescriptionExecutorRows(t, p, b, held, referenceSelect(pgCastProbe, pgCastColumns, pgCastTypes, [][]any{{ids[[2]int64{23, 20}], int64(23), int64(20), int64(481), "i", "f"}}, 23, 20))
}
