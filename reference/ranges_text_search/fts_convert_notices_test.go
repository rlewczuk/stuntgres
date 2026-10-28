package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func ftsConvertNotice(t *testing.T, got *[]fixture.Diagnostic, input string, count int) {
	t.Helper()
	var want []fixture.Diagnostic
	message := `text-search query doesn't contain lexemes: ""`
	if input != "" {
		message = "text-search query contains only stop words or doesn't contain lexemes, ignored"
	}
	for range count {
		want = append(want, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message})
	}
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("query notices=%#v want=%#v", *got, want)
	}
	*got = nil
}
func TestReferenceFTSConvertNotices(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
		var notices []fixture.Diagnostic
		_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		defer ftsConvertSetting(t, p, c)()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, recorder, s) }
		for _, name := range ftsConvertNames[1:] {
			for _, input := range []string{"", "the"} {
				query := "SELECT " + name + "('english'::regconfig,$1::text) AS normalized"
				s := ftsConvertValue(query, name, "", input)
				run(s)
				ftsConvertNotice(t, &notices, input, 1)
				run(fixture.Prepared(s))
				ftsConvertNotice(t, &notices, input, 1)
			}
		}
		for _, input := range []string{"", "the"} {
			for _, path := range []string{"explicit_literal", "explicit_parameter", "default_literal"} {
				expression := "'english'::regconfig,'" + input + "'::text"
				var args []any
				if path == "explicit_parameter" {
					expression = "'english'::regconfig,$1::text"
					args = []any{input}
				}
				if path == "default_literal" {
					expression = "'" + input + "'::text"
				}
				query := "SELECT to_tsquery(" + expression + ") AS normalized"
				stmt, err := c.PrepareContext(p.Ctx, query)
				if err != nil {
					t.Fatal(err)
				}
				ftsConvertNotice(t, &notices, input, 0)
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
				for execution := range 2 {
					fixture.RunStep(t, p.Ctx, held, recorder, ftsConvertValue(query, "to_tsquery", "", args...))
					count := 1
					if execution == 1 && path == "explicit_literal" {
						count = 0
					}
					ftsConvertNotice(t, &notices, input, count)
				}
				stmt.Close()
			}
		}
		run(ftsConvertValue("SELECT to_tsquery('english','the & cat') AS normalized", "to_tsquery", "'cat'"))
		ftsConvertNotice(t, &notices, "", 0)
		for _, name := range ftsConvertNames {
			run(ftsConvertValue("SELECT "+name+"('english',NULL::text) AS normalized", name, nil))
			ftsConvertNotice(t, &notices, "", 0)
		}
		for _, input := range []string{"", "the"} {
			run(ftsConvertValue("SELECT to_tsvector('english',$1::text) AS normalized", "to_tsvector", "", input))
			ftsConvertNotice(t, &notices, "", 0)
		}
		run(ftsConvertValue("SELECT to_tsvector('simple',$1::text) AS normalized", "to_tsvector", "'rats':1", strings.Repeat("a", 2047)+" Rats"))
		want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "54000", Message: "word is too long to be indexed", Detail: "Words longer than 2047 characters are ignored."}}
		if !reflect.DeepEqual(notices, want) {
			t.Fatal(notices, want)
		}
		notices = nil
		for _, prepared := range []bool{false, true} {
			emptyQuote := ftsConvertValue("SELECT websearch_to_tsquery('simple',$1::text) AS normalized", "websearch_to_tsquery", "", `""`)
			emptyQuote.Prepared = prepared
			run(emptyQuote)
			ftsConvertNotice(t, &notices, `""`, 1)
			webOR := ftsConvertValue("SELECT websearch_to_tsquery('simple',$1::text) AS normalized", "websearch_to_tsquery", "'cat' | 'dog'", `cat OR "" dog`)
			webOR.Prepared = prepared
			run(webOR)
			ftsConvertNotice(t, &notices, "", 0)
			boundary := ftsConvertValue("SELECT to_tsquery('english',$1::text) AS normalized", "to_tsquery", "'"+strings.Repeat("a", 2046)+"'", strings.Repeat("a", 2046))
			boundary.Prepared = prepared
			run(boundary)
			ftsConvertNotice(t, &notices, "", 0)
			for _, quoted := range []bool{false, true} {
				input := strings.Repeat("a", 2047)
				if quoted {
					input = "'" + input + "'"
				}
				oversized := ftsConvertValue("SELECT to_tsquery('english',$1::text) AS normalized", "to_tsquery", "", input)
				oversized.Prepared = prepared
				run(oversized)
				expected := []fixture.Diagnostic{
					{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "54000", Message: "word is too long to be indexed", Detail: "Words longer than 2047 characters are ignored."},
					{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "text-search query contains only stop words or doesn't contain lexemes, ignored"},
				}
				if !reflect.DeepEqual(notices, expected) {
					t.Fatal("oversized strict-token notices", notices, expected)
				}
				notices = nil
			}
		}
		run(fixture.Exec("SET client_min_messages=warning", 0))
		run(ftsConvertValue("SELECT to_tsquery('english',$1::text) AS normalized", "to_tsquery", "", "the"))
		ftsConvertNotice(t, &notices, "the", 0)
	})
}

