package reference_test

import (
	"context"
	"testing"

	"stuntgres/reference/fixture"
)

// Independently specified diagnostics; the pinned oracle must confirm all
// fields and stages, rather than deriving expectations from native errors.
func pgAmprocDiagnosticFailures(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	var notices []fixture.Diagnostic
	pool, _, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
	b.DB, b.Recorder = pool, recorder
	defer func() {
		if len(notices) != 0 {
			t.Fatal("unexpected diagnostic notices", notices)
		}
	}()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE amproc_diag_sentinel(v INT)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO amproc_diag_sentinel VALUES(7)", 1))
	checkSentinel := func() {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v FROM amproc_diag_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
	}
	for _, tc := range []struct{ query, code, message, position string }{
		{"SELECT missing FROM pg_catalog.pg_amproc", "42703", `column "missing" does not exist`, "8"},
		{"SELECT /*é*/ missing FROM pg_catalog.pg_amproc", "42703", `column "missing" does not exist`, "14"},
		{"SELECT 'x'::oid FROM pg_catalog.pg_amproc WHERE FALSE", "22P02", `invalid input syntax for type oid: "x"`, "8"},
		{"SELECT '4294967296'::oid FROM pg_catalog.pg_amproc WHERE FALSE", "22003", `value "4294967296" is out of range for type oid`, "8"},
	} {
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: tc.position})
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			checkSentinel()
		}
	}
	stmt, err := b.DB.PrepareContext(p.Ctx, pgAmprocProbe)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: pgAmprocProbe}
	pgAmprocRows(t, p, b, held, pgAmprocProbe, pgAmprocFacts(t, p, b)[1:2], false, pgAmprocFacts(t, p, b)[1][0])
	for _, tc := range []struct{ value, code, message string }{
		{"x", "22P02", `invalid input syntax for type oid: "x"`},
		{"4294967296", "22003", `value "4294967296" is out of range for type oid`},
	} {
		for _, route := range []struct {
			e        fixture.Executor
			prepared bool
		}{{b.DB, false}, {b.DB, true}, {held, false}} {
			s := fixture.ImplicitError(pgAmprocProbe, fixture.Diagnostic{Code: tc.code, Message: tc.message, Context: "unnamed portal parameter $1 = '...'"}, tc.value)
			s.SelectRows, s.Prepared, s.ErrorStage = true, route.prepared, "execute"
			fixture.RunStep(t, p.Ctx, route.e, b.Recorder, s)
			checkSentinel()
			pgAmprocRows(t, p, b, held, pgAmprocProbe, pgAmprocFacts(t, p, b)[2:3], false, pgAmprocFacts(t, p, b)[2][0])
		}
	}
	pgAmprocRows(t, p, b, b.DB, pgAmprocWitnessQuery, pgAmprocFacts(t, p, b), false)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TABLE amproc_diag_sentinel", 0))
}

func TestReferencePGAmprocDiagnostics(t *testing.T) { pgOpclassScenario(t, pgAmprocDiagnosticFailures) }

func TestReferencePGAmprocPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"amproc_reader"})
	pgOpclassScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		pool, _, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.DB, b.Recorder = pool, recorder
		defer func() {
			if len(notices) != 0 {
				t.Fatal("unexpected permission notices", notices)
			}
		}()
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE ROLE amproc_reader", 0))
		run(fixture.Exec("CREATE TABLE amproc_sentinel(v INT)", 0))
		run(fixture.Exec("INSERT INTO amproc_sentinel VALUES(7)", 1))
		mutations := []string{
			"INSERT INTO pg_catalog.pg_amproc SELECT * FROM pg_catalog.pg_amproc WHERE amprocfamily=1976 AND amproclefttype=23 AND amprocrighttype=23 AND amprocnum=1",
			"INSERT INTO pg_catalog.pg_amproc SELECT * FROM pg_catalog.pg_amproc WHERE FALSE",
			"UPDATE pg_catalog.pg_amproc SET amprocnum=1 WHERE amprocfamily=1976 AND amproclefttype=23 AND amprocrighttype=23 AND amprocnum=1",
			"UPDATE pg_catalog.pg_amproc SET amprocnum=1 WHERE FALSE",
			"DELETE FROM pg_catalog.pg_amproc WHERE amprocfamily=1976 AND amproclefttype=23 AND amprocrighttype=23 AND amprocnum=1",
			"DELETE FROM pg_catalog.pg_amproc WHERE FALSE",
		}
		// Like pg_amop, pg_amproc admits semantic planning before checking
		// execution-time authority. Never execute PostgreSQL owner mutations.
		commands := make([]fixture.HeldStatement, len(mutations))
		for i, q := range mutations {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("owner prepare", q, err)
			}
			defer stmt.Close()
			commands[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		}
		reader, err := c.PrepareContext(p.Ctx, pgAmprocProbe)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		held := fixture.HeldStatement{Executor: c, Statement: reader, SQL: pgAmprocProbe}
		run(fixture.Exec("SET ROLE amproc_reader", 0))
		defer c.ExecContext(context.Background(), "RESET ROLE")
		type permissionRoute struct {
			e        fixture.Executor
			prepared bool
		}
		for i, q := range mutations {
			// A distinct reader-prepared handle proves held execution denial,
			// independently of both fresh preparation and owner-plan reuse.
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("reader prepare", q, err)
			}
			defer stmt.Close()
			routes := []permissionRoute{
				{c, false},
				{c, true},
				{commands[i], false},
				{fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, false},
			}
			for _, route := range routes {
				pgAmprocShapes(t, p, b, c)
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_amproc"})
				s.Prepared, s.ErrorStage = route.prepared, "execute"
				fixture.RunStep(t, p.Ctx, route.e, b.Recorder, s)
				pgAmprocRows(t, p, b, c, pgAmprocWitnessQuery, pgAmprocFacts(t, p, b), false)
				pgAmprocRows(t, p, b, held, pgAmprocProbe, pgAmprocFacts(t, p, b)[1:2], false, pgAmprocFacts(t, p, b)[1][0])
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT v FROM amproc_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
		}
		run(fixture.Exec("RESET ROLE", 0))
		if !b.PG {
			for i, q := range mutations {
				routes := []permissionRoute{{c, false}, {c, true}, {commands[i], false}}
				for _, route := range routes {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
					s.Prepared, s.ErrorStage = route.prepared, "execute"
					fixture.RunStep(t, p.Ctx, route.e, b.Recorder, s)
					pgAmprocRows(t, p, b, c, pgAmprocWitnessQuery, pgAmprocFacts(t, p, b), false)
					pgAmprocRows(t, p, b, held, pgAmprocProbe, pgAmprocFacts(t, p, b)[1:2], false, pgAmprocFacts(t, p, b)[1][0])
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM amproc_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
				}
			}
		}
	})
}
