package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

const templateSimpleMethods = "INIT=pg_catalog.dsimple_init, LEXIZE=pg_catalog.dsimple_lexize"

func templateDefinition(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder, name string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TEXT SEARCH TEMPLATE "+name+" ("+templateSimpleMethods+")", 0))
}

func templateAbsent(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder, name string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, r, fixture.Select("SELECT count(*) FROM pg_ts_template WHERE tmplname=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, name))
}

func templateDiagnostic(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder, q, code, message string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := extensionError(q, code, message)
		s.Prepared = prepared
		s.ErrorStage = "execute" // Utilities bind callbacks only at execution.
		fixture.RunStep(t, p.Ctx, c, r, s)
	}
}

func TestReferenceTSTemplateDefinition(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		for _, tc := range []struct{ options, code, message string }{
			{"INIT=pg_catalog.dsimple_init", "42P17", "text search template lexize method is required"},
			{"bad=pg_catalog.dsimple_init", "42601", `text search template parameter "bad" not recognized`},
			{`"LEXIZE"=pg_catalog.dsimple_lexize`, "42601", `text search template parameter "LEXIZE" not recognized`},
			{"INIT, LEXIZE=pg_catalog.dsimple_lexize", "42601", "init requires a parameter"},
			{"INIT=1, LEXIZE=pg_catalog.dsimple_lexize", "42601", "argument of init must be a name"},
			{"INIT=pg_catalog.dsimple_lexize, LEXIZE=pg_catalog.dsimple_lexize", "42883", "function pg_catalog.dsimple_lexize(internal) does not exist"},
			{"LEXIZE=pg_catalog.dsimple_init", "42883", "function pg_catalog.dsimple_init(internal, internal, internal, internal) does not exist"},
			{"INIT=pg_catalog.bthandler, LEXIZE=pg_catalog.dsimple_lexize", "42P17", "function pg_catalog.bthandler(internal) should return type internal"},
			{"LEXIZE=pg_catalog.brin_minmax_add_value", "42P17", "function pg_catalog.brin_minmax_add_value(internal, internal, internal, internal) should return type internal"},
			{"INIT=pg_catalog.bthandler, INIT=pg_catalog.dsimple_init, LEXIZE=pg_catalog.dsimple_lexize", "42P17", "function pg_catalog.bthandler(internal) should return type internal"},
			{"LEXIZE=pg_catalog.template_no_handler", "42883", "function pg_catalog.template_no_handler(internal, internal, internal, internal) does not exist"},
			{"INIT='pg_catalog.dsimple_init', LEXIZE=pg_catalog.dsimple_lexize", "42883", `function pg_catalog.dsimple_init(internal) does not exist`},
		} {
			templateDiagnostic(t, p, c, r, "CREATE TEXT SEARCH TEMPLATE public.template_bad ("+tc.options+")", tc.code, tc.message)
			templateAbsent(t, p, c, r, "template_bad")
		}
		for i, options := range []string{templateSimpleMethods, "LEXIZE=pg_catalog.dsimple_lexize, INIT=pg_catalog.dsimple_init", "INIT=pg_catalog.dsimple_init, INIT=pg_catalog.dsimple_init, LEXIZE=pg_catalog.dsimple_lexize"} {
			s := fixture.Exec(fmt.Sprintf("CREATE TEXT SEARCH TEMPLATE public.template_good_%d (%s)", i, options), 0)
			s.Prepared = i%2 != 0
			run(s)
		}
		run(fixture.Exec(`CREATE TEXT SEARCH TEMPLATE public."Quoted Template" (LEXIZE=pg_catalog.dsimple_lexize)`, 0))
		zero := fixture.Select(`SELECT tmplinit::oid::bigint,tmpllexize::oid::bigint FROM pg_ts_template WHERE tmplname='Quoted Template'`, []string{"tmplinit", "tmpllexize"}, []string{"INT8", "INT8"}, [][]any{{int64(0), int64(3726)}})
		run(zero)
		run(fixture.Prepared(zero))
		templateDiagnostic(t, p, c, r, `CREATE TEXT SEARCH DICTIONARY public.template_bad_dict (TEMPLATE=public."Quoted Template", STOPWORDS=english)`, "42601", `text search template "Quoted Template" does not accept options`)
		// No unsafe PostgreSQL callback execution: optional INIT metadata is real,
		// but dsimple_lexize requires state from the genuine INIT callback.
		if !pg {
			run(extensionError(`CREATE TEXT SEARCH DICTIONARY public.template_bad_dict (TEMPLATE=public."Quoted Template")`, "0A000", `native dictionary callback execution is not supported for text search template "Quoted Template"`))
		}
		var namespace int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='public'").Scan(&namespace); err != nil { t.Fatal(err) }
		q := "CREATE TEXT SEARCH TEMPLATE public.template_good_0 ("+templateSimpleMethods+")"
		run(fixture.ImplicitError(q, fixture.Diagnostic{Code:"23505", Message:`duplicate key value violates unique constraint "pg_ts_template_tmplname_index"`, Detail:fmt.Sprintf("Key (tmplname, tmplnamespace)=(template_good_0, %d) already exists.", namespace), Schema:"pg_catalog", Table:"pg_ts_template", Constraint:"pg_ts_template_tmplname_index"}))
		q = "CREATE TEXT SEARCH TEMPLATE public.template_suffix ("+templateSimpleMethods+") junk"
		run(fixture.ImplicitError(q, fixture.Diagnostic{Code:"42601", Message:`syntax error at or near "junk"`, Position:fmt.Sprint(len(q)-3)}))
		templateAbsent(t, p, c, r, "template_suffix")
		run(fixture.Exec(`DROP TEXT SEARCH TEMPLATE public.template_good_0,public.template_good_1,public.template_good_2,public."Quoted Template" RESTRICT`, 0))
		templateDiagnostic(t, p, c, r, "ALTER TEXT SEARCH TEMPLATE public.template_missing RENAME TO x", "42704", `text search template "public.template_missing" does not exist`)
		for _, tc := range []struct{ query, message string }{
			{"DROP TEXT SEARCH TEMPLATE IF EXISTS public.template_notice_missing", `text search template "public.template_notice_missing" does not exist, skipping`},
			{"DROP TEXT SEARCH TEMPLATE IF EXISTS template_no_schema.missing", `schema "template_no_schema" does not exist, skipping`},
		} {
			for _, prepared := range []bool{false,true} {
				var notices []fixture.Diagnostic
				_, nc, nr := fixture.NoticeReferencePool(t,p,pg,&notices)
				s := fixture.Exec(tc.query,0)
				s.Prepared = prepared
				fixture.RunStep(t,p.Ctx,nc,nr,s)
				want := fixture.Diagnostic{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"00000",Message:tc.message}
				if len(notices)!=1 || notices[0]!=want { t.Fatalf("template notice got %#v want %#v",notices,want) }
			}
		}
	})
}
