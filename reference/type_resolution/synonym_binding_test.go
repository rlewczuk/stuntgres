package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceSynonymBinding(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		synonymCreate(t, p, b, c, "synonym_binding")
		for _, q := range []string{"SELECT ts_lexize($1,$2) AS lexemes", "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes", "SELECT ts_lexize($1::text::regdictionary,$2::text) AS lexemes"} {
			held := synonymHeld(t, p, c, q)
			for _, tc := range []struct {
				args []any
				want any
			}{
				{[]any{"synonym_binding", "POSTGRES"}, "{pgsql}"}, {[]any{[]byte("synonym_binding"), []byte("indices")}, "{index}"},
				{[]any{"synonym_binding", nil}, nil}, {[]any{"synonym_binding", []byte(nil)}, nil}, {[]any{nil, "postgres"}, nil}, {[]any{"synonym_binding", ""}, nil}, {[]any{"synonym_binding", []byte{}}, nil},
			} {
				dictionaryResult(t, p, b, c, q, tc.want, tc.args...)
				dictionaryResult(t, p, b, held, q, tc.want, tc.args...)
			}
			name, token := []byte("synonym_binding"), []byte("POSTGRES")
			rows, err := held.Statement.QueryContext(p.Ctx, name, token)
			if err != nil {
				t.Fatal(err)
			}
			name[0], token[0] = 'x', 'x'
			meta, err := rows.ColumnTypes()
			if err != nil || len(meta) != 1 || meta[0].DatabaseTypeName() != "_TEXT" {
				rows.Close()
				t.Fatal(meta, err)
			}
			if !rows.Next() {
				rows.Close()
				t.Fatal("owned byte lexeme missing", rows.Err())
			}
			var value any
			if err := rows.Scan(&value); err != nil || fixture.Normalize(value) != "{pgsql}" {
				rows.Close()
				t.Fatal(value, err)
			}
			if rows.Next() || rows.Err() != nil {
				rows.Close()
				t.Fatal(rows.Err())
			}
			rows.Close()
			held.Statement.Close()
		}
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				q, label string
				values   [][]any
			}{
				{"SELECT ts_lexize('synonym_binding','postgres') WHERE false", "ts_lexize", nil},
				{"SELECT ts_lexize('synonym_binding',NULL)", "ts_lexize", [][]any{{nil}}},
				{"SELECT ts_lexize('synonym_binding','indices')", "ts_lexize", [][]any{{"{index}"}}},
			} {
				s := synonymTypedStep(b, tc.q, tc.label, "_TEXT", tc.values)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		for _, tc := range []struct{ q, signature, position string }{
			{"SELECT ts_lexize('synonym_binding',true)", "unknown, boolean", "8"},
			{"SELECT ts_lexize('synonym_binding')", "unknown", "8"},
			{"SELECT ts_lexize('synonym_binding','postgres','extra')", "unknown, unknown, unknown", "8"},
			{"SELECT 'é' AS prefix, ts_lexize('synonym_binding',true)", "unknown, boolean", "23"},
		} {
			d := dictionaryDiagnostic("42883", "function ts_lexize("+tc.signature+") does not exist")
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			d.Position = tc.position
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.q, d)
				s.Prepared, s.ErrorStage = prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		missing := dictionaryDiagnostic("42704", `text search dictionary "synonym_absent" does not exist`)
		missing.Position = "18"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT ts_lexize('synonym_absent','postgres')", missing)
			s.Prepared, s.ErrorStage = prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		q := "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
		held := synonymHeld(t, p, c, q)
		defer held.Statement.Close()
		bound := dictionaryDiagnostic("42704", `text search dictionary "synonym_absent" does not exist`)
		bound.Context = "unnamed portal parameter $1 = '...'"
		for _, e := range []fixture.Executor{c, held} {
			s := fixture.ErrorDiagnostic(q, bound, "synonym_absent", "postgres")
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
		}
	})
}

