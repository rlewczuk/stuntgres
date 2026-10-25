package schemacmds_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestRoutineRetainedWriteInputAuthority(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE TYPE retained_routine_pair AS (p regprocedure, r regrole, t regtype)"))
	pair := domainUnitType(t, e.CatalogRead(), "retained_routine_pair").SQLType
	text, _ := e.CatalogRead().LookupType(sqltype.Text)
	value, err := text.Input(context.Background(), `("abs(integer)",postgres,integer)`, -1)
	if err != nil {
		t.Fatal(err)
	}
	coercionPlan, err := sqltype.PlanCoercion(text.Usage(), pair.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	// A restored fragment expression retains this character-to-container cast.
	// Its consuming statement must supply input authority without rebinding it.
	node := plan.CoercionExpr{
		Info:  expr.Info{Usage: pair.Usage()},
		Child: plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: value},
		Plan:  coercionPlan,
		Mode:  sqltype.Explicit,
	}
	for _, route := range []string{"default", "generation", "check"} {
		for _, named := range []bool{false, true} {
			name := route + "/fresh"
			input := sqltype.InputEnvironment{}
			if named {
				name = route + "/existing-diagnostic-name"
				input.TypeDiagnosticName = func(sqltype.ID) string { return "retained-name" }
			}
			t.Run(name, func(t *testing.T) {
				b := enginetest.ExpressionBinder(t, context.Background(), e, input, nil)
				var defaults []expr.Expr
				var generations map[int32]catalog.ColumnGeneration
				var checks []catalog.CheckConstraint
				switch route {
				case "default":
					defaults = []expr.Expr{node}
				case "generation":
					generations = map[int32]catalog.ColumnGeneration{1: {Planned: node}}
				case "check":
					checks = []catalog.CheckConstraint{{Expression: node}}
				}
				if err := b.CaptureRetainedWriteInput(defaults, generations, checks); err != nil {
					t.Fatal(err)
				}
				if named && b.Input.TypeDiagnosticName(pair.ID()) != "retained-name" {
					t.Fatal("catalog input capture replaced existing diagnostic authority")
				}
				actual, err := node.Eval(&exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Input: b.Input}})
				if err != nil {
					t.Fatal("retained coercion lost catalog input authority", err)
				}
				procedure, err := actual.CompositeField(0).ProcedureOID()
				if err != nil || procedure != 1397 {
					t.Fatal("retained routine identity", procedure, err)
				}
				role, err := actual.CompositeField(1).RoleOID()
				postgres, ok := e.Roles().Lookup("postgres")
				if err != nil || !ok || uint64(role) != postgres.Ref.ID() {
					t.Fatal("retained receiving role identity", role, err)
				}
				typeID, err := actual.CompositeField(2).TypeOID()
				if err != nil || typeID != uint32(sqltype.Int4) {
					t.Fatal("retained type identity", typeID, err)
				}
			})
		}
	}
}
