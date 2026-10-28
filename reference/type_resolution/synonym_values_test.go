package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations come from PG18 section 12.6.3 and REL_18_6 synonym_sample.syn,
// not a native lookup table or a comparison between the two backends.
func synonymCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, name string) {
	t.Helper()
	regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY "+name+" (TEMPLATE=pg_catalog.synonym,Synonyms=synonym_sample)", 0)
}

func synonymConfig(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, config, dictionary string) {
	t.Helper()
	regtsExec(t, p, b, c, "CREATE TEXT SEARCH CONFIGURATION "+config+" (COPY=pg_catalog.simple)", 0)
	regtsExec(t, p, b, c, "ALTER TEXT SEARCH CONFIGURATION "+config+" ALTER MAPPING FOR asciiword WITH "+dictionary+",pg_catalog.simple", 0)
}

func synonymHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, q string) fixture.HeldStatement {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
}

func synonymTypedStep(b fixture.Backend, q, label, typ string, values [][]any, args ...any) fixture.Step {
	s := fixture.Select(q, []string{label}, []string{typ}, values, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		ct, err := rows.ColumnTypes()
		if err != nil || len(ct) != 1 {
			t.Fatal(ct, err)
		}
		scan := reflect.TypeFor[string]()
		if b.PG {
			scan = reflect.TypeFor[any]()
		}
		length, lk := ct[0].Length()
		precision, scale, dk := ct[0].DecimalSize()
		nullable, nk := ct[0].Nullable()
		if ct[0].ScanType() != scan || length != 0 || lk || precision != 0 || scale != 0 || dk || nullable || nk {
			t.Fatalf("%s metadata: scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t", typ, ct[0].ScanType(), length, lk, precision, scale, dk, nullable, nk)
		}
	}
	s.CheckRaw = func(t *testing.T, col, row int, value any) bool {
		if value == nil {
			return true
		}
		if b.PG {
			if _, ok := value.([]byte); !ok {
				t.Fatalf("pq %s raw %T", typ, value)
			}
		} else if _, ok := value.(string); !ok {
			t.Fatalf("native %s raw %T", typ, value)
		}
		return true
	}
	return s
}

func synonymConverted(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q, typ string, want any, args ...any) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, synonymTypedStep(b, q, "normalized", typ, [][]any{{want}}, args...))
}

func TestReferenceSynonymValues(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsSelect(t, p, b, c, "SELECT oid::bigint AS id,tmplinit::oid::bigint AS init,tmpllexize::oid::bigint AS lexize FROM pg_ts_template WHERE tmplname='synonym' AND tmplnamespace='pg_catalog'::regnamespace", []string{"id", "init", "lexize"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(3730), int64(3728), int64(3729)}})
		regtsSelect(t, p, b, c, "SELECT oid::bigint,proname::text,prorettype::bigint,proargtypes::text FROM pg_proc WHERE oid IN (3728,3729) ORDER BY oid", []string{"oid", "proname", "prorettype", "proargtypes"}, []string{"INT8", "TEXT", "INT8", "TEXT"}, [][]any{{int64(3728), "dsynonym_init", int64(2281), "2281"}, {int64(3729), "dsynonym_lexize", int64(2281), "2281 2281 2281 2281"}})
		synonymCreate(t, p, b, c, "synonym_values")
		q := "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		held := synonymHeld(t, p, c, q)
		defer held.Statement.Close()
		for _, tc := range []struct {
			input string
			want  any
		}{
			{"postgres", "{pgsql}"}, {"postgresql", "{pgsql}"}, {"postgre", "{pgsql}"}, {"gogle", "{googl}"}, {"indices", "{index}"},
			{"POSTGRES", "{pgsql}"}, {"PostgreSQL", "{pgsql}"}, {"unknown", nil}, {"", nil}, {" postgres", nil}, {"postgres ", nil}, {"postgres word", nil}, {"pgsql", nil}, {"ÄBC", nil},
		} {
			dictionaryResult(t, p, b, c, q, tc.want, "synonym_values", tc.input)
			dictionaryResult(t, p, b, held, q, tc.want, "synonym_values", tc.input)
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('synonym_values',NULL) AS lexemes", nil)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize(NULL,'postgres') AS lexemes", nil)
		// A genuine standalone template uses the authenticated helper capability,
		// rather than a special template name.
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH TEMPLATE synonym_alternate (INIT=pg_catalog.dsynonym_init,LEXIZE=pg_catalog.dsynonym_lexize)", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY synonym_alternate_dict (TEMPLATE=synonym_alternate,Synonyms=synonym_sample)", 0)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('synonym_alternate_dict','indices') AS lexemes", "{index}")
	})
}

