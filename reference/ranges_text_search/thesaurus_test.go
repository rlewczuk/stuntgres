package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func thesaurusScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			body(t, p, backend, conn)
		})
	}
}

func thesaurusSetup(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn, file string) {
	t.Helper()
	for _, query := range []string{
		"CREATE TEXT SEARCH DICTIONARY public.thes_sub (TEMPLATE=pg_catalog.simple, STOPWORDS=english)",
		"CREATE TEXT SEARCH DICTIONARY public.thes_dict (TEMPLATE=pg_catalog.thesaurus, DictFile=" + file + ", Dictionary='public.thes_sub')",
		"CREATE TEXT SEARCH CONFIGURATION public.thes_cfg (COPY=pg_catalog.simple)",
		"ALTER TEXT SEARCH CONFIGURATION public.thes_cfg ALTER MAPPING FOR asciiword WITH public.thes_dict, public.thes_sub",
	} {
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec(query, 0))
	}
}

func TestReferenceThesaurusPhrases(t *testing.T) {
	thesaurusScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
		thesaurusSetup(t, p, backend, conn, "thesaurus_sample")
		const query = "SELECT to_tsvector('public.thes_cfg',$1::text) AS normalized"
		held, err := conn.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		for _, tc := range []struct {
			input any
			want  any
		}{
			{"ONE TWO THREE", "'123':1"},
			{"one two", "'12':1"},
			{"one two extra", "'12':1 'extra':2"},
			{"one extra two", "'1':1 '2':3 'extra':2"},
			{"booking", "'booking':1"},
			{"BOOKING TICKETS", "'cards':3 'invitation':2 'order':1"},
			{"booking the tickets", "'cards':3 'invitation':2 'order':1"},
			{"booking a tickets", "'cards':3 'invitation':2 'order':1"},
			{"booking unknown tickets", "'booking':1 'tickets':3 'unknown':2"},
			{"supernovae stars", "'sn':1"},
			{"supernova stars", "'stars':2 'supernova':1"},
			{"one\t\n two", "'12':1"},
			{"", ""},
			{nil, nil},
			{[]byte("ONE TWO THREE"), "'123':1"},
		} {
			s := fixture.Select(query, []string{"normalized"}, []string{"TSVECTOR"}, [][]any{{tc.want}}, tc.input)
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			// Twice through the same statement, interleaved with different inputs:
			// no phrase scratch may escape an invocation.
			for range 2 {
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: query}, backend.Recorder, s)
			}
		}
	})
}

func TestReferenceThesaurusRules(t *testing.T) {
	for _, tc := range []struct {
		name, file, input, want string
	}{
		{"last_normalized_rule", "stuntgres_thes_ties", "Alpha Beta", "'last':1"},
		{"reverse_order", "stuntgres_thes_ties_reverse", "Alpha Beta", "'first':1"},
		{"normalized_and_literal_rhs", "stuntgres_thes_literals", "BRIGHT STAR", "'KeEp':2 'mixed':1"},
		{"zero_placeholder", "thesaurus_sample", "booking tickets", "'cards':3 'invitation':2 'order':1"},
		{"two_stopwords", "thesaurus_sample", "booking the a tickets", "'booking':1 'tickets':4"},
		{"colon_rhs", "stuntgres_thes_bad_delimiter", "alpha", "':':2 'beta':1 'gamma':3"},
		{"comments", "stuntgres_thes_comments", "alpha", "'ok':1"},
		{"placeholder_one_the", "stuntgres_thes_placeholder", "booking the tickets", "'placeholder':1"},
		{"placeholder_one_a", "stuntgres_thes_placeholder", "booking a tickets", "'placeholder':1"},
		{"placeholder_requires_one", "stuntgres_thes_placeholder", "booking tickets", "'booking':1 'tickets':2"},
		{"placeholder_rejects_two", "stuntgres_thes_placeholder", "booking the a tickets", "'booking':1 'tickets':4"},
		{"placeholder_rejects_word", "stuntgres_thes_placeholder", "booking unknown tickets", "'booking':1 'tickets':3 'unknown':2"},
		{"repeated_phrases", "thesaurus_sample", "one two three one two", "'12':2 '123':1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			thesaurusScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
				thesaurusSetup(t, p, backend, conn, tc.file)
				for _, prepared := range []bool{false, true} {
					s := ftsConfigurationVector("SELECT to_tsvector('public.thes_cfg',$1::text) AS normalized", tc.want, tc.input)
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
				}
			})
		})
	}
	t.Run("whole_token_lexize", func(t *testing.T) {
		thesaurusScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
			thesaurusSetup(t, p, backend, conn, "thesaurus_sample")
			for _, tc := range []struct{ input, want any }{
				{"one", "{1}"}, {"one two", nil}, {"booking", nil}, {nil, nil},
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.Select("SELECT ts_lexize('public.thes_dict',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc.want}}, tc.input)
					if prepared {
						s = fixture.Prepared(s)
					}
					fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
				}
			}
		})
	})
}

