package stored

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDeparseStatisticsExpressionShapes(t *testing.T) {
	view, relation, integer, _ := routineCaptureFixture(t)
	text, _ := view.LookupType(sqltype.Text)
	bigint, _ := view.LookupType(sqltype.Int8)
	date, _ := view.LookupType(sqltype.Date)
	oneValue, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	one := plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}, Value: oneValue}
	column := plan.ColumnExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{RowDependent: true}}, Origin: view.Domain().Ref(view.Epoch(), relation.Address()), Scope: 1, Frame: 1}
	column.Origin.Address.SubID = 1
	provider, err := sqltype.BindArithmetic(sqltype.ArithmeticAdd, integer, integer, integer)
	if err != nil {
		t.Fatal(err)
	}
	operator := plan.ArithmeticExpr{Info: expr.Info{Usage: integer.Usage(), Property: column.Properties()}, Left: column, Right: one, Operator: sqltype.ArithmeticAdd, Provider: provider}
	conditional := plan.ConditionalExpr{Info: expr.Info{Usage: integer.Usage(), Property: column.Properties()}, Kind: parser.Coalesce, Args: []expr.Expr{column, one}}
	castPlan, err := sqltype.PlanCoercion(integer.Usage(), bigint.Usage(), sqltype.Explicit)
	if err != nil {
		t.Fatal(err)
	}
	cast := plan.CoercionExpr{Info: expr.Info{Usage: bigint.Usage(), Property: column.Properties()}, Child: column, Mode: sqltype.Explicit, Plan: castPlan}
	implicitPlan, err := sqltype.PlanCoercion(integer.Usage(), bigint.Usage(), sqltype.Implicit)
	if err != nil {
		t.Fatal(err)
	}
	implicit := plan.CoercionExpr{Info: expr.Info{Usage: bigint.Usage(), Property: conditional.Properties()}, Child: conditional, Mode: sqltype.Implicit, Plan: implicitPlan}
	version := functions.VersionInformation{Info: expr.Info{Usage: text.Usage(), Property: expr.IntrinsicProperties(functions.VersionInformationServer.IntrinsicRule())}, Kind: functions.VersionInformationServer}
	current := functions.CurrentDateTime{Info: expr.Info{Usage: date.Usage(), Property: expr.IntrinsicProperties(functions.CurrentDateTimeIntrinsicRule())}, Kind: parser.CurrentDate}
	timestamptz, _ := view.LookupType(sqltype.Timestamptz)
	timestamp, _ := view.LookupType(sqltype.Timestamp)
	clock := functions.CurrentDateTime{Info: expr.Info{Usage: timestamptz.Usage(), Property: expr.IntrinsicProperties(functions.CurrentDateTimeIntrinsicRule())}, Kind: parser.CurrentTimestamp}
	timezone := functions.Timezone{Info: expr.Info{Usage: timestamp.Usage(), Property: clock.Properties()}, Value: clock, Zone: plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "UTC")}}
	boolean, _ := view.LookupType(sqltype.Bool)
	predicate := functions.JSONPredicate{Info: expr.Info{Usage: boolean.Usage()}, Child: plan.LiteralExpr{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, "{}")}}
	negated := plan.LogicalExpr{Info: expr.Info{Usage: boolean.Usage()}, Op: parser.LogicalNot, Left: predicate}
	r := Deparser{View: view, Relation: relation}
	for _, tc := range []struct {
		name string
		node expr.Expr
		want string
		bare bool
	}{
		{"operator", operator, "(v + 1)", false},
		{"cast", cast, "(v::bigint)", false},
		{"coalesce", conditional, "COALESCE(v, 1)", true},
		{"implicit_function", implicit, "COALESCE(v, 1)", true},
		{"ordinary_call", version, "version()", true},
		{"sql_value", current, "CURRENT_DATE", true},
		{"at_time_zone", timezone, "(CURRENT_TIMESTAMP AT TIME ZONE 'UTC'::text)", true},
		{"json_predicate", predicate, "(('{}'::text IS JSON))", false},
		{"not_predicate", negated, "(NOT ('{}'::text IS JSON))", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := r.StatisticsElement(t.Context(), tc.node)
			if err != nil || got != tc.want {
				t.Fatal(got, tc.want, err)
			}
			bare, err := statisticsFunctionShape(tc.node)
			if err != nil || bare != tc.bare {
				t.Fatal("typed shape", bare, err)
			}
		})
	}
	// A real call/operator provider can share a hook but must retain its
	// accepted rendering shape. Bit-string concatenation is an operator.
	bytea, _ := view.LookupType(sqltype.Bytea)
	binaryPlan, err := sqltype.BindBinary("||", bytea.Usage(), bytea.Usage(), bytea.Usage())
	if err != nil {
		t.Fatal(err)
	}
	arg := plan.LiteralExpr{Info: expr.Info{Usage: bytea.Usage()}, Value: sqltype.Null(bytea)}
	binary := functions.Binary{Info: expr.Info{Usage: bytea.Usage()}, Plan: binaryPlan, Args: []expr.Expr{arg, arg}}
	if bare, err := statisticsFunctionShape(binary); err != nil || bare {
		t.Fatal("infix call escaped grouping", bare, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if got, err := r.StatisticsElement(ctx, operator); !errors.Is(err, context.Canceled) || got != "" {
		t.Fatal("canceled renderer published", got, err)
	}
}
