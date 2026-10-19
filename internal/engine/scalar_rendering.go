package engine

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// rendering replays a protocol node from its expr.Rendering. The node states
// its stored form; this renderer owns quoting, literal transport and the
// checks against engine state. Arguments render in order before the form is
// assembled, so literal capture and dependencies follow the argument order.
func (r scalarRenderer) rendering(ctx context.Context, view catalog.Read, spec expr.Rendering) (string, error) {
	replay := spec.Replay
	if replay.RelationOnly && r.owner == nil {
		return "", fmt.Errorf("stuntgres: volatile stored default")
	}
	if replay.Validate != nil {
		if err := replay.Validate(); err != nil {
			return "", err
		}
	}
	for _, t := range replay.Types {
		if t == nil {
			return "", fmt.Errorf("stuntgres: foreign stored function type")
		}
		if found, ok := definitionTypeAt(view, r.definition, t.ID()); !ok || found != t {
			return "", fmt.Errorf("stuntgres: foreign stored function type")
		}
	}
	args := spec.Args
	if replay.Args != nil {
		args = replay.Args
	}
	if replay.CatalogSnapshot && r.commentSnapshot != nil {
		*r.commentSnapshot = true
	}
	if replay.VisibilitySnapshot && r.visibilitySnapshot != nil {
		// A literal NULL argument decides the call without reading.
		demand := true
		for _, arg := range args {
			if randomLiteralNull(arg.Expr) {
				demand = false
				break
			}
		}
		*r.visibilitySnapshot = *r.visibilitySnapshot || demand
	}
	parts := make([]string, len(args))
	for i, arg := range args {
		if arg.Absorbed {
			if err := r.recordStoredRoutine(view, arg.Expr); err != nil {
				return "", err
			}
			continue
		}
		text, err := r.replayArgument(ctx, view, arg)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	if replay.Pieces != nil {
		return r.replayTemplate(replay.Pieces, args, parts)
	}
	switch spec.Form {
	case expr.FormLeaf:
		if replay.Text != "" {
			return replay.Text, nil
		}
		return spec.Text, nil
	case expr.FormCall:
		for i, arg := range args {
			if arg.Label != "" {
				label := arg.Label
				if spec.LabelQuote != expr.QuoteVerbatim {
					label = catalog.QuoteIdentifier(label)
				}
				parts[i] = label + " => " + parts[i]
			}
			if spec.Variadic && i == len(args)-1 {
				parts[i] = "VARIADIC " + parts[i]
			}
		}
		name := spec.Name
		if replay.Name != "" {
			name = replay.Name
		}
		return "pg_catalog." + name + "(" + strings.Join(parts, ", ") + ")", nil
	case expr.FormInfix:
		if len(parts) != 2 {
			return "", fmt.Errorf("stuntgres: invalid stored operator operands")
		}
		op := spec.Name
		if spec.Namespace != "" {
			namespace := pgIdentifier(spec.Namespace)
			if replay.QuoteNamespace {
				namespace = catalog.QuoteIdentifier(spec.Namespace)
			}
			op = "OPERATOR(" + namespace + "." + op + ")"
		}
		return "(" + parts[0] + " " + op + " " + parts[1] + ")", nil
	case expr.FormTemplate:
		return r.replayTemplate(spec.Pieces, args, parts)
	default:
		return "", fmt.Errorf("stuntgres: unsupported stored default node")
	}
}

// replayTemplate assembles keyword syntax over the rendered arguments.
func (r scalarRenderer) replayTemplate(pieces []expr.Piece, args []expr.RenderArg, parts []string) (string, error) {
	return templateSQL(pieces, parts, func(piece expr.Piece) (string, error) {
		if piece.Identifier {
			return pgIdentifier(piece.Text), nil
		}
		literal, ok := args[piece.Arg].Expr.(literalExpression)
		if !ok {
			return "", fmt.Errorf("stuntgres: invalid normalization syntax form")
		}
		return literal.value.Text()
	})
}

// templateSQL assembles template pieces over already rendered arguments; word
// renders identifier and value pieces.
func templateSQL(pieces []expr.Piece, parts []string, word func(expr.Piece) (string, error)) (string, error) {
	var text strings.Builder
	for _, piece := range pieces {
		switch {
		case piece.Identifier:
		case piece.Text != "":
			text.WriteString(piece.Text)
			continue
		case piece.Arg < 0 || piece.Arg >= len(parts):
			return "", fmt.Errorf("stuntgres: invalid stored syntax argument")
		case !piece.Value:
			text.WriteString(parts[piece.Arg])
			continue
		}
		part, err := word(piece)
		if err != nil {
			return "", err
		}
		text.WriteString(part)
	}
	return text.String(), nil
}

// replayArgument renders one argument with the transport that keeps its type
// on replay.
func (r scalarRenderer) replayArgument(ctx context.Context, view catalog.Read, arg expr.RenderArg) (string, error) {
	argRenderer := r
	if arg.Transport == expr.TransportIntervalNulls {
		argRenderer.uuidShiftIntervalNulls = true
	}
	text, err := argRenderer.sql(ctx, view, arg.Expr)
	if err != nil {
		return "", err
	}
	declaration, err := r.transportDeclaration(view, arg)
	if err != nil || declaration == "" {
		return text, err
	}
	return "CAST(" + text + " AS " + declaration + ")", nil
}

// transportDeclaration is the type a replayed argument is cast to, or "".
// Literal transports leave arguments alone while CHECK rebinding transports
// literals privately.
func (r scalarRenderer) transportDeclaration(view catalog.Read, arg expr.RenderArg) (string, error) {
	if arg.Transport == expr.TransportCast {
		return catalogTypeDeclaration(view, arg.TransportType)
	}
	literal, ok := arg.Expr.(literalExpression)
	if !ok || r.checkLiterals != nil {
		return "", nil
	}
	id := literal.Usage.Type.ID()
	switch arg.Transport {
	case expr.TransportName:
		if id == sqltype.Name {
			return "NAME", nil
		}
	case expr.TransportLiteral:
		if !literal.Usage.Type.IsDomain() {
			return catalogTypeDeclaration(view, literal.Usage)
		}
	case expr.TransportAmbiguous, expr.TransportAmbiguousText:
		if literal.UnknownNull || literal.Usage.Type.IsDomain() {
			break
		}
		spelled := id == sqltype.Text || id == sqltype.Int4 || id == sqltype.Bool || id == sqltype.Regclass || id == sqltype.Regtype || sqltype.IsOperatorAlias(id) || sqltype.IsRegTS(id) || id == sqltype.Bit
		if !spelled || id == sqltype.Text && arg.Transport == expr.TransportAmbiguousText {
			return catalogTypeDeclaration(view, literal.Usage)
		}
	}
	return "", nil
}

// rawSyntaxWord rejects identifier and value pieces, which raw SQL-standard
// call spellings never contain.
func rawSyntaxWord(expr.Piece) (string, error) {
	return "", fmt.Errorf("stuntgres: invalid SQL binary syntax")
}