func synonymMissingResource(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, basename string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		d := dictionaryDiagnostic("F0000", fmt.Sprintf("could not open synonym file %q: No such file or directory", basename+".syn"))
		pg := dictionaryDiagnostic("F0000", fmt.Sprintf("could not open synonym file %q: No such file or directory", "/usr/share/postgresql/18/tsearch_data/"+basename+".syn"))
		s := fixture.ErrorDiagnostic(q, d)
		s.PostgresDiagnostic, s.Prepared, s.ErrorStage = &pg, prepared, "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func TestReferenceSynonymDiagnostics(t *testing.T) {
	// These full diagnostic candidates are release-source-derived. The root's
	// pinned oracle round must characterize optional fields and execution phases.
	cases := []struct{ options, code, message string }{
		{"", "22023", "missing Synonyms parameter"}, {"synonyms", "42601", "synonyms requires a parameter"},
		{"synonyms=synonym_sample,casesensitive", "42601", "casesensitive requires a parameter"},
		{"synonyms=synonym_sample,unknown=true", "22023", `unrecognized synonym parameter: "unknown"`},
		{`"Synonyms"=synonym_sample`, "22023", `unrecognized synonym parameter: "Synonyms"`},
		{`synonyms=synonym_sample,"CaseSensitive"=true`, "22023", `unrecognized synonym parameter: "CaseSensitive"`},
		{"synonyms=synonym_sample,casesensitive='yes'", "42601", "casesensitive requires a Boolean value"},
		{"synonyms=synonym_sample,casesensitive='no'", "42601", "casesensitive requires a Boolean value"},
		{"synonyms=synonym_sample,casesensitive='t'", "42601", "casesensitive requires a Boolean value"},
		{"synonyms=synonym_sample,casesensitive='f'", "42601", "casesensitive requires a Boolean value"},
		{"synonyms=synonym_sample,casesensitive='1'", "42601", "casesensitive requires a Boolean value"},
		{"synonyms=synonym_sample,casesensitive=2", "42601", "casesensitive requires a Boolean value"},
		{"synonyms='../synonym_sample'", "22023", `invalid text search configuration file name "../synonym_sample"`},
		{"synonyms='SYNONYM_SAMPLE'", "22023", `invalid text search configuration file name "SYNONYM_SAMPLE"`},
		{"synonyms='synonym.sample'", "22023", `invalid text search configuration file name "synonym.sample"`},
		{"synonyms='synonym/sample'", "22023", `invalid text search configuration file name "synonym/sample"`},
		{"synonyms='café'", "22023", `invalid text search configuration file name "café"`},
	}
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				options := ""
				if tc.options != "" {
					options = "," + tc.options
				}
				dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY synonym_bad (TEMPLATE=synonym"+options+")", tc.code, tc.message)
				regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname='synonym_bad'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
			})
		})
	}
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY synonym_bad (Synonyms=synonym_sample)", "42P17", "text search template is required")
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY synonym_bad (TEMPLATE=synonym_absent,Synonyms=synonym_sample)", "42704", `text search template "synonym_absent" does not exist`)
		for _, options := range []string{"synonyms=synonym_missing", "synonyms=synonym_sample,synonyms=synonym_missing", "synonyms=synonym_missing,casesensitive"} {
			synonymMissingResource(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY synonym_missing_data (TEMPLATE=synonym,"+options+")", "synonym_missing")
		}
		synonymMissingResource(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY synonym_missing_data (TEMPLATE=synonym,synonyms='')", "")
		synonymCreate(t, p, b, c, "synonym_collision")
		var ns int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'public'::regnamespace::oid::bigint").Scan(&ns); err != nil {
			t.Fatal(err)
		}
		d := dictionaryDiagnostic("23505", `duplicate key value violates unique constraint "pg_ts_dict_dictname_index"`)
		d.Detail = fmt.Sprintf("Key (dictname, dictnamespace)=(synonym_collision, %d) already exists.", ns)
		d.Schema, d.Table, d.Constraint = "pg_catalog", "pg_ts_dict", "pg_ts_dict_dictname_index"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("CREATE TEXT SEARCH DICTIONARY synonym_collision (TEMPLATE=synonym,Synonyms=synonym_sample)", d)
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		prefix := "CREATE TEXT SEARCH DICTIONARY synonym_prefix (TEMPLATE=synonym,Synonyms=synonym_sample) "
		suffix := dictionaryDiagnostic("42601", `syntax error at or near "invalid_suffix"`)
		suffix.Position = fixture.Position(utf8.RuneCountInString(prefix) + 1)
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(prefix+"invalid_suffix", suffix)
			s.Prepared, s.ErrorStage = prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		regtsSelect(t, p, b, c, "SELECT count(*) AS n FROM pg_ts_dict WHERE dictname IN ('synonym_prefix','synonym_missing_data','synonym_bad')", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}})
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('synonym_collision','POSTGRES') AS lexemes", "{pgsql}")
	})
}
