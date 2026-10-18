package engine

import (
	"crypto/fips140"
	"crypto/rand"
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

func (c *EvalContext) FunctionPgcryptoEntropy(bytes []byte) error {
	if err := c.FunctionContext().Err(); err != nil {
		return err
	}
	_, err := rand.Read(bytes)
	return err
}

func (c *EvalContext) FunctionPgcryptoFIPSMode() bool { return fips140.Enabled() }

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
	text, bytea, integer := b.typ(sqltype.Text).Usage(), b.typ(sqltype.Bytea).Usage(), b.typ(sqltype.Int4).Usage()
	input, resolution := functions.PgcryptoInput(canonical, operands, text, bytea, integer)
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
	if canonical == "gen_salt" {
		signature = "25"
		if len(operands) == 2 {
			signature = "25,23"
		}
	}
	if canonical == "encrypt" || canonical == "decrypt" {
		signature = "17,17,25"
	}
	if canonical == "encrypt_iv" || canonical == "decrypt_iv" {
		signature = "17,17,17,25"
	}
	if canonical == "gen_random_bytes" {
		signature = "23"
	}
	if canonical == "gen_random_uuid" || canonical == "fips_mode" {
		signature = ""
	}
	var proc *procedureDescriptor
	for i := range path.count() {
		namespace := path.at(i)
		// The implicit catalog precedes ordinary paths; explicit ordering can
		// instead select the actual installed wrapper and its dependency.
		if name == "gen_random_uuid" && namespace.ObjectID == PGCatalogNamespaceOID {
			return b.bindUUIDGeneration(raw, true)
		}
		proc = procedureInNamespaces(auth.catalog, namespaceSelection{qualified: namespace}, name, signature)
		if proc != nil {
			break
		}
	}
	if proc == nil || proc.crypto != stockPgcrypto {
		return failure()
	}
	args, err := functions.CoercePgcryptoArguments(b, canonical, operands, input, text, integer)
	if err != nil {
		return nil, err
	}
	properties := ExprProperties{Volatility: proc.volatility}
	for _, arg := range args {
		p := arg.Properties()
		properties.Volatility = max(properties.Volatility, p.Volatility)
		properties.RowDependent = properties.RowDependent || p.RowDependent
		properties.ParameterDependent = properties.ParameterDependent || p.ParameterDependent
		properties.StageDependent = properties.StageDependent || p.StageDependent
		properties.DomainDependent = properties.DomainDependent || p.DomainDependent
		properties.RuntimeDomainConversion = properties.RuntimeDomainConversion || p.RuntimeDomainConversion
	}
	var result sqltype.Usage
	switch proc.result {
	case 17:
		result = bytea
	case 25:
		result = text
	case 2950:
		result = b.typ(sqltype.UUID).Usage()
	case 16:
		result = b.typ(sqltype.Bool).Usage()
	default:
		return nil, fmt.Errorf("stuntgres: invalid pgcrypto result")
	}
	info := b.info(result, raw.Pos, properties)
	info.routineReference = proc
	call, err := functions.NewPgcrypto[BoundExpr, *EvalContext](functionInfo(info), canonical, args)
	if err != nil {
		return nil, err
	}
	ref := ObjectRef{owner: b.engine.domain, Epoch: auth.catalog.Epoch(), Address: proc.address}
	b.dependency(ref, proc.revision)
	return installedCryptoFunctionExpression{Pgcrypto: call, procedure: proc, ref: ref}, nil
}

func (n installedCryptoFunctionExpression) validate(v CatalogRead) error {
	for _, arg := range n.Args {
		if arg == nil || arg.ResultUsage().Type == nil {
			return fmt.Errorf("stuntgres: missing stored crypto argument")
		}
	}
	proc, ok := v.state.descriptors[n.ref.Address].(*procedureDescriptor)
	if !ok || proc != n.procedure || proc.crypto != stockPgcrypto || proc.name != n.Plan.Name() || proc.signature != cryptoExpressionSignature(n) {
		return fmt.Errorf("stuntgres: invalid stored crypto identity")
	}
	if err := stockPgcrypto.validateProcedure(v, proc); err != nil {
		return err
	}
	return n.Validate()
}

func cryptoExpressionSignature(n installedCryptoFunctionExpression) procedureSignature {
	if n.Plan.Name() == "gen_random_bytes" {
		if len(n.Args) == 1 && n.Args[0].ResultUsage().Type.ID() == sqltype.Int4 {
			return "23"
		}
		return ""
	}
	if n.Plan.Name() == "gen_salt" {
		if len(n.Args) == 1 {
			return "25"
		}
		if len(n.Args) == 2 {
			return "25,23"
		}
		return ""
	}
	if len(n.Args) == 4 && (n.Plan.Name() == "encrypt_iv" || n.Plan.Name() == "decrypt_iv") {
		return "17,17,17,25"
	}
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

// Only genuine installed crypto calls introduce volatility here. Like the
// existing UUID/time default owners, recurse through supported scalar wrappers
// without admitting unrelated volatile producers or frame-dependent recipes.
func closedPgcryptoDefault(node BoundExpr) bool {
	if node == nil {
		return true
	}
	p := node.Properties()
	if p.RowDependent || p.ParameterDependent || p.StageDependent {
		return false
	}
	switch n := node.(type) {
	case installedCryptoFunctionExpression:
		if n.procedure == nil || n.procedure.crypto != stockPgcrypto || n.ref.Address != n.procedure.address || n.ref.Epoch == 0 || n.Plan.Name() != n.procedure.name || n.Validate() != nil {
			return false
		}
		member, ok := pgcryptoLookup(n.procedure.name, cryptoExpressionSignature(n))
		if !ok || n.procedure.signature != member.signature || n.procedure.result != member.result || n.procedure.volatility != member.volatility || n.procedure.strict != member.strict {
			return false
		}
	case coercionExpression, collationExpression, rowExpression, fieldExpression,
		comparisonExpression, arithmeticExpression, bitwiseExpression, likeExpression,
		nullTestExpression, truthTestExpression, logicalExpression, distinctExpression,
		conditionalExpression, caseExpression, scalarArrayComparisonExpression,
		functions.Binary[BoundExpr, *EvalContext, ExprProperties],
		functions.String[BoundExpr, *EvalContext, ExprProperties],
		functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin],
		functions.Format[BoundExpr, *EvalContext, ExprProperties],
		functions.NullCount[BoundExpr, *EvalContext, ExprProperties],
		functions.LikeEscape[BoundExpr, *EvalContext, ExprProperties],
		functions.JSONCreate[BoundExpr, *EvalContext, ExprProperties]:
	default:
		return p.Volatility <= Stable
	}
	valid := true
	boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedPgcryptoDefault(child) })
	return valid
}
