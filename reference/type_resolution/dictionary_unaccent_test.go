package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func dictionaryRejectMissingRules(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, qualified string) {
	t.Helper()
	var oid, namespace, owner, template, dependencies int64
	var options string
	snapshot := "SELECT d.oid::bigint AS id,d.dictnamespace::bigint AS namespace,d.dictowner::bigint AS owner,d.dicttemplate::bigint AS template,d.dictinitoption,(SELECT count(*) FROM pg_depend dep WHERE dep.classid=3600 AND dep.objid=d.oid) AS dependencies FROM pg_ts_dict d WHERE d.oid=$1::regdictionary::oid"
	if err := c.QueryRowContext(p.Ctx, snapshot, qualified).Scan(&oid, &namespace, &owner, &template, &options, &dependencies); err != nil {
		t.Fatal(err)
	}
	if options != "rules = 'unaccent'" || oid <= 0 || namespace <= 0 || owner <= 0 || template <= 0 || dependencies < 2 {
		t.Fatal("genuine explicit Rules state", oid, namespace, owner, template, options, dependencies)
	}
	dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY "+qualified+" (rules)", "22023", "missing Rules parameter")
	regtsSelect(t, p, b, c, snapshot, []string{"id", "namespace", "owner", "template", "dictinitoption", "dependencies"}, []string{"INT8", "INT8", "INT8", "INT8", "TEXT", "INT8"}, [][]any{{oid, namespace, owner, template, options, dependencies}}, qualified)
	dictionaryResult(t, p, b, c, "SELECT ts_lexize($1::oid::regdictionary,$2::text) AS lexemes", "{Hotel}", oid, "Hôtel")
	dictionaryResult(t, p, b, c, "SELECT ts_lexize($1::oid::regdictionary,$2::text) AS lexemes", nil, oid, "plain")
}

func TestReferenceDictionaryUnaccentCaller(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
		defer regtsExec(t, p, b, c, "DROP EXTENSION unaccent CASCADE", 0)
		regtsExec(t, p, b, c, "CREATE SCHEMA dictionary_right", 0)
		var memberOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'public.unaccent'::regdictionary::oid::bigint").Scan(&memberOID); err != nil {
			t.Fatal(err)
		}
		dictionaryRejectMissingRules(t, p, b, c, "public.unaccent")
		q := "SELECT public.unaccent($1::text) AS v"
		statement, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: q}
		result := func(e fixture.Executor, query string, want, input any) {
			regtsSelect(t, p, b, e, query, []string{"v"}, []string{"TEXT"}, [][]any{{want}}, input)
		}
		missing := func(e fixture.Executor, query, schema string) {
			step := fixture.ErrorDiagnostic(query, dictionaryDiagnostic("42704", `text search dictionary "`+schema+`.unaccent" does not exist`), "Hôtel")
			step.SelectRows, step.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, e, b.Recorder, step)
		}
		result(held, q, "Hotel", "Hôtel")
		result(held, q, "plain", "plain")
		result(held, q, "", "")
		regtsExec(t, p, b, c, "CREATE VIEW dictionary_unaccent_call AS SELECT public.unaccent('Hôtel'::text) AS v", 0)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.unaccent RENAME TO dictionary_member", 0)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize($1::oid::regdictionary,'Hôtel') AS lexemes", "{Hotel}", memberOID)
		missing(c, q, "public")
		missing(held, q, "public")
		result(held, q, nil, nil)
		fresh, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal("missing dictionary must not fail function binding", err)
		}
		missing(fixture.HeldStatement{Executor: c, Statement: fresh, SQL: q}, q, "public")
		fresh.Close()

		// The replacement is a genuine simple dictionary, not an extension
		// member or a stock-unaccent shortcut. Empty/unrecognized lexeme lists
		// return the original token, while a recognized simple lexeme is lowercased.
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY public.unaccent (TEMPLATE=pg_catalog.simple,stopwords=english)", 0)
		result(held, q, "hôtel", "Hôtel")
		result(held, q, "word", "WOrD")
		result(held, q, "The", "The")
		regtsSelect(t, p, b, c, "SELECT v FROM dictionary_unaccent_call", []string{"v"}, []string{"TEXT"}, [][]any{{"hôtel"}})
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY public.unaccent (accept=false)", 0)
		result(held, q, "WOrD", "WOrD")
		dictionaryResult(t, p, b, c, "SELECT ts_lexize($1::oid::regdictionary,'Hôtel') AS lexemes", "{Hotel}", memberOID)
		regtsExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY public.unaccent RESTRICT", 0)
		missing(held, q, "public")
		for _, sql := range []string{"ALTER TEXT SEARCH DICTIONARY public.dictionary_member RENAME TO unaccent", "ALTER TEXT SEARCH DICTIONARY public.unaccent SET SCHEMA dictionary_right"} {
			regtsExec(t, p, b, c, sql, 0)
		}
		missing(held, q, "public")
		statement.Close()

		// Neither the mutable routine name nor the extension's target schema
		// chooses the default. It is literal unaccent in the function's schema.
		regtsExec(t, p, b, c, "ALTER FUNCTION public.unaccent(text) RENAME TO dictionary_normalizer", 0)
		publicQuery := "SELECT public.dictionary_normalizer($1::text) AS v"
		missing(c, publicQuery, "public")
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY public.unaccent (TEMPLATE=pg_catalog.simple)", 0)
		result(c, publicQuery, "hôtel", "Hôtel")
		regtsExec(t, p, b, c, "ALTER FUNCTION public.dictionary_normalizer(text) SET SCHEMA dictionary_right", 0)
		rightQuery := "SELECT dictionary_right.dictionary_normalizer($1::text) AS v"
		regtsExec(t, p, b, c, "SET search_path=public", 0)
		result(c, rightQuery, "Hotel", "Hôtel")
		regtsSelect(t, p, b, c, "SELECT v FROM dictionary_unaccent_call", []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}})
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_right.unaccent RENAME TO dictionary_member_right", 0)
		missing(c, rightQuery, "dictionary_right")
		result(c, rightQuery, nil, nil)
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_right.unaccent (TEMPLATE=public.unaccent)", "22023", "missing Rules parameter")
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='unaccent' AND dictnamespace='dictionary_right'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		missing(c, rightQuery, "dictionary_right")
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_right.unaccent (TEMPLATE=public.unaccent,RULES=unaccent)", 0)
		dictionaryRejectMissingRules(t, p, b, c, "dictionary_right.unaccent")
		result(c, rightQuery, "Hotel", "Hôtel")
		dictionaryResult(t, p, b, c, "SELECT ts_lexize($1::oid::regdictionary,'Hôtel') AS lexemes", "{Hotel}", memberOID)
	})
}
