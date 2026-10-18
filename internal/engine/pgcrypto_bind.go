package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// bindPgcrypto binds installed pgcrypto calls. They sat before the shared
// gates and own labels; every decoration fails resolution. gen_random_uuid
// binds as the core builtin when pg_catalog precedes the installed wrapper.
func bindPgcrypto(b functions.Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var operands []functions.Argument
	if len(raw.Args) <= 100 {
		operands = make([]functions.Argument, len(raw.Args))
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
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := functions.CallLabelError(raw); err != nil {
		return nil, err
	}
	names := installedArgumentNames(b, raw, operands)
	canonical := strings.ToLower(raw.Name.Parts[len(raw.Name.Parts)-1].Text)
	text, bytea, integer := b.Type(sqltype.Text).Usage(), b.Type(sqltype.Bytea).Usage(), b.Type(sqltype.Int4).Usage()
	input, resolution := functions.PgcryptoInput(canonical, operands, text, bytea, integer)
	signature := pgcryptoSignature(canonical, len(operands), input.Type != nil && input.Type.ID() == sqltype.Bytea)
	routine, err := b.InstalledRoutine(raw.Name, functions.PgcryptoExtension, signature)
	if err != nil {
		return nil, err
	}
	if resolution != functions.Resolved || raw.Variadic || len(raw.ArgNames) != 0 || raw.Distinct || raw.All || raw.Star || raw.Window != nil || raw.Filter != nil || len(raw.Order) != 0 {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if _, ok := routine.(functions.CatalogBuiltin); ok {
		return functions.BindUUIDGeneration(b, raw, true)
	}
	member, ok := functions.PgcryptoLookup(raw.Name.Parts[len(raw.Name.Parts)-1].Text, signature)
	if routine == nil || !ok {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	args, err := functions.CoercePgcryptoArguments(b, canonical, operands, input, text, integer)
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
	call, err := functions.NewPgcrypto(b.Info(result, raw.Pos, properties), canonical, args)
	if err != nil {
		return nil, err
	}
	return routine.Attach(call)
}

// pgcryptoSignature is the installed signature an admitted call selects.
func pgcryptoSignature(name string, count int, bytea bool) string {
	switch name {
	case "hmac":
		if bytea {
			return "17,17,25"
		}
		return "25,25,25"
	case "gen_salt":
		if count == 2 {
			return "25,23"
		}
		return "25"
	case "encrypt", "decrypt":
		return "17,17,25"
	case "encrypt_iv", "decrypt_iv":
		return "17,17,17,25"
	case "gen_random_bytes":
		return "23"
	case "gen_random_uuid", "fips_mode":
		return ""
	}
	if bytea {
		return "17,25"
	}
	return "25,25"
}
