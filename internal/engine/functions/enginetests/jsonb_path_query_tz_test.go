package enginetests_test

import (
	"context"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func jsonpathTZUnitOrdinary(node expr.Expr) expr.Expr {
	switch e := node.(type) {
	case functions.JSONBPathExists:
		e.Timezone = false
		return e
	case functions.JSONBPathMatch:
		e.Timezone = false
		return e
	case functions.JSONBPathQuery:
		e.Timezone = false
		return e
	case functions.JSONBPathQueryFirst:
		e.Timezone = false
		return e
	case functions.JSONBPathQueryArray:
		e.Timezone = false
		return e
	default:
		panic("unexpected timezone carrier")
	}
}

func TestJSONBPathQueryTZIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		oid    ids.OID
		result sqltype.ID
		set    bool
	}{
		{"jsonb_path_exists_tz", 1177, sqltype.Bool, false},
		{"jsonb_path_match_tz", 2030, sqltype.Bool, false},
		{"jsonb_path_query_tz", 1179, sqltype.JSONB, true},
		{"jsonb_path_query_array_tz", 1180, sqltype.JSONB, false},
		{"jsonb_path_query_first_tz", 2023, sqltype.JSONB, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			eng := engine.New()
			b := enginetest.ExpressionBinder(t, ctx, eng, sqltype.InputEnvironment{}, nil)
			b.Clause.AggregateClause = "SELECT"
			literal := func(v string) parser.Expr {
				return parser.LiteralExpr{Value: parser.Value{Kind: parser.StringValue, Text: v}}
			}
			call, err := b.BindFunctionProvider(parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}}, Args: []parser.Expr{literal(`true`), literal(`$`)}})
			if err != nil {
				t.Fatal(err)
			}
			if call.Properties().Volatility != expr.Stable || exec.PlannedVolatility(call) != expr.Stable || stored.PlannedIntrinsicVolatility(call) != expr.Stable || call.ResultUsage().Type.ID() != tc.result || exec.SourceReturnsSet(call) != tc.set {
				t.Fatal("stable typed provider", call)
			}
			captured, err := exec.ExpressionRoutineDescriptor(call)
			if err != nil || captured == nil || captured.Builtin == nil {
				t.Fatal(captured, err)
			}
			identity := catalog.RoutineIdentity{tc.oid, ids.PGCatalogNamespaceOID, tc.name, "3802,4072,3802,16"}
			d := captured.Builtin
			if d.Identity != identity || d.Kind != catalog.FunctionRoutine || d.Result != ids.OID(tc.result) || !d.Strict || d.Volatility != expr.Stable || d.Parallel != 's' || d.ReturnsSet != tc.set || d.Defaults != 2 || d.Variadic != 0 || d.Providers != functions.JSONRoutineProvider {
				t.Fatal("released declaration", d)
			}
			if eng.CatalogRead().Get(captured.Address()) != captured || captured.Revision() == 0 {
				t.Fatal("unowned routine declaration")
			}
			wantRows := float32(0)
			if tc.set {
				wantRows = 1000
			}
			if captured.RowEstimate() != wantRows {
				t.Fatal("released row estimate", captured.RowEstimate())
			}
			composite := call.(expr.Composite)
			children := composite.Children()
			if len(children) != 4 {
				t.Fatal("missing genuine formals")
			}
			for i, id := range []sqltype.ID{sqltype.JSONB, sqltype.JSONPath, sqltype.JSONB, sqltype.Bool} {
				if children[i].ResultUsage().Type.ID() != id {
					t.Fatal("formal", i, children[i])
				}
			}
			c := &exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}}
			for slot, want := range map[int]string{2: "{}", 3: "f"} {
				v, err := children[slot].Eval(c)
				if err != nil {
					t.Fatal(err)
				}
				text, err := v.Type().Output(ctx, v)
				if err != nil || text != want {
					t.Fatal("default", slot, text, err)
				}
			}
			ordinary := jsonpathTZUnitOrdinary(call)
			if stored.PlannedIntrinsicVolatility(ordinary) != expr.Immutable || call.(expr.GroupIdentity).SameGroup(ordinary, analyze.SameGroupExpression, false) {
				t.Fatal("timezone policy lost from semantic identity")
			}
			if tc.set && call.(functions.JSONBPathQuery).SameSourceOccurrence(ordinary) {
				t.Fatal("source policy conflated")
			}
			rebuild, err := composite.Rebuild(expr.InfoOf(call), children)
			if err != nil {
				t.Fatal(err)
			}
			substituted, err := expr.Substitute(composite, expr.InfoOf(call), children)
			if err != nil {
				t.Fatal(err)
			}
			remapped, err := stored.RemapExpression(call, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, next := range []expr.Expr{rebuild, substituted, remapped} {
				if !analyze.SameGroupExpression(call, next) || stored.PlannedIntrinsicVolatility(next) != expr.Stable {
					t.Fatal("reconstruction changed identity", next)
				}
				spec, err := next.(expr.Renderable).Render()
				if err != nil || spec.Form != expr.FormCall || spec.Name != tc.name {
					t.Fatal("exact provider rendering", spec, err)
				}
				retained, err := exec.ExpressionRoutineDescriptor(next)
				if err != nil || retained != captured {
					t.Fatal("reconstruction changed captured authority", retained, err)
				}
				if tc.set && !analyze.SameQuerySetOccurrence(call, next) {
					t.Fatal("source occurrence changed")
				}
			}
			fresh := jsonObjectUnitTypes()
			retagged, changed, err := stored.RetagTypes(eng.CatalogRead(), call, func(d *sqltype.Descriptor) *sqltype.Descriptor { return fresh[d.ID()] })
			if err != nil || !changed {
				t.Fatal("remint", changed, err)
			}
			if retagged.ResultUsage().Type != fresh[tc.result] || stored.PlannedIntrinsicVolatility(retagged) != expr.Stable {
				t.Fatal("retag lost result or policy")
			}
			spec, err := retagged.(expr.Renderable).Render()
			if err != nil || spec.Name != tc.name {
				t.Fatal(spec, err)
			}
			if tc.set {
				cursor, err := exec.OpenFunctionSource(c, retagged)
				if err != nil {
					t.Fatal(err)
				}
				defer cursor.Close()
				v, present, err := cursor.Next(ctx)
				if err != nil || !present || v.Type() != fresh[sqltype.JSONB] {
					t.Fatal("reminted set plan authority", v, present, err)
				}
			} else {
				v, err := retagged.Eval(c)
				if err != nil || v.Type() != fresh[tc.result] {
					t.Fatal("reminted scalar plan authority", v, err)
				}
			}
		})
	}
}
