package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRegexpScalarBinding(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	literal := func(s string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
	}
	for _, tc := range []struct {
		name string
		args []parser.Expr
		typ  sqltype.ID
	}{
		{"regexp_count", []parser.Expr{literal("aba"), literal("a")}, sqltype.Int4},
		{"regexp_instr", []parser.Expr{literal("é🙂x"), literal("🙂")}, sqltype.Int4},
		{"regexp_like", []parser.Expr{literal("a"), literal("a")}, sqltype.Bool},
		{"regexp_match", []parser.Expr{literal("a"), literal("(a)")}, 1009},
		{"regexp_replace", []parser.Expr{literal("aba"), literal("a"), literal("X")}, sqltype.Text},
		{"regexp_split_to_array", []parser.Expr{literal("a b"), literal(" ")}, 1009},
	} {
		raw := nullCountRaw(tc.name, tc.args...)
		bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(tc.name, err)
		}
		node, ok := bound.(functions.Regexp)
		if !ok || node.ResultUsage().Type.ID() != tc.typ || node.Plan.ReturnsSet() {
			t.Fatal(tc.name, bound)
		}
		value, err := bound.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}})
		if err != nil || value.Type() != node.ResultUsage().Type {
			t.Fatal(tc.name, value, err)
		}
	}
	raw := nullCountRaw("regexp_like", literal("a"), literal("a"))
	raw.ArgNames = []parser.Identifier{{Text: "pattern"}, {Text: "string"}}
	first, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	raw.ArgNames = nil
	second, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil || !sameGroupExpression(first, second) {
		t.Fatal(first, second, err)
	}
	collate := func(name string, pos int) parser.Expr {
		return parser.CollateExpr{Child: literal("a"), Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Pos: parser.Position{Character: pos}}
	}
	raw = nullCountRaw("regexp_like", collate("C", 19), literal("a"))
	bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil || bound.(functions.Regexp).Plan.Collation().ID != 950 {
		t.Fatal(bound, err)
	}
	raw = nullCountRaw("regexp_like", collate("C", 19), collate("POSIX", 34))
	_, err = b.bind(raw, expressionPolicy{context: projectionContext})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42P21" {
		t.Fatal(err)
	}
}

func TestRegexpScalarPreflight(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := nullCountRaw("regexp_like", parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "a"}}, parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: "["}})
	bound, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal("Prepare compiled pattern", err)
	}
	_, _, err = preflightCoercionsWithPolicy(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, bound, true)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "2201B" || diagnostic.Position != "" {
		t.Fatal(err)
	}
	node := bound.(functions.Regexp)
	node.Args[1] = plan.ColumnExpr{Info: expr.Info{Usage: node.Args[1].ResultUsage(), Property: expr.Properties{Volatility: expr.Immutable, RowDependent: true}}, Slot: 0}
	node.Property.RowDependent = true
	_, _, err = preflightCoercionsWithPolicy(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}, node, true)
	if err != nil {
		t.Fatal("row-dependent pattern preflight", err)
	}
}

func TestRegexpScalarGroupingAlias(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.query.namespace = &queryNamespace{}
	literal := func(s string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
	}
	first, err := b.bind(nullCountRaw("regexp_split_to_table", literal("a b"), literal(" ")), expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	raw := &parser.GroupClause{Items: []parser.GroupItem{{Kind: parser.GroupExpression, Expr: parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "x"}}}}}}}
	keys, families, sets, err := b.bindGroupClause(raw, []expr.Expr{first}, []string{"x"})
	if err != nil || len(keys) != 1 || len(families) != 1 || !reflect.DeepEqual(sets, [][]int{{0}}) {
		t.Fatal(keys, families, sets, err)
	}
	if _, ok := keys[0].(functions.RegexpSet); !ok {
		t.Fatal("alias lost the set expression", keys[0])
	}
	second, err := b.bind(nullCountRaw("regexp_split_to_table", literal("c d"), literal(" ")), expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = b.bindGroupClause(raw, []expr.Expr{first, second}, []string{"x", "x"})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42702" || diagnostic.Message != `GROUP BY "x" is ambiguous` {
		t.Fatal("ambiguous output alias", err)
	}
}

