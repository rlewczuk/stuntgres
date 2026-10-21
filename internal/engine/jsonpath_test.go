package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestJSONPathDestinationParameterInference(t *testing.T) {
	ctx := context.Background()
	e := New()
	b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	d := b.Type(sqltype.JSONPath)
	integer := b.Type(sqltype.Int4)
	// INSERT's destination policy constrains reused slots before any input is
	// imported. UPDATE's two independent destination usages retain their IDs.
	for _, tc := range []struct {
		slot   int
		target *sqltype.Descriptor
	}{{1, integer}, {2, d}, {2, d}} {
		n, err := b.Bind(parser.ParameterExpr{Number: tc.slot}, analyze.ExpressionPolicy{Context: analyze.AssignmentContext, Expected: tc.target.Usage(), Mode: sqltype.Assignment, Column: "p"})
		if err != nil || n.ResultUsage().Type != tc.target {
			t.Fatal(n, err)
		}
	}
	info, err := b.Finish()
	if err != nil || info.Count != 2 || info.Types[0].Type.ID() != sqltype.Int4 || info.Types[1].Type.ID() != 4072 {
		t.Fatal(info, err)
	}
	expected := &StatementDescription{Parameters: info}
	held := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, expected)
	n, err := held.Bind(parser.ParameterExpr{Number: 2}, analyze.ExpressionPolicy{Context: analyze.AssignmentContext, Expected: d.Usage(), Mode: sqltype.Assignment, Column: "p"})
	if err != nil || n.ResultUsage().Type.ID() != 4072 {
		t.Fatal("retained input contract", n, err)
	}
	update := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	update.Params.UpdateParameters = true
	for _, tc := range []struct {
		slot   int
		target *sqltype.Descriptor
	}{{1, d}, {2, integer}} {
		if _, err := update.Bind(parser.ParameterExpr{Number: tc.slot}, analyze.ExpressionPolicy{Context: analyze.AssignmentContext, Expected: tc.target.Usage(), Mode: sqltype.Assignment, Column: "p"}); err != nil {
			t.Fatal(err)
		}
	}
	info, err = update.Finish()
	if err != nil || info.Count != 2 || info.Types[0].Type.ID() != 4072 || info.Types[1].Type.ID() != sqltype.Int4 {
		t.Fatal(info, err)
	}
	cast := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
	raw := parser.CastExpr{Child: parser.ParameterExpr{Number: 1}, TypeName: parser.TypeName{Name: "jsonpath"}, Syntax: parser.StandardCast}
	node, err := cast.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
	if err != nil || node.ResultUsage().Type.ID() != 4072 {
		t.Fatal(node, err)
	}
	info, err = cast.Finish()
	if err != nil || info.Count != 1 || info.Types[0].Type.ID() != 4072 {
		t.Fatal(info, err)
	}
}
func TestJSONPathSharedSourceErrorAndPersistenceLiteral(t *testing.T) {
	e := New()
	d, _ := e.CatalogRead().LookupType(sqltype.JSONPath)
	ctx := context.Background()
	_, err := d.Input(ctx, `$?(@ like_regex "(")`, -1)
	original, ok := err.(*sqlstate.Error)
	if !ok {
		t.Fatal(err)
	}
	enriched := analyze.InputErrorAt(err, parser.Position{Character: 14}).(*sqlstate.Error)
	if enriched.Code != "2201B" || enriched.Position != "14" || original.Position != "" || enriched.Context != "" {
		t.Fatal(enriched, original)
	}
	for _, tc := range []struct {
		input, want string
		null        bool
	}{{`"a'b"`, `CAST('"a''b"' AS JSONPATH)`, false}, {"", "CAST(NULL AS JSONPATH)", true}} {
		var v sqltype.Value
		if tc.null {
			v = sqltype.Null(d)
		} else {
			v, err = d.Input(ctx, tc.input, -1)
			if err != nil {
				t.Fatal(err)
			}
		}
		text, err := ConstantLiteral(ctx, plan.LiteralExpr{Info: expr.Info{Usage: d.Usage()}, Value: v})
		if err != nil || text != tc.want {
			t.Fatal(text, err)
		}
	}
}
