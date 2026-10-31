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
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestObjectAddressScalarNullPreflightInfo(t *testing.T) {
	e, op, scope := strictNullUnitFixture(t)
	// Generic strict-scalar policy: a domain-converting sibling cannot veto
	// a NULL proof supplied by a different imported argument. The proof must
	// still refuse to cross the domain conversion itself.
	for _, planner := range []struct {
		name string
		run  func(*exec.Context, expr.Expr, bool) (expr.Expr, bool, error)
	}{{"preflight", exec.PreflightCoercionsWithPolicy}, {"conditional", exec.SimplifyConditional}} {
		t.Run("domain_sibling_"+planner.name, func(t *testing.T) {
			node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "($1::integer)+(NULL::strict_null_checked)::integer")
			evaluation.Parameters = []sqltype.Value{sqltype.Null(binder.Params.Parameters[1].Type)}
			if !node.Properties().RuntimeDomainConversion {
				t.Fatal("fixture lost genuine domain-conversion sibling")
			}
			before := expr.InfoOf(node)
			proofs, barriers := 0, 0
			exec.StrictRoutineArguments(node, func(argument expr.Expr) {
				known, err := exec.ExecutionKnownNullArgument(&evaluation, argument)
				if err != nil {
					t.Fatal(err)
				}
				if known {
					proofs++
				}
				if argument.Properties().RuntimeDomainConversion {
					barriers++
					if known {
						t.Fatal("NULL proof crossed a domain conversion")
					}
				}
			})
			if proofs != 1 || barriers != 1 {
				t.Fatal("independent NULL proof/domain barrier", proofs, barriers)
			}
			folded, changed, err := planner.run(&evaluation, node, false)
			if err != nil || !changed || !exec.RandomLiteralNull(folded) {
				t.Fatal("generic domain-sibling pruning", changed, err)
			}
			value, err := folded.Eval(&evaluation)
			if err != nil || !value.IsNull() || value.Type() != node.ResultUsage().Type {
				t.Fatal("generic typed NULL", value, err)
			}
			if ref, err := exec.ExpressionRoutineDescriptor(folded); err != nil || ref != nil {
				t.Fatal("pruned callable identity", ref, err)
			}
			if folded.Properties() != node.Properties() || !reflect.DeepEqual(before, expr.InfoOf(node)) {
				t.Fatal("generic pruning changed conservative properties/original Info")
			}
		})
	}
	for _, text := range []string{"pg_describe_object(NULL,23,0)", "pg_describe_object($1::oid,23,0)", "pg_describe_object($1::oid,23,(NULL::strict_null_checked)::integer)"} {
		node, binder, evaluation := strictNullUnitBind(t, e, op, scope, text)
		if len(binder.Params.Parameters) != 0 {
			evaluation.Parameters = []sqltype.Value{sqltype.Null(binder.Params.Parameters[1].Type)}
		}
		before := expr.InfoOf(node)
		folded, changed, err := exec.PreflightCoercions(&evaluation, node)
		if err != nil || !changed || !exec.RandomLiteralNull(folded) {
			t.Fatal("scalar NULL folding", changed, err)
		}
		if ref, err := exec.ExpressionRoutineDescriptor(folded); err != nil || ref != nil {
			t.Fatal("folded literal retained executable identity", ref, err)
		}
		if ref, err := exec.ExpressionRoutineDescriptor(node); err != nil || ref == nil || ref.Address().ObjectID != 3537 {
			t.Fatal("folding erased original identity", ref, err)
		}
		if folded.ResultUsage() != node.ResultUsage() || !reflect.DeepEqual(before, expr.InfoOf(node)) {
			t.Fatal("folding changed original source info")
		}
		value, err := folded.Eval(&evaluation)
		if err != nil || !value.IsNull() || value.Type() != node.ResultUsage().Type {
			t.Fatal("pruned domain-literal sibling lost typed NULL", value, err)
		}
		raw := enginetest.Statement(t, "SELECT * FROM "+text+" WITH ORDINALITY").(*parser.Select)
		e.Mu.Lock()
		sourceBinder := core.NewExpressionBinder(context.Background(), e, scope.Input, op, nil)
		sourceBinder.Query.Auth = core.RoutineAuthorityView(&evaluation)
		query, bindErr := sourceBinder.BindSelect(raw, false)
		e.Mu.Unlock()
		if bindErr != nil {
			t.Fatal("literal-sibling source binding", bindErr)
		}
		source := query.Root.(*plan.ProjectNode).Child.(*plan.FunctionFromNode)
		sourceInfo := expr.InfoOf(source.Calls[0])
		sourceSchema := append(plan.QuerySchema(nil), source.QuerySchema...)
		sourceManifest := append([]catalog.ExpressionDependency(nil), sourceBinder.Dependencies...)
		sourceEvaluation := evaluation
		sourceEvaluation.RoutineChecks = sourceBinder.Dependencies
		planned, err := view.PreflightQuery(&sourceEvaluation, source)
		if err != nil {
			t.Fatal("literal sibling source pruning", err)
		}
		execution := planned.(*plan.FunctionFromNode)
		if execution.ScalarNull == nil || !execution.ScalarNull[0] {
			t.Fatal("known NULL source was not pruned")
		}
		cursor, err := exec.OpenFunctionCall(&sourceEvaluation, execution, 0)
		if err != nil || !cursor.Value.IsNull() || cursor.Value.Type() != source.Calls[0].ResultUsage().Type {
			t.Fatal("literal sibling source typed NULL", cursor, err)
		}
		cursor.Close()
		if source.ScalarNull != nil || !reflect.DeepEqual(sourceInfo, expr.InfoOf(source.Calls[0])) || !reflect.DeepEqual(sourceSchema, execution.QuerySchema) || !reflect.DeepEqual(sourceManifest, sourceBinder.Dependencies) {
			t.Fatal("literal sibling pruning changed original Info/schema/manifest")
		}
	}
	for _, text := range []string{"pg_describe_object($1::oid,23,1/0)"} {
		node, binder, evaluation := strictNullUnitBind(t, e, op, scope, text)
		evaluation.Parameters = []sqltype.Value{sqltype.Null(binder.Params.Parameters[1].Type)}
		_, _, err := exec.PreflightCoercions(&evaluation, node)
		want := sqlstate.Error{Code: "22012", Message: "division by zero"}
		enginetest.Diagnostic(t, err, want)
		for _, suffix := range []string{"", " WHERE FALSE"} {
			raw := enginetest.Statement(t, "SELECT * FROM "+text+suffix).(*parser.Select)
			e.Mu.Lock()
			sourceBinder := core.NewExpressionBinder(context.Background(), e, scope.Input, op, nil)
			sourceBinder.Query.Auth = core.RoutineAuthorityView(&evaluation)
			query, bindErr := sourceBinder.BindSelect(raw, false)
			e.Mu.Unlock()
			if bindErr != nil {
				t.Fatal("source child-error binding", bindErr)
			}
			sourceEvaluation := evaluation
			sourceEvaluation.RoutineChecks = sourceBinder.Dependencies
			_, err = view.PreflightQuery(&sourceEvaluation, query.Root)
			enginetest.Diagnostic(t, err, want)
		}
	}
	// Validate a real typed DOMAIN parameter at its input boundary, before
	// outer strict-NULL planning. A literal sibling is not this boundary.
	node, binder, evaluation := strictNullUnitBind(t, e, op, scope, "pg_describe_object(NULL::oid,23,($1::strict_null_checked)::integer)")
	domain := binder.Params.Parameters[1].Type
	if !domain.IsDomain() || !domain.HasDomainChecks() {
		t.Fatal("parameter lost genuine domain input authority")
	}
	before := expr.InfoOf(node)
	manifest := append([]catalog.ExpressionDependency(nil), binder.Dependencies...)
	input := evaluation.Input
	input.DomainChecks = &evaluation
	err := domain.CheckDomainInput(evaluation.Context, sqltype.Null(domain), input)
	var state interface{ SQLState() string }
	if !errors.As(err, &state) || state.SQLState() != "23514" {
		t.Fatal("typed domain input did not enforce its CHECK before planning", err)
	}
	var violation *sqlstate.Error
	if !errors.As(err, &violation) || violation.Schema != "public" || violation.DataType != "strict_null_checked" || violation.Constraint != "strict_null_present" {
		t.Fatal("typed domain input checked the wrong constraint", err)
	}
	// The raw domain-input seam has no portal Context; no adapter fields are
	// fabricated. A valid import permits ordinary outer-NULL simplification.
	value, err := domain.InputWithEnvironment(evaluation.Context, "7", -1, input)
	if err != nil {
		t.Fatal("valid typed domain import", err)
	}
	evaluation.Parameters = []sqltype.Value{value}
	folded, _, err := exec.PreflightCoercions(&evaluation, node)
	if err != nil || !exec.RandomLiteralNull(folded) {
		t.Fatal("valid domain import then strict NULL", err)
	}
	result, err := folded.Eval(&evaluation)
	if err != nil || !result.IsNull() || result.Type() != node.ResultUsage().Type {
		t.Fatal("imported-domain sibling result", result, err)
	}
	if !reflect.DeepEqual(before, expr.InfoOf(node)) || !reflect.DeepEqual(manifest, binder.Dependencies) {
		t.Fatal("domain import/pruning changed original Info or manifest")
	}
}
