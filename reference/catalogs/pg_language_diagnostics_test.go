package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func pgLanguageMissing(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, tc := range []struct{ query, code, message, position string }{
		{"SELECT missing FROM pg_catalog.pg_language", "42703", "column \"missing\" does not exist", "8"},
		{"SELECT /*é*/ missing FROM pg_catalog.pg_language", "42703", "column \"missing\" does not exist", "14"},
		{"SELECT 'bad'::oid FROM pg_catalog.pg_language WHERE FALSE", "22P02", "invalid input syntax for type oid: \"bad\"", "8"},
		{"SELECT '4294967296'::oid FROM pg_catalog.pg_language WHERE FALSE", "22003", "value \"4294967296\" is out of range for type oid", "8"},
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

var pgLanguageMutations = []struct{ query, stage string }{
	{"INSERT INTO pg_catalog.pg_language VALUES(999,'pglang_rejected',10,false,false,0,0,0,NULL)", "execute"},
	{"UPDATE pg_catalog.pg_language SET lanpltrusted=true WHERE oid=12", "prepare"},
	{"UPDATE pg_catalog.pg_language SET lanpltrusted=true WHERE FALSE", "prepare"},
	{"DELETE FROM pg_catalog.pg_language WHERE oid=12", "execute"},
	{"DELETE FROM pg_catalog.pg_language WHERE FALSE", "execute"},
}

func pgLanguageProtected(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	if b.PG {
		return
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE pglang_diagnostic_sentinel(id INT)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pglang_diagnostic_sentinel VALUES(7)", 1))
	check := func() {
		pgLanguageInventory(t, p, b)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT * FROM pglang_diagnostic_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	}
	for _, tc := range pgLanguageMutations {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
			s.Prepared, s.ErrorStage = prepared, "execute"
			if prepared {
				s.ErrorStage = tc.stage
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			check()
		}
	}
	for _, predicate := range []string{"FALSE", "oid=12"} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_language WHERE "+predicate+" FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			check()
		}
	}
}

func TestReferencePGLanguageDiagnostics(t *testing.T) {
	pgLanguageScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgLanguageMissing(t, p, b)
		pgLanguageProtected(t, p, b)
	})
}

func pgLanguagePermissions(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	owner, reader := fixture.LockConnections(t, p.Ctx, b.DB)
	fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("CREATE ROLE pglang_reader", 0))
	fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Exec("SET ROLE pglang_reader", 0))
	defer reader.ExecContext(context.Background(), "RESET ROLE")
	for _, value := range []any{"internal", pgLanguageAbsent, nil} {
		want := [][]any{}
		if value == "internal" {
			want = pgLanguageInternal()
		}
		pgLanguageBoth(t, p, b, reader, pgLanguageStep(b, pgLanguageProbe, want, value))
	}
	pgLanguageHeldReads(t, p, b, reader)
	if b.PG {
		// PostgreSQL protects this catalog by table ACL, not native immutability.
		for _, tc := range pgLanguageMutations {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_language"})
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, reader, b.Recorder, s)
				fixture.PgDescriptionExecutorRows(t, p, b, reader, pgLanguageStep(b, pgLanguageProbe, pgLanguageInternal(), "internal"))
			}
		}
	}
}

func TestReferencePGLanguagePermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pglang_reader"})
	pgLanguageScenario(t, pgLanguagePermissions)
}

func pgLanguageCanceled(t *testing.T, p *fixture.Pair, b fixture.Backend, held fixture.HeldStatement) {
	t.Helper()
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	rows, err := b.DB.QueryContext(ctx, pgLanguageProbe, "internal")
	if rows != nil {
		rows.Close()
		t.Fatal("canceled Query published cursor")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("Query cancellation", err)
	}
	stmt, err := b.DB.PrepareContext(ctx, pgLanguageProbe)
	if stmt != nil {
		stmt.Close()
		t.Fatal("canceled Prepare published statement")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("Prepare cancellation", err)
	}
	rows, err = held.QueryContext(ctx, pgLanguageProbe, "internal")
	if rows != nil {
		rows.Close()
		t.Fatal("canceled held execution published cursor")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("held execution cancellation", err)
	}
	pgLanguageHeld(t, p, b, held)
	fixture.PgDescriptionRows(t, p, b, pgLanguageStep(b, pgLanguageProbe, pgLanguageInternal(), "internal"))
}

func TestReferencePGLanguageHeldExecution(t *testing.T) {
	pgLanguageScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		stmt, err := b.DB.PrepareContext(p.Ctx, pgLanguageProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgLanguageProbe}
		pgLanguageHeld(t, p, b, held)
		if !b.PG {
			pgLanguageCanceled(t, p, b, held)
		}
	})
}
