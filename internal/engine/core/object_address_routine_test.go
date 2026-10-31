package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestObjectAddressSourceNullOriginalGraph(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, tc := range []struct {
		name, expression  string
		parameter, pruned bool
	}{
		{"literal_scalar", "pg_describe_object(NULL, nextval('strict_null_steps')::oid,0)", false, true},
		{"parameter_scalar", "pg_describe_object($1::oid,nextval('strict_null_steps')::oid,0)", true, true},
		{"nonnull_scalar", "pg_describe_object(1247,23,0)", false, false},
		{"runtime_scalar", "pg_describe_object(1247,23,v)", false, false},
		{"record_identify", "pg_identify_object($1::oid,nextval('strict_null_steps')::oid,0)", true, false},
		{"record_address", "pg_identify_object_as_address($1::oid,nextval('strict_null_steps')::oid,0)", true, false},
		{"record_inverse", "pg_get_object_address($1::text,ARRAY[nextval('strict_null_steps')::text],'{}')", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, evaluation := strictNullUnitBind(t, e, op, scope, "1")
			text := "SELECT * FROM " + tc.expression + " WITH ORDINALITY"
			if tc.name == "runtime_scalar" {
				text = "SELECT f.* FROM strict_null_rows,LATERAL " + tc.expression + " WITH ORDINALITY AS f"
			}
			raw := enginetest.Statement(t, text).(*parser.Select)
			e.Mu.Lock()
			binder := core.NewExpressionBinder(context.Background(), e, scope.Input, op, nil)
			binder.Query.Auth = core.RoutineAuthorityView(&evaluation)
			query, err := binder.BindSelect(raw, false)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal("actual source binding", err)
			}
			var source *plan.FunctionFromNode
			var findSource func(plan.Node)
			findSource = func(node plan.Node) {
				if node == nil {
					return
				}
				if found, ok := node.(*plan.FunctionFromNode); ok {
					source = found
					return
				}
				left, right := plan.QueryInputs(node)
				findSource(left)
				findSource(right)
			}
			findSource(query.Root)
			if source == nil {
				t.Fatal("missing analyzed function source")
			}
			evaluation.RoutineChecks = binder.Dependencies
			if tc.parameter {
				evaluation.Parameters = []sqltype.Value{sqltype.Null(binder.Params.Parameters[1].Type)}
			}
			originalCalls := append([]expr.Expr(nil), source.Calls...)
			originalSchema := append(plan.QuerySchema(nil), source.QuerySchema...)
			originalManifest := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
			planned, err := view.PreflightQuery(&evaluation, source)
			if err != nil {
				t.Fatal("source preflight", err)
			}
			execution := planned.(*plan.FunctionFromNode)
			pruned := execution.ScalarNull != nil && execution.ScalarNull[0]
			if pruned != tc.pruned || source.ScalarNull != nil || !reflect.DeepEqual(source.Calls, originalCalls) || !reflect.DeepEqual(source.QuerySchema, originalSchema) || !reflect.DeepEqual(binder.Dependencies, originalManifest) {
				t.Fatal("preflight lost source immutability or scalar/RECORD distinction")
			}
			if !reflect.DeepEqual(execution.QuerySchema, originalSchema) {
				t.Fatal("NULL execution changed analyzed stage schema")
			}
			// Dependency/render walkers must still see the original callable,
			// even though this execution has no invocation/argument demand.
			seen := 0
			plan.QueryExpressions(execution, func(call expr.Expr) {
				seen++
				if ref, err := exec.ExpressionRoutineDescriptor(call); err != nil || ref == nil {
					t.Fatal("lost original callable", ref, err)
				}
			})
			if seen != len(originalCalls) {
				t.Fatal("execution guard hid original graph")
			}
			probe := evaluation
			probe.RoutineChecks = []catalog.ExpressionDependency{}
			for _, empty := range []bool{true, false} {
				err := view.ValidateMarkedQueryJoin(&probe, execution, evaluation.Catalog, nil, plan.RowMark{}, empty, nil, nil)
				if (err == nil) != (tc.pruned || empty) {
					t.Fatal("execution demand versus original lifetime", empty, tc.pruned, err)
				}
			}
			if tc.pruned {
				// Both admission passes must consult the preflight-derived state.
				// Removing it on a private copy exposes the original missing refs.
				unpruned := *execution
				unpruned.ScalarNull = nil
				if err := view.ValidateMarkedQueryJoin(&probe, &unpruned, evaluation.Catalog, nil, plan.RowMark{}, false, nil, nil); err == nil {
					t.Fatal("nonpruned original graph did not demand its manifest")
				}
				cursor, err := exec.OpenFunctionCall(&probe, execution, 0)
				if err != nil || !cursor.Value.IsNull() || cursor.Type == nil {
					t.Fatal("typed NULL source cursor", cursor, err)
				}
				cursor.Close()
				if tc.parameter {
					value, err := sqltype.OIDValue(binder.Params.Parameters[1].Type, 1247)
					if err != nil {
						t.Fatal(err)
					}
					again := evaluation
					again.Parameters = []sqltype.Value{value}
					replanned, err := view.PreflightQuery(&again, source)
					if err != nil {
						t.Fatal(err)
					}
					fresh := replanned.(*plan.FunctionFromNode)
					if fresh.ScalarNull != nil && fresh.ScalarNull[0] {
						t.Fatal("original source retained a prior NULL frame")
					}
					again.RoutineChecks = []catalog.ExpressionDependency{}
					if err := view.ValidateMarkedQueryJoin(&again, fresh, evaluation.Catalog, nil, plan.RowMark{}, false, nil, nil); err == nil {
						t.Fatal("non-NULL frame borrowed NULL admission")
					}
				}
			}
			if err := view.ValidateMarkedQueryJoin(&evaluation, execution, evaluation.Catalog, nil, plan.RowMark{}, false, nil, nil); err != nil {
				t.Fatal("intact original manifest", err)
			}
			sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "strict_null_steps")
			e.Mu.Lock()
			called := e.Storage.Sequences[sequence.Address].Called
			e.Mu.Unlock()
			if called {
				t.Fatal("planning/admission replayed volatile child")
			}
			ctx, cancel := context.WithCancel(evaluation.Context)
			cancel()
			canceled := evaluation
			canceled.Context = ctx
			if _, err := view.PreflightQuery(&canceled, source); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation before source pruning", err)
			}
			if !reflect.DeepEqual(source.Calls, originalCalls) || !reflect.DeepEqual(source.QuerySchema, originalSchema) || !reflect.DeepEqual(binder.Dependencies, originalManifest) {
				t.Fatal("admission/replanning mutated analyzed source provenance")
			}
		})
	}
}

