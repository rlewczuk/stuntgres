package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type installedSoundexFunctionExpression struct {
	functions.Soundex[BoundExpr, *EvalContext, ExprProperties]
	procedure *procedureDescriptor
	ref       ObjectRef
}

func (b *expressionBinder) bindInstalledSoundex(raw parser.FunctionExpr) (BoundExpr, error) {
	var operands []functions.Argument[BoundExpr]
	if len(raw.Args) <= 100 {
		operands = make([]functions.Argument[BoundExpr], len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, functions.AnalysisRequest{Intent: functions.StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if operands != nil {
			operands[i] = o
		}
	}
	if len(raw.Args) > 100 {
		return nil, expressionError("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	issue := functions.FunctionLabels(raw)
	if issue.Malformed {
		return nil, fmt.Errorf("invalid function argument labels")
	}
	if issue.Positional {
		return nil, expressionError("42601", "positional argument cannot follow named argument", issue.Position)
	}
	if issue.Duplicate != "" {
		return nil, expressionError("42601", fmt.Sprintf("argument name %q used more than once", issue.Duplicate), issue.Position)
	}
	names := make([]string, len(operands))
	for i, o := range operands {
		names[i] = "unknown"
		if o.Usage.Type != nil {
			names[i] = b.diagnosticTypeName(o.Usage.Type)
		}
		names[i] = functionNamedType(raw, i, names[i])
	}
	failure := func() (BoundExpr, error) { return nil, b.functionSignatureErrorForTypes(raw.Name, raw.Pos, names) }
	auth, err := b.bindingAuthorization()
	if err != nil {
		return nil, err
	}
	path, err := b.engine.lookupNamespacesSnapshot(auth, raw.Name, installedFunctionLookup)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	canonical := strings.ToLower(name)
	text := b.typ(sqltype.Text).Usage()
	if functions.SoundexInput(canonical, operands, text) != functions.Resolved || raw.Variadic || len(raw.ArgNames) != 0 || raw.Star {
		return failure()
	}
	signature := procedureSignature(functions.SoundexSignature(canonical, len(operands)))
	proc := procedureInNamespaces(auth.catalog, path, name, signature)
	if proc == nil || proc.fuzzy != stockFuzzystrmatch {
		return failure()
	}
	args, err := functions.CoerceSoundexArguments(b, operands, text)
	if err != nil {
		return nil, err
	}
	switch {
	case raw.Distinct:
		return nil, expressionError("42809", "DISTINCT specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
	case len(raw.Order) != 0:
		return nil, expressionError("42809", "ORDER BY specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
	case raw.Filter != nil:
		return nil, expressionError("42809", "FILTER specified, but "+objectNameText(raw.Name)+" is not an aggregate function", raw.Pos)
	case raw.Window != nil:
		return nil, expressionError("42809", "OVER specified, but "+objectNameText(raw.Name)+" is not a window function nor an aggregate function", raw.Pos)
	}
	properties := ExprProperties{Volatility: Immutable}
	for _, arg := range args {
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	result := text
	if canonical == "difference" {
		result = b.typ(sqltype.Int4).Usage()
	}
	info := b.info(result, raw.Pos, properties)
	info.routineReference = proc
	call, err := functions.NewSoundex[BoundExpr, *EvalContext](functionInfo(info), canonical, args)
	if err != nil {
		return nil, err
	}
	ref := b.engine.domain.Ref(auth.catalog.Epoch(), proc.address)
	b.dependencies = append(b.dependencies, expressionDependency{ref: ref, revision: proc.revision})
	return installedSoundexFunctionExpression{Soundex: call, procedure: proc, ref: ref}, nil
}

func (n installedSoundexFunctionExpression) validate(v CatalogRead) error {
	for _, arg := range n.Args {
		if arg == nil || arg.ResultUsage().Type == nil {
			return fmt.Errorf("stuntgres: missing stored Soundex argument")
		}
	}
	result := OID(25)
	if n.Function == "difference" {
		result = 23
	}
	proc, ok := v.state.descriptors[n.ref.Address].(*procedureDescriptor)
	if !ok || proc != n.procedure || proc.fuzzy != stockFuzzystrmatch || proc.name != n.Function || proc.signature != procedureSignature(functions.SoundexSignature(n.Function, len(n.Args))) || proc.result != result {
		return fmt.Errorf("stuntgres: invalid stored Soundex identity")
	}
	if err := stockFuzzystrmatch.validateProcedure(v, proc); err != nil {
		return err
	}
	return n.Validate()
}
