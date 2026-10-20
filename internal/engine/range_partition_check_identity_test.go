package engine

import (
	"context"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRangePartitionInheritedCookedCheckStructuralIdentity(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE root(k INT,CONSTRAINT positive CHECK(k>0)) PARTITION BY RANGE(k)")
	roleExecute(t, e, op, "CREATE TABLE leaf PARTITION OF root(CONSTRAINT positive CHECK(k>0)) DEFAULT")
	parent := rangePartitionRelation(t, e, "root")
	child := rangePartitionRelation(t, e, "leaf")
	if len(parent.Constraints.Check) != 1 || len(child.Constraints.Check) != 1 || parent.Constraints.Check[0].OID == child.Constraints.Check[0].OID {
		t.Fatal("equal actual CHECK duplicated inherited constraint or reused identity")
	}
	if parent.Columns[0].OriginTable == child.Columns[0].OriginTable {
		t.Fatal("cooked row origin was not reowned")
	}
	original := child.Constraints.Check[0].Expression
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	equality := analyze.Binder{Consumer: analyze.ConsumerContext{CheckDefinitionEquality: true}}
	for _, test := range []struct {
		expression string
		equal      bool
	}{
		{"k>0", true}, {"(k)>0", true}, {"0<k", false}, {"k>0+0", false}, {"k>1", false},
	} {
		definition := *child
		definition.Constraints.Check = nil
		syntax := statement(t, "CREATE TABLE leaf(k INT,CONSTRAINT actual CHECK("+test.expression+"))").(*parser.CreateTable)
		e.mu.Lock()
		err := e.bindCreateChecks(op, auth, syntax, &definition, sqltype.InputEnvironment{}, nil)
		e.mu.Unlock()
		if err != nil {
			t.Fatal(test, err)
		}
		if len(definition.Constraints.Check) != 1 || equality.SameGroupExpression(original, definition.Constraints.Check[0].Expression) != test.equal {
			t.Fatal("cooked tree comparison simplified or retargeted definition", test)
		}
	}
	// Coercion display form is immaterial to CHECK cooked-tree identity but is
	// still significant for ordinary grouping's existing expression policy.
	coercionPlan, err := sqltype.PlanCoercion(original.ResultUsage(), original.ResultUsage(), sqltype.Implicit)
	if err != nil {
		t.Fatal(err)
	}
	binder := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, op, nil)
	implicit := plan.CoercionExpr{Info: binder.Info(original.ResultUsage(), original.SourcePosition(), original.Properties()), Child: original, Plan: coercionPlan, Mode: sqltype.Implicit}
	explicit := implicit
	explicit.Mode = sqltype.Explicit
	grouping := analyze.Binder{}
	if !equality.SameGroupExpression(implicit, explicit) || grouping.SameGroupExpression(implicit, explicit) {
		t.Fatal("CHECK-only coercion identity changed grouping semantics")
	}
	if len(child.Constraints.Check) != 1 || !equality.SameGroupExpression(child.Constraints.Check[0].Expression, original) {
		t.Fatal("private equality binding mutated installed CHECK")
	}
}
