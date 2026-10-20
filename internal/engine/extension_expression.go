package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Validate provider-specific captured authority without assuming a dictionary.
func validInstalledText(n plan.InstalledTextExpr) bool {
	p := n.Procedure
	if p == nil || n.Arg == nil || n.Usage.Type == nil || n.Arg.ResultUsage().Type == nil || p.Signature != catalog.UnaccentTextSignature || n.Arg.ResultUsage().Type.ID() != sqltype.Text || ids.OID(n.Usage.Type.ID()) != p.Result {
		return false
	}
	if extension.FuzzystrmatchProviderOf(p) != nil {
		return extension.FuzzystrmatchProviderOf(p) == extension.StockFuzzystrmatch && p.Name() == "daitch_mokotoff" && p.Result == 1009 && p.Volatility == expr.Immutable && p.Strict && p.Conversion.Name() == "" && p.Dictionary == (ids.ObjectAddress{}) && n.Dictionary == nil && n.Template == nil && n.Usage.Type.IsArray() && n.Usage.Type.Element() == n.Arg.ResultUsage().Type
	}
	return extension.UnaccentProviderOf(p) == extension.StockUnaccent && p.Conversion.Name() == "" && p.Result == 25 && p.Volatility == expr.Stable && p.Strict && n.Usage.Type == n.Arg.ResultUsage().Type && n.Dictionary != nil && n.Template != nil && p.Dictionary == n.Dictionary.Address() && n.Dictionary.Template == n.Template.Address() && extension.UnaccentProviderOf(p) == extension.UnaccentDictionaryProviderOf(n.Dictionary) && extension.UnaccentDictionaryProviderOf(n.Dictionary) == extension.UnaccentTemplateProviderOf(n.Template)
}
