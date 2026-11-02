package functions

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type pgcryptoPolicyUnitContext struct {
	pgcryptoEntropyUnitContext
	denied error
	checks int
	events []string
}

// This narrow test capability anticipates the live host method without naming a
// nonexistent production API. Its injected error is an identity/ordering witness,
// not a replacement implementation of settings authority or FIPS detection.
func (c *pgcryptoPolicyUnitContext) CheckPgcryptoBuiltinCrypto() error {
	c.checks++
	c.events = append(c.events, "policy")
	return c.denied
}

func (c *pgcryptoPolicyUnitContext) FunctionPgcryptoEntropy(dst []byte) error {
	c.events = append(c.events, "entropy")
	return c.pgcryptoEntropyUnitContext.FunctionPgcryptoEntropy(dst)
}

type pgcryptoPolicyUnitArgument struct {
	expr.Info
	value sqltype.Value
	failure error
}

func (a pgcryptoPolicyUnitArgument) Eval(ec expr.Context) (sqltype.Value, error) {
	c := ec.(*pgcryptoPolicyUnitContext)
	c.events = append(c.events, "argument")
	return a.value, a.failure
}

func TestPgcryptoPolicyEvaluation(t *testing.T) {
	types := formatUnitTypes()
	text, integer, binary := types[sqltype.Text], types[sqltype.Int4], types[sqltype.Bytea]
	count, err := sqltype.Integer(integer, 4)
	if err != nil { t.Fatal(err) }
	denial := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: "use of built-in crypto functions is disabled"}
	childError := errors.New("demanded child failure")
	for _, name := range []string{"crypt", "gen_salt"} {
		for _, scenario := range []string{"denied", "strict_null", "demanded_failure", "invalid_after_gate", "allowed"} {
			t.Run(name+"_"+scenario, func(t *testing.T) {
				first, second := sqltype.String(text, "foox"), sqltype.String(text, "NB")
				if name == "gen_salt" { first, second = sqltype.String(text, "bf"), count }
				if scenario == "strict_null" || scenario == "demanded_failure" { first = sqltype.Null(text) }
				if scenario == "invalid_after_gate" {
					if name == "crypt" { second = sqltype.String(text, "$5$rounds=10$!") } else { first = sqltype.String(text, "unknown") }
				}
				args := []expr.Expr{
					pgcryptoPolicyUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: first},
					pgcryptoPolicyUnitArgument{Info: expr.Info{Usage: second.Type().Usage()}, value: second},
				}
				if scenario == "demanded_failure" { args[1] = pgcryptoPolicyUnitArgument{Info: expr.Info{Usage: second.Type().Usage()}, failure: childError} }
				call, err := NewPgcrypto(expr.Info{Usage: text.Usage()}, name, args)
				if err != nil { t.Fatal(err) }
				for range 2 {
					ctx := &pgcryptoPolicyUnitContext{pgcryptoEntropyUnitContext: pgcryptoEntropyUnitContext{Context: context.Background()}, denied: denial}
					if scenario == "allowed" { ctx.denied = nil }
					got, err := call.Eval(ctx)
					wantEvents := []string{"argument", "argument"}
					switch scenario {
					case "strict_null":
						if err != nil || !got.IsNull() || got.Type() != text || ctx.checks != 0 { t.Fatal("strict policy boundary", got, err, ctx) }
					case "demanded_failure":
						if !errors.Is(err, childError) || got.Type() != nil || ctx.checks != 0 { t.Fatal("demanded failure priority", got, err, ctx) }
					case "denied", "invalid_after_gate":
						wantEvents = append(wantEvents, "policy")
						if err != denial || got.Type() != nil || ctx.checks != 1 { t.Fatal("policy error identity/priority", got, err, ctx) }
					case "allowed":
						wantEvents = append(wantEvents, "policy")
						if name == "gen_salt" { wantEvents = append(wantEvents, "entropy") }
						if err != nil || got.Type() != text || got.IsNull() || ctx.checks != 1 { t.Fatal("allowed evaluation", got, err, ctx) }
						value, err := got.Text()
						want := "NB53EGGqrrb5E"
						if name == "gen_salt" { want = "$2a$04$......................" }
						if err != nil || value != want { t.Fatal(value, err) }
					}
					if !reflect.DeepEqual(ctx.events, wantEvents) || len(ctx.notices) != 0 { t.Fatal("evaluation frontier", ctx.events, wantEvents, ctx.notices) }
					wantEntropy := 0
					if scenario == "allowed" && name == "gen_salt" { wantEntropy = 1 }
					if ctx.calls != wantEntropy { t.Fatal("policy entropy frontier", ctx.calls, wantEntropy) }
				}
			})
		}
	}
	t.Run("SingleArgumentSaltStrictAndPolicyFrontier", func(t *testing.T) {
		for _, null := range []bool{false, true} {
			value := sqltype.String(text, "unknown")
			if null { value = sqltype.Null(text) }
			call, err := NewPgcrypto(expr.Info{Usage: text.Usage()}, "gen_salt", []expr.Expr{pgcryptoPolicyUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: value}})
			if err != nil { t.Fatal(err) }
			ctx := &pgcryptoPolicyUnitContext{pgcryptoEntropyUnitContext: pgcryptoEntropyUnitContext{Context: context.Background()}, denied: denial}
			got, err := call.Eval(ctx)
			if null {
				if err != nil || got.Type() != text || !got.IsNull() || ctx.checks != 0 { t.Fatal("one-argument strict salt", got, err, ctx) }
			} else if err != denial || got.Type() != nil || ctx.checks != 1 {
				t.Fatal("one-argument salt policy before name validation", got, err, ctx)
			}
			if ctx.calls != 0 || len(ctx.notices) != 0 { t.Fatal("one-argument salt effects", ctx) }
		}
	})
	t.Run("SameRecipeDifferentExecutionAuthority", func(t *testing.T) {
		call, err := NewPgcrypto(expr.Info{Usage: text.Usage()}, "crypt", []expr.Expr{
			pgcryptoPolicyUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, "foox")},
			pgcryptoPolicyUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, "NB")},
		})
		if err != nil { t.Fatal(err) }
		for _, denied := range []error{nil, denial, nil, denial} {
			ctx := &pgcryptoPolicyUnitContext{pgcryptoEntropyUnitContext: pgcryptoEntropyUnitContext{Context: context.Background()}, denied: denied}
			got, err := call.Eval(ctx)
			if err != denied || ctx.checks != 1 || denied == nil && got.IsNull() || denied != nil && got.Type() != nil { t.Fatal("stale policy frame", got, err, ctx) }
		}
	})
	t.Run("UnrelatedDigestNeverBorrowsPasswordPolicy", func(t *testing.T) {
		call, err := NewPgcrypto(expr.Info{Usage: binary.Usage()}, "digest", []expr.Expr{
			pgcryptoPolicyUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, "abc")},
			pgcryptoPolicyUnitArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, "sha256")},
		})
		if err != nil { t.Fatal(err) }
		ctx := &pgcryptoPolicyUnitContext{pgcryptoEntropyUnitContext: pgcryptoEntropyUnitContext{Context: context.Background()}, denied: denial}
		got, err := call.Eval(ctx)
		if err != nil || got.Type() != binary || got.IsNull() || ctx.checks != 0 || ctx.calls != 0 { t.Fatal(got, err, ctx) }
	})
}
