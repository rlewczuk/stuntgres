package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// PgcryptoInput chooses between documented signatures using SQL usages.
// Unknown digest/HMAC data/key prefer TEXT; raw encryption always uses BYTEA.

func PgcryptoInput(name string, args []Argument, text, bytea, integer sqltype.Usage) (sqltype.Usage, Resolution) {
	count := 2
	rawCipher := name == "encrypt" || name == "decrypt" || name == "encrypt_iv" || name == "decrypt_iv"
	switch name {
	case "digest", "crypt":
	case "hmac", "encrypt", "decrypt":
		count = 3
	case "encrypt_iv", "decrypt_iv":
		count = 4
	case "gen_salt":
		if len(args) != 1 && len(args) != 2 {
			return sqltype.Usage{}, Missing
		}
		count = len(args)
	case "gen_random_bytes":
		if len(args) != 1 {
			return sqltype.Usage{}, Missing
		}
		if args[0].Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(args[0].Usage, integer, sqltype.Implicit); err != nil {
				return sqltype.Usage{}, Missing
			}
		}
		return integer, Resolved
	case "gen_random_uuid", "fips_mode":
		if len(args) == 0 {
			return text, Resolved
		}
		return sqltype.Usage{}, Missing
	default:
		return sqltype.Usage{}, Missing
	}
	if len(args) != count {
		return sqltype.Usage{}, Missing
	}
	input := text
	if rawCipher {
		input = bytea
	}
	for _, arg := range args[:count-1] {
		if (name == "digest" || name == "hmac") && arg.Usage.Type != nil && arg.Usage.Type.BaseType().ID() == sqltype.Bytea {
			input = bytea
		}
	}
	for i, arg := range args {
		if arg.Usage.Type == nil {
			continue
		}
		target := input
		if i == count-1 {
			target = text
		}
		if name == "gen_salt" && i == 1 {
			target = integer
		}
		if _, err := sqltype.PlanCoercion(arg.Usage, target, sqltype.Implicit); err != nil {
			return sqltype.Usage{}, Missing
		}
	}
	return input, Resolved
}

func CoercePgcryptoArguments(b ArgumentCoercer, name string, args []Argument, input, text, integer sqltype.Usage) ([]expr.Expr, error) {
	children := make([]expr.Expr, len(args))
	for i, arg := range args {
		target := input
		if i == len(args)-1 && name != "gen_random_bytes" {
			target = text
		}
		if name == "gen_salt" && i == 1 {
			target = integer
		}
		child, err := b.CoerceArgument(arg, CoercionRequest{Intent: DirectFormalArgument, Target: target})
		if err != nil {
			return nil, err
		}
		children[i] = child
	}
	return children, nil
}

type Pgcrypto struct {
	expr.Info
	Plan sqltype.PgcryptoPlan
	Args []expr.Expr
}

func (e Pgcrypto) Eval(c expr.Context) (sqltype.Value, error) {
	ctx, err := evaluationContext(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var values [4]sqltype.Value
	// Strictness belongs after demanded children, not before their errors/effects.
	for i, arg := range e.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
	}
	switch e.Plan.Name() {
	case "gen_random_bytes":
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		count, null, err := e.Plan.PrepareRandomBytes(values[:len(e.Args)]...)
		if err != nil {
			return sqltype.Value{}, err
		}
		if null {
			return sqltype.Null(e.Usage.Type), nil
		}
		entropyContext, ok := any(c).(interface{ FunctionPgcryptoEntropy([]byte) error })
		if !ok {
			return sqltype.Value{}, fmt.Errorf("missing pgcrypto entropy context")
		}
		entropy := make([]byte, count) // Count was validated in1..1024 above.
		if err := entropyContext.FunctionPgcryptoEntropy(entropy); err != nil {
			if canceled := ctx.Err(); canceled != nil {
				return sqltype.Value{}, canceled
			}
			return sqltype.Value{}, sqlError("XX000", "could not generate a random number")
		}
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Binary(e.Usage.Type, string(entropy))
	case "gen_random_uuid":
		source, ok := any(c).(interface {
			FunctionUUIDReady() error
			FunctionUUIDEntropy([]byte) error
		})
		if !ok {
			return sqltype.Value{}, fmt.Errorf("missing UUID entropy context")
		}
		if err := source.FunctionUUIDReady(); err != nil {
			return sqltype.Value{}, err
		}
		return uuidGenerateV4(ctx, source, e.Usage.Type)
	case "fips_mode":
		source, ok := any(c).(interface{ FunctionPgcryptoFIPSMode() bool })
		if !ok {
			return sqltype.Value{}, fmt.Errorf("missing pgcrypto FIPS context")
		}
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		mode := source.FunctionPgcryptoFIPSMode()
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Boolean(e.Usage.Type, mode), nil
	}
	if e.Plan.Name() == "gen_salt" {
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		parameters, null, err := e.Plan.PrepareSalt(values[:len(e.Args)]...)
		if err != nil {
			return sqltype.Value{}, err
		}
		if null {
			return sqltype.Null(e.Usage.Type), nil
		}
		entropyContext, ok := any(c).(interface{ FunctionPgcryptoEntropy([]byte) error })
		if !ok {
			return sqltype.Value{}, fmt.Errorf("missing pgcrypto entropy context")
		}
		var entropy [16]byte
		if err := entropyContext.FunctionPgcryptoEntropy(entropy[:parameters.EntropySize()]); err != nil {
			if canceled := ctx.Err(); canceled != nil {
				return sqltype.Value{}, canceled
			}
			return sqltype.Value{}, sqlError("22023", "gen_salt: Failed to generate strong random bits")
		}
		if err := ctx.Err(); err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.String(e.Usage.Type, sqltype.PgcryptoSalt(parameters, entropy)), nil
	}
	return e.Plan.Evaluate(ctx, values[:len(e.Args)]...)
}

func NewPgcrypto(info expr.Info, name string, args []expr.Expr) (Pgcrypto, error) {
	var usages [4]sqltype.Usage
	if len(args) > 4 {
		return Pgcrypto{}, fmt.Errorf("invalid crypto call")
	}
	for i, arg := range args {
		usages[i] = arg.ResultUsage()
	}
	plan, err := sqltype.BindPgcrypto(name, info.Usage, usages[:len(args)]...)
	return Pgcrypto{Info: info, Plan: plan, Args: args}, err
}

func (e Pgcrypto) Reconstruct(info expr.Info, args []expr.Expr) (Pgcrypto, error) {
	return NewPgcrypto(info, e.Plan.Name(), args)
}

// Pgcrypto implements expr.Composite.
var _ expr.Composite = Pgcrypto{}

func (e Pgcrypto) Children() []expr.Expr { return e.Args }
func (e Pgcrypto) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("Pgcrypto", children, len(e.Args)); err != nil {
		return nil, err
	}
	return rebuilt(e.Reconstruct(info, children))
}

func (e Pgcrypto) Validate() error {
	rebuilt, err := e.Reconstruct(e.Info, e.Args)
	if err != nil || rebuilt.Plan != e.Plan {
		return fmt.Errorf("invalid stored crypto plan")
	}
	return nil
}
