package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func pgExtensionCanceled(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	rows, err := b.DB.QueryContext(ctx, pgExtensionProbe, pgExtensionAbsent)
	if rows != nil {
		rows.Close()
		t.Fatal("canceled Query published cursor")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("Query cancellation", err)
	}
	stmt, err := b.DB.PrepareContext(ctx, pgExtensionProbe)
	if stmt != nil {
		stmt.Close()
		t.Fatal("canceled Prepare published handle")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("Prepare cancellation", err)
	}
	stmt, err = b.DB.PrepareContext(p.Ctx, pgExtensionProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	rows, err = stmt.QueryContext(ctx, pgExtensionAbsent)
	if rows != nil {
		rows.Close()
		t.Fatal("canceled Execute published cursor")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("Execute cancellation", err)
	}
	pgExtensionHeld(t, p, b, fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgExtensionProbe}, 0)
}

func pgExtensionMissing(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, tc := range []struct{ query, code, message, position string }{
		{"SELECT missing FROM pg_catalog.pg_extension", "42703", "column \"missing\" does not exist", "8"},
		{"SELECT /*é*/ missing FROM pg_catalog.pg_extension", "42703", "column \"missing\" does not exist", "14"},
		{"SELECT 'bad'::oid FROM pg_catalog.pg_extension WHERE FALSE", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
		{"SELECT '4294967296'::oid FROM pg_catalog.pg_extension WHERE FALSE", "22003", "value \"4294967296\" is out of range for type oid", "8"},
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

func pgExtensionProtected(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	if b.PG {
		return
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE pgext_diagnostic_sentinel(id INT)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgext_diagnostic_sentinel VALUES(7)", 1))
	check := func() {
		pgExtensionInstalled(t, p, b)
		pgExtensionClass(t, p, b)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT * FROM pgext_diagnostic_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	}
	for _, tc := range []struct{ query, stage string }{
		{"INSERT INTO pg_catalog.pg_extension VALUES(1,'pgext_rejected',10,11,false,'1.0',NULL,NULL)", "execute"},
		{"UPDATE pg_catalog.pg_extension SET extversion='x' WHERE FALSE", "prepare"},
		{"UPDATE pg_catalog.pg_extension SET extversion='x' WHERE extname='plpgsql'", "prepare"},
		{"DELETE FROM pg_catalog.pg_extension WHERE FALSE", "execute"},
		{"DELETE FROM pg_catalog.pg_extension WHERE extname='plpgsql'", "execute"},
	} {
		for _, prepared := range []bool{false, true} {
			check()
			s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
			s.Prepared, s.ErrorStage = prepared, "execute"
			if prepared {
				s.ErrorStage = tc.stage
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			check()
		}
	}
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_extension WHERE FALSE FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
		s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		check()
	}
	pgExtensionCanceled(t, p, b)
}

func TestReferencePGExtensionDiagnostics(t *testing.T) {
	pgExtensionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgExtensionMissing(t, p, b)
		pgExtensionProtected(t, p, b)
	})
}

func TestReferencePGExtensionCommandBoundaries(t *testing.T) {
	pgExtensionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// CREATE parses and prepares on both backends, but only the registered
		// native provider is available. Native does not invent host control files
		// or PostgreSQL's system-installation Hint.
		missing := fixture.Diagnostic{Code: "0A000", Message: "extension \"pgext_missing_control\" is not available"}
		if b.PG {
			missing.Hint = "The extension must first be installed on the system where PostgreSQL is running."
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("CREATE EXTENSION pgext_missing_control", missing)
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		for _, query := range []string{
			"DROP EXTENSION pgext_missing_control",
			"ALTER EXTENSION pgext_missing_control UPDATE",
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42704", Message: `extension "pgext_missing_control" does not exist`})
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		if !b.PG {
			// These unchanged command families remain outside native admission.
			for _, tc := range []struct{ query, code, message, position string }{
				{"LOAD 'pgext_missing_library'", "42601", "syntax error at or near \"LOAD\"", "1"},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
					s.Prepared, s.ErrorStage = prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		}
		pgExtensionEmpty(t, p, b, pgExtensionProbe, "pgext_missing_control")
		pgExtensionInstalled(t, p, b)
		pgExtensionClass(t, p, b)
	})
}
