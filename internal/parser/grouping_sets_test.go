package parser

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
	"unicode/utf8"
)

func TestGroupingSetsStructure(t *testing.T) {
	q := "/*é*/ SELECT GROUPING(a,b) FROM t GROUP BY DISTINCT ROLLUP(a,(b,v)),GROUPING SETS((),(a))"
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	s := statements[0].(*Select)
	operation, ok := s.Targets[0].(GroupingExpr)
	position := func(token string) int { return utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1 }
	if !ok || len(operation.Args) != 2 || operation.Pos.Character != position("GROUPING") {
		t.Fatal(operation)
	}
	g := s.GroupBy
	if g == nil || !g.Distinct || g.All || g.Pos.Character != position("GROUP BY") || len(g.Items) != 2 {
		t.Fatal(g)
	}
	rollup, sets := g.Items[0], g.Items[1]
	if rollup.Kind != GroupRollup || len(rollup.Items) != 2 || rollup.Items[1].Kind != GroupList || len(rollup.Items[1].Items) != 2 || sets.Kind != GroupSets || sets.Items[0].Kind != GroupEmpty {
		t.Fatal(rollup, sets)
	}
	var names []string
	if err := g.VisitExpressions(func(e Expr) error { names = append(names, e.(ColumnExpr).Name.Parts[0].Text); return nil }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{"a", "b", "v", "a"}) {
		t.Fatal(names)
	}
	for _, tc := range []struct {
		q      string
		active bool
		all    bool
	}{
		{"SELECT 1", false, false}, {"SELECT 1 GROUP BY ()", true, false}, {"SELECT 1 GROUP BY ALL ()", true, true},
	} {
		ss, err := Parse(tc.q)
		if err != nil {
			t.Fatal(tc.q, err)
		}
		clause := ss[0].(*Select).GroupBy
		if (clause != nil) != tc.active || clause != nil && clause.All != tc.all {
			t.Fatal(tc.q, clause)
		}
	}
}

func TestGroupingSetsNamesAndWholeInput(t *testing.T) {
	for _, q := range []string{
		"SELECT cube,rollup,grouping,sets FROM t GROUP BY cube,rollup,grouping,sets",
		"SELECT GROUPING(a) FROM t GROUP BY ALL CUBE(a)",
		"SELECT a FROM t GROUP BY (a,b)",
		"SELECT a FROM t GROUP BY GROUPING SETS(GROUPING SETS((a),()),())",
	} {
		if ss, err := Parse(q); err != nil || len(ss) != 1 {
			t.Fatal(q, ss, err)
		}
	}
	for _, suffix := range []string{"SELECT GROUPING()", "SELECT 1 GROUP BY ROLLUP()", "SELECT 1 GROUP BY CUBE()", "SELECT 1 GROUP BY GROUPING SETS()", "SELECT 1 GROUP BY ALL", "SELECT 1 GROUP BY ROLLUP(a),"} {
		ss, err := Parse("INSERT INTO t VALUES(1);" + suffix)
		var diagnostic *sqlstate.Error
		if len(ss) != 0 || !errors.As(err, &diagnostic) || diagnostic.Code != "42601" {
			t.Fatal(suffix, ss, err)
		}
	}
}

func TestGroupingSetsSourceTraversal(t *testing.T) {
	ss, err := Parse("SELECT GROUPING((SELECT z FROM u)) FROM t GROUP BY ROLLUP((SELECT z FROM v))")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	err = VisitQuerySources(ss[0].(*Select), nil, func(table *TableReference, _ *Select) error {
		names = append(names, table.Name.Parts[0].Text)
		return nil
	})
	if err != nil || !reflect.DeepEqual(names, []string{"t", "u", "v"}) {
		t.Fatal(names, err)
	}
}

func TestGroupingSetsDoNotReplaceOtherExpressionLists(t *testing.T) {
	ss, err := Parse("SELECT count(*) OVER(PARTITION BY a,b) FROM t GROUP BY ROLLUP(a,b)")
	if err != nil || len(ss) != 1 {
		t.Fatal(ss, err)
	}
	s := ss[0].(*Select)
	call, ok := s.Targets[0].(AggregateExpr)
	if !ok || call.Window == nil || s.Distinct != nil {
		t.Fatal(s.Targets, s.Distinct)
	}
	list := call.Window.Partition
	if len(list) != 2 {
		t.Fatal(list)
	}
	for i, name := range []string{"a", "b"} {
		column, ok := list[i].(ColumnExpr)
		if !ok || column.Name.Parts[0].Text != name {
			t.Fatal(list)
		}
	}
	if s.GroupBy == nil || len(s.GroupBy.Items) != 1 || s.GroupBy.Items[0].Kind != GroupRollup {
		t.Fatal(s.GroupBy)
	}
	statements, err := Parse("SELECT DISTINCT ON (a,b) a FROM t")
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	on := statements[0].(*Select).Distinct
	if on == nil || len(on.On) != 2 || on.On[0].SourcePosition().Character != 21 || on.On[1].SourcePosition().Character != 23 {
		t.Fatal(on)
	}
}

