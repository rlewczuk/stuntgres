package view

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// MaterializedSnapshotEnvironment checks that b is a snapshot row INSERT
// whose constructors use the builtin providers, then returns input with b's
// statement resolvers.
func MaterializedSnapshotEnvironment(b *analyze.Binding, input sqltype.InputEnvironment) (sqltype.InputEnvironment, error) {
	if b.Parameters.Count != 0 || len(b.QuerySources) != 0 || len(b.Insert.Defaults) != 0 {
		return input, fmt.Errorf("stuntgres: invalid materialized view snapshot row binding")
	}
	for _, expressions := range b.Expressions {
		for _, expression := range expressions {
			if err := materializedSnapshotConstructorProviders(expression); err != nil {
				return input, err
			}
		}
	}
	environment := input
	environment.Regclass, environment.Regtype = b.Resolvers.Regclass, b.Resolvers.Regtype
	environment.Regrole, environment.Operator, environment.Regproc = b.Resolvers.Regrole, b.Resolvers.Operator, b.Resolvers.Regproc
	environment.Regcollation, environment.Regnamespace = b.Resolvers.Regcollation, b.Resolvers.Regnamespace
	environment.RegTS, environment.ACLItem = b.Resolvers.RegTS, b.Resolvers.ACLItem
	environment.FTS = b.Resolvers.FTS
	environment.TypeDiagnosticName, environment.EnumMemberSafe = b.Resolvers.TypeDiagnosticName, b.Resolvers.EnumMemberSafe
	return environment, nil
}

// EvaluateMaterializedSnapshotRows evaluates the bound snapshot rows of s
// into d's column types under evaluation.
func EvaluateMaterializedSnapshotRows(evaluation *exec.Context, b *analyze.Binding, d *catalog.Relation, s *parser.Insert) (rows [][]sqltype.Value, err error) {
	for _, expressions := range b.Expressions {
		values := make([]sqltype.Value, len(d.Columns))
		for i, expression := range expressions {
			expression, _, err = exec.PreflightCoercionsWithPolicy(evaluation, expression, false)
			if err != nil {
				return nil, err
			}
			value, evalErr := expression.Eval(evaluation)
			if evalErr != nil {
				return nil, evalErr
			}
			if len(b.ArrayTargets) != 0 && b.ArrayTargets[i] != nil {
				// Only the constructor carrier emitted by the dump may use a
				// slice target. Apply the ordinary target semantics, but reject
				// partial shapes rather than silently clipping a captured row.
				if !value.RequiresCircleConstructorReplay() && !regobj.NeedsFloat4RawReplay(value) {
					return nil, fmt.Errorf("stuntgres: unnecessary materialized view snapshot bounds")
				}
				if value.IsNull() || !value.Type().BaseType().IsArray() || value.ArrayRank() != len(s.Targets[i].Steps) {
					return nil, fmt.Errorf("stuntgres: invalid materialized view snapshot array shape")
				}
				bounded, applyErr := b.ArrayTargets[i].Apply(evaluation, sqltype.Null(d.Columns[i].Usage.Type), value)
				if applyErr != nil {
					return nil, applyErr
				}
				if bounded.ArrayRank() != value.ArrayRank() || regobj.ConstructorArrayTargetBounds(bounded) == "" {
					return nil, fmt.Errorf("stuntgres: invalid materialized view snapshot array bounds")
				}
				for dimension := range value.ArrayRank() {
					if bounded.ArrayDimension(dimension).Length != value.ArrayDimension(dimension).Length {
						return nil, fmt.Errorf("stuntgres: truncated materialized view snapshot array")
					}
				}
				value = bounded
			}
			if value.Type() != d.Columns[i].Usage.Type {
				return nil, fmt.Errorf("stuntgres: invalid materialized view snapshot value type")
			}
			values[i] = value
		}
		rows = append(rows, values)
	}
	return rows, nil
}

// Closed array-codec syntax is not permission to execute a same-named SQL
// overload. Check the selected provider before any coercion preflight or Eval;
// normal binding and execution still own its dependencies and authorization.
func materializedSnapshotConstructorProviders(expression expr.Expr) error {
	var invalid error
	plan.WalkExpression(expression, func(node expr.Expr) {
		procedure, ok := expr.InfoOf(node).RoutineReference.(*catalog.Procedure)
		if !ok || procedure.Name() != "array_fill" && procedure.Name() != "array_cat" {
			return
		}
		array, ok := node.(functions.ArrayTransform)
		if !ok || array.Operator || array.Namespace != "pg_catalog" || array.Kind != functions.ArrayFill && array.Kind != functions.ArrayConcatenate ||
			procedure.Builtin == nil || procedure.Builtin.Providers&functions.ArrayRoutineProvider == 0 || array.Kind.Name() != procedure.Name() {
			invalid = fmt.Errorf("stuntgres: invalid materialized view snapshot constructor provider")
		}
	})
	return invalid
}
