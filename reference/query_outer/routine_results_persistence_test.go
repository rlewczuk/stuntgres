package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineResultsPersistence(t *testing.T) {
	resultsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		resultsMultiSetup(t, p, b, c)
		resultsSetSetup(t, p, b, c)
		resultsCompositeSetup(t, p, b, c)
		resultsExec(t, p, b, c,
			"CREATE TABLE rr_stored(id integer,p rr_pair)",
			"CREATE VIEW rr_fields AS SELECT (rr_multi(4)).inc AS inc,(rr_multi(4)).twice AS twice",
			"CREATE VIEW rr_source AS SELECT key,val FROM rr_table(2)",
			"CREATE FUNCTION rr_standard(IN x integer,OUT inc integer,OUT twice integer) BEGIN ATOMIC SELECT x+1,x*2; END")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rr_stored VALUES(1,'(4,8)'),(2,NULL),(3,'(,)')", 3))
		// NULL in persistent source data must retain distinct cardinality from EOF.
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rr_rows VALUES(3,NULL)", 1))
		check := func(conn *sql.Conn, backend fixture.Backend) {
			resultsMultiCheck(t, p, backend, conn)
			resultsSetCheck(t, p, backend, conn)
			resultsBoth(t, p, backend, conn, "SELECT * FROM rr_standard(4)", []string{"inc", "twice"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(8)}})
			resultsStoredBoth(t, p, backend, conn, "SELECT inc,twice FROM rr_fields", []string{"inc", "twice"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(8)}})
			resultsStoredBoth(t, p, backend, conn, "SELECT key,val FROM rr_source ORDER BY key,val", []string{"key", "val"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}})
			resultsStoredBoth(t, p, backend, conn, "SELECT id,p FROM rr_stored ORDER BY id", []string{"id", "p"}, []string{"INT4", ""}, [][]any{{int64(1), "(4,8)"}, {int64(2), nil}, {int64(3), "(,)"}})
			resultsBoth(t, p, backend, conn, "SELECT (p).key,(p).val FROM rr_stored ORDER BY id", []string{"key", "val"}, []string{"INT4", "INT4"}, [][]any{{int64(4), int64(8)}, {nil, nil}, {nil, nil}})
			resultsBoth(t, p, backend, conn, "SELECT rr_named(4) AS v,rr_whole(4) AS w,rr_arg('(4,6)'::rr_pair) AS sum", []string{"v", "w", "sum"}, []string{"", "", "INT4"}, [][]any{{"(4,8)", "(4,8)", int64(10)}})
			resultsBoth(t, p, backend, conn, "SELECT * FROM rr_table(3) ORDER BY key,val", []string{"key", "val"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}, {int64(2), int64(20)}, {int64(3), nil}})
			resultsBoth(t, p, backend, conn, "SELECT rr_empty_pair(0) AS absent,rr_null_pair() AS present", []string{"absent", "present"}, []string{"", ""}, [][]any{{nil, "(,)"}})
			resultsBothMetadata(t, p, backend, conn, "SELECT proargnames::text AS names,proargmodes::text AS modes,pronargs::integer AS inputs,proretset AS sets FROM pg_proc WHERE proname='rr_multi'", []string{"names", "modes", "inputs", "sets"}, []string{"TEXT", "TEXT", "INT4", "BOOL"}, [][]any{{"{x,inc,twice}", "{i,o,o}", int64(1), false}}, []resultsNativeColumn{resultsTextMetadata, resultsTextMetadata, {}, resultsNonNullMetadata})
			resultsBothMetadata(t, p, backend, conn, "SELECT proname::text AS name,proretset AS sets FROM pg_proc WHERE proname IN ('rr_record_set','rr_named_set','rr_table','rr_set') ORDER BY proname", []string{"name", "sets"}, []string{"TEXT", "BOOL"}, [][]any{{"rr_named_set", true}, {"rr_record_set", true}, {"rr_set", true}, {"rr_table", true}}, []resultsNativeColumn{resultsTextMetadata, resultsNonNullMetadata})
		}
		check(c, b)
		resultsPersistenceAllocationCheck(t, p, b, c)
		if b.PG {
			return
		}
		retained, err := c.QueryContext(p.Ctx, "SELECT * FROM rr_multi(4)")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		old, err := c.PrepareContext(p.Ctx, "SELECT rr_named($1::integer) AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		var emitted []byte
		for round := range 2 {
			emitted = resultsNativeReplayRound(t, p, b, c, check)
			resultsPersistenceAllocationCheck(t, p, b, c)
			if round == 0 {
				rows, err := old.QueryContext(p.Ctx, 4)
				if rows != nil {
					rows.Close()
					t.Fatal("old composite prepared result silently retargeted")
				}
				want := fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type")
				fixture.AssertDiagnostic(t, err, &want)
				if err := old.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if p.Postgres != nil {
				func() {
					pg, err := p.Postgres.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer pg.Close()
					if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
					if _, err := pg.ExecContext(p.Ctx, string(emitted)); err != nil {
						t.Fatal("unchanged result dump replay", err)
					}
					// The unchanged dump disables PostgreSQL body checking for
					// replay. Restore admission checks before new CREATE probes.
					if _, err := pg.ExecContext(p.Ctx, "SET check_function_bodies = true"); err != nil {
						t.Fatal(err)
					}
					check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
					resultsPersistenceAllocationCheck(t, p, fixture.Backend{PG: true, Recorder: p.PostgresRecord}, pg)
					if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
				}()
			}
		}
		resultsMetadata(b, []string{"inc", "twice"}, []string{"INT4", "INT4"}, false)(t, retained)
		if !retained.Next() {
			t.Fatal("detached original row lost", retained.Err())
		}
		var inc, twice int64
		if err := retained.Scan(&inc, &twice); err != nil || inc != 5 || twice != 8 {
			t.Fatal(inc, twice, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained cardinality", retained.Err())
		}
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/independent")
		if err != nil {
			t.Fatal(err)
		}
		raw := sql.OpenDB(connector)
		defer raw.Close()
		recorded, rec := fixture.RecordedNativePool(t, connector)
		cloneBackend := fixture.Backend{Recorder: rec}
		fixture.RunStep(t, p.Ctx, recorded, rec, fixture.Exec("CREATE TYPE rr_collision AS (x integer)", 0))
		fixture.RunStep(t, p.Ctx, recorded, rec, fixture.Exec("CREATE TABLE rr_filler(v integer)", 0))
		if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		clone, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer clone.Close()
		// Restore has no PostgreSQL control analogue. The same public SQL
		// allocation/diagnostic witness above now runs on a fresh native owner.
		t.Run("fresh_driver_restored", func(t *testing.T) {
			resultsPersistenceAllocationCheck(t, p, cloneBackend, clone)
		})
		check(clone, cloneBackend)
		check(c, b)
		// Independent owners must not share persistent rows or function frames.
		fixture.RunStep(t, p.Ctx, clone, rec, fixture.Exec("INSERT INTO rr_stored VALUES(4,'(9,18)')", 1))
		resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM rr_stored", 3)
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		bad := before.String() + "; CREATE TABLE rr_prefix(v integer); CREATE FUNCTION rr_late_bad(OUT a integer,OUT b integer) LANGUAGE SQL AS 'SELECT 1';"
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("late invalid output accepted")
		}
		check(c, b)
		resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM pg_class WHERE relname='rr_prefix'", 0)
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(before.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal("pre-canceled Restore", err)
		}
		check(c, b)
		failure := errors.New("routine results reader failure")
		if err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(strings.NewReader("CREATE TABLE rr_reader_prefix(v integer);"), resultsFailReader{failure})); !errors.Is(err, failure) {
			t.Fatal("Restore reader error", err)
		}
		check(c, b)
		resultsCountBoth(t, p, b, c, "SELECT count(*) AS v FROM pg_class WHERE relname='rr_reader_prefix'", 0)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Error("failed controls changed persistent catalog/data")
		}
	})
}

