package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePseudoTypesPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pseudo_saved")
	routineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		routineExec(t, p, b, c,
			"CREATE SCHEMA pseudo_saved",
			"CREATE FUNCTION pseudo_saved.id(x anyelement) RETURNS anyelement LANGUAGE SQL AS $$SELECT x$$",
			"CREATE FUNCTION pseudo_saved.first(a anycompatible,b anycompatible) RETURNS anycompatible LANGUAGE SQL AS $$SELECT a$$",
			"CREATE FUNCTION pseudo_saved.multi(anycompatiblemultirange) RETURNS anycompatiblemultirange LANGUAGE SQL AS $$SELECT $1$$",
			"CREATE FUNCTION pseudo_saved.pl(x anyelement) RETURNS anyelement LANGUAGE plpgsql AS $$DECLARE y x%TYPE; BEGIN y := x; RETURN y; END$$",
			"CREATE FUNCTION pseudo_saved.pl_multi(anycompatiblerange) RETURNS anycompatiblemultirange LANGUAGE plpgsql AS $$BEGIN RETURN NULL; END$$",
			"CREATE FUNCTION pseudo_saved.rec() RETURNS record LANGUAGE SQL AS $$SELECT 1 AS a,'x'::text AS b$$",
			"CREATE TABLE pseudo_saved.data(v integer DEFAULT pseudo_saved.id(9))",
			"CREATE VIEW pseudo_saved.result AS SELECT pseudo_saved.id(v) AS v FROM pseudo_saved.data")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO pseudo_saved.data VALUES(7)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO pseudo_saved.data DEFAULT VALUES", 1))
		check := func(backend fixture.Backend, executor fixture.Executor, recorder *fixture.Recorder) {
			t.Helper()
			for _, step := range []fixture.Step{
				fixture.Select("SELECT v FROM pseudo_saved.result ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(9)}}),
				fixture.Select("SELECT pseudo_saved.id('x'::text) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"x"}}),
				fixture.Select("SELECT pseudo_saved.pl('y'::text) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"y"}}),
				routineSelect(backend, "SELECT pseudo_saved.multi('{[1,3)}'::int4multirange) AS v", "v", "INT4MULTIRANGE", "{[1,3)}"),
				routineSelect(backend, "SELECT pseudo_saved.pl_multi('[1,3)'::int4range) AS v", "v", "INT4MULTIRANGE", nil),
				fixture.Select("SELECT pseudo_saved.rec() AS v", []string{"v"}, []string{"RECORD"}, [][]any{{"(1,x)"}}),
				fixture.Select("SELECT proargtypes::text,prorettype::integer,proargnames::text FROM pg_proc WHERE oid='pseudo_saved.id(anyelement)'::regprocedure", []string{"proargtypes", "prorettype", "proargnames"}, []string{"TEXT", "INT4", "TEXT"}, [][]any{{"2283", int64(2283), "{x}"}}),
			} {
				fixture.RunStep(t, p.Ctx, executor, recorder, step)
			}
			for _, query := range []string{"SELECT pg_typeof(pseudo_saved.multi(NULL::int4multirange))::text AS v", "SELECT pg_typeof(pseudo_saved.pl_multi('[1,3)'::int4range))::text AS v"} {
				fixture.RunStep(t, p.Ctx, executor, recorder, fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{"int4multirange"}}))
			}
		}
		check(b, c, b.Recorder)
		if b.PG {
			return
		}
		held, err := c.QueryContext(p.Ctx, "SELECT pseudo_saved.id(7) AS v")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var stream bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &stream); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(stream.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(b, c, b.Recorder)
		var retained int64
		if !held.Next() {
			t.Fatal(held.Err())
		}
		if err := held.Scan(&retained); err != nil || retained != 7 {
			t.Fatal(retained, err)
		}
		if held.Next() || held.Err() != nil {
			t.Fatal(held.Err())
		}
		held.Close()
		connector, err := stuntgres.NewDriver().OpenConnector("pseudo-destination")
		if err != nil {
			t.Fatal(err)
		}
		destination := sql.OpenDB(connector)
		defer destination.Close()
		if err := stuntgres.Restore(p.Ctx, destination, bytes.NewReader(stream.Bytes())); err != nil {
			t.Fatal(err)
		}
		check(b, destination, b.Recorder)
		if p.Postgres != nil {
			pg, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pg.Close()
			if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS pseudo_saved CASCADE"); err != nil {
				t.Fatal(err)
			}
			if _, err := pg.ExecContext(p.Ctx, stream.String()); err != nil {
				t.Fatal("actual emitted PostgreSQL script", err)
			}
			check(fixture.Backend{PG: true}, pg, p.PostgresRecord)
			if _, err := pg.ExecContext(p.Ctx, "SET check_function_bodies=true; DROP SCHEMA pseudo_saved CASCADE"); err != nil {
				t.Fatal(err)
			}
		}
		bad := "CREATE FUNCTION public.pseudo_unsafe() RETURNS internal LANGUAGE SQL AS $$SELECT NULL$$;"
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("unsafe restore succeeded")
		}
		check(b, c, b.Recorder)
	})
}