func TestReferenceFTSConvertFeatureSmoke(t *testing.T) {
	ftsConfigurationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		defer ftsConvertSetting(t, p, c)()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, tc := range [][3]string{
			{"to_tsvector", "The Fat Rats", "'fat':2 'rat':3"},
			{"to_tsquery", "The & Fat & Rats", "'fat' & 'rat'"},
			{"plainto_tsquery", "The Fat & Rats:C", "'fat' & 'rat' & 'c'"},
			{"phraseto_tsquery", "The Cat and Rats", "'cat' <2> 'rat'"},
			{"websearch_to_tsquery", `signal -"segmentation fault"`, "'signal' & !( 'segment' <-> 'fault' )"},
		} {
			query := "SELECT " + tc[0] + "($1::text)"
			s := fixture.Select(query, []string{tc[0]}, []string{ftsConvertType(tc[0])}, [][]any{{tc[2]}}, tc[1])
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { ftsConvertMetadata(t, r, b.PG, tc[0], ftsConvertType(tc[0])) }
			run(s)
			run(fixture.Prepared(s))
		}
		query := "SELECT plainto_tsquery($1::text) AS normalized"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsConvertValue(query, "plainto_tsquery", "'rat'", "The Rats"))
		run(fixture.Exec("SET default_text_search_config='simple'", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, ftsConvertValue(query, "plainto_tsquery", "'the' & 'rats'", "The Rats"))
		run(ftsConvertValue("SELECT plainto_tsquery('english','The Rats') AS normalized", "plainto_tsquery", "'rat'"))
		run(ftsConvertValue("SELECT to_tsquery('english','foo <-> a <-> the <-> bar') AS normalized", "to_tsquery", "'foo' <3> 'bar'"))
		padding := ftsConvertValue("SELECT to_tsquery('english',$1::text) AS normalized", "to_tsquery", "'foo' <-> 'bar'", "foo <-> 'the bar'")
		run(padding)
		run(fixture.Prepared(padding))
		punctuationOR := ftsConvertValue("SELECT websearch_to_tsquery('simple',$1::text) AS normalized", "websearch_to_tsquery", "'cat' | 'dog'", "cat OR,dog")
		run(punctuationOR)
		run(fixture.Prepared(punctuationOR))
		run(ftsConvertValue("SELECT websearch_to_tsquery('english',$1::text) AS normalized", "websearch_to_tsquery", "!'fine' & 'dear' <-> 'friend' | 'good'", `(()) )))) this ||| is && -fine, "dear friend" OR good`))
		s := fixture.ErrorDiagnostic("SELECT to_tsquery('english',$1::text)", fixture.ControlsReferenceDiagnostic("42601", `no operand in tsquery: "a &"`), "a &")
		s.SelectRows, s.ErrorStage = true, "execute"
		run(s)
		run(fixture.Prepared(s))
		run(ftsConvertValue("SELECT websearch_to_tsquery('english','cat &') AS normalized", "websearch_to_tsquery", "'cat'"))
		run(ftsConvertValue("SELECT to_tsquery('english',NULL::text) AS normalized", "to_tsquery", nil))
		run(ftsConvertValue("SELECT to_tsquery('english','') AS normalized", "to_tsquery", ""))
		run(ftsConvertValue("SELECT to_tsquery('english','the') AS normalized", "to_tsquery", ""))
		var notices []fixture.Diagnostic
		_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		nq := "SELECT to_tsquery('english','the') AS normalized"
		ns, err := nc.PrepareContext(p.Ctx, nq)
		if err != nil {
			t.Fatal(err)
		}
		defer ns.Close()
		ftsConvertNotice(t, &notices, "the", 0)
		for execution := range 2 {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: nc, Statement: ns, SQL: nq}, recorder, ftsConvertValue(nq, "to_tsquery", ""))
			ftsConvertNotice(t, &notices, "the", 1-execution)
		}
		fixture.RunStep(t, p.Ctx, nc, recorder, ftsConvertValue("SELECT websearch_to_tsquery('simple',$1::text) AS normalized", "websearch_to_tsquery", "", `""`))
		ftsConvertNotice(t, &notices, `""`, 1)
	})
}
