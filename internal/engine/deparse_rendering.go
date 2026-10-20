package engine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// rendering displays a protocol node from its expr.Rendering. The node states
// its shape; this renderer owns quoting, casts and parenthesization. r is the
// renderer after sql reset its implicit-cast display.
func (r pgRenderer) rendering(ctx context.Context, node expr.Expr, spec expr.Rendering, parent pgRenderParent) (string, error) {
	return r.renderingWithReturning(ctx, node, spec, parent, sqltype.Usage{})
}

func (r pgRenderer) renderingWithReturning(ctx context.Context, node expr.Expr, spec expr.Rendering, parent pgRenderParent, returning sqltype.Usage) (string, error) {
	switch spec.Form {
	case expr.FormLeaf:
		return spec.Text, nil
	case expr.FormCall:
		parts := make([]string, len(spec.Args))
		for i, arg := range spec.Args {
			text, err := r.renderingArg(ctx, spec, arg, pgRenderParent{functionArgument: arg.Listed})
			if err != nil {
				return "", err
			}
			if arg.Label != "" {
				text = r.renderingName(arg.Label, spec.LabelQuote) + " => " + text
			}
			if spec.Variadic && i == len(spec.Args)-1 {
				text = "VARIADIC " + text
			}
			parts[i] = text
		}
		return r.renderingName(spec.Name, spec.Quote) + "(" + strings.Join(parts, ", ") + ")", nil
	case expr.FormInfix:
		return r.renderingInfix(ctx, spec, parent)
	case expr.FormTemplate:
		var text strings.Builder
		for _, piece := range spec.Pieces {
			if piece.Returning {
				if returning.Type != nil {
					declaration, err := r.typeSQL(returning)
					if err != nil {
						return "", err
					}
					text.WriteString(piece.Text)
					text.WriteString(declaration)
					text.WriteString(piece.Suffix)
				}
				continue
			}
			if piece.Identifier {
				text.WriteString(r.identifier(piece.Text))
				continue
			}
			if piece.Text != "" {
				text.WriteString(piece.Text)
				continue
			}
			if piece.Arg < 0 || piece.Arg >= len(spec.Args) {
				return "", fmt.Errorf("stuntgres: invalid deparse syntax argument")
			}
			arg := spec.Args[piece.Arg]
			if piece.Value {
				literal, ok := arg.Expr.(plan.LiteralExpr)
				if !ok {
					return "", fmt.Errorf("stuntgres: invalid deparse syntax keyword")
				}
				word, err := literal.Value.Text()
				if err != nil {
					return "", err
				}
				text.WriteString(word)
				continue
			}
			part, err := r.renderingArg(ctx, spec, arg, pgRenderParent{functionArgument: arg.Listed})
			if err != nil {
				return "", err
			}
			text.WriteString(part)
		}
		return text.String(), nil
	default:
		return "", fmt.Errorf("stuntgres: unsupported deparse node %T", node)
	}
}

func (r pgRenderer) renderingInfix(ctx context.Context, spec expr.Rendering, parent pgRenderParent) (string, error) {
	if len(spec.Args) != 2 {
		return "", fmt.Errorf("stuntgres: invalid deparse operator operands")
	}
	op := r.operatorSpelling(spec.Namespace, spec.Name)
	var operands [2]string
	for i, arg := range spec.Args {
		operandParent := pgRenderParent{functionArgument: arg.Listed}
		if spec.Grouping == expr.GroupPrecedence {
			operandParent = pgRenderParent{precedence: 5, operator: op, right: i == 1}
		}
		text, err := r.renderingArg(ctx, spec, arg, operandParent)
		if err != nil {
			return "", err
		}
		operands[i] = text
	}
	text := operands[0] + " " + op + " " + operands[1]
	grouped := true
	switch spec.Grouping {
	case expr.GroupPrecedence:
		grouped = !r.pretty || pgOperatorGrouped(op, true, parent)
	case expr.GroupNested:
		grouped = pgOperatorGrouped(spec.Name, true, parent)
	}
	if grouped {
		text = "(" + text + ")"
	}
	return text, nil
}

func (r pgRenderer) renderingArg(ctx context.Context, spec expr.Rendering, arg expr.RenderArg, parent pgRenderParent) (string, error) {
	if arg.JSONFormattedSource {
		arg.Expr = sqlJSONSource(arg.Expr)
	}
	argRenderer := r
	argRenderer.showImplicit = !arg.HideImplicit
	if spec.UnknownTexts {
		argRenderer.unknownTextLiteral = arg.UnknownText
	}
	text, err := argRenderer.sql(ctx, arg.Expr, parent)
	if err != nil || arg.Cast.Type == nil {
		return text, err
	}
	simple := false
	switch arg.Expr.(type) {
	case plan.ColumnExpr, plan.LiteralExpr:
		simple = arg.CastForm != expr.CastParenthesized
	case plan.CoercionExpr:
		simple = arg.CastForm == expr.CastLeafOrCoercion
	}
	return r.cast(text, arg.Cast, simple)
}

func (r pgRenderer) renderingName(name string, quote expr.Quote) string {
	switch quote {
	case expr.QuoteVerbatim:
		return name
	case expr.QuoteIdentifier:
		return r.identifier(name)
	case expr.QuoteWhenNeeded:
		return pgIdentifier(name)
	default:
		if r.output.QuoteAllIdentifiers && !strings.HasPrefix(name, "\"") {
			return r.identifier(name)
		}
		return name
	}
}

// operatorSpelling displays an operator, qualified outside pg_catalog.
func (r pgRenderer) operatorSpelling(namespace, spelling string) string {
	if namespace == "" || namespace == "pg_catalog" {
		return spelling
	}
	return "OPERATOR(" + r.identifier(namespace) + "." + spelling + ")"
}
