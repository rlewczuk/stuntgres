package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArrayNativeControls(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE DOMAIN public.array_control_di AS INT", "CREATE DOMAIN public.array_control_dj AS public.array_control_di",
			"CREATE DOMAIN public.array_control_da AS INT[]", "CREATE DOMAIN public.array_control_dd AS public.array_control_di[]",
			"CREATE TABLE public.array_control (id INT,i INT[],t TEXT[][],d public.array_control_dj[],a public.array_control_da[],n public.array_control_dd,b BYTEA[],z TIMESTAMPTZ[],r INTERVAL[])",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec(`INSERT INTO public.array_control VALUES (1,'[-1:0][3:4]={{1,NULL},{2,3}}','{NULL,"NULL","","a,b","a\\b"}','{7,NULL}','{"{1,2}",NULL}','{9,NULL}','{"\\x00ff",NULL}','{"2024-02-29 12:00:00+00",NULL}','{"1 day 02:03:04",NULL}'),(2,NULL,NULL,NULL,NULL,NULL,NULL,NULL,NULL),(3,'{}','{}','{}','{}','{}','{}','{}','{}')`, 3))
		types := []string{"INT4", "_INT4", "_TEXT", "_ARRAY_CONTROL_DJ", "_ARRAY_CONTROL_DA", "_ARRAY_CONTROL_DI", "_BYTEA", "_TIMESTAMPTZ", "_INTERVAL"}
		if b.PG {
			types[3], types[4], types[5] = "", "", ""
		}
		raw := []string{"INT4", "_INT4", "_TEXT", "_INT4", "_INT4", "_INT4", "_BYTEA", "_TIMESTAMPTZ", "_INTERVAL"}
		check := fixture.ArrayReferenceSelect("SELECT id,i,t,d,a,n,b,z,r FROM public.array_control ORDER BY id", []string{"id", "i", "t", "d", "a", "n", "b", "z", "r"}, types, raw, [][]any{
			{int64(1), "[-1:0][3:4]={{1,NULL},{2,3}}", `{NULL,"NULL","","a,b","a\\b"}`, "{7,NULL}", `{"{1,2}",NULL}`, "{9,NULL}", `{"\\x00ff",NULL}`, `{"2024-02-29 12:00:00+00",NULL}`, `{"1 day 02:03:04",NULL}`},
			{int64(2), nil, nil, nil, nil, nil, nil, nil, nil}, {int64(3), "{}", "{}", "{}", "{}", "{}", "{}", "{}", "{}"},
		}, b.PG)
		run(check)
		run(fixture.Prepared(check))
		if !b.PG {
			fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check)
		}
		arrayReferenceTemporaryControls(t, p, c, b, check)
	})
}

func TestReferenceArraySmoke(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE DOMAIN public.array_smoke_di AS INT", 0))
		run(fixture.Exec("CREATE TABLE public.array_smoke (id INT,i INT[],t TEXT[][],d public.array_smoke_di[])", 0))
		run(fixture.Exec(`INSERT INTO public.array_smoke VALUES (1,'[-1:0][3:4]={{1,NULL},{2,3}}','{NULL,"NULL","","a,b"}','{7,NULL}')`, 1))
		input := []byte(`{NULL,"NULL","","a,b"}`)
		run(fixture.Prepared(fixture.Exec("INSERT INTO public.array_smoke VALUES (2,$1,$2,$3)", 1, "[-1:0][3:4]={{1,NULL},{2,3}}", input, "{7,NULL}")))
		for i := range input {
			input[i] = 'x'
		}
		name := "_ARRAY_SMOKE_DI"
		if b.PG {
			name = ""
		}
		check := fixture.ArrayReferenceSelect("SELECT id,i,t,d FROM public.array_smoke ORDER BY id", []string{"id", "i", "t", "d"}, []string{"INT4", "_INT4", "_TEXT", name}, []string{"INT4", "_INT4", "_TEXT", "_INT4"}, [][]any{{int64(1), "[-1:0][3:4]={{1,NULL},{2,3}}", `{NULL,"NULL","","a,b"}`, "{7,NULL}"}, {int64(2), "[-1:0][3:4]={{1,NULL},{2,3}}", `{NULL,"NULL","","a,b"}`, "{7,NULL}"}}, b.PG)
		run(check)
		run(fixture.Prepared(check))
		want := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{1,,2}"`)
		want.Detail = `Unexpected "," character.`
		want.Position = "8"
		s := fixture.ErrorDiagnostic("SELECT '{1,,2}'::INT4[] AS a FROM public.one", want)
		s.SelectRows = true
		s.ErrorStage = "execute"
		run(s)
		want.Position = ""
		want.Context = "unnamed portal parameter $1 = '...'"
		s = fixture.ErrorDiagnostic("SELECT $1::INT4[] AS a FROM public.one", want, "{1,,2}")
		s.SelectRows = true
		s.Prepared = true
		s.ErrorStage = "execute"
		run(s)
		want.Context = "unnamed portal parameter $2 = '...'"
		run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO public.array_smoke (id,i) VALUES (3,$1),(4,$2)", want, "{9}", "{1,,2}")))
		run(check)
		for _, s := range []fixture.Step{
			fixture.ArrayReferenceSelect("SELECT ARRAY[1,2] AS a FROM public.one", []string{"a"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{1,2}"}}, b.PG),
			fixture.Select("SELECT i[1] AS e FROM public.array_smoke ORDER BY id", []string{"e"}, []string{"INT4"}, [][]any{{nil}, {nil}}),
		} {
			run(s)
			run(fixture.Prepared(s))
		}
		if !b.PG {
			for _, q := range []string{"SELECT i FROM public.array_smoke ORDER BY i", "SELECT i=i FROM public.array_smoke"} {
				run(fixture.Error(q, "42883"))
			}
			fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check)
		}
		functionQuery := "SELECT array_length(i,1) AS n FROM public.array_smoke ORDER BY id"
		if b.PG {
			run(fixture.Select(functionQuery, []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(2)}}))
		} else {
			want := fixture.ControlsReferenceDiagnostic("0A000", "functions are unsupported")
			want.Position = "8"
			step := fixture.ErrorDiagnostic(functionQuery, want)
			step.SelectRows = true
			step.ErrorStage = "execute"
			run(step)
		}
	})
}

func arrayReferenceTemporaryControls(t *testing.T, p *fixture.Pair, c *sql.Conn, b fixture.Backend, persistent fixture.Step) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TEMP SEQUENCE array_temp_seed", 0))
	// Supported array declarations retain the table's physical TEMP ownership.
	for _, prepared := range []bool{false, true} {
		name := "array_temp_direct"
		if prepared {
			name = "array_temp_prepared"
		}
		query := "CREATE TABLE pg_temp." + name + " (i INT[],t TEXT[])"
		step := fixture.Exec(query, 0)
		step.Prepared = prepared
		run(step)
		run(fixture.Exec("INSERT INTO pg_temp."+name+` VALUES ('[-2:-1]={5,NULL}','{NULL,"NULL","a,b"}')`, 1))
		run(fixture.ArrayReferenceSelect("SELECT i,t FROM pg_temp."+name, []string{"i", "t"}, []string{"_INT4", "_TEXT"}, []string{"_INT4", "_TEXT"}, [][]any{{"[-2:-1]={5,NULL}", `{NULL,"NULL","a,b"}`}}, b.PG))
		run(fixture.Exec("DROP TABLE pg_temp."+name, 0))
		run(persistent)
	}
	run(fixture.Exec("DROP SEQUENCE pg_temp.array_temp_seed", 0))
}
