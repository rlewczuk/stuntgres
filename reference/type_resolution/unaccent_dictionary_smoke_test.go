package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceUnaccentDictionaryFeatureSmoke(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE EXTENSION unaccent", "CREATE SCHEMA dictionary_right",
			"CREATE TEXT SEARCH DICTIONARY dictionary_right.smoke_unaccent (TEMPLATE=public.unaccent,RULES=unaccent)",
			"CREATE TEXT SEARCH CONFIGURATION unaccent_smoke_config (COPY=pg_catalog.simple)",
			"ALTER TEXT SEARCH CONFIGURATION unaccent_smoke_config ALTER MAPPING FOR word WITH dictionary_right.smoke_unaccent,pg_catalog.simple",
			"CREATE TABLE unaccent_smoke_rows(id int,d regdictionary DEFAULT 'dictionary_right.smoke_unaccent'::regdictionary,v text DEFAULT public.unaccent('dictionary_right.smoke_unaccent'::regdictionary,'Hôtel'))",
			"CREATE VIEW unaccent_smoke_view AS SELECT public.unaccent('dictionary_right.smoke_unaccent'::regdictionary,'Hôtel') AS v",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		regtsExec(t, p, b, c, "INSERT INTO unaccent_smoke_rows(id) VALUES(1)", 1)
		q := "SELECT public.unaccent($1::regdictionary,$2::text) AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		defer stmt.Close()
		for _, e := range []fixture.Executor{c, held} {
			unaccentDictionaryText(t, p, b, e, q, "Hotel", "dictionary_right.smoke_unaccent", "Hôtel")
			unaccentDictionaryText(t, p, b, e, q, "AEsir Strasse OEuf ffi", "dictionary_right.smoke_unaccent", "Æsir Straße Œuf ﬃ")
			unaccentDictionaryText(t, p, b, e, q, "word", "pg_catalog.simple", "WOrD")
			unaccentDictionaryText(t, p, b, e, q, nil, "dictionary_right.smoke_unaccent", nil)
		}
		check := func() {
			regtsSelect(t, p, b, c, "SELECT d='dictionary_right.smoke_unaccent'::regdictionary AS same,v,public.unaccent(d,'Hôtel') AS selected FROM unaccent_smoke_rows ORDER BY id", []string{"same", "v", "selected"}, []string{"BOOL", "TEXT", "TEXT"}, [][]any{{true, "Hotel", "Hotel"}})
			regtsSelect(t, p, b, c, "SELECT v FROM unaccent_smoke_view", []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}})
			regtsSelect(t, p, b, c, "SELECT to_tsvector('unaccent_smoke_config','Hôtel') AS v", []string{"v"}, []string{"TSVECTOR"}, [][]any{{"'hotel':1"}})
			regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_depend dep JOIN pg_extension e ON e.oid=dep.refobjid WHERE dep.refclassid=3079 AND dep.deptype='e' AND e.extname='unaccent'", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}})
			regtsExec(t, p, b, c, "INSERT INTO unaccent_smoke_rows(id) VALUES(2)", 1)
			regtsSelect(t, p, b, c, "SELECT v FROM unaccent_smoke_rows WHERE id=2", []string{"v"}, []string{"TEXT"}, [][]any{{"Hotel"}})
			regtsExec(t, p, b, c, "DELETE FROM unaccent_smoke_rows WHERE id=2", 1)
		}
		check()
		fixture.RunStep(t, p.Ctx, c, b.Recorder, unaccentDictionaryMissingRulesStep("CREATE TEXT SEARCH DICTIONARY unaccent_smoke_bad (TEMPLATE=public.unaccent,RULES=unaccent_missing)", "unaccent_missing"))
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='unaccent_smoke_bad' AND dictnamespace='public'::regnamespace", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		if !b.PG {
			stmt.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_extension WHERE extname='unaccent'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			check()
			unaccentDictionaryText(t, p, b, c, q, "Hotel", "dictionary_right.smoke_unaccent", "Hôtel")
		}
	})
}
