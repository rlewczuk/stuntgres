package functions

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func aggregateUnitType(id sqltype.ID) (*sqltype.Descriptor, bool) {
	for _, d := range aggregateUnitTypes {
		if d.ID() == id {
			return d, true
		}
	}
	return nil, false
}

var aggregateUnitTypes = sqltype.Builtins()

func aggregateUnitState(t *testing.T, err error, code string) {
	t.Helper()
	d, ok := err.(interface{ SQLState() string })
	if !ok || d.SQLState() != code {
		t.Fatal(err, code)
	}
}

type aggregateUnitProvider interface {
	ResultUsage() sqltype.Usage
	Init(context.Context) (any, error)
	Advance(context.Context, any, []sqltype.Value, expr.Context) error
	Finalize(context.Context, any) (sqltype.Value, error)
}
