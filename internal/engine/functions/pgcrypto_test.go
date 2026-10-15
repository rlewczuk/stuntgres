package functions

import (
	"context"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPgcryptoOverloadResolution(t *testing.T) {
	types := formatUnitTypes()
	text, binary := types[sqltype.Text].Usage(), types[sqltype.Bytea].Usage()
	for _, test := range []struct {
		name       string
		ids        []sqltype.ID
		input      sqltype.ID
		resolution Resolution
	}{
		{"digest", []sqltype.ID{0, 0}, sqltype.Text, Resolved},
		{"digest", []sqltype.ID{sqltype.Bytea, 0}, sqltype.Bytea, Resolved},
		{"hmac", []sqltype.ID{0, 0, 0}, sqltype.Text, Resolved},
		{"hmac", []sqltype.ID{0, sqltype.Bytea, 0}, sqltype.Bytea, Resolved},
		{"hmac", []sqltype.ID{sqltype.Bytea, sqltype.Text, 0}, 0, Missing},
		{"hmac", []sqltype.ID{sqltype.Text, sqltype.Bytea, 0}, 0, Missing},
		{"digest", []sqltype.ID{sqltype.Int4, 0}, 0, Missing},
		{"digest", []sqltype.ID{0}, 0, Missing},
		{"crypt", []sqltype.ID{0, 0}, sqltype.Text, Resolved},
		{"crypt", []sqltype.ID{sqltype.Bytea, 0}, 0, Missing},
		{"gen_salt", []sqltype.ID{0}, sqltype.Text, Resolved},
		{"gen_salt", []sqltype.ID{0, 0}, sqltype.Text, Resolved},
		{"gen_salt", []sqltype.ID{sqltype.Text, sqltype.Int2}, sqltype.Text, Resolved},
		{"gen_salt", []sqltype.ID{sqltype.Text, sqltype.Int8}, 0, Missing},
	} {
		args := make([]Argument[struct{}], len(test.ids))
		for i, id := range test.ids {
			if id != 0 {
				args[i].Usage = types[id].Usage()
			}
		}
		input, resolution := PgcryptoInput(test.name, args, text, binary, types[sqltype.Int4].Usage())
		if resolution != test.resolution || resolution == Resolved && input.Type.ID() != test.input {
			t.Fatal(test, input, resolution)
		}
	}
}

func TestPgcryptoRawVectors(t *testing.T) {
	types := formatUnitTypes()
	text, binary := types[sqltype.Text], types[sqltype.Bytea]
	// RFC2202/RFC4231 and independently specified abc digest vectors.
	vectors := []struct {
		algorithm, digest, mac, block, over string
		size                                int
	}{
		{"md5", "900150983cd24fb0d6963f7d28e17f72", "750c783e6ab0b503eaa86e310a5db738", "81a6963c6f25e3002c2372247c99ecb1", "f962fb84546b784c85ce2fd0d005514a", 64},
		{"sha1", "a9993e364706816aba3e25717850c26c9cd0d89d", "effcdf6ae5eb2fa2d27416d5f184df9c259a7c79", "c8ac140f19c8efb2881da4546329460847217eba", "10f3fcd6d8720e551b639cbc84f8436a17d0498c", 64},
		{"sha224", "23097d223405d8228642a477bda255b32aadbce4bda0b3f7e36c9da7", "a30e01098bc6dbbf45690f3a7e9e6d0f8bbea2a39e6148008fd05e44", "0f75fbe2675baf79b7e93a95daa79527774513d89acd3e7e5331a6ae", "e810c30c52e6afdfc8f7474368a190cf41aa9332a717de8afda4edab", 64},
		{"sha256", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", "5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843", "2f8cff867f2668ca93d3c5b03ba9f816746742eda349b3bc4bb35aa27816754c", "102ebc7ceb9dff09e5a77413a3cd67382908ece255487006eb0e1e1b801f9ce6", 64},
		{"sha384", "cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7", "af45d2e376484031617f78d2b58a6b1b9c7ef464f5a01b47e42ec3736322445e8e2240ca5e69e2c78b3239ecfab21649", "b6b4e107425289d77b04f0857d531600348f725f48baeeb129718cc6b4e22012fb406ce85dc8c9f64765336e7d1b6afa", "1cd03e84204a37f498f949a0a5a1d36015c6f6685734003ea238d98c223d566c3b849da8f82ff808afb310cdf8de8386", 128},
		{"sha512", "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f", "164b7a7bfcf819e2e395fbe73b56e0a387bd64222e831fd610270cd7ea2505549758bf75c05a994a6d034f65f8f0e6fdcaeab1a34d4a6b4b636e070a38bce737", "92cc1e875510916b8f80a8f09db8c48769e2c248c917eb7ed03b7df45fe61e48019e2d035fa4a910391b7f7387b535d350f9c16cb346ab578d199ab6e559f960", "91da74be2b02e8d40c63431a2ada5c236d8b9afc19bfb7823e2361dd12ecf452fca3a6983cbb53067afd75fb31d075755990913f8a08ee307fc713f142498bf8", 128},
	}
	for _, vector := range vectors {
		for _, input := range []*sqltype.Descriptor{text, binary} {
			value := func(raw string) sqltype.Value {
				if input == text {
					return sqltype.String(text, raw)
				}
				v, err := sqltype.Binary(binary, raw)
				if err != nil {
					t.Fatal(err)
				}
				return v
			}
			for _, test := range []struct{ name, data, key, expected string }{
				{"digest", "abc", "", vector.digest},
				{"hmac", "what do ya want for nothing?", "Jefe", vector.mac},
				{"hmac", "abc", strings.Repeat("\xaa", vector.size), vector.block},
				{"hmac", "abc", strings.Repeat("\xaa", vector.size+1), vector.over},
			} {
				usages := []sqltype.Usage{input.Usage()}
				values := []sqltype.Value{value(test.data)}
				if test.name == "hmac" {
					usages = append(usages, input.Usage())
					values = append(values, value(test.key))
				}
				usages = append(usages, text.Usage())
				values = append(values, sqltype.String(text, vector.algorithm))
				plan, err := sqltype.BindPgcrypto(test.name, binary.Usage(), usages...)
				if err != nil {
					t.Fatal(err)
				}
				got, err := plan.Evaluate(context.Background(), values...)
				if err != nil {
					t.Fatal(err)
				}
				raw, err := got.Binary()
				if err != nil || hex.EncodeToString([]byte(raw)) != test.expected {
					t.Fatal(vector.algorithm, test.name, test.expected, raw, err)
				}
			}
		}
	}
}

func TestPgcryptoStrictDemand(t *testing.T) {
	types := formatUnitTypes()
	text, binary := types[sqltype.Text], types[sqltype.Bytea]
	var order []int
	args := []binaryUnitExpression{
		binaryUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 0, literal: true},
		binaryUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, value: sqltype.String(text, "key"), order: &order, index: 1},
		binaryUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, value: sqltype.String(text, "NO_SUCH_HASH"), order: &order, index: 2},
	}
	call, err := NewPgcrypto[binaryUnitExpression, *binaryUnitContext](Info[struct{}]{Usage: binary.Usage()}, "hmac", args)
	if err != nil {
		t.Fatal(err)
	}
	got, err := call.Eval(&binaryUnitContext{Context: context.Background()})
	if err != nil || !got.IsNull() || got.Type() != binary || !reflect.DeepEqual(order, []int{0, 1, 2}) {
		t.Fatal(got, order, err)
	}
	failure := errors.New("demanded sibling")
	args[1] = binaryUnitArgument{Info: Info[struct{}]{Usage: text.Usage()}, err: failure, order: &order, index: 1}
	order = nil
	if _, err := call.Eval(&binaryUnitContext{Context: context.Background()}); !errors.Is(err, failure) || !reflect.DeepEqual(order, []int{0, 1}) {
		t.Fatal(order, err)
	}
}

