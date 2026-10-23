package reference_test

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

const cubeBindingSchema = "Cube Binding Path"
const cubeBindingShadow = "cube_binding_shadow"

func cubeBindingIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func cubeBindingResults(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Select("SELECT id,member,chosen FROM public.cube_binding_view ORDER BY id", []string{"id", "member", "chosen"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, true}, {int64(2), true, false}, {int64(3), nil, false}}),
		cubeSelect(b, "SELECT id,nullified FROM public.cube_binding_view ORDER BY id", []string{"id", "nullified"}, [][]any{{int64(1), nil}, {int64(2), "(2)"}, {int64(3), nil}}),
		cubeSelect(b, `SELECT 1 AS id,public.cube_binding_function('2'::"Cube Binding Path".cube) AS value`, []string{"id", "value"}, [][]any{{int64(1), "(2)"}}),
		cubeSelect(b, "SELECT 1 AS id,v FROM public.cube_binding_defaults", []string{"id", "v"}, [][]any{{int64(1), nil}}),
		cubeSelect(b, "SELECT 1 AS id,d FROM public.cube_binding_defaults", []string{"id", "d"}, [][]any{{int64(1), nil}}),
		cubeSelect(b, "SELECT id,g FROM public.cube_binding_data ORDER BY id", []string{"id", "g"}, [][]any{{int64(1), nil}, {int64(2), "(2)"}, {int64(3), nil}}),
		cubeSelect(b, "SELECT id,value FROM public.cube_binding_materialized ORDER BY id", []string{"id", "value"}, [][]any{{int64(1), nil}, {int64(2), "(2)"}, {int64(3), nil}}),
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	}
	for _, once := range []fixture.Step{
		fixture.Select("SELECT value FROM public.cube_binding_once_in", []string{"value"}, []string{"BOOL"}, [][]any{{true}}),
		fixture.Select("SELECT value FROM public.cube_binding_once_not_in", []string{"value"}, []string{"BOOL"}, [][]any{{false}}),
		fixture.Select("SELECT value FROM public.cube_binding_once_case", []string{"value"}, []string{"BOOL"}, [][]any{{true}}),
		cubeSelect(b, "SELECT 1 AS id,value FROM public.cube_binding_once_nullif", []string{"id", "value"}, [][]any{{int64(1), "(1)"}}),
	} {
		for _, s := range []fixture.Step{once, fixture.Prepared(once)} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT setval('"Cube Binding Path".calls',1,false) AS value`, []string{"value"}, []string{"INT8"}, [][]any{{int64(1)}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(`SELECT last_value FROM "Cube Binding Path".calls`, []string{"last_value"}, []string{"INT8"}, [][]any{{int64(1)}}))
		}
	}
}

func TestReferenceCubeComparisonBindingPath(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, cubeBindingSchema)
	fixture.SchemaReferenceCleanup(t, cubeBindingShadow)
	cubeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ns := cubeBindingIdentifier(cubeBindingSchema)
		run := func(s fixture.Step) {
			defer func() {
				if t.Failed() {
					t.Logf("cube binding setup SQL: %s", s.Query)
				}
			}()
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		cleanup := "DROP TABLE IF EXISTS public.cube_binding_defaults CASCADE; DROP TABLE IF EXISTS public.cube_binding_data CASCADE; DROP SCHEMA IF EXISTS " + ns + " CASCADE; DROP SCHEMA IF EXISTS " + cubeBindingShadow + " CASCADE"
		if b.PG {
			defer func() {
				if _, err := c.ExecContext(p.Ctx, cleanup); err != nil {
					t.Error(err)
				}
			}()
		}
		run(fixture.Exec("CREATE SCHEMA "+ns, 0))
		run(fixture.Exec("CREATE SCHEMA "+cubeBindingShadow, 0))
		run(fixture.Exec("CREATE EXTENSION cube SCHEMA "+ns, 0))
		run(fixture.Exec("SET search_path="+ns+",public", 0))
		run(fixture.Exec("CREATE FUNCTION "+cubeBindingShadow+".opposite("+ns+".cube,"+ns+".cube) RETURNS boolean LANGUAGE SQL AS $$ SELECT false $$", 0))
		run(fixture.Exec("CREATE OPERATOR "+cubeBindingShadow+".= (LEFTARG="+ns+".cube,RIGHTARG="+ns+".cube,FUNCTION="+cubeBindingShadow+".opposite)", 0))
		run(fixture.Exec("CREATE TABLE public.cube_binding_data(id integer NOT NULL,v "+ns+".cube,g "+ns+".cube GENERATED ALWAYS AS (NULLIF(v,'1'::"+ns+".cube)) STORED,CHECK(NULLIF(v,'1'::"+ns+".cube) IS NULL OR v='2'::"+ns+".cube))", 0))
		run(fixture.Exec("INSERT INTO public.cube_binding_data(id,v) VALUES(1,'1'),(2,'2'),(3,NULL)", 3))
		run(fixture.Exec("CREATE INDEX cube_binding_index ON public.cube_binding_data((NULLIF(v,'1'::"+ns+".cube))) WHERE NULLIF(v,'1'::"+ns+".cube) IS NULL OR v='2'::"+ns+".cube", 0))
		run(fixture.Exec("CREATE VIEW public.cube_binding_view AS SELECT id,v IN ('1'::"+ns+".cube,'2'::"+ns+".cube) AS member,CASE v WHEN '1'::"+ns+".cube THEN true ELSE false END AS chosen,NULLIF(v,'1'::"+ns+".cube) AS nullified FROM public.cube_binding_data", 0))
		run(fixture.Exec("CREATE MATERIALIZED VIEW public.cube_binding_materialized AS SELECT id,NULLIF(v,'1'::"+ns+".cube) AS value FROM public.cube_binding_data", 3))
		run(fixture.Exec("CREATE POLICY cube_binding_policy ON public.cube_binding_data USING(NULLIF(v,'1'::"+ns+".cube) IS NULL)", 0))
		run(fixture.Exec("CREATE RULE cube_binding_rule AS ON UPDATE TO public.cube_binding_data WHERE NEW.v IN ('1'::"+ns+".cube,'2'::"+ns+".cube) DO ALSO NOTHING", 0))
		run(fixture.Exec("CREATE DOMAIN public.cube_binding_domain AS "+ns+".cube DEFAULT NULLIF('1'::"+ns+".cube,'1'::"+ns+".cube) CHECK(NULLIF(VALUE,'1'::"+ns+".cube) IS NULL OR VALUE='2'::"+ns+".cube)", 0))
		run(fixture.Exec("CREATE TABLE public.cube_binding_defaults(v "+ns+".cube DEFAULT NULLIF('1'::"+ns+".cube,'1'::"+ns+".cube),d public.cube_binding_domain)", 0))
		run(fixture.Exec("INSERT INTO public.cube_binding_defaults DEFAULT VALUES", 1))
		run(fixture.Exec("CREATE FUNCTION public.cube_binding_function(v "+ns+".cube DEFAULT NULLIF('1'::"+ns+".cube,'1'::"+ns+".cube)) RETURNS "+ns+".cube LANGUAGE SQL RETURN NULLIF(v,'1'::"+ns+".cube)", 0))
		run(fixture.Exec("CREATE SEQUENCE "+ns+".calls", 0))
		lhs := "(nextval('\"Cube Binding Path\".calls')::text)::" + ns + ".cube"
		run(fixture.Exec("CREATE VIEW public.cube_binding_once_in AS SELECT "+lhs+" IN ('0'::"+ns+".cube,'1'::"+ns+".cube) AS value", 0))
		run(fixture.Exec("CREATE VIEW public.cube_binding_once_not_in AS SELECT "+lhs+" NOT IN ('0'::"+ns+".cube,'1'::"+ns+".cube) AS value", 0))
		run(fixture.Exec("CREATE VIEW public.cube_binding_once_case AS SELECT CASE "+lhs+" WHEN '0'::"+ns+".cube THEN false WHEN '1'::"+ns+".cube THEN true ELSE false END AS value", 0))
		run(fixture.Exec("CREATE VIEW public.cube_binding_once_nullif AS SELECT NULLIF("+lhs+",'0'::"+ns+".cube) AS value", 0))
		cubeBindingResults(t, p, b, c)
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		text := dump.String()
		if !strings.Contains(text, "-- stuntgres:comparison-context begin\nSET search_path = pg_catalog;") || !strings.Contains(text, "-- stuntgres:comparison-context end\nRESET search_path;") {
			t.Fatal("missing dedicated comparison replay context", text)
		}
		run(fixture.Exec("SET search_path=public", 0))
		run(fixture.Exec("SET extra_float_digits=2", 0))
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(text)); err != nil {
			t.Fatal(err)
		}
		settings := func() {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"public"}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SHOW extra_float_digits", []string{"extra_float_digits"}, []string{"TEXT"}, [][]any{{"2"}}))
		}
		settings()
		cubeBindingResults(t, p, b, c)
		held, err := c.QueryContext(p.Ctx, "SELECT id FROM public.cube_binding_data ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		marker := "-- stuntgres:comparison-binding "
		record := base64.StdEncoding.EncodeToString([]byte(`{"namespace":"Cube Binding Path"}`))
		wrongRecord := base64.StdEncoding.EncodeToString([]byte(`{"namespace":"cube_binding_shadow"}`))
		unknown := base64.StdEncoding.EncodeToString([]byte(`{"namespace":"Cube Binding Path","unknown":true}`))
		duplicate := base64.StdEncoding.EncodeToString([]byte(`{"namespace":"Cube Binding Path","namespace":"Cube Binding Path"}`))
		first := marker + record
		lateWindow := func(ddl string) string {
			const footer = "-- stuntgres:comparison-context end\nRESET search_path;"
			window := first + "\nSET search_path = pg_catalog, " + ns + ";\n" + ddl + "\n-- stuntgres:comparison-binding end\nSET search_path = pg_catalog;\n"
			return strings.Replace(text, footer, window+footer, 1)
		}
		for _, bad := range []string{
			"SET search_path=public;" + text,
			strings.Replace(text, first, marker+wrongRecord, 1),
			strings.Replace(text, first, marker+unknown, 1),
			strings.Replace(text, first, marker+duplicate, 1),
			strings.Replace(text, "SET search_path = pg_catalog, "+ns+";", "SET LOCAL search_path = pg_catalog, "+ns+";", 1),
			strings.Replace(text, "SET search_path = pg_catalog, "+ns+";", "SET search_path = pg_catalog, "+ns+",public;", 1),
			strings.Replace(text, "-- stuntgres:comparison-binding end\nSET search_path = pg_catalog;", "", 1),
			strings.Replace(text, "-- stuntgres:comparison-context end\nRESET search_path;", "", 1),
			strings.Replace(text, "SET search_path = pg_catalog, "+ns+";", "SET search_path = pg_catalog, "+ns+"; SELECT 1;", 1),
			lateWindow("ALTER TABLE public.cube_binding_data ALTER COLUMN id SET DEFAULT 1;"),
			lateWindow("CREATE TABLE public.cube_binding_unrelated() INHERITS(public.cube_binding_data);"),
		} {
			if bad == text {
				t.Fatal("malformed context mutation did not exercise emitted window")
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil || !strings.Contains(err.Error(), "invalid comparison binding context") {
				t.Fatal("malformed binding context diagnostic", err)
			}
			settings()
			cubeBindingResults(t, p, b, c)
		}
		var retained []int64
		for held.Next() {
			var id int64
			if err := held.Scan(&id); err != nil {
				t.Fatal(err)
			}
			retained = append(retained, id)
		}
		if err := held.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(retained, []int64{1, 2, 3}) {
			t.Fatal("failed context changed retained rows", retained)
		}
		if p.Postgres != nil {
			pgc, err := p.Postgres.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer pgc.Close()
			if _, err := pgc.ExecContext(p.Ctx, cleanup); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := pgc.ExecContext(p.Ctx, cleanup); err != nil {
					t.Error(err)
				}
			}()
			if _, err := pgc.ExecContext(p.Ctx, "RESET search_path"); err != nil {
				t.Fatal(err)
			}
			var configured string
			if err := pgc.QueryRowContext(p.Ctx, "SHOW search_path").Scan(&configured); err != nil {
				t.Fatal(err)
			}
			if _, err := pgc.ExecContext(p.Ctx, "SET search_path="+cubeBindingShadow+",pg_catalog"); err != nil {
				t.Fatal(err)
			}
			if _, err := pgc.ExecContext(p.Ctx, text); err != nil {
				t.Fatal("actual hostile-path native dump PG replay", err)
			}
			// Native snapshot comments do not populate PostgreSQL materialized views.
			if _, err := pgc.ExecContext(p.Ctx, "REFRESH MATERIALIZED VIEW public.cube_binding_materialized"); err != nil {
				t.Fatal("refresh emitted materialized definition", err)
			}
			cubeBindingResults(t, p, fixture.Backend{PG: true, Recorder: p.PostgresRecord}, pgc)
			var final string
			if err := pgc.QueryRowContext(p.Ctx, "SHOW search_path").Scan(&final); err != nil {
				t.Fatal(err)
			}
			if final != configured {
				t.Fatal("dedicated external replay did not finish at configured default", final, configured)
			}
		}
	})
}
