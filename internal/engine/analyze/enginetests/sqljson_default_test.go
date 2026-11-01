package enginetests_test

import (
	"errors"
	"strconv"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestSQLJSONDefaultSelectedCastShape(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	integer, _ := view.LookupType(sqltype.Int4)
	bigint, _ := view.LookupType(sqltype.Int8)
	text, _ := view.LookupType(sqltype.Text)
	varchar, _ := view.LookupType(sqltype.VarChar)
	procedure, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 481})
	if !ok {
		t.Fatal("missing released int4 to int8 cast routine")
	}
	binder := new(analyze.Binder)
	for _, position := range []parser.Position{{Offset: 45, Character: 46}, {Offset: 56, Character: 53}} {
		raw := parser.CastExpr{Child: parser.ParameterExpr{Number: 1, Pos: position}, Pos: parser.Position{Offset: position.Offset + 8, Character: position.Character + 8}}
		parameter := plan.ParameterExpr{Info: expr.Info{Usage: integer.Usage(), Pos: position}, Number: 1}
		wrap := func(child expr.Expr) expr.Expr {
			t.Helper()
			io, err := plan.NewDatumCast(expr.Info{Usage: text.Usage(), Pos: raw.Pos}, child, "i", sqltype.Explicit)
			if err != nil {
				t.Fatal(err)
			}
			relabel, err := plan.NewDatumCast(expr.Info{Usage: varchar.Usage(), Pos: raw.Pos}, io, "b", sqltype.Explicit)
			if err != nil {
				t.Fatal(err)
			}
			return plan.CollationExpr{Info: expr.Info{Usage: varchar.Usage(), Pos: raw.Pos}, Child: relabel}
		}
		assertDiagnostic := func(node expr.Expr, message string, want parser.Position) {
			t.Helper()
			err := binder.ValidateSQLJSONDefault(raw, node)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != "42804" || diagnostic.Message != message || diagnostic.Position != strconv.Itoa(want.Character) {
				t.Fatalf("diagnostic = %#v (%v), want 42804 %q at %d", diagnostic, err, message, want.Character)
			}
		}
		shapeError := "can only specify a constant, non-aggregate function, or operator expression for DEFAULT"
		assertDiagnostic(wrap(parameter), shapeError, position)
		prefixPosition := parser.Position{Offset: position.Offset - 1, Character: position.Character - 1}
		prefixCast, err := plan.NewDatumCast(expr.Info{Usage: text.Usage(), Pos: prefixPosition}, parameter, "i", sqltype.Explicit)
		if err != nil {
			t.Fatal(err)
		}
		assertDiagnostic(wrap(prefixCast), shapeError, prefixPosition)
		cast, err := functions.NewCastRoutine(expr.Info{Usage: bigint.Usage(), Pos: raw.Pos, RoutineReference: procedure}, procedure.Name(), "pg_catalog", []expr.Expr{parameter}, procedure.Volatility)
		if err != nil {
			t.Fatal(err)
		}
		if err := binder.ValidateSQLJSONDefault(raw, wrap(cast)); err != nil {
			t.Fatal("selected checked function hidden by relabel/IO", err)
		}
		missing := cast
		missing.RoutineReference = nil
		assertDiagnostic(wrap(missing), shapeError, raw.Pos)
		forged := cast
		forged.Result = sqltype.Int4
		assertDiagnostic(wrap(forged), shapeError, raw.Pos)
		aggregate := *procedure
		aggregate.RoutineKind = catalog.AggregateRoutine
		wrongKind := cast
		wrongKind.RoutineReference = &aggregate
		assertDiagnostic(wrap(wrongKind), shapeError, raw.Pos)
		column := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Pos: position, Property: expr.Properties{RowDependent: true}}}
		columnCast, err := functions.NewCastRoutine(cast.Info, procedure.Name(), "pg_catalog", []expr.Expr{column}, procedure.Volatility)
		if err != nil {
			t.Fatal(err)
		}
		assertDiagnostic(wrap(columnCast), "DEFAULT expression must not contain column references", raw.Pos)
		assertDiagnostic(wrap(column), shapeError, position)

		definition := &proc.Definition{Result: bigint.Usage()}
		if err := definition.SetFormals([]proc.Formal{{Mode: "i", Usage: integer.Usage()}}); err != nil {
			t.Fatal(err)
		}
		sqlProcedure := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 98000}, SQL: definition, Result: ids.OID(sqltype.Int8), RoutineKind: catalog.FunctionRoutine}
		domain := new(ids.Domain)
		sqlCall := plan.SQLRoutineExpr{
			Info:      expr.Info{Usage: bigint.Usage(), Pos: raw.Pos, RoutineReference: sqlProcedure},
			Procedure: sqlProcedure, Ref: domain.Ref(1, sqlProcedure.Address()),
			Args: []expr.Expr{parameter}, WrittenCount: 1,
			InputMap: []plan.RoutineInput{{Start: 0, Count: 1}}, ChildSlots: []int{0},
			Signature: proc.InvocationSignature{Inputs: definition.Inputs, Result: definition.Result},
		}
		sqlCall.Property = sqlCall.GroupProperties(sqlCall.Args)
		if err := binder.ValidateSQLJSONDefault(raw, wrap(sqlCall)); err != nil {
			t.Fatal("selected SQL cast hidden by relabel/IO", err)
		}
		missingSQL := sqlCall
		missingSQL.RoutineReference = nil
		assertDiagnostic(wrap(missingSQL), shapeError, raw.Pos)
		forgedSQL := sqlCall
		forgedSQL.Ref = ids.ObjectRef{}
		assertDiagnostic(wrap(forgedSQL), shapeError, raw.Pos)
		sqlColumn := sqlCall
		sqlColumn.Args = []expr.Expr{column}
		sqlColumn.Property = sqlColumn.GroupProperties(sqlColumn.Args)
		assertDiagnostic(wrap(sqlColumn), "DEFAULT expression must not contain column references", raw.Pos)
		sqlAggregate := *sqlProcedure
		sqlAggregate.RoutineKind = catalog.AggregateRoutine
		sqlWrongKind := sqlCall
		sqlWrongKind.Procedure = &sqlAggregate
		sqlWrongKind.RoutineReference = &sqlAggregate
		assertDiagnostic(wrap(sqlWrongKind), shapeError, raw.Pos)
	}
}
