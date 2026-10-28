package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func templateLexemes(t *testing.T, p *fixture.Pair, c fixture.Executor, r *fixture.Recorder, pg bool, q string, want any, args ...any) {
	t.Helper()
	s := fixture.Select(q, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{want}}, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 1 { t.Fatal(columns, err) }
		ct := columns[0]
		scan := reflect.TypeFor[string]()
		if pg { scan = reflect.TypeFor[any]() }
		length, lk := ct.Length()
		precision, scale, dk := ct.DecimalSize()
		nullable, nk := ct.Nullable()
		if ct.ScanType() != scan || length != 0 || lk || precision != 0 || scale != 0 || dk || nullable || nk { t.Fatal("lexeme metadata", ct.ScanType(), length, lk, precision, scale, dk, nullable, nk) }
	}
	s.CheckRaw = func(t *testing.T, col, row int, v any) bool {
		if v != nil {
			if pg { if _, ok := v.([]byte); !ok { t.Fatalf("pq lexemes raw %T", v) } } else if _, ok := v.(string); !ok { t.Fatalf("native lexemes raw %T", v) }
		}
		return true
	}
	fixture.RunStep(t, p.Ctx, c, r, s)
}

func templateTokens(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool, dictionary string) {
	t.Helper()
	q := "SELECT ts_lexize('"+dictionary+"',$1::text) AS lexemes"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil { t.Fatal(err) }
	defer stmt.Close()
	held := fixture.HeldStatement{Executor:c, Statement:stmt, SQL:q}
	for _, tc := range []struct{ input, want any }{{"WORD", "{word}"}, {"YeS", "{yes}"}, {"The", "{}"}, {"", "{}"}, {nil, nil}} {
		templateLexemes(t, p, c, r, pg, q, tc.want, tc.input)
		templateLexemes(t, p, held, r, pg, q, tc.want, tc.input)
	}
}

func TestReferenceTSTemplateExecution(t *testing.T) {
	extensionCase(t, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		templateDefinition(t, p, c, r, "public.template_exec")
		run(fixture.Prepared(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.template_exec_dict (TEMPLATE=public.template_exec, STOPWORDS=english)", 0)))
		templateTokens(t, p, c, r, pg, "public.template_exec_dict")
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.template_reject_dict (TEMPLATE=public.template_exec, ACCEPT=false, STOPWORDS=english)", 0))
		templateLexemes(t, p, c, r, pg, "SELECT ts_lexize('public.template_reject_dict','WORD') AS lexemes", nil)
		templateLexemes(t, p, c, r, pg, "SELECT ts_lexize('public.template_reject_dict','The') AS lexemes", "{}")
		run(fixture.Exec("CREATE SCHEMA ext_addon", 0))
		run(fixture.Exec("CREATE EXTENSION unaccent SCHEMA ext_addon", 0))
		run(fixture.Exec("CREATE TEXT SEARCH TEMPLATE public.template_unaccent (INIT=ext_addon.unaccent_init,LEXIZE=ext_addon.unaccent_lexize)", 0))
		run(fixture.Exec("CREATE TEXT SEARCH DICTIONARY public.template_unaccent_dict (TEMPLATE=public.template_unaccent,RULES=unaccent)", 0))
		for _, prepared := range []bool{false, true} {
			q := "SELECT ts_lexize('public.template_unaccent_dict',$1::text) AS lexemes"
			if !prepared { templateLexemes(t, p, c, r, pg, q, "{Hotel}", "Hôtel"); continue }
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil { t.Fatal(err) }
			templateLexemes(t, p, fixture.HeldStatement{Executor:c, Statement:stmt, SQL:q}, r, pg, q, "{Hotel}", "Hôtel")
			stmt.Close()
		}
	})
}