// Exercise admission after replay, not just reads of restored definitions.
// Expectations are shared with PostgreSQL before the native-only control phase.
func resultsPersistenceAllocationCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	var incumbent int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname='rr_multi'").Scan(&incumbent); err != nil || incumbent <= 0 {
		t.Fatal("incumbent routine identity", incumbent, err)
	}
	want := fixture.ControlsReferenceDiagnostic("42P13", "return type mismatch in function declared to return record")
	want.Detail = "Final statement returns too few columns."
	want.Context = `SQL function "rr_bad"`
	resultsError(t, p, c, "CREATE FUNCTION rr_bad(OUT a integer,OUT b integer) LANGUAGE SQL AS 'SELECT 1'", false, "execute", want)
	var invalidCount int64
	if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM pg_proc WHERE proname='rr_bad'").Scan(&invalidCount); err != nil || invalidCount != 0 {
		t.Fatal("invalid routine published", invalidCount, err)
	}
	resultsExec(t, p, b, c, "CREATE FUNCTION rr_after_restore(OUT a integer,OUT b integer) LANGUAGE SQL AS 'SELECT 11,22'")
	var created, retained int64
	if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname='rr_after_restore'").Scan(&created); err != nil || created <= 0 || created == incumbent {
		t.Fatal("new routine must have a valid distinct identity", created, incumbent, err)
	}
	if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname='rr_multi'").Scan(&retained); err != nil || retained != incumbent {
		t.Fatal("new CREATE changed restored identity", retained, incumbent, err)
	}
	// Do not pin generated numbers to the oracle's allocator sequence.
	for _, tc := range []struct {
		catalog string
		count   int64
	}{{"pg_proc", 1}, {"pg_type", 0}, {"pg_class", 0}, {"pg_namespace", 0}} {
		var count int64
		if err := c.QueryRowContext(p.Ctx, "SELECT count(*) FROM "+tc.catalog+" WHERE oid=$1::oid", created).Scan(&count); err != nil || count != tc.count {
			t.Fatal("new routine identity collision", tc.catalog, created, count, err)
		}
	}
	resultsBoth(t, p, b, c, "SELECT * FROM rr_after_restore()", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(22)}})
	resultsBoth(t, p, b, c, "SELECT * FROM rr_multi(4)", []string{"inc", "twice"}, []string{"INT4", "INT4"}, [][]any{{int64(5), int64(8)}})
	// Keep the original persistent scenario and emitted stream unchanged.
	resultsExec(t, p, b, c, "DROP FUNCTION rr_after_restore()")
}

type resultsFailReader struct{ err error }

func (r resultsFailReader) Read([]byte) (int, error) { return 0, r.err }
