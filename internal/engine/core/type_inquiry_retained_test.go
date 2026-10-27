package core_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

func TestTypeDescribeRetainedSnapshotDemand(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE TABLE retained_inquiry(a text DEFAULT format_type(23,NULL),b regtype DEFAULT pg_basetype(23::regtype),c regtype DEFAULT pg_typeof(NULL::integer),d regtype DEFAULT pg_typeof(format_type(23,NULL)))")
	view := e.CatalogRead()
	relation := view.MustRelation(enginetest.CatalogRef(t, e, view, "retained_inquiry").Address)
	for _, tc := range []struct {
		attribute int32
		demand    bool
	}{
		{1, true},
		{2, true},
		{3, false},
		{4, true},
	} {
		original := relation.ColumnMeta.Defaults[tc.attribute].Expression
		for _, route := range []string{"default", "generation", "check"} {
			t.Run(relation.Columns[tc.attribute-1].Name+"/"+route, func(t *testing.T) {
				// The real catalog-owned recipe is reused without rebinding SQL.
				// Capturing demand must not capture a scope or evaluate its child.
				b := core.NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
				var defaults []expr.Expr
				var generations map[int32]catalog.ColumnGeneration
				var checks []catalog.CheckConstraint
				switch route {
				case "default":
					defaults = []expr.Expr{original}
				case "generation":
					generations = map[int32]catalog.ColumnGeneration{1: {Planned: original}}
				case "check":
					checks = []catalog.CheckConstraint{{Expression: original}}
				}
				e.Mu.Lock()
				err := b.CaptureRetainedWriteInput(defaults, generations, checks)
				e.Mu.Unlock()
				if err != nil || b.Consumer.VisibilitySnapshot != tc.demand {
					t.Fatalf("retained visibility demand = %t, want %t: %v", b.Consumer.VisibilitySnapshot, tc.demand, err)
				}
				if !reflect.DeepEqual(relation.ColumnMeta.Defaults[tc.attribute].Expression, original) {
					t.Fatal("capture replaced catalog-owned expression")
				}
				// A later catalog-free sibling cannot erase accumulated demand.
				b.Consumer.VisibilitySnapshot = true
				e.Mu.Lock()
				err = b.CaptureRetainedWriteInput([]expr.Expr{relation.ColumnMeta.Defaults[3].Expression}, nil, nil)
				e.Mu.Unlock()
				if err != nil || !b.Consumer.VisibilitySnapshot {
					t.Fatal("catalog-free sibling erased existing demand", err)
				}
			})
		}
	}
}