func TestReferenceSynonymCaseOptions(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Last recognized CREATE option wins; only the final file is opened.
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY synonym_options (TEMPLATE=synonym,Synonyms=synonym_missing,Synonyms=synonym_sample,CaseSensitive=true,CaseSensitive=false)", 0)
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='synonym_options'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"synonyms = 'synonym_missing', synonyms = 'synonym_sample', casesensitive = 'true', casesensitive = 'false'"}})
		q := "SELECT ts_lexize('synonym_options',$1::text) AS lexemes"
		held := synonymHeld(t, p, c, q)
		defer held.Statement.Close()
		dictionaryResult(t, p, b, held, q, "{pgsql}", "POSTGRES")
		var id, owner, template int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint FROM pg_ts_dict WHERE oid='synonym_options'::regdictionary").Scan(&id, &owner, &template); err != nil || id <= 0 || owner <= 0 || template != 3730 {
			t.Fatal(id, owner, template, err)
		}
		for _, tc := range []struct {
			options, serialized string
			upper               any
		}{
			{"casesensitive=true", "casesensitive = 'true'", nil}, {"casesensitive='ON'", "casesensitive = 'ON'", nil}, {"casesensitive=1", "casesensitive = 1", nil},
			{"casesensitive=false", "casesensitive = 'false'", "{pgsql}"}, {"casesensitive='OFF'", "casesensitive = 'OFF'", "{pgsql}"}, {"casesensitive=0", "casesensitive = 0", "{pgsql}"},
			{"casesensitive=true,casesensitive=false", "casesensitive = 'false'", "{pgsql}"}, {"casesensitive", "", "{pgsql}"}, {"dummy", "", "{pgsql}"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY synonym_options ("+tc.options+")", 0)
			options := "synonyms = 'synonym_missing', synonyms = 'synonym_sample'"
			if tc.serialized != "" {
				options += ", " + tc.serialized
			}
			regtsSelect(t, p, b, c, "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE oid='synonym_options'::regdictionary", []string{"oid", "dictowner", "dicttemplate", "dictinitoption"}, []string{"INT8", "INT8", "INT8", "TEXT"}, [][]any{{id, owner, template, options}})
			dictionaryResult(t, p, b, c, q, tc.upper, "POSTGRES")
			dictionaryResult(t, p, b, held, q, tc.upper, "POSTGRES")
			dictionaryResult(t, p, b, held, q, "{pgsql}", "postgres")
			dictionaryResult(t, p, b, held, q, "{index}", "indices")
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY synonym_options (synonyms=synonym_sample)", 0)
		regtsSelect(t, p, b, c, "SELECT dictinitoption FROM pg_ts_dict WHERE oid='synonym_options'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"synonyms = 'synonym_sample'"}})
	})
}

func TestReferenceSynonymConversion(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		synonymCreate(t, p, b, c, "synonym_convert")
		synonymConfig(t, p, b, c, "synonym_config", "synonym_convert")
		for _, tc := range []struct{ function, input, typ, want string }{
			{"to_tsvector", "indices", "TSVECTOR", "'index':1"}, {"to_tsquery", "indices", "TSQUERY", "'index':*"}, {"to_tsquery", "indices:AB", "TSQUERY", "'index':*AB"}, {"to_tsquery", "indices:*AB", "TSQUERY", "'index':*AB"},
			{"to_tsvector", "postgres WORD", "TSVECTOR", "'pgsql':1 'word':2"}, {"to_tsquery", "postgres & WORD", "TSQUERY", "'pgsql' & 'word'"},
			{"plainto_tsquery", "indices WORD", "TSQUERY", "'index':* & 'word'"}, {"phraseto_tsquery", "indices WORD", "TSQUERY", "'index':* <-> 'word'"}, {"websearch_to_tsquery", "indices WORD", "TSQUERY", "'index':* & 'word'"},
		} {
			q := "SELECT " + tc.function + "('synonym_config'::regconfig,$1::text) AS normalized"
			held := synonymHeld(t, p, c, q)
			synonymConverted(t, p, b, c, q, tc.typ, tc.want, tc.input)
			synonymConverted(t, p, b, held, q, tc.typ, tc.want, tc.input)
			synonymConverted(t, p, b, held, q, tc.typ, nil, nil)
			held.Statement.Close()
		}
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY synonym_convert (CaseSensitive=true)", 0)
		synonymConverted(t, p, b, c, "SELECT to_tsvector('synonym_config',$1::text) AS normalized", "TSVECTOR", "'postgres':1", "POSTGRES")
		synonymConverted(t, p, b, c, "SELECT to_tsvector('synonym_config',$1::text) AS normalized", "TSVECTOR", "'pgsql':1", "postgres")
		for _, tc := range []struct{ function, typ string }{{"to_tsvector", "TSVECTOR"}, {"to_tsquery", "TSQUERY"}} {
			for _, prepared := range []bool{false, true} {
				s := synonymTypedStep(b, "SELECT "+tc.function+"('synonym_config','indices') AS normalized WHERE false", "normalized", tc.typ, nil)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
	})
}
