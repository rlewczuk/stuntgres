package functions

import (
	"errors"
	"fmt"

	"stuntgres/internal/engine/expr"
)

// Shared builders for the nodes' expr.Rendering values.

// unsupportedReplay refuses nodes stored scalar SQL has no form for, such as
// set-returning calls.
func unsupportedReplay() error { return errors.New("stuntgres: unsupported stored default node") }

// refuseReplay is a replay check that always fails with message.
func refuseReplay(message string) func() error {
	return func() error { return errors.New(message) }
}

// catalogArguments reports whether an argument's type holds catalog
// identifiers, so a call over it reads catalog names.
func catalogArguments(args []expr.Expr) bool {
	for _, arg := range args {
		if catalogArgument(arg) {
			return true
		}
	}
	return false
}

func catalogArgument(arg expr.Expr) bool {
	if arg == nil {
		return false
	}
	t := arg.ResultUsage().Type
	return t != nil && t.ContainsCatalogIdentifier()
}

// missingArgument reports a nil argument.
func missingArgument(args []expr.Expr) bool {
	for _, arg := range args {
		if arg == nil {
			return true
		}
	}
	return false
}

// transportArgs sets the replay transport of every argument.
func transportArgs(args []expr.RenderArg, transport expr.Transport) {
	for i := range args {
		args[i].Transport = transport
	}
}

// callRendering is an ordinary call whose arguments show implicit coercions.
func callRendering(tree string, inputs []expr.Expr, name string, args []expr.Expr) expr.Rendering {
	return expr.Rendering{Tree: tree, Inputs: inputs, Form: expr.FormCall, Name: name, Args: renderArgs(args)}
}

// renderArgs lists call arguments that show their implicit coercions.
func renderArgs(args []expr.Expr) []expr.RenderArg {
	out := make([]expr.RenderArg, len(args))
	for i, arg := range args {
		out[i] = expr.RenderArg{Expr: arg}
	}
	return out
}

// hiddenArgs lists keyword-syntax arguments and operands, which hide their
// implicit coercions.
func hiddenArgs(args ...expr.Expr) []expr.RenderArg {
	out := make([]expr.RenderArg, len(args))
	for i, arg := range args {
		out[i] = expr.RenderArg{Expr: arg, HideImplicit: true}
	}
	return out
}

// listedArgs lists arguments in function-argument position.
func listedArgs(args []expr.Expr) []expr.RenderArg {
	out := renderArgs(args)
	for i := range out {
		out[i].Listed = true
	}
	return out
}

// writtenArgs lists the arguments a call wrote, in written order with their
// labels. slots maps each written argument to its formal slot below limit;
// unique rejects a slot written twice.
func writtenArgs(args []expr.Expr, slots []uint8, labels []string, limit int, unique bool, failure string) ([]expr.RenderArg, error) {
	out := make([]expr.RenderArg, len(slots))
	supplied := make([]bool, limit)
	for i, s := range slots {
		slot := int(s)
		if slot >= limit || unique && supplied[slot] || args[slot] == nil {
			return nil, fmt.Errorf("%s", failure)
		}
		supplied[slot] = true
		out[i] = expr.RenderArg{Expr: args[slot], Label: labels[i]}
	}
	return out, nil
}

// writtenCall is a call rendered from writtenArgs, with minimally quoted labels.
func writtenCall(tree string, inputs []expr.Expr, name string, args []expr.RenderArg, variadic bool) expr.Rendering {
	return expr.Rendering{Tree: tree, Inputs: inputs, Form: expr.FormCall, Name: name, Quote: expr.QuoteVerbatim, LabelQuote: expr.QuoteWhenNeeded, Args: args, Variadic: variadic}
}

// infixRendering is an operator whose operands hide implicit coercions.
func infixRendering(tree string, inputs []expr.Expr, namespace, operator string, left, right expr.Expr) expr.Rendering {
	return expr.Rendering{Tree: tree, Inputs: inputs, Form: expr.FormInfix, Name: operator, Namespace: namespace, Args: hiddenArgs(left, right)}
}

// SyntaxPieces are the SQL-standard spellings of OVERLAY, SUBSTRING, POSITION
// and TRIM over count arguments; the TRIM forms list the source first.
func SyntaxPieces(name string, count int) ([]expr.Piece, error) {
	text, arg := expr.TextPiece, expr.ArgPiece
	switch name {
	case "overlay":
		if count != 3 && count != 4 {
			break
		}
		pieces := []expr.Piece{text("OVERLAY("), arg(0), text(" PLACING "), arg(1), text(" FROM "), arg(2)}
		if count == 4 {
			pieces = append(pieces, text(" FOR "), arg(3))
		}
		return append(pieces, text(")")), nil
	case "substring":
		if count != 2 && count != 3 {
			break
		}
		pieces := []expr.Piece{text("SUBSTRING("), arg(0), text(" FROM "), arg(1)}
		if count == 3 {
			pieces = append(pieces, text(" FOR "), arg(2))
		}
		return append(pieces, text(")")), nil
	case "position":
		if count != 2 {
			break
		}
		return []expr.Piece{text("POSITION(("), arg(1), text(") IN ("), arg(0), text("))")}, nil
	case "btrim", "ltrim", "rtrim":
		if count != 2 {
			break
		}
		return TrimPieces(name, count)
	}
	return nil, fmt.Errorf("stuntgres: invalid SQL binary syntax")
}

// TrimPieces is TRIM(direction [set] FROM source) over (source[, set]).
func TrimPieces(name string, count int) ([]expr.Piece, error) {
	direction := ""
	switch name {
	case "btrim":
		direction = "BOTH"
	case "ltrim":
		direction = "LEADING"
	case "rtrim":
		direction = "TRAILING"
	default:
		return nil, fmt.Errorf("stuntgres: invalid trim syntax provider")
	}
	pieces := []expr.Piece{expr.TextPiece("TRIM(" + direction)}
	if count == 2 {
		pieces = append(pieces, expr.TextPiece(" "), expr.ArgPiece(1))
	} else if count != 1 {
		return nil, fmt.Errorf("stuntgres: invalid trim syntax arity")
	}
	return append(pieces, expr.TextPiece(" FROM "), expr.ArgPiece(0), expr.TextPiece(")")), nil
}
