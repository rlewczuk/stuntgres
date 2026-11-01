package functions

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

var pgcryptoNames = [...]string{"digest", "hmac", "crypt", "gen_salt", "encrypt", "decrypt", "encrypt_iv", "decrypt_iv", "gen_random_bytes", "gen_random_uuid", "fips_mode", "armor", "dearmor"}

// bindPgcrypto binds installed pgcrypto calls through the ordinary call envelope.
// Decorations are diagnosed only after selecting an installed scalar signature.
// gen_random_uuid binds as the core builtin when pg_catalog precedes its wrapper.
func bindPgcrypto(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var single [4]Argument
	operands := single[:min(len(raw.Args), 4)]
	if len(raw.Args) > 4 && len(raw.Args) <= 100 {
		operands = make([]Argument, len(raw.Args))
	}
	for i, arg := range raw.Args {
		o, err := b.AnalyzeArgument(arg, AnalysisRequest{Intent: StringOtherArgument})
		if err != nil {
			return nil, err
		}
		if i < len(operands) {
			operands[i] = o
		}
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	names := installedArgumentNames(b, raw, operands)
	canonical := strings.ToLower(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	text, bytea := b.Type(sqltype.Text).Usage(), b.Type(sqltype.Bytea).Usage()
	recipe, resolution := ResolvePgcrypto(canonical, operands, b)
	signature := recipe.SQL
	routine, err := b.InstalledRoutine(raw.Name, PgcryptoExtension, signature)
	if err != nil {
		return nil, err
	}
	if resolution != Resolved || raw.Variadic || len(raw.ArgNames) != 0 || raw.All || raw.Star {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if _, ok := routine.(CatalogBuiltin); ok {
		return BindUUIDGeneration(b, raw, true)
	}
	member, ok := PgcryptoLookup(raw.Name.Parts[len(raw.Name.Parts)-1].Text, signature)
	if routine == nil || !ok {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if err := routine.Validate(); err != nil {
		return nil, err
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	var formals [4]sqltype.Usage
	for i := range recipe.Count {
		formals[i] = b.Type(recipe.Inputs[i]).Usage()
	}
	args, err := CoercePgcrypto(b, operands, formals[:recipe.Count])
	if err != nil {
		return nil, err
	}
	properties := expr.Properties{Volatility: member.Volatility}
	for _, arg := range args {
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	var result sqltype.Usage
	switch member.Result {
	case 17:
		result = bytea
	case 25:
		result = text
	case 2950:
		result = b.Type(sqltype.UUID).Usage()
	case 16:
		result = b.Type(sqltype.Bool).Usage()
	default:
		return nil, fmt.Errorf("stuntgres: invalid pgcrypto result")
	}
	call, err := NewPgcrypto(b.Info(result, raw.Pos, properties), canonical, args)
	if err != nil {
		return nil, err
	}
	return routine.Attach(call)
}
