package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func dictionaryScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.RoleReferenceCleanup(t, []string{"dictionary_creator", "dictionary_peer", "dictionary_recipient"})
	fixture.SchemaReferenceCleanup(t, "dictionary_left", "dictionary_right", "dictionary_owned")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}
func dictionaryResult(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, q string, want any, args ...any) {
	t.Helper()
	s := fixture.Select(q, []string{"lexemes"}, []string{"_TEXT"}, [][]any{{want}}, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		types, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		ct := types[0]
		scan := reflect.TypeFor[string]()
		if b.PG {
			scan = reflect.TypeFor[any]()
		}
		length, lk := ct.Length()
		precision, scale, dk := ct.DecimalSize()
		nullable, nk := ct.Nullable()
		if ct.ScanType() != scan || length != 0 || lk || precision != 0 || scale != 0 || dk || nullable || nk {
			t.Fatalf("typed lexize metadata: scan=%v length=%d/%t decimal=%d,%d/%t nullable=%t/%t", ct.ScanType(), length, lk, precision, scale, dk, nullable, nk)
		}
	}
	s.CheckRaw = func(t *testing.T, col, row int, value any) bool {
		if value == nil {
			return true
		}
		if b.PG {
			if _, ok := value.([]byte); !ok {
				t.Fatalf("pq array raw %T", value)
			}
		} else {
			if _, ok := value.(string); !ok {
				t.Fatalf("native array raw %T", value)
			}
		}
		return true
	}
	fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
}
func dictionaryDiagnostic(code, message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic(code, message)
}
func dictionaryError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, code, message string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic(q, dictionaryDiagnostic(code, message))
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	}
}

func TestReferenceDictionaryValues(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsSelect(t, p, b, c, "SELECT oid::bigint AS id,dicttemplate::bigint AS template,dictinitoption FROM pg_ts_dict WHERE dictname='simple' AND dictnamespace='pg_catalog'::regnamespace", []string{"id", "template", "dictinitoption"}, []string{"INT8", "INT8", "TEXT"}, [][]any{{int64(3765), int64(3727), nil}})
		regtsSelect(t, p, b, c, "SELECT oid::bigint AS id,tmplinit::oid::bigint AS init,tmpllexize::oid::bigint AS lexize FROM pg_ts_template WHERE tmplname='simple' AND tmplnamespace='pg_catalog'::regnamespace", []string{"id", "init", "lexize"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(3727), int64(3725), int64(3726)}})
		regtsSelect(t, p, b, c, "SELECT oid::bigint AS id,prorettype::bigint AS result,proisstrict,provolatile,proparallel FROM pg_proc WHERE oid=3723", []string{"id", "result", "proisstrict", "provolatile", "proparallel"}, []string{"INT8", "INT8", "BOOL", "CHAR", "CHAR"}, [][]any{{int64(3723), int64(1009), true, "i", "s"}})
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_english (TEMPLATE=pg_catalog.simple,STOPWORDS=english)", 0)
		for _, tc := range []struct {
			dictionary, input string
			want              any
		}{
			{"simple", "WORD", "{word}"}, {"simple", "Cats!", "{cats!}"}, {"simple", "A B", `{"a b"}`}, {"simple", "ÄBC", "{Äbc}"}, {"simple", "", "{}"},
			{"dictionary_english", "The", "{}"}, {"dictionary_english", "YeS", "{yes}"}, {"dictionary_english", "DON", "{}"}, {"dictionary_english", "S", "{}"}, {"dictionary_english", "T", "{}"},
			{"simple", `A,B"C\D{ E}`, `{"a,b\"c\\d{ e}"}`},
		} {
			q := "SELECT ts_lexize($1::regdictionary,$2::text) AS lexemes"
			dictionaryResult(t, p, b, c, q, tc.want, tc.dictionary, tc.input)
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			dictionaryResult(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, q, tc.want, tc.dictionary, tc.input)
			stmt.Close()
		}
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('simple',NULL) AS lexemes", nil)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize(NULL,'WORD') AS lexemes", nil)
		regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_english (accept=false)", 0)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_english','YeS') AS lexemes", nil)
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_english','The') AS lexemes", "{}")
		dictionaryResult(t, p, b, c, "SELECT ts_lexize('dictionary_english','') AS lexemes", "{}")
		dictionaryResult(t, p, b, c, `SELECT ts_lexize('simple','A,B"C\D{ E}') AS lexemes`, `{"a,b\"c\\d{ e}"}`)
	})
}

