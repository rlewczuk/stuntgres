package enginetests_test

import (
	"context"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"testing"
)

func TestSPGiSTTextPatternProviders(t *testing.T) {
	for _, tc := range []struct {
		sql, name           string
		operator, procedure ids.OID
		want                bool
	}{
		{"'a'::text ~<~ 'aa'::text", "text_pattern_lt", 2314, 2160, true},
		{"'a'::text ~<=~ 'a'::text", "text_pattern_le", 2315, 2161, true},
		{"'é'::text ~>=~ 'b'::text", "text_pattern_ge", 2317, 2163, true},
		{"'a '::text ~>~ 'a'::text", "text_pattern_gt", 2318, 2164, true},
		{"text_pattern_lt('a','A')", "text_pattern_lt", 2314, 2160, false},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			e := engine.New()
			op, finish := enginetest.RoleOperation(t, e, "postgres")
			defer finish()
			input, _ := op.Session.Settings.Presentation()
			raw := enginetest.Statement(t, "SELECT "+tc.sql).(*parser.Select).Targets[0]
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			b := core.NewExpressionBinder(context.Background(), e, input, op, nil)
			b.Query.Auth = &auth
			node, err := b.Bind(raw, analyze.ExpressionPolicy{Context: analyze.ProjectionContext})
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			declaration := routine.BuiltinRoutine(tc.procedure)
			if declaration == nil || declaration.Identity.Name != tc.name || declaration.Providers != functions.OperatorRoutineProvider || !declaration.Strict || declaration.Volatility != expr.Immutable {
				t.Fatal("actual scalar authority", declaration)
			}
			operator, ok := auth.Catalog.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: tc.operator})
			if !ok || operator.Function != tc.procedure || routine.OperatorRoutine(operator.Name(), operator.Left, operator.Right) != declaration {
				t.Fatal("operator routine association", operator, declaration)
			}
			selected, err := exec.ExpressionRoutineDescriptor(node)
			if err != nil || selected == nil || selected.Address().ObjectID != tc.procedure {
				t.Fatal(selected, err)
			}
			eval := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: auth.Catalog}, EvalAdmission: exec.EvalAdmission{RoutineAuthority: &auth, RoutineRecipe: true, RoutineChecks: b.Dependencies}}
			value, err := node.Eval(eval)
			if err != nil {
				t.Fatal(err)
			}
			got, err := value.Boolean()
			if err != nil || got != tc.want {
				t.Fatal(got, err)
			}
		})
	}
	for _, id := range []ids.OID{2174, 2175, 2177, 2178} {
		if routine.BuiltinRoutine(id) != nil {
			t.Fatal("accidental BPCHAR provider", id)
		}
	}
}
