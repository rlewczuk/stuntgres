package parser

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/internal/sqlstate"
)

func parseExplain(t *testing.T, source string) *Explain {
	t.Helper()
	statements, err := Parse(source)
	if err != nil || len(statements) != 1 {
		t.Fatalf("Parse(%q) = %#v, %v", source, statements, err)
	}
	statement, ok := statements[0].(*Explain)
	if !ok {
		t.Fatalf("Parse(%q) returned %T", source, statements[0])
	}
	return statement
}

func TestExplainParserOptions(t *testing.T) {
	const source = "/* λ */ EXPLAIN (ANALYSE false, verbose, costs 0, costs +1, timing -1, memory 1.5, format 'text', serialize BINARY, settings ON, buffers OFF, format \"Text\") SELECT $1"
	statement := parseExplain(t, source)
	position := func(token string) Position {
		offset := strings.Index(source, token)
		return Position{Offset: offset, Character: utf8.RuneCountInString(source[:offset]) + 1}
	}
	if statement.Pos != position("EXPLAIN") {
		t.Fatal(statement.Pos)
	}
	wantNames := []string{"analyze", "verbose", "costs", "costs", "timing", "memory", "format", "serialize", "settings", "buffers", "format"}
	var names []string
	for _, option := range statement.Options {
		names = append(names, option.Name)
	}
	if !reflect.DeepEqual(names, wantNames) {
		t.Fatal(names)
	}
	options := statement.Options
	if options[0].Pos != position("ANALYSE") || options[0].Value.(LiteralExpr).Value != (Value{Kind: BooleanValue, Pos: position("false")}) || options[1].Value != nil {
		t.Fatal(options[:2])
	}
	for _, tc := range []struct {
		index   int
		kind    ValueKind
		integer int64
		text    string
		token   string
	}{
		{2, IntegerValue, 0, "", "0"},
		{3, IntegerValue, 1, "", "+1"},
		{4, IntegerValue, -1, "", "-1"},
		{5, NumericValue, 0, "1.5", "1.5"},
		{6, StringValue, 0, "text", "'text'"},
	} {
		value, ok := options[tc.index].Value.(LiteralExpr)
		if !ok || value.Value.Kind != tc.kind || value.Value.Integer != tc.integer || value.Value.Text != tc.text || value.SourcePosition() != position(tc.token) {
			t.Fatalf("option %d: %#v", tc.index, options[tc.index])
		}
	}
	for index, want := range map[int]string{7: "binary", 8: "on", 9: "off", 10: "Text"} {
		value, ok := options[index].Value.(ColumnExpr)
		if !ok || len(value.Name.Parts) != 1 || value.Name.Parts[0].Text != want {
			t.Fatalf("option %d: %#v", index, options[index])
		}
	}
	query := statement.Statement.(*Select)
	if query.Targets[0].(ParameterExpr).Number != 1 || query.Targets[0].SourcePosition() != position("$1") {
		t.Fatal(query.Targets)
	}
}

func TestExplainParserChildren(t *testing.T) {
	for _, body := range []string{
		"SELECT 1", "(SELECT 1)", "VALUES (1),(2)",
		"((SELECT 1))", "(VALUES (1),(2))",
		"INSERT INTO t VALUES (1)", "UPDATE t SET id=1", "DELETE FROM t",
		"MERGE INTO t USING s ON t.id=s.id WHEN MATCHED THEN DO NOTHING",
		"WITH w AS (INSERT INTO t VALUES (1) RETURNING id) SELECT * FROM w",
		"WITH w AS (SELECT 1 AS id) DELETE FROM t WHERE id IN (SELECT id FROM w)",
		"EXECUTE p(1)", "DECLARE c CURSOR FOR SELECT 1",
		"CREATE TABLE copied AS SELECT 1", "CREATE MATERIALIZED VIEW copied AS SELECT 1",
		"SELECT 1 INTO copied",
	} {
		t.Run(body, func(t *testing.T) {
			statement := parseExplain(t, "EXPLAIN "+body)
			if statement.Statement == nil || ModifyingStatement(statement) || HasModifyingCTE(statement) || len(StatementWith(statement)) != 0 {
				t.Fatal(statement)
			}
		})
	}
	for _, source := range []string{
		"EXPLAIN (COSTS OFF) (SELECT 1)",
		"EXPLAIN VERBOSE (SELECT 1)",
		"EXPLAIN ANALYZE VERBOSE ((SELECT 1))",
	} {
		statement := parseExplain(t, source)
		if _, ok := statement.Statement.(*Select); !ok {
			t.Fatalf("%q: %T", source, statement.Statement)
		}
	}
	for _, legacy := range []string{"ANALYZE", "ANALYSE", "VERBOSE", "ANALYZE VERBOSE", "ANALYSE VERBOSE"} {
		statement := parseExplain(t, "EXPLAIN "+legacy+" SELECT 1")
		if len(statement.Options) != len(strings.Fields(legacy)) {
			t.Fatal(statement.Options)
		}
		for _, option := range statement.Options {
			if option.Value != nil || option.Pos.Character < 9 || (option.Name != "analyze" && option.Name != "verbose") {
				t.Fatal(option)
			}
		}
	}
	for _, source := range []string{"SELECT explain FROM explain", "SELECT explain(1) AS explain", "CREATE TABLE explain(explain explain)", "SELECT explain 'value'"} {
		if _, err := Parse(source); err != nil {
			t.Fatalf("unreserved identifier %q: %v", source, err)
		}
	}
}

