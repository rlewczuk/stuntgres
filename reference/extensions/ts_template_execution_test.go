package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func templateTokens(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, dictionary string) {
	t.Helper()
	q := "SELECT ts_lexize('"+dictionary+"',$1::text) AS lexemes"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil { t.Fatal(err) }
	defer stmt.Close()
	held := fixture.HeldStatement{Executor:c, Statement:stmt, SQL:q}
	for _, tc := range []struct{ input, want any }{{"WORD", "{word}"}, {"YeS", "{yes}"}, {"The", "{}"}, {"", "{}"}, {nil, nil}} {
		fixture.TSTemplateLexemes(t, p, c, r, pg, q, tc.want, tc.input)
		fixture.TSTemplateLexemes(t, p, held, r, pg, q, tc.want, tc.input)
	}
}

func TestReferenceTSTemplateExecution(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		templateDefinition(t, p, c, r, "public.template_exec")
		run(fixture.Prepared(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.template_exec_dict (TEMPLATE=public.template_exec, STOPWORDS=english)", 0)))
		templateTokens(t, p, c, r, pg, "public.template_exec_dict")
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.template_reject_dict (TEMPLATE=public.template_exec, ACCEPT=false, STOPWORDS=english)", 0))
		fixture.TSTemplateLexemes(t, p, c, r, pg, "SELECT ts_lexize('public.template_reject_dict','WORD') AS lexemes", nil)
		fixture.TSTemplateLexemes(t, p, c, r, pg, "SELECT ts_lexize('public.template_reject_dict','The') AS lexemes", "{}")
		run(fixture.Exec("CREATE SCHEMA ext_addon", 0))
		run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
		run(fixture.Exec("CREATE TEXT SEARCH TEMPLATE public.template_unaccent (INIT=ext_addon.unaccent_init,LEXIZE=ext_addon.unaccent_lexize)", 0))
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.template_unaccent_dict (TEMPLATE=public.template_unaccent,RULES=unaccent)", 0))
		for _, prepared := range []bool{false, true} {
			q := "SELECT ts_lexize('public.template_unaccent_dict',$1::text) AS lexemes"
			if !prepared { fixture.TSTemplateLexemes(t, p, c, r, pg, q, "{Hotel}", "Hôtel"); continue }
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			fixture.TSTemplateLexemes(t, p, fixture.HeldStatement{Executor:c, Statement:stmt, SQL:q}, r, pg, q, "{Hotel}", "Hôtel")
			stmt.Close()
		}
	})
}
