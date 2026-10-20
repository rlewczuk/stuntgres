package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type installedFunctionExpression struct {
	functions.InstalledText
	procedure  *catalog.Procedure
	dictionary *catalog.TSDictionary
	template   *catalog.TSTemplate
	ref        ids.ObjectRef
}

// Validate provider-specific captured authority without assuming a dictionary.
func (n installedFunctionExpression) validProvider() bool {
	p := n.procedure
	if p == nil || n.Arg == nil || n.Usage.Type == nil || n.Arg.ResultUsage().Type == nil || p.Signature != catalog.UnaccentTextSignature || n.Arg.ResultUsage().Type.ID() != sqltype.Text || ids.OID(n.Usage.Type.ID()) != p.Result {
		return false
	}
	if FuzzystrmatchProviderOf(p) != nil {
		return FuzzystrmatchProviderOf(p) == StockFuzzystrmatch && p.Name() == "daitch_mokotoff" && p.Result == 1009 && p.Volatility == expr.Immutable && p.Strict && p.Conversion.Name() == "" && p.Dictionary == (ids.ObjectAddress{}) && n.dictionary == nil && n.template == nil && n.Usage.Type.IsArray() && n.Usage.Type.Element() == n.Arg.ResultUsage().Type
	}
	return UnaccentProviderOf(p) == StockUnaccent && p.Conversion.Name() == "" && p.Result == 25 && p.Volatility == expr.Stable && p.Strict && n.Usage.Type == n.Arg.ResultUsage().Type && n.dictionary != nil && n.template != nil && p.Dictionary == n.dictionary.Address() && n.dictionary.Template == n.template.Address() && UnaccentProviderOf(p) == UnaccentDictionaryProviderOf(n.dictionary) && UnaccentDictionaryProviderOf(n.dictionary) == UnaccentTemplateProviderOf(n.template)
}

func (n installedFunctionExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	if err := evaluationContext(evalContext(ctx)); err != nil {
		return sqltype.Value{}, err
	}
	if !n.validProvider() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid installed function expression")
	}
	return n.InstalledText.Eval(ctx)
}
