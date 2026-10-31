package index

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
)

// ValidateIndexDeclaration applies CREATE admission to a newly cloned index's
// cooked providers. It is not validation of an existing index: maintenance and
// attachment may retain routines whose current declaration is no longer immutable.
// Callers still perform the ordinary structural and stored-program validation.
func ValidateIndexDeclaration(definition *catalog.IndexDefinition) error {
	if predicate := definition.Predicate; predicate != nil && exec.PlannedVolatility(predicate.Execution) != expr.Immutable {
		return sqlError("42P17", "functions in index predicate must be marked IMMUTABLE")
	}
	for _, key := range definition.Keys {
		if key.Derived != nil && exec.PlannedVolatility(key.Derived.Execution) != expr.Immutable {
			return sqlError("42P17", "functions in index expression must be marked IMMUTABLE")
		}
	}
	return nil
}
