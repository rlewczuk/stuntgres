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

func pgpEvaluationCall(t *testing.T, name, options string) Pgcrypto {
	t.Helper()
	types := formatUnitTypes()
	args := []pgcryptoRandomArgument{
		{Info: expr.Info{Usage: types[sqltype.Text].Usage()}, value: sqltype.String(types[sqltype.Text], "message")},
		{Info: expr.Info{Usage: types[sqltype.Text].Usage()}, value: sqltype.String(types[sqltype.Text], "password")},
		{Info: expr.Info{Usage: types[sqltype.Text].Usage()}, value: sqltype.String(types[sqltype.Text], options)},
	}
	if name == "pgp_sym_encrypt_bytea" {
		args[0].Info.Usage = types[sqltype.Bytea].Usage()
		args[0].value, _ = sqltype.Binary(types[sqltype.Bytea], "message")
	}
	call, err := NewPgcrypto(expr.Info{Usage: types[sqltype.Bytea].Usage()}, name, unitArgs(args))
	if err != nil {
		t.Fatal(err)
	}
	return call
}

func TestPgcryptoPGPEntropy(t *testing.T) {
	for _, name := range []string{"pgp_sym_encrypt", "pgp_sym_encrypt_bytea"} {
		for _, options := range []string{"s2k-count=1024", "unknown=1"} {
			for nullIndex := range 3 {
				call := pgpEvaluationCall(t, name, options)
				child := call.Args[nullIndex].(pgcryptoRandomArgument)
				child.value = sqltype.Null(child.value.Type())
				call.Args[nullIndex] = child
				ctx := &pgcryptoRandomContext{Context: context.Background()}
				value, err := call.Eval(ctx)
				if err != nil || !value.IsNull() || value.Type() != call.Usage.Type || len(ctx.requests) != 0 || !reflect.DeepEqual(ctx.order, []int{0, 0, 0}) {
					t.Fatal("strict PGP NULL entropy/demand", err, ctx.requests, ctx.order)
				}
			}
		}
		for _, options := range []string{"unknown=1", "s2k-count=1023", "cipher-algo=twofish", "compress-algo=3"} {
			call := pgpEvaluationCall(t, name, options)
			ctx := &pgcryptoRandomContext{Context: context.Background()}
			value, err := call.Eval(ctx)
			if err == nil || value.Type() != nil || len(ctx.requests) != 0 {
				t.Fatal("invalid PGP operation requested entropy", err, ctx.requests)
			}
		}
		call := pgpEvaluationCall(t, name, "s2k-count=1024")
		initialPlan := call.Plan
		ctx := &pgcryptoRandomContext{Context: context.Background(), fill: 7}
		value, err := call.Eval(ctx)
		if err != nil || value.Type() != call.Usage.Type || value.IsNull() || !reflect.DeepEqual(ctx.requests, []int{25}) || call.Plan != initialPlan {
			t.Fatal("PGP execution entropy/signature retention", err, ctx.requests)
		}
		if err := call.Validate(); err != nil {
			t.Fatal(err)
		}
		child := call.Args[1].(pgcryptoRandomArgument)
		child.value = sqltype.String(child.value.Type(), "")
		call.Args[1] = child
		ctx = &pgcryptoRandomContext{Context: context.Background()}
		value, err = call.Eval(ctx)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "39000" || diagnostic.Message != "Illegal argument to function" || value.Type() != nil || len(ctx.requests) != 0 {
			t.Fatal("empty password entropy", err, ctx.requests)
		}
	}
}

func TestPgcryptoPGPSecurity(t *testing.T) {
	for _, name := range []string{"pgp_sym_encrypt", "pgp_sym_encrypt_bytea"} {
		call := pgpEvaluationCall(t, name, "s2k-count=1024")
		first := call.Args[0].(pgcryptoRandomArgument)
		first.value = sqltype.Null(first.value.Type())
		call.Args[0] = first
		last := call.Args[2].(pgcryptoRandomArgument)
		last.failure = errors.New("demanded child error")
		call.Args[2] = last
		ctx := &pgcryptoRandomContext{Context: context.Background()}
		value, err := call.Eval(ctx)
		if !errors.Is(err, last.failure) || value.Type() != nil || len(ctx.requests) != 0 || len(ctx.order) != 3 {
			t.Fatal("strict NULL suppressed demanded error", err)
		}
		call = pgpEvaluationCall(t, name, "s2k-count=1024")
		ctx = &pgcryptoRandomContext{Context: context.Background(), failure: errors.New("source failed")}
		value, err = call.Eval(ctx)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "XX000" || diagnostic.Message != "could not generate a random number" || value.Type() != nil {
			t.Fatal("entropy error publication", err)
		}
		canceled, cancel := context.WithCancel(context.Background())
		ctx = &pgcryptoRandomContext{Context: canceled, cancel: cancel}
		value, err = call.Eval(ctx)
		if !errors.Is(err, context.Canceled) || value.Type() != nil {
			t.Fatal("entropy cancellation ownership", err)
		}
	}
}

func TestPgcryptoPGPResolution(t *testing.T) {
	types := formatUnitTypes()
	for _, tc := range []struct {
		name   string
		ids    []sqltype.ID
		result sqltype.ID
	}{
		{"pgp_sym_encrypt", []sqltype.ID{sqltype.Text, sqltype.Text}, sqltype.Bytea},
		{"pgp_sym_encrypt", []sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Text}, sqltype.Bytea},
		{"pgp_sym_decrypt", []sqltype.ID{sqltype.Bytea, sqltype.Text}, sqltype.Text},
		{"pgp_sym_decrypt", []sqltype.ID{sqltype.Bytea, sqltype.Text, sqltype.Text}, sqltype.Text},
		{"pgp_sym_encrypt_bytea", []sqltype.ID{sqltype.Bytea, sqltype.Text}, sqltype.Bytea},
		{"pgp_sym_encrypt_bytea", []sqltype.ID{sqltype.Bytea, sqltype.Text, sqltype.Text}, sqltype.Bytea},
		{"pgp_sym_decrypt_bytea", []sqltype.ID{sqltype.Bytea, sqltype.Text}, sqltype.Bytea},
		{"pgp_sym_decrypt_bytea", []sqltype.ID{sqltype.Bytea, sqltype.Text, sqltype.Text}, sqltype.Bytea},
	} {
		args := make([]Argument, len(tc.ids))
		for i, id := range tc.ids {
			args[i].Usage = types[id].Usage()
		}
		s, got := ResolvePgcrypto(tc.name, args, pgcryptoUnitTypes(types))
		if got != Resolved || s.Result != tc.result || s.Count != len(args) {
			t.Fatal("PGP formal resolution", tc.name, got)
		}
		for i, id := range tc.ids {
			if s.Inputs[i] != id {
				t.Fatal("PGP formal type", i)
			}
		}
	}
}
