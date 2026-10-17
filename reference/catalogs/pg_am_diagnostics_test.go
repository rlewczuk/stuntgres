package reference_test

import (
	"context"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGAMDiagnostics(t *testing.T) {
	pgAMScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ query, code, message string }{
			{"SELECT missing FROM pg_catalog.pg_am", "42703", `column "missing" does not exist`},
			{"SELECT 'x'::oid FROM pg_catalog.pg_am WHERE FALSE", "22P02", `invalid input syntax for type oid: "x"`},
			{"SELECT '4294967296'::oid FROM pg_catalog.pg_am WHERE FALSE", "22003", `value "4294967296" is out of range for type oid`},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: "8"})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			}
		}
		stmt, err := b.DB.PrepareContext(p.Ctx, pgAMProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgAMProbe}
		pgAMRows(t, p, b, held, pgAMProbe, pgAMLookup(int64(2), false), false, int64(2))
		for _, tc := range []struct{ value, code, message string }{
			{"x", "22P02", `invalid input syntax for type oid: "x"`},
			{"4294967296", "22003", `value "4294967296" is out of range for type oid`},
		} {
			for _, route := range []struct {
				e        fixture.Executor
				prepared bool
			}{{b.DB, false}, {b.DB, true}, {held, false}} {
				want := fixture.Diagnostic{Code: tc.code, Message: tc.message, Context: "unnamed portal parameter $1 = '...'"}
				s := fixture.ImplicitError(pgAMProbe, want, tc.value)
				s.SelectRows, s.Prepared, s.ErrorStage = true, route.prepared, "execute"
				fixture.RunStep(t, p.Ctx, route.e, b.Recorder, s)
			}
			pgAMRows(t, p, b, held, pgAMProbe, pgAMLookup(int64(403), false), false, int64(403))
		}
		pgAMInventory(t, p, b)
	})
}

var pgAMMutations = []string{
	"INSERT INTO pg_catalog.pg_am SELECT * FROM pg_catalog.pg_am WHERE FALSE",
	"INSERT INTO pg_catalog.pg_am SELECT * FROM pg_catalog.pg_am WHERE oid=2",
	"UPDATE pg_catalog.pg_am SET amname='heap' WHERE oid=2",
	"UPDATE pg_catalog.pg_am SET amname='heap' WHERE FALSE",
	"DELETE FROM pg_catalog.pg_am WHERE oid=2",
	"DELETE FROM pg_catalog.pg_am WHERE FALSE",
	"TRUNCATE pg_catalog.pg_am",
	"MERGE INTO pg_catalog.pg_am a USING (SELECT 2::oid AS id) s ON a.oid=s.id WHEN MATCHED THEN UPDATE SET amname='heap'",
	"WITH denied AS (UPDATE pg_catalog.pg_am SET amname='heap' WHERE FALSE RETURNING oid) SELECT * FROM denied",
}

func pgAMPermissions(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE ROLE pgam_reader", 0))
	run(fixture.Exec("CREATE TABLE pgam_sentinel(v INT)", 0))
	run(fixture.Exec("INSERT INTO pgam_sentinel VALUES(7)", 1))
	// Hold real statements planned under the owner, but never execute a catalog
	// mutation as a real PostgreSQL superuser.
	commands := make([]fixture.HeldStatement, len(pgAMMutations))
	for i, q := range pgAMMutations {
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatalf("owner prepare %q: %v", q, err)
		}
		defer stmt.Close()
		commands[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	}
	reader, err := c.PrepareContext(p.Ctx, pgAMProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	held := fixture.HeldStatement{Executor: c, Statement: reader, SQL: pgAMProbe}
	run(fixture.Exec("SET ROLE pgam_reader", 0))
	defer c.ExecContext(context.Background(), "RESET ROLE")
	pgAMReads(t, p, b, c)
	pgAMRows(t, p, b, held, pgAMProbe, pgAMLookup(int64(2), false), false, int64(2))
	for i, q := range pgAMMutations {
		message := "permission denied for table pg_am"
		if strings.HasPrefix(q, "TRUNCATE") {
			message = `permission denied: "pg_am" is a system catalog`
		}
		for _, route := range []struct {
			e        fixture.Executor
			prepared bool
		}{{c, false}, {c, true}, {commands[i], false}} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: message})
			s.SelectRows, s.Prepared, s.ErrorStage = strings.HasPrefix(q, "WITH"), route.prepared, "execute"
			fixture.RunStep(t, p.Ctx, route.e, b.Recorder, s)
			pgAMRows(t, p, b, c, "SELECT * FROM pg_am ORDER BY oid", pgAMFacts(b.PG, false), false)
			// Inspect user effects as the owner, on a separate real connection.
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v FROM pgam_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
	}
	run(fixture.Exec("RESET ROLE", 0))
	if !b.PG {
		for _, q := range pgAMMutations {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
			s.SelectRows, s.ErrorStage = strings.HasPrefix(q, "WITH"), "execute"
			run(s)
		}
		// Virtual catalog rows are not physical lockable storage.
		s := fixture.ImplicitError("SELECT * FROM pg_am FOR UPDATE", fixture.Diagnostic{Code: "0A000", Message: "row locking is not supported for virtual catalog relations"})
		s.SelectRows = true
		run(s)
	}
	pgAMRows(t, p, b, c, "SELECT * FROM pg_am ORDER BY oid", pgAMFacts(b.PG, false), false)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v FROM pgam_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
}

func TestReferencePGAMPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgam_reader"})
	pgAMScenario(t, pgAMPermissions)
}
