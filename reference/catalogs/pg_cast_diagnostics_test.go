package reference_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func pgCastParameterError(query, typ, value string, args ...any) fixture.Step {
	portal := "unnamed portal parameter $1 = '...'"
	if value == "" {
		portal = "unnamed portal parameter $1 = ''"
	}
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22P02", Message: "invalid input syntax for type " + typ + ": \"" + value + "\"", Context: portal}, args...)
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}

func pgCastMissing(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	s := fixture.ImplicitError("SELECT /*é*/ missing FROM pg_catalog.pg_cast", fixture.Diagnostic{Code: "42703", Message: "column \"missing\" does not exist", Position: "14"})
	s.SelectRows, s.ErrorStage = true, "execute"
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	s.Prepared, s.ErrorStage = true, "prepare"
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
}

func TestReferencePGCastDiagnostics(t *testing.T) {
	pgCastScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
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
				s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		for _, value := range []string{"bad", ""} {
			for _, prepared := range []bool{false, true} {
				s := pgCastParameterError(pgCastProbe, "oid", value, value, int64(20))
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		ids := pgCastIdentities(t, p, b)
		pgCastHeld(t, p, b, ids)
		if !b.PG {
			for _, tc := range []struct{ query, stage string }{
				{"INSERT INTO pg_catalog.pg_cast VALUES(1,23,20,481,'i','f')", "execute"},
				{"UPDATE pg_catalog.pg_cast SET castfunc=0", "prepare"},
				{"DELETE FROM pg_catalog.pg_cast", "execute"},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared {
						s.ErrorStage = tc.stage
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					fixture.PgCastCheckFacts(t, p, b, b.DB, pgCastFacts(), true)
				}
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_cast WHERE FALSE FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
			pgCastCanceled(t, p, b, ids)
		}
		fixture.PgCastCheckFacts(t, p, b, b.DB, pgCastFacts(), true)
	})
}

func pgCastCanceled(t *testing.T, p *fixture.Pair, b fixture.Backend, ids map[[2]int64]int64) {
	t.Helper()
	canceled, cancel := context.WithCancel(p.Ctx)
	cancel()
	rows, err := b.DB.QueryContext(canceled, pgCastProbe, 23, 20)
	if rows != nil {
		rows.Close()
		t.Fatal("canceled Query published cursor")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("Query cancellation", err)
	}
	stmt, err := b.DB.PrepareContext(canceled, pgCastProbe)
	if stmt != nil {
		stmt.Close()
		t.Fatal("canceled Prepare published statement")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("Prepare cancellation", err)
	}
	stmt, err = b.DB.PrepareContext(p.Ctx, pgCastProbe)
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
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgCastProbe}
	fixture.PgDescriptionExecutorRows(t, p, b, held, fixture.Select(pgCastProbe, fixture.PgCastColumns, fixture.PgCastTypes, [][]any{{ids[[2]int64{23, 20}], int64(23), int64(20), int64(481), "i", "f"}}, 23, 20))
}
