//go:build reference

package reference_test

import "strings"

// Independent expectations copied from identifier_test.go; no engine implementation
// is used to derive PostgreSQL results or native capability diagnostics.
var identifierSyntaxCases = []struct{ q, word, pos string }{
	{"CREATE TABLE select (x INT)", "select", "14"}, {"CREATE TABLE t (select INT)", "select", "17"}, {"CREATE TABLE join (x INT)", "join", "14"}, {"CREATE TABLE t (authorization INT)", "authorization", "17"},
	{"INSERT INTO select VALUES (1)", "select", "13"}, {"INSERT INTO t (select) VALUES (1)", "VALUES", "24"}, {"SELECT select FROM t", "select", "8"}, {"SELECT x FROM t WHERE select = 1", "select", "23"}, {"SELECT x FROM t ORDER BY select", "select", "26"},
	{"INSERT INTO t ((SELECT)) VALUES (1)", "VALUES", "26"}, {"INSERT INTO t (select) values (1)", "values", "24"}, {"INSERT INTO t (authorization) VALUES (1)", "authorization", "16"},
	{"CREATE TABLE end (x INT)", "end", "14"}, {"CREATE TABLE analyze (x INT)", "analyze", "14"}, {"CREATE TABLE do (x INT)", "do", "14"}, {"CREATE TABLE grant (x INT)", "grant", "14"}, {"CREATE TABLE SELECT (x INT)", "SELECT", "14"}, {"/*é*/ CREATE TABLE SELECT (x INT)", "SELECT", "20"}, {"CREATE TABLE select.t (x INT)", "select", "14"},
}

var identifierNormalizationCases = []struct{ spelling, name string }{
	{"MiXeD", "mixed"}, {"ÉCOLE", "École"}, {"_select$1", "_select$1"}, {"select1", "select1"},
	{`"Odd"`, "Odd"}, {`"a""b"`, `a"b`}, {`"a.b"`, "a.b"},
	{strings.Repeat("A", 63), strings.Repeat("a", 63)},
	{strings.Repeat("B", 64), strings.Repeat("b", 63)},
	{strings.Repeat("C", 62) + "éZ", strings.Repeat("c", 62)},
	{`"` + strings.Repeat("D", 62) + `éZ"`, strings.Repeat("D", 62)},
}

// PostgreSQL outcomes are independently specified; native capability errors
// deliberately differ for legal calls, expressions, ordering and aliases.
var identifierContextCases = []struct {
	q, code, message, position, column, typ string
	row                                     any
}{
	{"SELECT between(x) FROM t", "42601", "syntax error at or near \"(\"", "15", "", "", nil},
	{"SELECT integer(x) FROM t", "42601", "syntax error at or near \"(\"", "15", "", "", nil},
	{"SELECT abs(x) FROM t", "0A000", "functions are unsupported", "8", "abs", "INT4", int64(7)},
	{"SELECT authorization(x) FROM t", "0A000", "functions are unsupported", "8", "authorization", "INT4", int64(7)},
	{"SELECT f(x) FROM t", "0A000", "functions are unsupported", "8", "f", "INT4", int64(7)},
	{"SELECT abort(x) FROM t", "0A000", "functions are unsupported", "8", "abort", "INT4", int64(7)},
	{"SELECT \"between\"(x) FROM t", "0A000", "functions are unsupported", "8", "between", "INT4", int64(7)},
	{"INSERT INTO t VALUES (between(7))", "42601", "syntax error at or near \"(\"", "30", "", "", nil},
	{"SELECT x FROM t WHERE x=between(7)", "42601", "syntax error at or near \"(\"", "32", "", "", nil},
	{"SELECT x FROM t ORDER BY x NULLS bogus", "42601", "syntax error at or near \"NULLS\"", "28", "", "", nil},
	{"SELECT x FROM t ORDER BY x NULLS \"FIRST\"", "42601", "syntax error at or near \"NULLS\"", "28", "", "", nil},
	{"SELECT x FROM t ORDER BY x NULLS", "42601", "syntax error at or near \"NULLS\"", "28", "", "", nil},
	{"SELECT x FROM t ORDER BY x nulls bogus", "42601", "syntax error at or near \"nulls\"", "28", "", "", nil},
	{"SELECT x FROM t ORDER BY x NULLS 1", "42601", "syntax error at or near \"NULLS\"", "28", "", "", nil},
	{"/*é*/ SELECT x FROM t ORDER BY x NULLS bogus", "42601", "syntax error at or near \"NULLS\"", "34", "", "", nil},
	{"SELECT x FROM t ORDER BY x NULLS /* never closed", "42601", "unterminated /* comment at or near \"/* never closed\"", "34", "", "", nil},
}

var identifierLaterCallCases = []struct {
	q, code, message, position string
	columns, types             []string
	rows                       [][]any
}{
	{"SELECT x, abs(x) FROM t", "0A000", "functions are unsupported", "11", []string{"x", "abs"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}},
	{"/*é*/ SELECT x, abs(x) FROM t", "0A000", "functions are unsupported", "17", []string{"x", "abs"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}},
	{"SELECT x, abort(x) FROM t", "0A000", "functions are unsupported", "11", []string{"x", "abort"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}},
	{"SELECT x, authorization(x) FROM t", "0A000", "functions are unsupported", "11", []string{"x", "authorization"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}},
	{"SELECT x, \"between\"(x) FROM t", "0A000", "functions are unsupported", "11", []string{"x", "between"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(7)}}},
	{"SELECT x, between(x) FROM t", "42601", "syntax error at or near \"(\"", "18", nil, nil, nil},
	{"SELECT x, x, abs(x) FROM t", "0A000", "functions are unsupported", "14", []string{"x", "x", "abs"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(7), int64(7), int64(7)}}},
	{"SELECT x, x, between(x) FROM t", "42601", "syntax error at or near \"(\"", "21", nil, nil, nil},
}
