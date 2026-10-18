package engine

import (
	"crypto/fips140"
	"crypto/rand"
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

type installedCryptoFunctionExpression struct {
	functions.Pgcrypto
	procedure *procedureDescriptor
	ref       ids.ObjectRef
}

// Rebuild keeps the installed-routine identity; the promoted Rebuild would
// return the bare functions node.
func (e installedCryptoFunctionExpression) Rebuild(info expressionInfo, children []BoundExpr) (BoundExpr, error) {
	node, err := e.Pgcrypto.Rebuild(info, children)
	if err != nil {
		return nil, err
	}
	e.Pgcrypto = node.(functions.Pgcrypto)
	return e, nil
}

func (c *EvalContext) FunctionPgcryptoEntropy(bytes []byte) error {
	if err := c.FunctionContext().Err(); err != nil {
		return err
	}
	_, err := rand.Read(bytes)
	return err
}

func (c *EvalContext) FunctionPgcryptoFIPSMode() bool { return fips140.Enabled() }

func (n installedCryptoFunctionExpression) validate(v CatalogRead) error {
	for _, arg := range n.Args {
		if arg == nil || arg.ResultUsage().Type == nil {
			return fmt.Errorf("stuntgres: missing stored crypto argument")
		}
	}
	proc, ok := v.Procedure(n.ref.Address)
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
		member, ok := functions.PgcryptoLookup(n.procedure.name, string(cryptoExpressionSignature(n)))
		if !ok || string(n.procedure.signature) != member.Signature || n.procedure.result != member.Result || n.procedure.volatility != member.Volatility || n.procedure.strict != member.Strict {
			return false
		}
	case coercionExpression, collationExpression, rowExpression, fieldExpression,
		comparisonExpression, arithmeticExpression, bitwiseExpression, likeExpression,
		nullTestExpression, truthTestExpression, logicalExpression, distinctExpression,
		conditionalExpression, caseExpression, scalarArrayComparisonExpression,
		functions.Binary,
		functions.String,
		functions.StringVariadic,
		functions.Format,
		functions.NullCount,
		functions.LikeEscape,
		functions.JSONCreate:
	default:
		return p.Volatility <= Stable
	}
	valid := true
	boundExpressionChildren(node, func(child BoundExpr) { valid = valid && closedPgcryptoDefault(child) })
	return valid
}