func TestReferenceDictionaryBinding(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		q := "SELECT ts_lexize($1,$2) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, args := range [][]any{{"simple", "WORD"}, {[]byte("simple"), []byte("Cats!")}, {"simple", nil}, {"simple", []byte(nil)}, {nil, "WORD"}} {
			var want any
			if args[1] != nil && args[0] != nil {
				switch v := args[1].(type) {
				case string:
					if v == "WORD" {
						want = "{word}"
					}
				case []byte:
					if v != nil {
						want = "{cats!}"
					}
				}
			}
			if token, ok := args[1].([]byte); ok && token == nil && !b.PG {
				want = "{}"
			}
			dictionaryResult(t, p, b, c, q, want, args...)
			dictionaryResult(t, p, b, held, q, want, args...)
		}
		for _, empty := range []any{[]byte{}, ""} {
			dictionaryResult(t, p, b, c, q, "{}", "simple", empty)
			dictionaryResult(t, p, b, held, q, "{}", "simple", empty)
		}
		nameBytes, tokenBytes := []byte("simple"), []byte("WORD")
		rows, err := stmt.QueryContext(p.Ctx, nameBytes, tokenBytes)
		if err != nil {
			t.Fatal(err)
		}
		nameBytes[0], tokenBytes[0] = 'x', 'x'
		metadata, err := rows.ColumnTypes()
		if err != nil || metadata[0].DatabaseTypeName() != "_TEXT" {
			rows.Close()
			t.Fatal(metadata, err)
		}
		if !rows.Next() {
			rows.Close()
			t.Fatal("owned byte lexeme missing")
		}
		var detached any
		if err := rows.Scan(&detached); err != nil || fixture.Normalize(detached) != "{word}" {
			rows.Close()
			t.Fatal("owned byte alias", detached, err)
		}
		if rows.Next() || rows.Err() != nil {
			rows.Close()
			t.Fatal(rows.Err())
		}
		rows.Close()
		dictionaryResult(t, p, b, c, "SELECT ts_lexize($1::text::regdictionary,$2::text) AS lexemes", "{word}", "simple", "WORD")
		regtsSelect(t, p, b, c, "SELECT ts_lexize('simple',$1::text)=ts_lexize('simple',$1::text) AS same", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, "WORD")
		for _, tc := range []struct{ q, signature string }{{"SELECT ts_lexize(true,'word')", "boolean, unknown"}, {"SELECT ts_lexize('simple',1)", "unknown, integer"}, {"SELECT ts_lexize('simple')", "unknown"}} {
			d := dictionaryDiagnostic("42883", "function ts_lexize("+tc.signature+") does not exist")
			d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			d.Position = "8"
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(tc.q, d)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		dictionaryError(t, p, b, c, "SELECT ts_lexize(4294967295::oid::regdictionary,'word')", "XX000", "cache lookup failed for text search dictionary 4294967295")
		dictionaryError(t, p, b, c, "SELECT ts_lexize(1,'word')", "XX000", "cache lookup failed for text search dictionary 1")
	})
}

func TestReferenceDictionaryOptions(t *testing.T) {
	dictionaryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		regtsExec(t, p, b, c, "CREATE EXTENSION unaccent", 0)
		regtsExec(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_options (TEMPLATE=public.unaccent,TEMPLATE=pg_catalog.simple,STOPWORDS=english)", 0)
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_bad (TEMPLATE=missing,TEMPLATE=pg_catalog.simple)", "42704", `text search template "missing" does not exist`)
		q := "SELECT ts_lexize('dictionary_options'::regdictionary,$1::text) AS lexemes"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			options  string
			yes, the any
		}{
			{"accept=false", nil, "{}"}, {"accept=true,accept=false", nil, "{}"}, {"accept", "{yes}", "{}"}, {"stopwords", "{yes}", "{the}"}, {"dummy", "{yes}", "{the}"}, {"stopwords=english,accept=0", nil, "{}"}, {"accept=1", "{yes}", "{}"}, {"accept='OFF'", nil, "{}"}, {"accept='ON'", "{yes}", "{}"}, {"stopwords=''", "{yes}", "{the}"},
		} {
			regtsExec(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_options ("+tc.options+")", 0)
			dictionaryResult(t, p, b, held, q, tc.yes, "YeS")
			dictionaryResult(t, p, b, held, q, tc.the, "The")
		}
		var before string
		var beforeID, beforeOwner, beforeTemplate int64
		snapshot := "SELECT oid::bigint,dictowner::bigint,dicttemplate::bigint,dictinitoption FROM pg_ts_dict WHERE dictname='dictionary_options'"
		if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&beforeID, &beforeOwner, &beforeTemplate, &before); err != nil {
			t.Fatal(err)
		}
		if before != "accept = 'ON', stopwords = ''" || beforeID <= 0 || beforeOwner <= 0 || beforeTemplate != 3727 {
			t.Fatal("independent option metadata", beforeID, beforeOwner, beforeTemplate, before)
		}
		for _, tc := range []struct{ options, code, message string }{
			{"accept=false,unknown=1", "22023", `unrecognized simple dictionary parameter: "unknown"`}, {"template=simple", "22023", `unrecognized simple dictionary parameter: "template"`}, {`"Accept"=false`, "22023", `unrecognized simple dictionary parameter: "Accept"`}, {"accept='yes'", "42601", "accept requires a Boolean value"}, {"accept='no'", "42601", "accept requires a Boolean value"}, {"accept='t'", "42601", "accept requires a Boolean value"}, {"accept=2", "42601", "accept requires a Boolean value"}, {"stopwords='../english'", "22023", `invalid text search configuration file name "../english"`},
		} {
			dictionaryError(t, p, b, c, "ALTER TEXT SEARCH DICTIONARY dictionary_options ("+tc.options+")", tc.code, tc.message)
			var after string
			var id, owner, template int64
			if err := c.QueryRowContext(p.Ctx, snapshot).Scan(&id, &owner, &template, &after); err != nil || after != before || id != beforeID || owner != beforeOwner || template != beforeTemplate {
				t.Fatal("failed option update published", id, owner, template, after, err)
			}
			dictionaryResult(t, p, b, held, q, "{yes}", "YeS")
		}
		for _, tc := range []struct{ options, code, message string }{
			{"accept=true,accept=false", "22023", "multiple Accept parameters"}, {"stopwords=english,stopwords=english", "22023", "multiple StopWords parameters"}, {"stopwords", "42601", "stopwords requires a parameter"},
		} {
			dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_bad (TEMPLATE=simple,"+tc.options+")", tc.code, tc.message)
		}
		dictionaryError(t, p, b, c, "CREATE TEXT SEARCH DICTIONARY dictionary_bare (TEMPLATE=simple,accept)", "42601", "accept requires a parameter")
	})
}
