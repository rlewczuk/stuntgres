package stored

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// RemapStatisticsExpressions reowns a checked statistics recipe through the
// shared reference/type/provider mapper. TEMP retention supplies destination
// identity maps here; imported recipes use their ordinary source remap.
func RemapStatisticsExpressions(view catalog.Read, d *catalog.Statistics, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, procedure catalog.ProcedureRemapper, collations ...typedef.CollationRemapper) (*catalog.Statistics, error) {
	mapper := &ViewQueryRemapper{Catalog: view, Ref: func(r ids.ObjectRef) ids.ObjectRef { return r }, Type: typ, Procedure: procedure}
	if len(collations) != 0 {
		mapper.Collation = collations[0]
	}
	retag := func(node expr.Expr) (expr.Expr, bool, error) {
		return mapExpressionWithProcedure(view, nil, node, typ, mapper.Nested, nil, false, nil, procedure, collations...)
	}
	return reownStatisticsExpressions(d, ref, retag)
}

func reownStatisticsExpressions(d *catalog.Statistics, ref func(ids.ObjectRef) ids.ObjectRef, retag func(expr.Expr) (expr.Expr, bool, error)) (*catalog.Statistics, error) {
	copy := *d
	copy.Relation = ref(d.Relation)
	var values []catalog.StatisticsExpression
	for i := range d.Expressions.Len() {
		old := d.Expressions.At(i)
		refsChanged := copy.Relation != d.Relation
		for j := range old.InputLen() {
			refsChanged = ref(old.Input(j)) != old.Input(j) || refsChanged
		}
		for j := range old.DependencyLen() {
			refsChanged = ref(old.Dependency(j)) != old.Dependency(j) || refsChanged
		}
		next, changed, err := reownStatisticsExpression(old, ref, retag, refsChanged)
		if err != nil {
			return nil, err
		}
		if changed && values == nil {
			values = make([]catalog.StatisticsExpression, d.Expressions.Len())
			for j := range i {
				values[j] = d.Expressions.At(j)
			}
		}
		if values != nil {
			values[i] = next
		}
	}
	if values != nil {
		copy.Expressions = catalog.NewStatisticsExpressions(values)
	}
	if copy.Relation == d.Relation && values == nil {
		return d, nil
	}
	return &copy, nil
}

func reownStatisticsExpression(old catalog.StatisticsExpression, ref func(ids.ObjectRef) ids.ObjectRef, retag func(expr.Expr) (expr.Expr, bool, error), refsChanged bool) (catalog.StatisticsExpression, bool, error) {
	definition, execution := old.Definition(), old.Execution()
	var err error
	// An identity reference map must not remint occurrence nodes or their recipe
	// identity during unrelated catalog publication, including column rename.
	if refsChanged {
		definition, err = RemapExpression(definition, ref, nil)
		if err != nil {
			return old, false, err
		}
		execution, err = RemapExpression(execution, ref, nil)
		if err != nil {
			return old, false, err
		}
	}
	definition, definitionChanged, err := retag(definition)
	if err != nil {
		return old, false, err
	}
	execution, executionChanged, err := retag(execution)
	if err != nil {
		return old, false, err
	}
	if !refsChanged && !definitionChanged && !executionChanged {
		return old, false, nil
	}
	attributes := make([]int32, old.AttributeLen())
	for i := range attributes {
		attributes[i] = old.Attribute(i)
	}
	inputs := make([]ids.ObjectRef, old.InputLen())
	for i := range inputs {
		inputs[i] = ref(old.Input(i))
	}
	dependencies := make([]ids.ObjectRef, old.DependencyLen())
	for i := range dependencies {
		dependencies[i] = ref(old.Dependency(i))
	}
	return catalog.NewStatisticsExpression(definition, execution, attributes, inputs, dependencies, definition.ResultUsage()), true, nil
}
