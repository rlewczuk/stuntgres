package sqltype

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/util"
)

var binaryHashTestNames = []string{"crc32", "crc32c", "md5", "sha224", "sha256", "sha384", "sha512"}

// Independent manual/OpenSSL vectors, validated against pinned PostgreSQL18.0.
// Never compute expected digests with the provider or Go cryptographic APIs.
var binaryHashTestVectors = []struct {
	raw       string
	crc, crcc int64
	crypto    [5]string
}{
	{"", 0, 0, [5]string{"d41d8cd98f00b204e9800998ecf8427e", "d14a028c2a3a2bc9476102bb288234c415a2b01f828ea62ac5b3e42f", "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "38b060a751ac96384cd9327eb1b1e36a21fdb71114be07434c0cc7bf63f6e1da274edebfe76f65fbd51ad2f14898b95b", "cf83e1357eefb8bdf1542850d66d8007d620e4050b5715dc83f4a921d36ce9ce47d0d13c5d85f2b0ff8318d2877eec2f63b931bd47417a81a538327af927da3e"}},
	{"abc", 891568578, 910901175, [5]string{"900150983cd24fb0d6963f7d28e17f72", "23097d223405d8228642a477bda255b32aadbce4bda0b3f7e36c9da7", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", "cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7", "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"}},
	{"\x00", 3523407757, 1383945041, [5]string{"93b885adfe0da089cdf634904fd59f71", "fff9292b4201617bdc4d3053fce02734166a683d7d858a7f5f59b073", "6e340b9cffb37a989ca544e6bb780a2c78901d3fb33738768511a30617afa01d", "bec021b4f368e3069134e012c2b4307083d3a9bdd206e24e5f0d86e13d6636655933ec2b413465966817a9c208a11717", "b8244d028981d693af7b456af8efa4cad63d282e19ff14942c246e50d9351d22704a802a71c3580b6370de4ceb293c324a8423342557d4e5c38438f0e36910ee"}},
	{"\x00\x80\xff", 3921718996, 920259282, [5]string{"edaa0390b22ab88b20932fa7a5e019ff", "dbd8a40778a553071c0c875c4d059a4959dc2b727f03ab77c0f07355", "5240672d7b51756b829ad0ef8d9468b7a078afa2f410484fd3892dab47becb72", "6c7dc7a851e8aa9dd104ee2b23706bb2cd6f49bf1bcebeb255913a96cde07fc81d24803be8989d68e52e118176864060", "ab78f768987c9de19eae63b76c8a7eb3228f170dc2b8b56c95b6a3404a317612a8751c9cf93f568e3d19b42aeb1a40b7b732d52f4c9c5e6c96fe223b3622f7e0"}},
	{"123456789", 3421780262, 3808858755, [5]string{"25f9e794323b453885f5181f1b624d0b", "9b3e61bf29f17c75572fae2e86e17809a4513d07c8a18152acf34521", "15e2b0d3c33891ebb0f1ef609ec419420c20e320ce94c65fbc8c3312448eb225", "eb455d56d2c1a69de64e832011f3393d45f3fa31d6842f21af92d2fe469c499da5e3179847334a18479c8d1dedea1be3", "d9e6762dd1c8eaf6d61b3c6192fc408d4d6d5f1176d0c29169bc24e71c3f274ad27fcd5811b313d681f7e55ec02d73d499c95455b6b5bb503acf574fba8ffe85"}},
}

func binaryHashTestPlan(t *testing.T, name string, d *Descriptor) BinaryPlan {
	t.Helper()
	result := d
	if name == "crc32" || name == "crc32c" || name == "md5" {
		id := Int8
		if name == "md5" {
			id = Text
		}
		for _, typ := range Builtins() {
			if typ.ID() == id {
				result = typ
				break
			}
		}
	}
	p, err := BindBinary(name, result.Usage(), d.Usage())
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func TestBinaryHashVectors(t *testing.T) {
	d := byteaDescriptor(t)
	for i, name := range binaryHashTestNames {
		t.Run(name, func(t *testing.T) {
			p := binaryHashTestPlan(t, name, d)
			for _, tc := range binaryHashTestVectors {
				input, err := Binary(d, tc.raw)
				if err != nil {
					t.Fatal(err)
				}
				before := input
				got, err := p.Evaluate(context.Background(), input)
				if err != nil || got.IsNull() || got.Type() != p.ResultUsage().Type || input != before {
					t.Fatal(got, err)
				}
				if i < 2 {
					want := tc.crc
					if i == 1 {
						want = tc.crcc
					}
					n, err := got.Integer()
					if err != nil || n != want {
						t.Fatal(n, want, err)
					}
				} else if i == 2 {
					s, err := got.Text()
					if err != nil || s != tc.crypto[0] || len(s) != 32 {
						t.Fatal(s, err)
					}
				} else {
					want, err := hex.DecodeString(tc.crypto[i-2])
					if err != nil {
						t.Fatal(err)
					}
					raw, err := got.Binary()
					if err != nil || raw != string(want) || len(raw) != []int{28, 32, 48, 64}[i-3] {
						t.Fatalf("digest %x want %x: %v", raw, want, err)
					}
				}
			}
			got, err := p.Evaluate(context.Background(), Null(d))
			if err != nil || !got.IsNull() || got.Type() != p.ResultUsage().Type {
				t.Fatal(got, err)
			}
		})
	}
	for _, tc := range []struct {
		n    int
		want int64
	}{{127, 291820082}, {128, 816091258}, {129, 4213642571}, {800, 3134039419}} {
		p := binaryHashTestPlan(t, "crc32c", d)
		v, _ := Binary(d, strings.Repeat("A", tc.n))
		got, err := p.Evaluate(context.Background(), v)
		if err != nil || got.integer != tc.want {
			t.Fatal(tc.n, got, err)
		}
	}
}

func TestBinaryHashInvariants(t *testing.T) {
	d, other := byteaDescriptor(t), byteaDescriptor(t)
	for _, name := range binaryHashTestNames {
		t.Run(name, func(t *testing.T) {
			p := binaryHashTestPlan(t, name, d)
			result := p.ResultUsage()
			noncanonical := d.Usage()
			noncanonical.Typmod = 7
			for _, args := range [][]Usage{nil, {d.Usage(), d.Usage()}, {result}, {noncanonical}, {Usage{}}} {
				// SHA result itself is valid BYTEA, so exercise a wrong descriptor ID instead.
				if len(args) == 1 && args[0] == d.Usage() {
					wrong := *d
					wrong.id = Int4
					args = []Usage{wrong.Usage()}
				}
				if _, err := BindBinary(name, result, args...); err == nil {
					t.Fatal("accepted invalid arguments", args)
				}
			}
			invalidResult := result
			invalidResult.Typmod = 7
			if _, err := BindBinary(name, invalidResult, d.Usage()); err == nil {
				t.Fatal("accepted noncanonical result")
			}
			if _, err := BindBinary(name, d.Usage(), d.Usage()); err == nil && result.Type.ID() != Bytea {
				t.Fatal("accepted wrong result type")
			}
			if result.Type.ID() == Bytea {
				if _, err := BindBinary(name, other.Usage(), d.Usage()); err == nil {
					t.Fatal("accepted foreign SHA result")
				}
			}
			v, _ := Binary(d, "abc")
			foreign, _ := Binary(other, "abc")
			for _, values := range [][]Value{nil, {v, v}, {foreign}, {Null(other)}, {Value{}}} {
				got, err := p.Evaluate(context.Background(), values...)
				var state interface{ SQLState() string }
				if err == nil || got != (Value{}) || errors.As(err, &state) {
					t.Fatal("invalid values did not fail ordinarily", got, err)
				}
			}
		})
	}
	if got, err := (BinaryPlan{}).Evaluate(context.Background()); err == nil || got != (Value{}) {
		t.Fatal("zero plan", got, err)
	}
}

func TestBinaryHashCancellation(t *testing.T) {
	d := byteaDescriptor(t)
	input, _ := Binary(d, strings.Repeat("\x00\x80\xffA", 8192))
	original := input
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, name := range binaryHashTestNames {
		t.Run(name, func(t *testing.T) {
			p := binaryHashTestPlan(t, name, d)
			for _, v := range []Value{input, Null(d)} {
				got, err := p.Evaluate(canceled, v)
				if !errors.Is(err, context.Canceled) || got != (Value{}) {
					t.Fatal("precancel", got, err)
				}
			}
			for _, checkpoint := range []int{4, 5, 6} {
				ctx := &mathCheckpointContext{Context: context.Background(), remaining: checkpoint}
				got, err := p.Evaluate(ctx, input)
				if !errors.Is(err, context.Canceled) || got != (Value{}) || input != original {
					t.Fatal("checkpoint cancellation", checkpoint, got, err)
				}
			}
			empty, _ := Binary(d, "")
			ctx := &mathCheckpointContext{Context: context.Background(), remaining: 2}
			got, err := p.Evaluate(ctx, empty)
			if !errors.Is(err, context.Canceled) || got != (Value{}) {
				t.Fatal("empty final cancellation", got, err)
			}
		})
	}
}

func TestBinaryHashAllocations(t *testing.T) {
	d := byteaDescriptor(t)
	small, _ := Binary(d, "abc")
	large, _ := Binary(d, strings.Repeat("\x00\x80\xffA", 1<<18))
	ctx := context.Background()
	for _, name := range binaryHashTestNames {
		t.Run(name, func(t *testing.T) {
			p := binaryHashTestPlan(t, name, d)
			var sink Value
			evaluate := func(v Value) {
				var err error
				sink, err = p.Evaluate(ctx, v)
				if err != nil {
					panic(err)
				}
			}
			smallCount := testing.AllocsPerRun(20, func() { evaluate(small) })
			largeCount := testing.AllocsPerRun(20, func() { evaluate(large) })
			if smallCount != largeCount || largeCount > 10 {
				t.Fatal("payload-scaled allocations", smallCount, largeCount)
			}
			if (name == "crc32" || name == "crc32c") && largeCount != 0 {
				t.Fatal("CRC allocations", largeCount)
			}
			// A payload-sized scratch copy can keep the allocation count constant.
			// Each byte sample averages 64 evaluations; the smallest of three
			// samples discards unrelated runtime allocations.
			bytesPerEvaluation := func(v Value) int64 {
				_, bytes := util.AllocationsPerCall(64, func() { evaluate(v) })
				return bytes
			}
			smallBytes, largeBytes := bytesPerEvaluation(small), bytesPerEvaluation(large)
			if largeBytes > 32<<10 || largeBytes > smallBytes+1024 {
				t.Fatal("payload-scaled bytes", smallBytes, largeBytes)
			}
			if sink.Type() != p.ResultUsage().Type {
				t.Fatal(sink)
			}
		})
	}
}
