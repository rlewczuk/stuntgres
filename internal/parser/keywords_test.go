package parser

import (
	"errors"
	"fmt"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestKeywordInventory(t *testing.T) {
	counts := map[keywordCategory]int{}
	bare := 0
	if len(keywords) != 494 {
		t.Fatalf("keywords=%d", len(keywords))
	}
	for i := range keywords {
		k := &keywords[i]
		counts[k.category]++
		if k.bareLabel {
			bare++
		}
		if i > 0 && keywords[i-1].word >= k.word {
			t.Fatalf("unsorted: %s", k.word)
		}
		if lookupKeyword(k.word) != k {
			t.Fatalf("lookup %s", k.word)
		}
		if k.columnName() != (k.category == Unreserved || k.category == ColumnName) || k.typeFunctionName() != (k.category == Unreserved || k.category == TypeFunctionName) {
			t.Fatalf("contexts %s", k.word)
		}
	}
	if counts[Unreserved] != 330 || counts[ColumnName] != 63 || counts[TypeFunctionName] != 23 || counts[Reserved] != 78 || bare != 455 {
		t.Fatalf("counts=%v bare=%d", counts, bare)
	}
	for _, s := range []struct {
		word string
		cat  keywordCategory
		bare bool
	}{
		{"abort", Unreserved, true}, {"by", Unreserved, true}, {"insert", Unreserved, true}, {"text", Unreserved, true}, {"merge", Unreserved, true}, {"virtual", Unreserved, true},
		{"between", ColumnName, true}, {"bigint", ColumnName, true}, {"integer", ColumnName, true}, {"values", ColumnName, true}, {"json", ColumnName, true}, {"char", ColumnName, false}, {"day", Unreserved, false},
		{"authorization", TypeFunctionName, true}, {"join", TypeFunctionName, true}, {"isnull", TypeFunctionName, false},
		{"notnull", TypeFunctionName, false}, {"default", Reserved, true},
		{"if", Unreserved, true}, {"exists", ColumnName, true},
		{"select", Reserved, true}, {"check", Reserved, true}, {"analyze", Reserved, true}, {"end", Reserved, true}, {"do", Reserved, true}, {"grant", Reserved, false}, {"from", Reserved, false}, {"where", Reserved, false}, {"order", Reserved, false},
	} {
		k := lookupKeyword(s.word)
		if k == nil || k.category != s.cat || k.label() != s.bare {
			t.Fatalf("%s=%#v", s.word, k)
		}
	}
	for _, w := range []string{"bool", "int4", "int8", "date", "uuid", "serial", "jsonb", "bytea", "float4", "float8", "SELECT", "selectx"} {
		if lookupKeyword(w) != nil {
			t.Fatalf("unexpected keyword %s", w)
		}
	}
}

func TestKeywordContexts(t *testing.T) {
	for _, k := range keywords {
		for _, q := range []string{fmt.Sprintf("CREATE TABLE %s (x INT)", k.word), fmt.Sprintf("CREATE TABLE %s.t (x INT)", k.word), fmt.Sprintf("CREATE TABLE t (%s INT)", k.word)} {
			got, err := Parse(q)
			// LIKE now introduces a table element, not an ordinary column.
			if k.word == "like" && q == "CREATE TABLE t (like INT)" {
				if err != nil || len(got) != 1 || got[0].(*CreateTable).Elements[0].Like == nil {
					t.Fatal(q, got, err)
				}
				continue
			}
			if k.columnName() {
				if err != nil || len(got) != 1 {
					t.Fatalf("%s: %v", q, err)
				}
			} else {
				var e *sqlstate.Error
				if got != nil || !errors.As(err, &e) || e.Code != "42601" {
					t.Fatalf("%s: %#v %v", q, got, err)
				}
			}
		}
		if _, err := Parse(fmt.Sprintf("CREATE TABLE \"%s\" (\"%s\" INT)", k.word, k.word)); err != nil {
			t.Fatalf("quoted %s: %v", k.word, err)
		}
		for _, q := range []string{
			fmt.Sprintf("CREATE TABLE public.%s (x INT)", k.word),
			fmt.Sprintf("INSERT INTO public.%s (x) VALUES (7)", k.word),
			fmt.Sprintf("SELECT x FROM public.%s WHERE x=7 ORDER BY x", k.word),
		} {
			if _, err := Parse(q); err != nil {
				t.Fatalf("qualified %s: %v", q, err)
			}
		}
	}
	for _, q := range []string{
		"CREATE TABLE abort (by INT, between TEXT, values BOOL, integer BIGINT, char TEXT)",
		"INSERT INTO public.abort (by,between,values,integer,char) VALUES (1,'one',TRUE,10,'a')",
		"SELECT by,between,values,integer,char FROM public.abort WHERE by=2 ORDER BY by DESC",
		"CREATE TABLE insert (by INT4, int8 INT8, bool BOOL)",
	} {
		if _, err := Parse(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func keywordDiagnostic(t *testing.T, q, code, message, position string) {
	t.Helper()
	stmts, err := Parse(q)
	var got *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Position: position}
	if stmts != nil || !errors.As(err, &got) || *got != want {
		t.Fatalf("%s: %#v %v; want %#v", q, got, err, want)
	}
}
func TestKeywordDiagnostics(t *testing.T) {
	for _, s := range []struct{ q, word, pos string }{
		{"CREATE TABLE select (x INT)", "select", "14"}, {"CREATE TABLE t (select INT)", "select", "17"}, {"CREATE TABLE join (x INT)", "join", "14"}, {"CREATE TABLE t (authorization INT)", "authorization", "17"},
		{"INSERT INTO select VALUES (1)", "select", "13"}, {"INSERT INTO t (select) VALUES (1)", "VALUES", "24"}, {"SELECT select FROM t", "select", "8"}, {"SELECT x FROM t WHERE select = 1", "select", "23"}, {"SELECT x FROM t ORDER BY select", "select", "26"},
		{"CREATE TABLE end (x INT)", "end", "14"}, {"CREATE TABLE analyze (x INT)", "analyze", "14"}, {"CREATE TABLE do (x INT)", "do", "14"}, {"CREATE TABLE grant (x INT)", "grant", "14"}, {"CREATE TABLE SELECT (x INT)", "SELECT", "14"}, {"/*é*/ CREATE TABLE SELECT (x INT)", "SELECT", "20"},
		{"CREATE TABLE select.t (x INT)", "select", "14"}, {"CREATE TABLE staged (x INT); CREATE TABLE select (x INT)", "select", "43"},
		{"INSERT INTO t ((SELECT)) VALUES (1)", "VALUES", "26"},
		{"INSERT INTO t (select) values (1)", "values", "24"},
		{"INSERT INTO t (authorization) VALUES (1)", "authorization", "16"},
	} {
		keywordDiagnostic(t, s.q, "42601", fmt.Sprintf("syntax error at or near \"%s\"", s.word), s.pos)
	}
}
func TestKeywordLabels(t *testing.T) {
	for _, s := range []struct{ q, code, msg, pos string }{
		{"SELECT x char FROM t", "42601", "syntax error at or near \"char\"", "10"}, {"SELECT x day FROM t", "42601", "syntax error at or near \"day\"", "10"},
	} {
		keywordDiagnostic(t, s.q, s.code, s.msg, s.pos)
	}
	for _, label := range []string{"check", "authorization", "default"} {
		orderLabel(t, "SELECT x "+label+" FROM t", label)
	}
	for _, label := range []string{"char", "grant", "Mixed"} {
		orderLabel(t, `SELECT x AS "`+label+`" FROM t`, label)
	}
}

func TestKeywordInsertQueries(t *testing.T) {
	for _, s := range []struct{ q, pos string }{
		{"INSERT INTO t (SELECT)", "16"},
		{"INSERT INTO t SELECT x FROM source_rows", "15"},
		{"INSERT INTO t ((SELECT x FROM source_rows WHERE x=7 ORDER BY x))", "17"},
		{"INSERT INTO t (x) SELECT x FROM source_rows", "19"},
	} {
		statements, err := Parse(s.q)
		if err != nil || len(statements) != 1 || statements[0].(*Insert).Source == nil {
			t.Fatal(s.q, statements, err)
		}
		source := statements[0].(*Insert).Source.Select
		if source == nil || fmt.Sprint(source.Pos.Character) != s.pos {
			t.Fatal("source occurrence lost", s.q, source)
		}
	}
	// A valid query prefix must never conceal a later statement's syntax.
	keywordDiagnostic(t, "INSERT INTO t (SELECT); CREATE TABLE select (x INT)", "42601", "syntax error at or near \"select\"", "38")
}

func TestMappedKeywordLabels(t *testing.T) {
	for _, k := range keywords {
		if k.token == 0 {
			continue
		}
		orderLabel(t, "SELECT x AS "+k.word+" FROM t", k.word)
		// Predicate and logical tokens continue SELECT expressions, not aliases.
		if k.bareLabel && k.token != IS && k.token != BETWEEN && k.token != NOT && k.word != "and" && k.word != "or" {
			orderLabel(t, "SELECT x "+k.word+" FROM t", k.word)
		}
	}
}
func TestKeywordContextualCapabilities(t *testing.T) {
	for _, s := range []struct{ q, code, msg, pos string }{
		{"SELECT between(x) FROM t", "42601", "syntax error at or near \"(\"", "15"},
		{"SELECT integer(x) FROM t", "42601", "syntax error at or near \"(\"", "15"},
		{"SELECT abs(x) FROM t", "0A000", "functions are unsupported", "8"},
		{"SELECT authorization(x) FROM t", "0A000", "functions are unsupported", "8"},
		{"SELECT \"between\"(x) FROM t", "0A000", "functions are unsupported", "8"},
		{"SELECT abort(x) FROM t", "0A000", "functions are unsupported", "8"},
		{"INSERT INTO t VALUES (between(7))", "42601", "syntax error at or near \"(\"", "30"},
		{"SELECT x FROM t WHERE x=between(7)", "42601", "syntax error at or near \"(\"", "32"},
		{"SELECT x FROM t ORDER BY x NULLS bogus", "42601", "syntax error at or near \"NULLS\"", "28"},
		{"SELECT x FROM t ORDER BY x NULLS \"FIRST\"", "42601", "syntax error at or near \"NULLS\"", "28"},
		{"SELECT x FROM t ORDER BY x NULLS", "42601", "syntax error at or near \"NULLS\"", "28"},
		{"SELECT x FROM t ORDER BY x nulls bogus", "42601", "syntax error at or near \"nulls\"", "28"},
		{"SELECT x FROM t ORDER BY x NULLS 1", "42601", "syntax error at or near \"NULLS\"", "28"},
		{"/*é*/ SELECT x FROM t ORDER BY x NULLS bogus", "42601", "syntax error at or near \"NULLS\"", "34"},
		{"SELECT x FROM t ORDER BY x NULLS /* never closed", "42601", "unterminated /* comment at or near \"/* never closed\"", "34"},
	} {
		keywordDiagnostic(t, s.q, s.code, s.msg, s.pos)
	}
	orderLabel(t, `SELECT x "isnull" FROM t`, "isnull")
}

func TestNullsTokenLookahead(t *testing.T) {
	for _, s := range []struct {
		input     string
		tokens    []int
		raw       []string
		positions []int
	}{
		{"NULLS FIRST x", []int{NULLS_LA, KW_U, IDENT}, []string{"NULLS", "FIRST", "x"}, []int{1, 7, 13}},
		{"NULLS bogus", []int{KW_U, IDENT}, []string{"NULLS", "bogus"}, []int{1, 7}},
		{`NULLS "FIRST"`, []int{KW_U, IDENT}, []string{"NULLS", `"FIRST"`}, []int{1, 7}},
		{`"NULLS" FIRST`, []int{IDENT, KW_U}, []string{`"NULLS"`, "FIRST"}, []int{1, 9}},
		{"NULLS /*é*/ FIRST", []int{NULLS_LA, KW_U}, []string{"NULLS", "FIRST"}, []int{1, 13}},
		{"NULLS NULLS FIRST", []int{KW_U, NULLS_LA, KW_U}, []string{"NULLS", "NULLS", "FIRST"}, []int{1, 7, 13}},
		{"NULLS", []int{KW_U}, []string{"NULLS"}, []int{1}},
		{"NULLS 1", []int{KW_U, NUMBER}, []string{"NULLS", "1"}, []int{1, 7}},
		{"nUlLs lAsT", []int{NULLS_LA, KW_U}, []string{"nUlLs", "lAsT"}, []int{1, 7}},
	} {
		l := &lexer{input: s.input}
		var v sqlSymType
		for i, want := range s.tokens {
			token := l.Lex(&v)
			if token != want || v.tok.raw != s.raw[i] || v.tok.pos.Character != s.positions[i] || l.current != v.tok {
				t.Fatalf("%q token%d: %d %#v current%#v", s.input, i, token, v.tok, l.current)
			}
		}
		if token := l.Lex(&v); token != 0 || l.err != nil {
			t.Fatalf("%q end: %d %v", s.input, token, l.err)
		}
	}
	// Consuming the buffered FIRST must not invoke the scanner again.
	l := &lexer{input: "NULLS FIRST x"}
	var v sqlSymType
	if l.Lex(&v) != NULLS_LA {
		t.Fatal("missing ordering token")
	}
	offset := l.offset
	if l.Lex(&v) != KW_U || l.offset != offset {
		t.Fatal("lookahead rescanned or lost")
	}
}

func TestKeywordLaterProjectionCalls(t *testing.T) {
	for _, s := range []struct{ q, code, msg, pos string }{
		{"SELECT x, abs(x) FROM t", "0A000", "functions are unsupported", "11"},
		{"/*é*/ SELECT x, abs(x) FROM t", "0A000", "functions are unsupported", "17"},
		{"CREATE TABLE staged (x INT); SELECT x, abs(x) FROM t", "0A000", "functions are unsupported", "40"},
		{"SELECT x, abort(x) FROM t", "0A000", "functions are unsupported", "11"},
		{"SELECT x, authorization(x) FROM t", "0A000", "functions are unsupported", "11"},
		{"SELECT x, \"between\"(x) FROM t", "0A000", "functions are unsupported", "11"},
		{"SELECT x, between(x) FROM t", "42601", "syntax error at or near \"(\"", "18"},
		{"SELECT x, x, abs(x) FROM t", "0A000", "functions are unsupported", "14"},
		{"SELECT x, x, between(x) FROM t", "42601", "syntax error at or near \"(\"", "21"},
	} {
		keywordDiagnostic(t, s.q, s.code, s.msg, s.pos)
	}
}

func TestKeywordConditionalCreateTerminals(t *testing.T) {
	l := &lexer{input: "IF EXISTS"}
	var v sqlSymType
	for _, s := range []struct {
		token    int
		word     string
		category keywordCategory
	}{
		{IF, "if", Unreserved}, {EXISTS, "exists", ColumnName},
	} {
		if token := l.Lex(&v); token != s.token || v.tok.keyword == nil || v.tok.keyword.word != s.word || v.tok.keyword.category != s.category || !v.tok.keyword.bareLabel {
			t.Fatalf("%s token=%d lexeme=%+v error=%v", s.word, token, v.tok, l.err)
		}
	}
}
