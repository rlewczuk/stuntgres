package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceUnaccentDictionarySelection(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE EXTENSION unaccent", "CREATE SCHEMA dictionary_left", "CREATE SCHEMA dictionary_right",
			"CREATE TEXT SEARCH DICTIONARY dictionary_left.pick (TEMPLATE=pg_catalog.simple)",
			"CREATE TEXT SEARCH DICTIONARY dictionary_right.pick (TEMPLATE=pg_catalog.simple,accept=false)",
			"CREATE TEXT SEARCH DICTIONARY public.unaccent_stop (TEMPLATE=pg_catalog.simple,stopwords=english)",
			"CREATE TEXT SEARCH DICTIONARY public.unaccent_reject (TEMPLATE=pg_catalog.simple,accept=false)",
		} {
			regtsExec(t, p, b, c, q, 0)
		}
		q := "SELECT public.unaccent($1::regdictionary,$2::text) AS v"
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY pg_temp.pick (TEMPLATE=pg_catalog.simple,accept=false)", 0)
		regtsExec(t, p, b, c, "SET search_path=pg_temp,dictionary_left,public", 0)
		unaccentDictionaryText(t, p, b, c, "SELECT public.unaccent('pick','WOrD') AS v", "word")
		unaccentDictionaryText(t, p, b, c, "SELECT public.unaccent('pg_temp.pick','WOrD') AS v", "WOrD")
		regtsExec(t, p, b, c, "SET search_path=public", 0)
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct{ dictionary, input, want string }{
			{"pg_catalog.simple", "WOrD", "word"}, {"unaccent_stop", "The", "The"},
			{"unaccent_reject", "WOrD", "WOrD"}, {"public.unaccent", "Hôtel", "Hotel"},
			{"public.unaccent", "plain", "plain"}, {"pg_catalog.simple", "", ""},
		} {
			unaccentDictionaryText(t, p, b, c, q, tc.want, tc.dictionary, tc.input)
			unaccentDictionaryText(t, p, b, held, q, tc.want, tc.dictionary, tc.input)
		}
		for _, tc := range []struct{ path, want string }{{"dictionary_left,dictionary_right,public", "word"}, {"dictionary_right,dictionary_left,public", "WOrD"}} {
			regtsExec(t, p, b, c, "SET search_path="+tc.path, 0)
			unaccentDictionaryText(t, p, b, held, q, tc.want, "pick", "WOrD")
			unaccentDictionaryText(t, p, b, held, q, "word", "dictionary_left.pick", "WOrD")
			unaccentDictionaryText(t, p, b, held, q, "WOrD", "dictionary_right.pick", "WOrD")
		}
		regtsExec(t, p, b, c, "SET search_path=public", 0)
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'dictionary_left.pick'::regdictionary::oid::bigint").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		numericSQL := "SELECT public.unaccent($1::oid::regdictionary,$2::text) AS v"
		numeric, err := c.PrepareContext(p.Ctx, numericSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer numeric.Close()
		numericHeld := fixture.HeldStatement{Executor: c, Statement: numeric, SQL: numericSQL}
		unaccentDictionaryText(t, p, b, numericHeld, numericSQL, "word", oid, "WOrD")
		for _, ddl := range []string{
			"ALTER TEXT SEARCH DICTIONARY dictionary_left.pick RENAME TO picked",
			"ALTER TEXT SEARCH DICTIONARY dictionary_left.picked SET SCHEMA dictionary_right",
			"CREATE TEXT SEARCH DICTIONARY dictionary_left.pick (TEMPLATE=pg_catalog.simple,accept=false)",
		} {
			regtsExec(t, p, b, c, ddl, 0)
		}
		// The held typed selector keeps the dictionary through relabeling.
		unaccentDictionaryText(t, p, b, numericHeld, numericSQL, "word", oid, "WOrD")
		numeric.Close()
		for _, ddl := range []string{
			"ALTER FUNCTION public.unaccent(regdictionary,text) RENAME TO normalize_selected",
			"ALTER FUNCTION public.normalize_selected(regdictionary,text) SET SCHEMA dictionary_left",
		} {
			regtsExec(t, p, b, c, ddl, 0)
		}
		numericSQL = "SELECT dictionary_left.normalize_selected($1::oid::regdictionary,$2::text) AS v"
		movedNumeric, err := c.PrepareContext(p.Ctx, numericSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer movedNumeric.Close()
		numericHeld = fixture.HeldStatement{Executor: c, Statement: movedNumeric, SQL: numericSQL}
		unaccentDictionaryText(t, p, b, numericHeld, numericSQL, "word", oid, "WOrD")
		moved := "SELECT dictionary_left.normalize_selected($1::regdictionary,$2::text) AS v"
		unaccentDictionaryText(t, p, b, c, moved, "word", "dictionary_right.picked", "WOrD")
		unaccentDictionaryText(t, p, b, c, moved, "WOrD", "dictionary_left.pick", "WOrD")
		unaccentDictionaryText(t, p, b, c, moved, "Hotel", "public.unaccent", "Hôtel")
		regtsSelect(t, p, b, c, "SELECT 'dictionary_right.picked'::regdictionary::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{oid}})
		regtsExec(t, p, b, c, "DROP TEXT SEARCH DICTIONARY dictionary_right.picked", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_right.picked (TEMPLATE=pg_catalog.simple,accept=false)", 0)
		s := fixture.ErrorDiagnostic(numericSQL, dictionaryDiagnostic("XX000", fmt.Sprintf("cache lookup failed for text search dictionary %d", oid)), oid, "WOrD")
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, numericHeld, b.Recorder, s)
		unaccentDictionaryText(t, p, b, c, moved, "WOrD", "dictionary_right.picked", "WOrD")
	})
}
