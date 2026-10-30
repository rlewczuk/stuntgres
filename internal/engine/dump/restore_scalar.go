package dump

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
)

// RestoreScalar validates one Restore scalar. Stored CREATE expressions
// (stored) retain their binder's admission rules; INSERT expressions retain
// the Restore whitelist. Both modes reject every parameter reached through
// the shared recursive visitor.
// The optional eligibility hook recognizes catalog-owned constructor names
// only. It neither resolves an overload nor grants execution authority; the
// ordinary binder must enforce the selected-provider restriction.
func RestoreScalar(raw parser.Expr, stored bool, eligible ...func(parser.FunctionExpr) bool) error {
	switch r := raw.(type) {
	case parser.XMLConstructExpr:
		for _,operand:=range r.Children(){if err:=RestoreScalar(operand,stored,eligible...);err!=nil{return err}}
		return nil
	case parser.XMLDocumentPredicateExpr:
		return RestoreScalar(r.Child, stored, eligible...)
	case parser.XMLParseExpr:
		return RestoreScalar(r.Child, stored, eligible...)
	case parser.XMLSerializeExpr:
		return RestoreScalar(r.Child, stored, eligible...)
	case parser.SQLJSONQueryExpr:
		for _, child := range r.Children() {
			if err := RestoreScalar(child, stored, eligible...); err != nil {
				return err
			}
		}
		return nil
	case parser.SQLJSONExpr:
		for _, child := range r.Args {
			if err := RestoreScalar(child, stored, eligible...); err != nil {
				return err
			}
		}
		if r.Query != nil {
			return RestoreSQLJSONQuery(r.Query, stored)
		}
		return nil
	case parser.JSONPredicateExpr:
		return RestoreScalar(r.Child, stored, eligible...)
	case parser.SubqueryExpr:
		if r.Left != nil {
			if err := RestoreScalar(r.Left, stored, eligible...); err != nil {
				return err
			}
		}
		if err := RestoreSQLJSONQuery(r.Query, stored); err != nil {
			return err
		}
		if !stored {
			return fmt.Errorf("stuntgres: invalid restore scalar")
		}
		return nil
	case parser.FunctionExpr:
		if !stored && !RestoreFunctionRecipe(r) && (len(eligible) == 0 || eligible[0] == nil || !eligible[0](r)) {
			return fmt.Errorf("stuntgres: invalid restore scalar")
		}
		for _, child := range r.Args {
			if err := RestoreScalar(child, stored, eligible...); err != nil {
				return err
			}
		}
		return nil
	case parser.CaseExpr:
		if r.Test != nil {
			if err := RestoreScalar(r.Test, stored, eligible...); err != nil {
				return err
			}
		}
		for _, arm := range r.Arms {
			if err := RestoreScalar(arm.Condition, stored, eligible...); err != nil {
				return err
			}
			if err := RestoreScalar(arm.Result, stored, eligible...); err != nil {
				return err
			}
		}
		if r.Else != nil {
			return RestoreScalar(r.Else, stored, eligible...)
		}
		return nil
	case parser.ConditionalExpr:
		for _, child := range r.Args {
			if err := RestoreScalar(child, stored, eligible...); err != nil {
				return err
			}
		}
		return nil
	case parser.LogicalExpr:
		if err := RestoreScalar(r.Left, stored, eligible...); err != nil {
			return err
		}
		if r.Right != nil {
			return RestoreScalar(r.Right, stored, eligible...)
		}
		return nil
	case parser.PredicateExpr:
		if err := RestoreScalar(r.Left, stored, eligible...); err != nil {
			return err
		}
		if r.Right != nil {
			return RestoreScalar(r.Right, stored, eligible...)
		}
		return nil
	case parser.CollateExpr:
		return RestoreScalar(r.Child, stored, eligible...)
	case parser.CastExpr:
		return RestoreScalar(r.Child, stored, eligible...)
	case parser.RegclassExpr:
		return RestoreScalar(r.Child, stored, eligible...)
	case parser.FieldExpr:
		return RestoreScalar(r.Base, stored, eligible...)
	case parser.RowExpr:
		for _, field := range r.Fields {
			if err := RestoreScalar(field, stored, eligible...); err != nil {
				return err
			}
		}
		return nil
	case parser.InListExpr:
		if err := RestoreScalar(r.Left, stored, eligible...); err != nil {
			return err
		}
		for _, item := range r.Items {
			if err := RestoreScalar(item, stored, eligible...); err != nil {
				return err
			}
		}
		return nil
	case parser.ArrayComparisonExpr:
		if err := RestoreScalar(r.Left, stored, eligible...); err != nil {
			return err
		}
		return RestoreScalar(r.Right, stored, eligible...)
	case parser.ArrayExpr:
		for _, element := range r.Elements {
			if err := RestoreScalar(element, stored, eligible...); err != nil {
				return err
			}
		}
		return nil
	case parser.SubscriptExpr:
		var storage [13]parser.Expr
		for _, child := range r.Children(&storage) {
			if err := RestoreScalar(child, stored, eligible...); err != nil {
				return err
			}
		}
		return nil
	case parser.TimeZoneExpr:
		if err := RestoreScalar(r.Value, stored, eligible...); err != nil {
			return err
		}
		if !r.Local {
			return RestoreScalar(r.Zone, stored, eligible...)
		}
		return nil
	case parser.ParameterExpr:
		return expr.ErrorAt("42P02", "parameters are not allowed in restore", r.Pos)
	case parser.CurrentDateTimeExpr, parser.IdentityExpr, parser.LiteralExpr, parser.TypedLiteralExpr, parser.DefaultExpr:
		return nil
	case parser.OperatorExpr:
		if !stored {
			if len(r.Name.Parts) < 1 || len(r.Name.Parts) > 2 {
				return fmt.Errorf("stuntgres: invalid restore scalar")
			}
			spelling := r.Name.Parts[len(r.Name.Parts)-1].Text
			if r.SQLSimilar {
				if (spelling != "~" && spelling != "!~") || len(r.Args) != 2 {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
				normalizer, ok := r.Args[1].(parser.FunctionExpr)
				if !ok || normalizer.Syntax != parser.SimilarEscapeFunctionSyntax {
					return fmt.Errorf("stuntgres: invalid restore scalar")
				}
			} else if functions.PosixName(spelling) && len(r.Args) != 2 && (spelling != "~" || len(r.Args) != 1) {
				return fmt.Errorf("stuntgres: invalid restore scalar")
			}
		}
		for _, child := range r.Args {
			if err := RestoreScalar(child, stored, eligible...); err != nil {
				return err
			}
		}
		return nil
	default:
		if stored {
			return nil
		} // Existing stored binder owns unsupported syntax.
		return fmt.Errorf("stuntgres: invalid restore scalar")
	}
}

// RestoreSQLJSONQuery rejects every parameter in a SQL/JSON query.
func RestoreSQLJSONQuery(query *parser.Select, stored bool) error {
	return parser.VisitQueryExpressions(query, func(raw parser.Expr) error {
		if parameter, ok := raw.(parser.ParameterExpr); ok {
			return expr.ErrorAt("42P02", "parameters are not allowed in restore", parameter.Pos)
		}
		return nil
	})
}
