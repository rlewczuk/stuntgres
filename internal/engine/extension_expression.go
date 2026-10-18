package engine

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type installedFunctionExpression struct {
	functions.InstalledText
	procedure  *procedureDescriptor
	dictionary *tsDictionaryDescriptor
	template   *tsTemplateDescriptor
	ref        ids.ObjectRef
}

// Validate provider-specific captured authority without assuming a dictionary.
func (n installedFunctionExpression) validProvider() bool {
	p := n.procedure
	if p == nil || n.Arg == nil || n.Usage.Type == nil || n.Arg.ResultUsage().Type == nil || p.signature != unaccentTextSignature || n.Arg.ResultUsage().Type.ID() != sqltype.Text || ids.OID(n.Usage.Type.ID()) != p.result {
		return false
	}
	if p.fuzzy != nil {
		return p.fuzzy == stockFuzzystrmatch && p.name == "daitch_mokotoff" && p.result == 1009 && p.volatility == expr.Immutable && p.strict && p.provider == nil && p.queryCodec == nil && p.conversion.Name() == "" && p.dictionary == (ids.ObjectAddress{}) && n.dictionary == nil && n.template == nil && n.Usage.Type.IsArray() && n.Usage.Type.Element() == n.Arg.ResultUsage().Type
	}
	return p.provider == stockUnaccent && p.queryCodec == nil && p.conversion.Name() == "" && p.result == 25 && p.volatility == expr.Stable && p.strict && n.Usage.Type == n.Arg.ResultUsage().Type && n.dictionary != nil && n.template != nil && p.dictionary == n.dictionary.address && n.dictionary.template == n.template.address && p.provider == n.dictionary.provider && n.dictionary.provider == n.template.provider
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
