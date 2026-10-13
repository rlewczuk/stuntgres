package engine

import (
	"context"
	"reflect"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestArrayCompareBinding(t *testing.T) {
	e := New()
	for _, tc := range []struct {
		q     string
		types []sqltype.ID
		batch bool
	}{
		{"SELECT $1 IN(1,2147483648)", []sqltype.ID{20}, true},
		{"SELECT $1 IN($2)", []sqltype.ID{25, 25}, false},
		{"SELECT $1 IN($1,1)", []sqltype.ID{23}, true},
		{"SELECT $1 IN($1)", []sqltype.ID{25}, false},
		{"SELECT $1=ANY($2)", []sqltype.ID{25, 1009}, true},
		{"SELECT $1=ANY(ARRAY[1,2])", []sqltype.ID{23}, true},
		{"SELECT 1=ANY($1)", []sqltype.ID{1007}, true},
	} {
		t.Run(tc.q, func(t *testing.T) {
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			raw := statement(t, tc.q).(*parser.Select).Targets[0]
			node, err := b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			_, batch := node.(scalarArrayComparisonExpression)
			if batch != tc.batch {
				t.Fatalf("lowering=%T batch=%t", node, tc.batch)
			}
			if node.ResultUsage().Type.ID() != 16 || node.ResultUsage().Typmod != -1 || node.ResultUsage().Collation.ID != 0 || node.Properties().RowDependent || !node.Properties().ParameterDependent || node.Properties().StageDependent || node.Properties().Volatility != Immutable {
				t.Fatal(node.ResultUsage(), node.Properties())
			}
			manifest, err := b.finish()
			if err != nil || manifest.Count != len(tc.types) {
				t.Fatal(manifest, err)
			}
			for i, typ := range tc.types {
				if manifest.Types[i].Type.ID() != typ {
					t.Fatal(manifest.Types, tc.types)
				}
			}
		})
	}
	for _, tc := range []struct{ q, code string }{
		{"SELECT 1 IN(1,TRUE)", "42883"}, {"SELECT 1 NOT IN(1,TRUE)", "42883"},
		{"SELECT 1=ANY(1)", "42809"}, {"SELECT NULL::INT=ANY(ARRAY[TRUE])", "42883"},
		{"SELECT $1,$1 IN(1,2147483648)", "42P08"},
		{"SELECT $1::INT,$1=ANY($1)", "42809"},
	} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, tc.q).(*parser.Select)
		e.mu.Lock()
		auth, err := e.authorizationLocked(b.op)
		b.queryAuth = &auth
		if err == nil {
			_, err = b.bindSelect(raw, false)
		}
		e.mu.Unlock()
		state(t, err, tc.code)
	}
	run(t, e, "CREATE TABLE ac_unit(i INT,j BIGINT)")
	for _, tc := range []struct{ q, code string }{
		{"SELECT '2147483648' IN(i,j) FROM ac_unit", "22003"},
		{"SELECT '2147483648' IN(1,2147483648,i,j) FROM ac_unit", "22003"},
		{"SELECT $1 IN(i,j) FROM ac_unit", "42P08"},
		{"SELECT $1 IN(1,2147483648,i),$1 FROM ac_unit", ""},
		{"SELECT $1 IN($1,2147483648,i),$1 FROM ac_unit", ""},
		// The existing query-argument walker must count only our frame's Vars.
		{"SELECT '2147483648' IN((SELECT ac_unit.i),2147483648) FROM ac_unit", "22003"},
		{"SELECT '2147483648' IN((SELECT i FROM ac_unit LIMIT 1),2147483648) FROM ac_unit", ""},
	} {
		b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		raw := statement(t, tc.q).(*parser.Select)
		e.mu.Lock()
		auth, err := e.authorizationLocked(b.op)
		b.queryAuth = &auth
		if err == nil {
			_, err = b.bindSelect(raw, false)
		}
		e.mu.Unlock()
		if tc.code == "" {
			if err != nil {
				t.Fatal(tc.q, err)
			}
		} else {
			state(t, err, tc.code)
		}
	}
}

func TestArrayCompareCommonType(t *testing.T) {
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	integer := func(id sqltype.ID) comparisonOperand {
		return comparisonOperand{node: parameterExpression{expressionInfo: b.info(b.typ(id).Usage(), parser.Position{}, ExprProperties{}), number: 1}}
	}
	unknown := comparisonOperand{node: parameterExpression{number: 1}}
	common, err := commonExpressionUsage([]comparisonOperand{unknown, integer(sqltype.Int4), integer(sqltype.Int8)}, b.typ(sqltype.Text).Usage(), "")
	if err != nil || common.Type.ID() != 20 || len(b.parameters) != 0 {
		t.Fatal(common, err, b.parameters)
	}
	common, err = commonExpressionUsage([]comparisonOperand{integer(sqltype.Int4), integer(sqltype.Bool)}, b.typ(sqltype.Text).Usage(), "")
	if err != nil || common.Type != nil {
		t.Fatal("category mismatch must silently decline batching", common, err)
	}
	_, err = commonExpressionUsage([]comparisonOperand{integer(sqltype.Int4), integer(sqltype.Bool)}, b.typ(sqltype.Text).Usage(), "ARRAY")
	state(t, err, "42804")
	// Lexical frame identity, not dependence flags, distinguishes Vars.
	local := columnExpression{expressionInfo: b.info(b.typ(sqltype.Int4).Usage(), parser.Position{}, ExprProperties{RowDependent: true}), frame: b.frameID()}
	other := local
	other.frame++
	if !b.inListHasVar(local) || b.inListHasVar(other) || b.inListHasVar(parameterExpression{expressionInfo: expressionInfo{properties: ExprProperties{RowDependent: true}}}) {
		t.Fatal("IN Var classification lost lexical identity")
	}
	b.noParameters = true
	raw := statement(t, "SELECT '1' IN(1,2,3)").(*parser.Select).Targets[0]
	node, err := b.bind(raw, expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	template := b.inListSyntax[inListSyntaxKey{frame: b.frameID(), position: raw.SourcePosition()}]
	array, ok := template.(parser.ArrayComparisonExpr)
	if !ok || len(array.Right.(parser.ArrayExpr).Elements) != 3 || !reflect.DeepEqual(array.Left, raw.(parser.InListExpr).Left) {
		t.Fatal("missing exact frozen lowering", template, node)
	}
}
