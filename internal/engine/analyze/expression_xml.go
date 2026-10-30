package analyze

import (
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Released coerce_to_specific_type uses assignment context, including catalog
// cast selection and string I/O fallback. It is not an explicit CAST shortcut.
func (b *Binder) xmlSource(raw parser.Expr, target sqltype.Usage, construct string, opener parser.Position, sourceSyntax parser.Expr) (expr.Expr, error) {
	operand, err := b.AnalyzeArgument(raw, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
	if err != nil {
		return nil, err
	}
	child := operand.Expression
	if operand.Usage.Type == nil {
		child, err = b.CoerceArgument(operand, functions.CoercionRequest{Intent: functions.FinalizeArgument, Target: target})
		if err != nil {
			return nil, err
		}
	} else if !b.CanCoerce(operand.Usage, target, sqltype.Assignment) {
		return nil, expr.ErrorAt("42804", "argument of "+construct+" must be type "+b.DiagnosticTypeName(target.Type)+", not type "+b.DiagnosticTypeName(operand.Usage.Type), parser.ExpressionStart(raw))
	}
	// The opener identifies this assignment conversion independently of written
	// operand positions. Relational sources retain a selected conversion's real
	// recipe; scalar XML keeps its existing synthetic-spine presentation.
	converted, err := b.CoerceSelected(child, target, sqltype.Assignment, opener, sourceSyntax)
	if err != nil {
		return nil, err
	}
	switch conversion := converted.(type) {
	case plan.CoercionExpr:
		if conversion.Plan.RequiresCatalogOutput() {
			b.NoteCatalogText()
		}
	case plan.DatumCastExpr:
		if conversion.Method == "i" && child.ResultUsage().Type.ContainsCatalogIdentifier() {
			b.NoteCatalogText()
		}
	}
	return converted, nil
}

func (b *Binder) bindXMLParse(raw parser.XMLParseExpr) (expr.Expr, error) {
	source, err := b.xmlSource(raw.Child, b.Type(sqltype.Text).Usage(), "XMLPARSE", raw.Pos, nil)
	if err != nil {
		return nil, err
	}
	if b.Params.NoParameters || b.Consumer.frozenDefinition {
		raw.Child = b.NormalizeStoredExpression(raw.Child)
	}
	return functions.BindXMLParse(b, raw, source)
}

func (b *Binder) bindXMLSerialize(raw parser.XMLSerializeExpr) (expr.Expr, error) {
	source, err := b.xmlSource(raw.Child, b.Type(sqltype.XML).Usage(), "XMLSERIALIZE", raw.Pos, nil)
	if err != nil {
		return nil, err
	}
	if b.Params.NoParameters || b.Consumer.frozenDefinition {
		raw.Child = b.NormalizeStoredExpression(raw.Child)
	}
	characters, target, err := functions.BindXMLSerialize(b, raw, source)
	if err != nil {
		return nil, err
	}
	if err := b.CaptureTypeDiagnosticInput(target.Type); err != nil {
		return nil, err
	}
	converted, err := b.CoerceSelected(characters, target, sqltype.Implicit, raw.Pos, nil)
	if err != nil {
		return nil, err
	}
	identity, err := sqltype.PlanCoercion(converted.ResultUsage(), converted.ResultUsage(), sqltype.Implicit)
	if err != nil {
		return nil, err
	}
	return plan.CoercionExpr{Info: b.Info(converted.ResultUsage(), raw.Pos, converted.Properties()), Child: converted, Plan: identity, Mode: sqltype.Implicit, XMLSerializeResult: true}, nil
}

func (b *Binder) bindXMLDocumentPredicate(raw parser.XMLDocumentPredicateExpr) (expr.Expr, error) {
	source, err := b.xmlSource(raw.Child, b.Type(sqltype.XML).Usage(), "IS DOCUMENT", raw.Pos, nil)
	if err != nil {
		return nil, err
	}
	if b.Params.NoParameters || b.Consumer.frozenDefinition {
		raw.Child = b.NormalizeStoredExpression(raw.Child)
	}
	node := functions.XMLDocumentPredicate{Info: b.Info(b.Type(sqltype.Bool).Usage(), raw.Pos, source.Properties()), Child: source, Negated: raw.Negated, Syntax: raw}
	return node, node.ValidateStoredProvider()
}
