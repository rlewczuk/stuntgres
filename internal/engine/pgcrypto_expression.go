package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type installedCryptoFunctionExpression struct {
	functions.Pgcrypto[BoundExpr, *EvalContext, ExprProperties]
	procedure *procedureDescriptor
	ref       ObjectRef
}

func (b *expressionBinder) bindInstalledCrypto(raw parser.FunctionExpr) (BoundExpr, error) {
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
	text, bytea := b.typ(sqltype.Text).Usage(), b.typ(sqltype.Bytea).Usage()
	input, resolution := functions.PgcryptoInput(canonical, operands, text, bytea)
	if resolution != functions.Resolved || raw.Variadic || len(raw.ArgNames) != 0 || raw.Distinct || raw.All || raw.Star || raw.Window != nil || raw.Filter != nil || len(raw.Order) != 0 {
		return failure()
	}
	signature := procedureSignature("25,25")
	if input.Type.ID() == sqltype.Bytea {
		signature = "17,25"
	}
	if canonical == "hmac" {
		signature = "25,25,25"
		if input.Type.ID() == sqltype.Bytea {
			signature = "17,17,25"
		}
	}
	proc := procedureInNamespaces(auth.catalog, path, name, signature)
	if proc == nil || proc.crypto != stockPgcrypto {
		return failure()
	}
	args, err := functions.CoercePgcryptoArguments(b, operands, input, text)
	if err != nil {
		return nil, err
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
	call, err := functions.NewPgcrypto[BoundExpr, *EvalContext](functionInfo(b.info(bytea, raw.Pos, properties)), canonical, args)
	if err != nil {
		return nil, err
	}
	ref := ObjectRef{owner: b.engine.owner, Epoch: auth.catalog.Epoch(), Address: proc.address}
	b.dependencies = append(b.dependencies, expressionDependency{ref: ref, revision: proc.revision})
	return installedCryptoFunctionExpression{Pgcrypto: call, procedure: proc, ref: ref}, nil
}

func (n installedCryptoFunctionExpression) validate(v CatalogRead) error {
	for _, arg := range n.Args {
		if arg == nil || arg.ResultUsage().Type == nil {
			return fmt.Errorf("stuntgres: missing stored crypto argument")
		}
	}
	proc, ok := v.state.descriptors[n.ref.Address].(*procedureDescriptor)
	if !ok || proc != n.procedure || proc.crypto != stockPgcrypto || proc.name != n.Plan.Name() || proc.signature != cryptoExpressionSignature(n) || proc.result != 17 {
		return fmt.Errorf("stuntgres: invalid stored crypto identity")
	}
	if err := stockPgcrypto.validateProcedure(v, proc); err != nil {
		return err
	}
	return n.Validate()
}

func cryptoExpressionSignature(n installedCryptoFunctionExpression) procedureSignature {
	if len(n.Args) == 2 {
		if n.Args[0].ResultUsage().Type.ID() == sqltype.Bytea {
			return "17,25"
		}
		return "25,25"
	}
	if len(n.Args) == 3 {
		if n.Args[0].ResultUsage().Type.ID() == sqltype.Bytea {
			return "17,17,25"
		}
		return "25,25,25"
	}
	return ""
}
