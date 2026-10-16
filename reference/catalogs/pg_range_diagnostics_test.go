package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func pgRangeMissing(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, tc := range []struct{ query, code, message, position string }{
		{"SELECT missing FROM pg_catalog.pg_range", "42703", `column "missing" does not exist`, "8"},
		{"SELECT /*é*/ missing FROM pg_catalog.pg_range", "42703", `column "missing" does not exist`, "14"},
		{"SELECT 'bad'::oid FROM pg_catalog.pg_range WHERE FALSE", "22P02", `invalid input syntax for type oid: "bad"`, "8"},
		{"SELECT '4294967296'::oid FROM pg_catalog.pg_range WHERE FALSE", "22003", `value "4294967296" is out of range for type oid`, "8"},
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

var pgRangeMutations = []struct{ query, stage string }{
	{"INSERT INTO pg_catalog.pg_range VALUES(999,23,0,0,1978,0,3922)", "execute"},
	{"UPDATE pg_catalog.pg_range SET rngcollation=0 WHERE rngtypid=3904", "prepare"},
	{"UPDATE pg_catalog.pg_range SET rngcollation=0 WHERE FALSE", "prepare"},
	{"DELETE FROM pg_catalog.pg_range WHERE rngtypid=3904", "execute"},
	{"DELETE FROM pg_catalog.pg_range WHERE FALSE", "execute"},
}

func pgRangeProtected(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	if b.PG {
		return
	} // Never write/lock PostgreSQL system catalogs as superuser.
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE pgrange_diagnostic_sentinel(id INT)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO pgrange_diagnostic_sentinel VALUES(7)", 1))
	stmt, err := b.DB.PrepareContext(p.Ctx, pgRangeProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgRangeProbe}
	check := func() {
		pgRangeInventory(t, p, b)
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT * FROM pgrange_diagnostic_sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		pgRangeRows(t, p, b, held, pgRangeProbe, pgRangeLookup(int64(3904)), false, int64(3904))
	}
	for _, tc := range pgRangeMutations {
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
	for _, predicate := range []string{"FALSE", "rngtypid=3904"} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("SELECT * FROM pg_catalog.pg_range WHERE "+predicate+" FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			check()
		}
	}
}
func TestReferencePGRangeDiagnostics(t *testing.T) {
	pgRangeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		pgRangeMissing(t, p, b)
		pgRangeProtected(t, p, b)
	})
}
func pgRangePermissions(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	owner, reader := fixture.LockConnections(t, p.Ctx, b.DB)
	fixture.RunStep(t, p.Ctx, owner, b.Recorder, fixture.Exec("CREATE ROLE pgrange_reader", 0))
	fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Exec("SET ROLE pgrange_reader", 0))
	defer reader.ExecContext(context.Background(), "RESET ROLE")
	pgRangeRows(t, p, b, reader, "SELECT * FROM pg_range ORDER BY rngtypid", pgRangeFacts(), false)
	pgRangeReads(t, p, b, reader)
	if b.PG {
		for _, tc := range pgRangeMutations {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_range"})
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, reader, b.Recorder, s)
				pgRangeRows(t, p, b, reader, "SELECT * FROM pg_range ORDER BY rngtypid", pgRangeFacts(), false)
			}
		}
	} else {
		for _, tc := range pgRangeMutations {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared {
					s.ErrorStage = tc.stage
				}
				fixture.RunStep(t, p.Ctx, reader, b.Recorder, s)
				pgRangeRows(t, p, b, reader, "SELECT * FROM pg_range ORDER BY rngtypid", pgRangeFacts(), false)
			}
		}
	}
	pgRangeReads(t, p, b, reader)
}
func TestReferencePGRangePermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgrange_reader"})
	pgRangeScenario(t, pgRangePermissions)
}