func TestRegexpScalarOuterFromFrame(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	raw := statement(t, `SELECT v.id,(SELECT (SELECT count(*) FROM regexp_split_to_table(v.s,' ') r(s))) AS n FROM (VALUES(1,'a b'),(2,'c')) v(id,s)`).(*parser.Select)
	e.mu.Lock()
	auth, err := e.authorizationLocked(binderOperation(b))
	b.query.auth = &auth
	var query *plan.Query
	if err == nil {
		query, err = b.bindSelect(raw, false)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	seen := make(map[plan.Node]bool)
	from, captured := 0, 0
	var inspect func(plan.Node)
	inspect = func(node plan.Node) {
		if node == nil || seen[node] {
			return
		}
		seen[node] = true
		if n, ok := node.(*plan.FunctionFromNode); ok {
			from++
			if n.Frame <= 1 {
				t.Fatal("function lost its inner lexical frame", n.Frame)
			}
			for _, call := range n.Calls {
				for _, arg := range expr.Expr(call).(expr.Composite).Children() {
					plan.WalkExpression(arg, func(child expr.Expr) {
						if column, ok := child.(plan.ColumnExpr); ok {
							captured++
							if column.Frame != 1 {
								t.Fatal("outer reference rebound to inner frame", column.Frame)
							}
						}
					})
				}
			}
		}
		plan.QueryExpressions(node, func(bound expr.Expr) {
			plan.WalkExpression(bound, func(child expr.Expr) {
				if subquery, ok := child.(plan.SubqueryExpr); ok {
					inspect(subquery.QueryPlan.Root)
				}
			})
		})
		left, right := plan.QueryInputs(node)
		inspect(left)
		inspect(right)
	}
	inspect(query.Root)
	if from != 1 || captured != 1 {
		t.Fatal("missing correlated function argument", from, captured)
	}
}

func TestRegexpScalarFilterPlacement(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.clause.aggregateClause = "SELECT"
	b.grouping.argumentOwner = argumentWindowOwner{active: true}
	literal := func(s string) parser.Expr {
		return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: s}}
	}
	set := nullCountRaw("regexp_split_to_table", literal("a b"), literal(" "))
	filter := nullCountRaw("regexp_like", set, literal("a"))
	_, err := b.bindAggregateFilter(parser.AggregateExpr{Name: "count", Filter: filter})
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "set-returning functions are not allowed in FILTER" || diagnostic.Hint != "" {
		t.Fatal("FILTER lost its expression owner to aggregate arguments", err)
	}
	if b.clause.aggregateClause != "SELECT" || b.clause.regexpClause != "" || !b.grouping.argumentOwner.active {
		t.Fatal("FILTER leaked binding context", b.clause.aggregateClause, b.clause.regexpClause, b.grouping.argumentOwner)
	}
}

func TestRegexpScalarViewPrefix(t *testing.T) {
	for _, sql := range []string{
		`SELECT r.x FROM (VALUES('a b')) a(s) CROSS JOIN LATERAL regexp_split_to_table(a.s,' ') r(x)`,
		`SELECT r.x FROM (VALUES('a b')) a(s) CROSS JOIN ((VALUES('x y')) b(t) CROSS JOIN LATERAL regexp_split_to_table(a.s||b.t,' ') r(x))`,
	} {
		t.Run(sql, func(t *testing.T) {
			e := New()
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			b.params.noParameters = true
			raw := statement(t, sql).(*parser.Select)
			e.mu.Lock()
			auth, err := e.authorizationLocked(binderOperation(b))
			b.query.auth = &auth
			var query *plan.Query
			if err == nil {
				query, err = b.bindSelect(raw, false)
			}
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			// Actual analyzed occurrences and lexical frames, with no query
			// execution or captured output used as an expected SQL golden.
			definition := &viewDefinition{
				query: query, syntax: raw, literals: b.capture.Literals,
				castUsages: b.capture.CastUsages, columns: b.capture.Columns,
				stars: b.capture.Stars, namespaces: b.capture.Namespaces,
				collations: b.capture.Collations,
			}
			definition.frames, definition.queries = collectQueryFrames(query.Root)
			text, err := definition.selectSQL(context.Background(), auth.Catalog, viewPresentation{})
			if err != nil || !strings.Contains(text, `"a"."s"`) {
				t.Fatal("lost dependent function argument prefix", text, err)
			}
			if strings.Contains(sql, "b.t") && !strings.Contains(text, `"b"."t"`) {
				t.Fatal("lost nested RHS local prefix", text)
			}
			public, err := definition.selectSQL(context.Background(), auth.Catalog, viewPresentation{deparse: true, pretty: true})
			if err != nil || !strings.Contains(public, "a.s") || strings.Contains(public, `"a"`) || strings.Contains(public, " AS ") {
				t.Fatal("public inquiry fell back to Dump bridge aliases", public, err)
			}
			if strings.Contains(sql, "b.t") && !strings.Contains(public, "b.t") {
				t.Fatal("public inquiry lost nested current source names", public)
			}
		})
	}
}
