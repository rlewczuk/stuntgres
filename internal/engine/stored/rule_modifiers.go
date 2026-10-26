package stored

import (
	"fmt"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
)

// VisitRuleModifiers shares the declaration traversal used to freeze and check
// actual modifier presentations. Shared definitions are visited once.
func VisitRuleModifiers(binding analyze.Binding, visit func(*plan.CTEDefinition, *analyze.Binding) error) error {
	var seen map[*plan.CTEDefinition]bool
	var collect func(analyze.Binding) error
	collect = func(body analyze.Binding) error {
		for _, definition := range body.CTEs {
			if definition == nil {
				return fmt.Errorf("stuntgres: missing retained CTE definition")
			}
			if definition.Mutation == nil {
				if definition.Statement != nil {
					return fmt.Errorf("stuntgres: missing stored mutation binding")
				}
				continue
			}
			if seen[definition] {
				continue
			}
			if seen == nil {
				seen = make(map[*plan.CTEDefinition]bool)
			}
			seen[definition] = true
			mutation, err := checkedMutationDefinition(definition)
			if err != nil {
				return err
			}
			if err := visit(definition, mutation); err != nil {
				return err
			}
			if err := collect(*mutation); err != nil {
				return err
			}
		}
		return nil
	}
	return collect(binding)
}

func checkedMutationDefinition(definition *plan.CTEDefinition) (*analyze.Binding, error) {
	if definition == nil || definition.Query != nil || definition.Recursive || definition.Search != nil || definition.Cycle != nil || definition.Syntax != nil {
		return nil, fmt.Errorf("stuntgres: invalid stored mutation definition")
	}
	mutation, ok := analyze.MutationBinding(definition)
	if !ok || mutation.Relation == nil {
		return nil, fmt.Errorf("stuntgres: missing stored mutation binding")
	}
	valid := false
	switch statement := definition.Statement.(type) {
	case *parser.Insert:
		valid = statement != nil && mutation.Update == nil && mutation.Merge == nil && mutation.Delete.Using == nil
	case *parser.Update:
		valid = statement != nil && mutation.Update != nil && mutation.Merge == nil && mutation.Delete.Using == nil
	case *parser.Delete:
		valid = statement != nil && mutation.Update == nil && mutation.Merge == nil && mutation.Query == nil
	case *parser.Merge:
		valid = statement != nil && mutation.Merge != nil && mutation.Update == nil && mutation.Delete.Using == nil
	}
	if !valid {
		return nil, fmt.Errorf("stuntgres: mismatched stored mutation statement")
	}
	if mutation.Returning == nil {
		if len(definition.Schema) != 0 {
			return nil, fmt.Errorf("stuntgres: unexpected stored mutation output")
		}
		return mutation, nil
	}
	returning := mutation.Returning
	if len(definition.Schema) != len(returning.Schema) || len(returning.Expressions) != len(returning.Schema) {
		return nil, fmt.Errorf("stuntgres: invalid stored mutation output width")
	}
	for i, column := range definition.Schema {
		if column != returning.Schema[i] || returning.Expressions[i] == nil || column.Usage != returning.Expressions[i].ResultUsage() {
			return nil, fmt.Errorf("stuntgres: invalid stored mutation output usage")
		}
	}
	return mutation, nil
}
