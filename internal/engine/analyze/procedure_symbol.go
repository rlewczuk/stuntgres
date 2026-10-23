package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/parser"
)

// callableProviderSymbol is execution identity, never a second public name.
// Discovery has already selected the descriptor through the live name index.
func callableProviderSymbol(view catalog.Read, p *catalog.Procedure) (string, error) {
	if p == nil {
		return "", nil
	} // The existing selected typename-cast shortcut.
	if p.Builtin != nil {
		return p.Builtin.Identity.Name, nil
	}
	if p.Implementation != nil {
		return extension.ProcedureSymbol(view, p)
	}
	if p.Foreign != nil {
		return p.Foreign.Name.Name, nil
	}
	if p.Conversion.Name() != "" {
		return p.Conversion.Name(), nil
	}
	return p.Name(), nil
}

func providerFunctionSyntax(raw parser.FunctionExpr, symbol string) parser.FunctionExpr {
	last := len(raw.Name.Parts) - 1
	if last >= 0 && raw.Name.Parts[last].Text != symbol {
		parts := append([]parser.Identifier(nil), raw.Name.Parts...)
		parts[last].Text = symbol
		raw.Name.Parts = parts
	}
	return raw
}