// A deterministic cancellation frontier exercises streaming without timing races.
type cryptoCancelContext struct {
	context.Context
	calls, after int
}

func (c *cryptoCancelContext) Err() error {
	c.calls++
	if c.calls >= c.after {
		return context.Canceled
	}
	return nil
}

func TestPgcryptoCancellation(t *testing.T) {
	types := formatUnitTypes()
	text, binary := types[sqltype.Text], types[sqltype.Bytea]
	for _, name := range []string{"digest", "hmac"} {
		usages := []sqltype.Usage{text.Usage()}
		values := []sqltype.Value{sqltype.String(text, strings.Repeat("a", 16384))}
		if name == "hmac" {
			usages = append(usages, text.Usage())
			values = append(values, sqltype.String(text, strings.Repeat("k", 16384)))
		}
		usages = append(usages, text.Usage())
		values = append(values, sqltype.String(text, "sha512"))
		plan, err := sqltype.BindPgcrypto(name, binary.Usage(), usages...)
		if err != nil {
			t.Fatal(err)
		}
		for _, after := range []int{1, 3, 5} {
			ctx := &cryptoCancelContext{Context: context.Background(), after: after}
			got, err := plan.Evaluate(ctx, values...)
			if !errors.Is(err, context.Canceled) || got.Type() != nil {
				t.Fatal(name, after, got, err)
			}
		}
	}
}
