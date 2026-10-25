package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAggregateFinalWindowDiagnostics(t *testing.T) {
	// PostgreSQL REL_18_6 nodeWindowAgg.c uses format_procedure(winfunc OID).
	// regproc.c's format_procedure_extended uses declaration visibility, quoted
	// identifiers and comma-separated (not comma-space-separated) argument types.
	for _, mode := range []string{"shareable", "read_write"} {
		t.Run(mode, func(t *testing.T) {
			for _, tc := range []struct {
				name, declaration, call, signature, searchPath string
				twoInputs, schema                              bool
			}{
				{
					name:        "visible_public_unary",
					declaration: "public.fawd_guard", call: "public.fawd_guard(x)",
					signature: "fawd_guard(integer)", searchPath: "public",
				},
				{
					name:        "visible_public_two_inputs",
					declaration: "public.fawd_guard", call: "public.fawd_guard(x,label)",
					signature: "fawd_guard(integer,text)", searchPath: "public", twoInputs: true,
				},
				{
					name:        "visible_public_quoted",
					declaration: `public."fa Window""Guard"`, call: `public."fa Window""Guard"(x)`,
					signature: `"fa Window""Guard"(integer)`, searchPath: "public",
				},
				{
					name:        "nonvisible_quoted_schema",
					declaration: `"fa Window Scope"."fa Window""Guard"`, call: `"fa Window Scope"."fa Window""Guard"(x,label)`,
					signature: `"fa Window Scope"."fa Window""Guard"(integer,text)`, searchPath: "pg_catalog", twoInputs: true, schema: true,
				},
				{
					name:        "visible_quoted_schema",
					declaration: `"fa Window Scope"."fa Window""Guard"`, call: `"fa Window Scope"."fa Window""Guard"(x,label)`,
					signature: `"fa Window""Guard"(integer,text)`, searchPath: `"fa Window Scope",public`, twoInputs: true, schema: true,
				},
			} {
				t.Run(tc.name, func(t *testing.T) {
					if tc.schema {
						fixture.SchemaReferenceCleanup(t, "fa Window Scope")
					}
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							c, err := b.DB.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer c.Close()
							createAggregateRun(t, p, b, c,
								fixture.Exec("SET search_path TO public", 0),
								fixture.Exec("CREATE TABLE public.fawd_input(id integer,x integer,label text)", 0),
								fixture.Exec("INSERT INTO public.fawd_input VALUES(1,1,'a'),(2,2,'b'),(3,3,'c')", 3),
								fixture.Exec("CREATE TABLE public.fawd_log(v integer)", 0),
								fixture.Exec("CREATE FUNCTION public.fawd_final(integer) RETURNS text BEGIN ATOMIC INSERT INTO public.fawd_log VALUES($1); SELECT $1::text; END", 0))
							if tc.schema {
								createAggregateRun(t, p, b, c, fixture.Exec(`CREATE SCHEMA "fa Window Scope"`, 0))
							}
							arguments, step := "integer", "pg_catalog.int4pl"
							if tc.twoInputs {
								createAggregateRun(t, p, b, c, fixture.Exec("CREATE FUNCTION public.fawd_step(integer,integer,text) RETURNS integer LANGUAGE SQL AS $$SELECT $1+$2$$", 0))
								arguments, step = "integer,text", "public.fawd_step"
							}
							createAggregateRun(t, p, b, c,
								fixture.Exec("CREATE AGGREGATE "+tc.declaration+"("+arguments+") (SFUNC="+step+",STYPE=int4,INITCOND='0',FINALFUNC=public.fawd_final,FINALFUNC_MODIFY="+mode+")", 0),
								fixture.Exec("SET search_path TO "+tc.searchPath, 0))

							q := "SELECT " + tc.call + " OVER() AS v FROM public.fawd_input WHERE id<=$1"
							stmt, err := c.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatalf("window Prepare must succeed: %v", err)
							}
							defer stmt.Close()
							held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
							for _, limit := range []int64{3, 0, 3} {
								s := fixture.ImplicitError(q, fixture.Diagnostic{
									Code: "0A000", Message: "aggregate function " + tc.signature + " does not support use as a window function",
								}, limit)
								s.SelectRows, s.ErrorStage = true, "execute"
								createAggregateBoth(t, p, b, c, s)
								fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
							}
							createAggregateRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM public.fawd_log", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))

							// The same declaration has a real final routine: ordinary
							// aggregation executes it on both populated and empty input.
							ordinary := "SELECT " + tc.call + " AS v FROM public.fawd_input WHERE id<=$1"
							createAggregateBoth(t, p, b, c, aggregateFinalText(ordinary, "6", int64(3)))
							createAggregateBoth(t, p, b, c, aggregateFinalText(ordinary, "0", int64(0)))
							createAggregateRun(t, p, b, c, fixture.Select("SELECT v FROM public.fawd_log ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(0)}, {int64(6)}, {int64(6)}}))
						})
					}
				})
			}
		})
	}
}
