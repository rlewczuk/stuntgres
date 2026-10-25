package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRoutineDefaultAttributePersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "rda_saved")
	parametersScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c, "CREATE SCHEMA rda_saved", "CREATE TABLE rda_saved.t(a integer,b integer)", "CREATE TYPE rda_saved.c AS (a integer,b integer)",
			"CREATE FUNCTION rda_saved.q(x integer DEFAULT (NULL::rda_saved.t).a) RETURNS integer LANGUAGE SQL AS 'SELECT x'",
			"CREATE FUNCTION rda_saved.s(x integer DEFAULT (NULL::rda_saved.t).a) RETURNS integer LANGUAGE SQL RETURN x",
			"CREATE FUNCTION rda_saved.cq(x integer DEFAULT (NULL::rda_saved.c).a) RETURNS integer LANGUAGE SQL AS 'SELECT x'",
			"CREATE FUNCTION rda_saved.cs(x integer DEFAULT (NULL::rda_saved.c).a) RETURNS integer LANGUAGE SQL RETURN x")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO rda_saved.t VALUES(31,32)", 1))
		check := func(conn *sql.Conn, backend fixture.Backend) {
			for _, f := range []string{"q", "s", "cq", "cs"} {
				defaultAttributeCalls(t, p, backend, conn, "rda_saved."+f)
				relation := "rda_saved.t"
				if f == "cq" || f == "cs" {
					relation = "rda_saved.c"
				}
				defaultAttributeEdges(t, p, backend, conn, "rda_saved."+f+"(integer)", relation, []int64{1}, []int64{1})
				defaultAttributeTypeEdge(t, p, backend, conn, "rda_saved."+f+"(integer)", relation, 1)
			}
			defaultAttributeStoredValue(t, p, backend, conn, "SELECT a AS v FROM rda_saved.t", "INT4", int64(31), true)
		}
		check(c, b)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		// Return borrowers before every replacement; reacquire fresh handles.
		c.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE proname='q' AND pronamespace IN (SELECT oid FROM pg_namespace WHERE nspname='rda_saved')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		fresh, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fresh.Close()
		check(fresh, b)
		connector, err := stuntgres.NewDriver().OpenConnector(t.Name() + "/independent")
		if err != nil {
			t.Fatal(err)
		}
		raw := sql.OpenDB(connector)
		defer raw.Close()
		recorded, rec := fixture.RecordedNativePool(t, connector)
		cloneBackend := fixture.Backend{Recorder: rec}
		fixture.RunStep(t, p.Ctx, recorded, rec, fixture.Select("SELECT count(*) AS n FROM pg_namespace WHERE nspname='rda_saved'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		fixture.RunStep(t, p.Ctx, recorded, rec, fixture.Exec("CREATE TABLE public.rda_filler(z integer)", 0))
		if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		clone, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		check(clone, cloneBackend)
		check(fresh, b)
		userOperatorExec(t, p, cloneBackend, clone, "CREATE TABLE public.rda_sentinel(v integer)")
		fixture.RunStep(t, p.Ctx, clone, rec, fixture.Exec("INSERT INTO public.rda_sentinel VALUES(73)", 1))
		retained, err := clone.QueryContext(p.Ctx, "SELECT rda_saved.q(17) AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, raw, &before); err != nil {
			t.Fatal(err)
		}
		bad := dump.String() + "; CREATE TABLE public.rda_prefix(v integer); CREATE FUNCTION public.rda_bad(x public.rda_missing) RETURNS integer LANGUAGE SQL AS 'SELECT 1';"
		if err := stuntgres.Restore(p.Ctx, raw, strings.NewReader(bad)); err == nil {
			t.Fatal("late replacement failure accepted")
		}
		check(clone, cloneBackend)
		defaultAttributeStoredValue(t, p, cloneBackend, clone, "SELECT v FROM public.rda_sentinel", "INT4", int64(73), true)
		defaultAttributeStoredValue(t, p, cloneBackend, clone, "SELECT count(*) AS v FROM pg_class WHERE relname='rda_prefix'", "INT8", int64(0), false)
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, raw, &after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Error("failed replacement changed destination catalog/data")
		}
		if !retained.Next() {
			t.Fatal("retained row lost", retained.Err())
		}
		var value int64
		if err := retained.Scan(&value); err != nil || value != 17 {
			t.Fatal(value, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained cardinality", retained.Err())
		}
		retained.Close()
		clone.Close()
		if err := stuntgres.Reset(p.Ctx, raw); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		clone, err = recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer clone.Close()
		check(clone, cloneBackend)
		if p.Postgres != nil {
			pg, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pg.Close()
			if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS rda_saved CASCADE"); err != nil {
				t.Fatal(err)
			}
			if _, err := pg.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("unchanged emitted SQL replay", err)
			}
			check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
			if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA rda_saved CASCADE"); err != nil {
				t.Fatal(err)
			}
		}
	})
}