func TestReferenceThesaurusBindingMetadata(t *testing.T) {
	thesaurusScenario(t, func(t *testing.T, p *fixture.Pair, backend fixture.Backend, conn *sql.Conn) {
		thesaurusSetup(t, p, backend, conn, "thesaurus_sample")
		for _, prepared := range []bool{false, true} {
			steps := []fixture.Step{
				fixture.Select("SELECT oid::bigint, tmplname, tmplinit::oid::bigint, tmpllexize::oid::bigint FROM pg_catalog.pg_ts_template WHERE oid=3742", []string{"oid", "tmplname", "tmplinit", "tmpllexize"}, []string{"INT8", "NAME", "INT8", "INT8"}, [][]any{{int64(3742), "thesaurus", int64(3740), int64(3741)}}),
				fixture.Select("SELECT oid::bigint,proname,proargtypes::text,prorettype::bigint,proisstrict,provolatile,proparallel,prosrc,probin,proargnames::text,proallargtypes::text,proargmodes::text FROM pg_catalog.pg_proc WHERE oid IN (3740,3741) ORDER BY oid", []string{"oid", "proname", "proargtypes", "prorettype", "proisstrict", "provolatile", "proparallel", "prosrc", "probin", "proargnames", "proallargtypes", "proargmodes"}, []string{"INT8", "NAME", "TEXT", "INT8", "BOOL", "CHAR", "CHAR", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(3740), "thesaurus_init", "2281", int64(2281), true, "i", "s", "thesaurus_init", nil, nil, nil, nil}, {int64(3741), "thesaurus_lexize", "2281 2281 2281 2281", int64(2281), true, "i", "s", "thesaurus_lexize", nil, nil, nil, nil}}),
				fixture.Select("SELECT dictinitoption FROM pg_catalog.pg_ts_dict WHERE oid='public.thes_dict'::regdictionary", []string{"dictinitoption"}, []string{"TEXT"}, [][]any{{"dictfile = 'thesaurus_sample', dictionary = 'public.thes_sub'"}}),
				fixture.Select("SELECT count(*)::bigint AS edges FROM pg_catalog.pg_depend WHERE classid=3600 AND objid='public.thes_dict'::regdictionary AND refclassid=3600 AND refobjid='public.thes_sub'::regdictionary", []string{"edges"}, []string{"INT8"}, [][]any{{int64(0)}}),
			}
			for _, s := range steps {
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			}
		}
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct{ input, want any }{{"one", "{1}"}, {[]byte("one"), "{1}"}, {"booking", nil}, {nil, nil}} {
				step := fixture.Select("SELECT ts_lexize('public.thes_dict',$1::text) AS lexemes", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{tc.want}}, tc.input)
				step.Prepared = prepared
				step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					types, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					scan := reflect.TypeFor[string]()
					if backend.PG {
						scan = reflect.TypeFor[any]()
					}
					if len(types) != 1 || types[0].ScanType() != scan {
						t.Fatal("lexize metadata", types)
					}
					if nullable, known := types[0].Nullable(); nullable || known {
						t.Fatal("lexize nullability", nullable, known)
					}
				}
				step.CheckRaw = func(t *testing.T, col, row int, value any) bool {
					if value == nil {
						return true
					}
					if backend.PG {
						if _, ok := value.([]byte); !ok {
							t.Fatalf("array raw %T", value)
						}
					} else if _, ok := value.(string); !ok {
						t.Fatalf("array raw %T", value)
					}
					return true
				}
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
			}
			step := ftsConfigurationVector("SELECT to_tsvector('public.thes_cfg',$1::text) AS normalized", "'123':1", "one two three")
			step.Prepared = prepared
			step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { configurationVectorMetadata(t, rows, backend.PG) }
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, step)
		}
		for _, query := range []string{"PREPARE thes_sql(text) AS SELECT to_tsvector('public.thes_cfg',$1) AS normalized", "PREPARE thes_scalar(text) AS SELECT ts_lexize('public.thes_dict',$1) AS lexemes"} {
			fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec(query, 0))
		}
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, ftsConfigurationVector("EXECUTE thes_sql('one two three')", "'123':1"))
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("EXECUTE thes_scalar('one')", []string{"lexemes"}, []string{"_TEXT"}, [][]any{{"{1}"}}))
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("DEALLOCATE thes_sql", 0))
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("DEALLOCATE thes_scalar", 0))
		const createSQL = "CREATE TEXT SEARCH DICTIONARY public.thes_prepared_create (TEMPLATE=pg_catalog.thesaurus,DictFile=thesaurus_sample,Dictionary='public.thes_sub')"
		creation, err := conn.PrepareContext(p.Ctx, createSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer creation.Close()
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Select("SELECT count(*)::bigint AS n FROM pg_ts_dict WHERE dictname='thes_prepared_create'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: creation, SQL: createSQL}, backend.Recorder, fixture.Exec(createSQL, 0))
		retained, err := conn.QueryContext(p.Ctx, "SELECT ts_lexize('public.thes_dict','one') AS lexemes")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		metadata, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=stuntgres_thes_ties)", 0))
		if !retained.Next() {
			t.Fatal("retained lexeme Rows", retained.Err())
		}
		var lexemes string
		if err := retained.Scan(&lexemes); err != nil || lexemes != "{1}" {
			t.Fatal("retained lexeme datum", lexemes, err)
		}
		if retained.Next() || retained.Err() != nil || metadata[0].DatabaseTypeName() != "_TEXT" {
			t.Fatal("retained array metadata", retained.Err())
		}
		retained.Close()
		fixture.RunStep(t, p.Ctx, conn, backend.Recorder, fixture.Exec("ALTER TEXT SEARCH DICTIONARY public.thes_dict (DictFile=thesaurus_sample)", 0))
	})
}