func TestGroupingSetsOperationRejectsAggregateDecorations(t *testing.T) {
	for _, suffix := range []string{"FILTER(WHERE TRUE)", "OVER ()"} {
		q := "/*é*/ SELECT GROUPING(a) " + suffix + " FROM t GROUP BY a"
		statements, err := Parse(q)
		var got *sqlstate.Error
		token := strings.SplitN(suffix, "(", 2)[0]
		token = strings.TrimSpace(token)
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at or near \"" + token + "\"", Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1)}
		if len(statements) != 0 || !errors.As(err, &got) || *got != want {
			t.Fatal(q, statements, got, err, want)
		}
	}
}

func TestGroupingSetsProducerExpressionUnits(t *testing.T) {
	q := "/*é界*/ SELECT a FROM t GROUP BY GROUPING SETS(ROLLUP(a,(b,v)),CUBE((a,b),v),GROUPING SETS((),(a)))"
	statements, err := Parse(q)
	if err != nil || len(statements) != 1 {
		t.Fatal(statements, err)
	}
	clause := statements[0].(*Select).GroupBy
	if clause == nil || len(clause.Items) != 1 {
		t.Fatal(clause)
	}
	sets := clause.Items[0]
	if sets.Kind != GroupSets || len(sets.Items) != 3 {
		t.Fatal(sets)
	}
	rollup, cube, nested := sets.Items[0], sets.Items[1], sets.Items[2]
	position := func(token string) int { return utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1 }
	if rollup.Kind != GroupRollup || len(rollup.Items) != 2 || rollup.Pos.Character != position("ROLLUP") || rollup.Items[0].Kind != GroupExpression || rollup.Items[1].Kind != GroupList || len(rollup.Items[1].Items) != 2 || rollup.Items[1].Pos.Character != position("(b,v)") {
		t.Fatal(rollup)
	}
	if cube.Kind != GroupCube || len(cube.Items) != 2 || cube.Pos.Character != position("CUBE") || cube.Items[0].Kind != GroupList || len(cube.Items[0].Items) != 2 || cube.Items[1].Kind != GroupExpression {
		t.Fatal(cube)
	}
	if nested.Kind != GroupSets || len(nested.Items) != 2 || nested.Items[0].Kind != GroupEmpty || nested.Items[1].Kind != GroupExpression {
		t.Fatal(nested)
	}
	var names []string
	if err := clause.VisitExpressions(func(expr Expr) error { names = append(names, expr.(ColumnExpr).Name.Parts[0].Text); return nil }); err != nil || !reflect.DeepEqual(names, []string{"a", "b", "v", "a", "b", "v", "a"}) {
		t.Fatal(names, err)
	}
	for _, q := range []string{"SELECT cube FROM t GROUP BY ROLLUP(cube,rollup)", "SELECT rollup FROM t GROUP BY CUBE((cube,rollup),sets)"} {
		if statements, err := Parse(q); err != nil || len(statements) != 1 {
			t.Fatal(q, statements, err)
		}
	}
}

func TestGroupingSetsProducerChildWholeInputDiagnostics(t *testing.T) {
	for _, tc := range []struct{ clause, token, messageToken string }{
		{"ROLLUP(())", "))", ")"}, {"CUBE(())", "))", ")"},
		{"ROLLUP(a,())", "))", ")"}, {"CUBE(a,())", "))", ")"},
		{"ROLLUP(GROUPING SETS((a),()))", "SETS", "SETS"}, {"CUBE(GROUPING SETS((a),()))", "SETS", "SETS"},
	} {
		for _, prefix := range []string{"/*é界*/ ", "INSERT INTO t VALUES(1); /*é界*/ "} {
			q := prefix + "SELECT count(*) FROM t GROUP BY " + tc.clause
			statements, err := Parse(q)
			var got *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42601", Message: "syntax error at or near \"" + tc.messageToken + "\"", Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, tc.token)]) + 1)}
			if len(statements) != 0 || !errors.As(err, &got) || *got != want {
				t.Fatal(q, statements, got, err, want)
			}
		}
	}
}

func TestGroupingSetsProducerNestedOrdinaryCallBoundary(t *testing.T) {
	for _, tc := range []struct{ clause, callee string }{
		{"ROLLUP(cube(a))", "cube(a)"}, {"CUBE(rollup(a))", "rollup(a)"},
	} {
		q := "/*é界*/ SELECT count(*) FROM t GROUP BY " + tc.clause
		statements, err := Parse(q)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "functions are unsupported", Position: strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, tc.callee)]) + 1)}
		if len(statements) != 0 || !errors.As(err, &got) || *got != want {
			t.Fatal(q, statements, got, err, want)
		}
	}
}