// Exercise the existing proven-empty footprint on real analyzed plans. Missing
// original manifests distinguish invocation demand from merely returning no rows.
func TestObjectAddressSourceFalseDemand(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	for _, expression := range []string{
		"pg_identify_object(1247,23,0)",
		"pg_identify_object_as_address(1247,23,0)",
		"pg_get_object_address('type','{integer}','{}')",
		"pg_describe_object(1247,23,0)",
		"pg_identify_object(NULL,nextval('strict_null_steps')::oid,0)",
		"pg_identify_object_as_address(NULL,nextval('strict_null_steps')::oid,0)",
		"pg_get_object_address(NULL,ARRAY[nextval('strict_null_steps')::text],'{}')",
	} {
		for _, route := range []struct {
			name, prefix, suffix string
			pruned               bool
		}{
			{"from_false", "SELECT * FROM ", " WHERE FALSE", true},
			{"from_limit0", "SELECT * FROM ", " LIMIT 0", false},
			{"target_false", "SELECT ", " AS v WHERE FALSE", false},
			{"from_runtime_filter", "SELECT * FROM ", " AS f WHERE ordinal > 99", false},
		} {
			t.Run(expression+"/"+route.name, func(t *testing.T) {
				text := route.prefix + expression + route.suffix
				if route.name == "from_runtime_filter" {
					text = "SELECT * FROM " + expression + " WITH ORDINALITY AS f"
					// The last output is runtime ordinality, not a planning proof.
					if expression == "pg_identify_object(1247,23,0)" || expression == "pg_identify_object(NULL,nextval('strict_null_steps')::oid,0)" {
						text += "(kind,namespace,name,identity,ordinal) WHERE ordinal > 99"
					} else if expression == "pg_describe_object(1247,23,0)" {
						text += "(value,ordinal) WHERE ordinal > 99"
					} else {
						text += "(a,b,c,ordinal) WHERE ordinal > 99"
					}
				}
				raw := enginetest.Statement(t, text).(*parser.Select)
				_, _, evaluation := strictNullUnitBind(t, e, op, scope, "1")
				e.Mu.Lock()
				binder := core.NewExpressionBinder(context.Background(), e, scope.Input, op, nil)
				binder.Query.Auth = core.RoutineAuthorityView(&evaluation)
				query, err := binder.BindSelect(raw, false)
				e.Mu.Unlock()
				if err != nil {
					t.Fatal("actual source/target binding", err)
				}
				manifest := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
				evaluation.RoutineChecks = binder.Dependencies
				root, err := view.PreflightQuery(&evaluation, query.Root)
				if err != nil {
					t.Fatal("child preflight before source pruning", err)
				}
				probe := evaluation
				probe.RoutineChecks = []catalog.ExpressionDependency{}
				err = view.ValidateMarkedQueryJoin(&probe, root, evaluation.Catalog, nil, plan.RowMark{}, false, nil, nil)
				if (err == nil) != route.pruned {
					t.Fatal("proven source demand", route.name, err)
				}
				if err := view.ValidateMarkedQueryJoin(&evaluation, root, evaluation.Catalog, nil, plan.RowMark{}, false, nil, nil); err != nil {
					t.Fatal("intact original authority", err)
				}
				if !reflect.DeepEqual(binder.Dependencies, manifest) {
					t.Fatal("pruning erased original manifest")
				}
				sequence := enginetest.CatalogRef(t, e, e.CatalogRead(), "strict_null_steps")
				e.Mu.Lock()
				called := e.Storage.Sequences[sequence.Address].Called
				e.Mu.Unlock()
				if called {
					t.Fatal("source-demand proof evaluated volatile data")
				}
			})
		}
	}
}
