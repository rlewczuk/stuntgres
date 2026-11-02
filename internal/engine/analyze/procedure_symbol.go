package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/parser"
)

// callableProviderBindingName selects the immutable internal binding recipe,
// never a public alias. Installed registry gateways and specialized engine
// families independently select their SQL recipe or executable symbol.
func callableProviderBindingName(view catalog.Read, p *catalog.Procedure) (string, error) {
	if p == nil {
		return "", nil
	} // The existing selected typename-cast shortcut.
	if p.Builtin != nil {
		return p.Builtin.Identity.Name, nil
	}
	if p.Implementation != nil {
		return extension.ProcedureBindingName(view, p)
	}
	if p.Foreign != nil {
		return p.Foreign.Name.Name, nil
	}
	if p.Conversion.Name() != "" {
		return p.Conversion.Name(), nil
	}
	return p.Name(), nil
}

func providerFunctionSyntax(raw parser.FunctionExpr, binding string) parser.FunctionExpr {
	last := len(raw.Name.Parts) - 1
	if last >= 0 && raw.Name.Parts[last].Text != binding {
		parts := append([]parser.Identifier(nil), raw.Name.Parts...)
		parts[last].Text = binding
		raw.Name.Parts = parts
	}
	return raw
}
