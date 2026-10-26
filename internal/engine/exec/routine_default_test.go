package exec

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type routineDefaultOriginExpr struct {
	expr.Info
	evaluate func(*Context) (sqltype.Value, error)
}

func (e routineDefaultOriginExpr) Eval(context expr.Context) (sqltype.Value, error) {
	return e.evaluate(context.(*Context))
}

func TestRoutineDefaultOriginRestoration(t *testing.T) {
	domain := new(ids.Domain)
	caller := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 7})
	owner := domain.Ref(1, ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 8})
	consumer := &catalog.Relation{ObjectAddress: caller.Address}
	previous := []catalog.ExpressionDependency{{Ref: caller, Revision: 2}}
	defaults := []catalog.ExpressionDependency{{Ref: owner, Revision: 3}}
	for _, failure := range []error{nil, errors.New("default evaluation failed"), context.Canceled} {
		c := &Context{}
		c.FoldOrigin = FoldOrigin{Owner: caller, Attribute: 4, Definition: 5, Revision: 6}
		c.RoutineRecipe, c.RoutineGraft = false, true
		c.RoutineChecks, c.RoutineReferences, c.RoutineConsumer = previous, []ids.ObjectRef{caller}, consumer
		before, previousOrigin := c.EvalAdmission, c.FoldOrigin
		node := routineDefaultOriginExpr{evaluate: func(active *Context) (sqltype.Value, error) {
			if active.FoldOrigin != (FoldOrigin{Owner: owner, Attribute: 2}) || !active.RoutineRecipe || active.RoutineGraft || active.RoutineConsumer != nil || active.RoutineReferences != nil || !reflect.DeepEqual(active.RoutineChecks, defaults) {
				t.Fatal("default borrowed caller origin", active)
			}
			return sqltype.Value{}, failure
		}}
		if _, err := c.evalRoutineDefault(owner, 1, defaults, node, expr.Info{}); !errors.Is(err, failure) {
			t.Fatal(err, failure)
		}
		if c.FoldOrigin != previousOrigin || !reflect.DeepEqual(c.EvalAdmission, before) {
			t.Fatal("default leaked origin after evaluation", failure, c)
		}
	}
}
