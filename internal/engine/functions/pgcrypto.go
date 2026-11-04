package functions

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// ResolvePgcrypto selects the shared formal recipe. Unknown digest/HMAC inputs
// prefer TEXT; an explicitly binary data/key selects the BYTEA overload.
func ResolvePgcrypto(name string, args []Argument, types interface {
	Type(sqltype.ID) *sqltype.Descriptor
}) (sqltype.PgcryptoSignature, Resolution) {
	binary := false
	if name == "digest" || name == "hmac" {
		for i := range max(0, len(args)-1) {
			if args[i].Usage.Type != nil && args[i].Usage.Type.BaseType().ID() == sqltype.Bytea {
				binary = true
			}
		}
	}
	s, ok := sqltype.SelectPgcryptoSignature(name, len(args), binary)
	if !ok {
		return s, Missing
	}
	for i, arg := range args {
		if arg.Usage.Type != nil {
			if _, err := sqltype.PlanCoercion(arg.Usage, types.Type(s.Inputs[i]).Usage(), sqltype.Implicit); err != nil {
				return s, Missing
			}
		}
	}
	return s, Resolved
}

func CoercePgcrypto(b ArgumentCoercer, args []Argument, formals []sqltype.Usage) ([]expr.Expr, error) {
	children := make([]expr.Expr, len(args))
	for i, arg := range args {
		child, err := b.CoerceArgument(arg, CoercionRequest{Intent: DirectFormalArgument, Target: formals[i]})
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
	body, err := e.EnterRoutine(c, values[:len(e.Args)])
	if err != nil {
		return sqltype.Value{}, err
	}
	defer body.Leave()
	var checkBuiltin func() error
	if e.Plan.Name() == "crypt" || e.Plan.Name() == "gen_salt" {
		if runtime, ok := c.(interface{ CheckPgcryptoBuiltinCrypto() error }); ok {
			checkBuiltin = runtime.CheckPgcryptoBuiltinCrypto
		} else {
			checkBuiltin = missingPgcryptoBuiltinPolicy
		}
	}
	switch e.Plan.Name() {
	case "pgp_sym_encrypt":
		call, null, err := e.Plan.PreparePGP(ctx, values[:len(e.Args)]...)
		if err != nil {
			return sqltype.Value{}, err
		}
		if null {
			return sqltype.Null(e.Usage.Type), nil
		}
		source, ok := c.(interface{ FunctionPgcryptoEntropy([]byte) error })
		if !ok {
			return sqltype.Value{}, fmt.Errorf("missing pgcrypto entropy context")
		}
		packet, err := sqltype.PgcryptoPGPEncrypt(ctx, call, source.FunctionPgcryptoEntropy)
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Binary(e.Usage.Type, packet)
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
		parameters, null, err := e.Plan.PrepareSalt(checkBuiltin, values[:len(e.Args)]...)
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
	var notice func(sqlstate.Error)
	if sink, ok := any(c).(interface{ FunctionPgcryptoNotice(sqlstate.Error) }); ok {
		notice = sink.FunctionPgcryptoNotice
	}
	return e.Plan.Evaluate(ctx, notice, checkBuiltin, values[:len(e.Args)]...)
}

func missingPgcryptoBuiltinPolicy() error {
	return fmt.Errorf("stuntgres: missing pgcrypto policy context")
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
