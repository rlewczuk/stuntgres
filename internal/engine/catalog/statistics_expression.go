package catalog

import (
	"slices"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// StatisticsExpression retains binding authority, never parser syntax or SQL.
// All slice inputs are copied; readers can only index the immutable backing.
type StatisticsExpression struct {
	definition, execution expr.Expr
	attributes            []int32
	inputs, dependencies  []ids.ObjectRef
	usage                 sqltype.Usage
}

func NewStatisticsExpression(definition, execution expr.Expr, attributes []int32, inputs, dependencies []ids.ObjectRef, usage sqltype.Usage) StatisticsExpression {
	return StatisticsExpression{definition: definition, execution: execution, attributes: slices.Clone(attributes), inputs: slices.Clone(inputs), dependencies: slices.Clone(dependencies), usage: usage}
}
func (e StatisticsExpression) Definition() expr.Expr          { return e.definition }
func (e StatisticsExpression) Execution() expr.Expr           { return e.execution }
func (e StatisticsExpression) Usage() sqltype.Usage           { return e.usage }
func (e StatisticsExpression) AttributeLen() int              { return len(e.attributes) }
func (e StatisticsExpression) Attribute(i int) int32          { return e.attributes[i] }
func (e StatisticsExpression) InputLen() int                  { return len(e.inputs) }
func (e StatisticsExpression) Input(i int) ids.ObjectRef      { return e.inputs[i] }
func (e StatisticsExpression) DependencyLen() int             { return len(e.dependencies) }
func (e StatisticsExpression) Dependency(i int) ids.ObjectRef { return e.dependencies[i] }

type statisticsExpressions struct{ values []StatisticsExpression }

// StatisticsExpressions is a comparable retained-definition identity. Descriptor
// COW shares it; rebinding or restoring creates a new identity.
type StatisticsExpressions struct{ backing *statisticsExpressions }

func NewStatisticsExpressions(values []StatisticsExpression) StatisticsExpressions {
	if len(values) == 0 {
		return StatisticsExpressions{}
	}
	return StatisticsExpressions{&statisticsExpressions{values: slices.Clone(values)}}
}
func (e StatisticsExpressions) Len() int {
	if e.backing == nil {
		return 0
	}
	return len(e.backing.values)
}
func (e StatisticsExpressions) At(i int) StatisticsExpression { return e.backing.values[i] }