func TestExplainParserDiagnostics(t *testing.T) {
	for _, tc := range []struct{ source, token string }{
		{"EXPLAIN", ""},
		{"EXPLAIN () SELECT 1", ")"},
		{"EXPLAIN (costs,) SELECT 1", ")"},
		{"EXPLAIN (format = text) SELECT 1", "="},
		{"EXPLAIN (costs NULL) SELECT 1", "NULL"},
		{"EXPLAIN (costs $1) SELECT 1", "$1"},
		{"EXPLAIN SELECT 1 trailing junk", "junk"},
		{"EXPLAIN SELECT * FROM t WHERE", ""},
		{"EXPLAIN DROP TABLE t", "DROP"},
		{"EXPLAIN EXPLAIN SELECT 1", "EXPLAIN"},
		{"EXPLAIN CREATE TABLE explain_bad(id integer)", "integer"},
		{"EXPLAIN VERBOSE ANALYZE SELECT 1", "ANALYZE"},
		{"PREPARE p AS EXPLAIN SELECT 1", "EXPLAIN"},
	} {
		t.Run(tc.source, func(t *testing.T) {
			statements, err := Parse(tc.source)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || statements != nil {
				t.Fatalf("%#v, %v", statements, err)
			}
			offset := len(tc.source)
			message := "syntax error at end of input"
			if tc.token != "" {
				offset = strings.LastIndex(tc.source, tc.token)
				message = "syntax error at or near \"" + tc.token + "\""
			}
			if diagnostic.Message != message || diagnostic.Position != strconv.Itoa(utf8.RuneCountInString(tc.source[:offset])+1) {
				t.Fatalf("%#v; want %q at byte %d", diagnostic, message, offset)
			}
		})
	}
}

func TestExplainParserTraversal(t *testing.T) {
	statement := parseExplain(t, "EXPLAIN WITH w AS (INSERT INTO target VALUES ($1) RETURNING id) SELECT (SELECT id FROM nested_src) FROM outer_src, w WHERE outer_src.id=$2")
	var tables []string
	if err := VisitQuerySources(statement, nil, func(table *TableReference, _ *Select) error {
		tables = append(tables, table.Name.Parts[0].Text)
		return nil
	}); err != nil || !reflect.DeepEqual(tables, []string{"outer_src", "nested_src"}) {
		t.Fatal(tables, err)
	}
	var parameters []int
	if err := VisitQueryExpressions(statement, func(expression Expr) error {
		if parameter, ok := expression.(ParameterExpr); ok {
			parameters = append(parameters, parameter.Number)
		}
		return nil
	}); err != nil || !reflect.DeepEqual(parameters, []int{1, 2}) {
		t.Fatal(parameters, err)
	}
	var targets []string
	if err := VisitMutationTargets(statement, func(statement Statement) error {
		targets = append(targets, statement.(*Insert).Name.Parts[0].Text)
		return nil
	}); err != nil || !reflect.DeepEqual(targets, []string{"target"}) {
		t.Fatal(targets, err)
	}
	stop := errors.New("stop")
	if err := VisitQuerySources(statement, nil, func(*TableReference, *Select) error { return stop }); err != stop {
		t.Fatal(err)
	}
	recursive := parseExplain(t, "EXPLAIN WITH RECURSIVE w(id) AS (VALUES (1) UNION ALL SELECT id+1 FROM w WHERE id<2) SELECT * FROM w")
	analysis, err := AnalyzeRecursiveCTEs(recursive)
	if err != nil || !reflect.DeepEqual(analysis.Order, []int{0}) || !reflect.DeepEqual(analysis.Recursive, []bool{true}) {
		t.Fatal(analysis, err)
	}
}
