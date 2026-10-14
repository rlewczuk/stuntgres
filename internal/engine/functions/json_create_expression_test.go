package functions

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type jsonCreateUnitContext struct{ context.Context }

func (c *jsonCreateUnitContext) FunctionContext() context.Context { return c.Context }
func (c *jsonCreateUnitContext) FunctionJSONOutput() sqltype.OutputEnvironment {
	return sqltype.OutputEnvironment{}
}

type jsonCreateUnitExpression interface {
	ResultUsage() sqltype.Usage
	Eval(*jsonCreateUnitContext) (sqltype.Value, error)
}
type jsonCreateCountingExpression struct {
	Info[struct{}]
	value   sqltype.Value
	count   *int
	failure error
}

func (e jsonCreateCountingExpression) Eval(*jsonCreateUnitContext) (sqltype.Value, error) {
	*e.count++
	return e.value, e.failure
}
func TestJSONCreateEvaluatesChildrenOnceInOrder(t *testing.T) {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	one, _ := sqltype.Integer(types[sqltype.Int4], 1)
	counts := [3]int{}
	failure := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22012", Message: "division by zero"}
	args := []jsonCreateUnitExpression{
		jsonCreateCountingExpression{Info: Info[struct{}]{Usage: one.Type().Usage()}, value: one, count: &counts[0]},
		jsonCreateCountingExpression{Info: Info[struct{}]{Usage: one.Type().Usage()}, count: &counts[1], failure: failure},
		jsonCreateCountingExpression{Info: Info[struct{}]{Usage: one.Type().Usage()}, value: one, count: &counts[2]},
	}
	node, err := NewJSONCreate[jsonCreateUnitExpression, *jsonCreateUnitContext](Info[struct{}]{Usage: types[sqltype.JSON].Usage()}, sqltype.JSONBuildObject, args, false, [2]uint64{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = node.Eval(&jsonCreateUnitContext{context.Background()})
	if !errors.Is(err, failure) || counts != [3]int{1, 1, 0} {
		t.Fatal("arity validation hid child evaluation", err, counts)
	}
}
