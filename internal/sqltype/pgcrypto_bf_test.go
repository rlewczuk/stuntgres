package sqltype

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestPgcryptoBFVectors(t *testing.T) {
	// Published PostgreSQL release and Eric Young block vectors, including
	// short/high-bit keys, endian witnesses and full448-bit key expansion.
	for _, v := range []struct{ plain, key, cipher string }{
		{"0000000000000000", "0000000000000000", "4ef997456198dd78"},
		{"ffffffffffffffff", "ffffffffffffffff", "51866fd5b85ecb8a"},
		{"1000000000000001", "3000000000000000", "7d856f9a613063f2"},
		{"0123456789abcdef", "fedcba9876543210", "0aceab0fc6a0a28d"},
		{"fedcba9876543210", "f0", "f9ad597c49db005e"},
		{"fedcba9876543210", "f0e1", "e91d21c1d961a6d6"},
		{"fedcba9876543210", "f0e1d2", "e9c2b70a1bc65cf3"},
		{"fedcba9876543210", "f0e1d2c3b4a5968778695a4b3c2d1e0f001122334455667704689104c2fd3b2f584023641aba61761f1f1f1f0e0e0e0effffffffffffffff", "c04504012e4e1f53"},
	} {
		state := pgcryptoBlowfishInitial
		words := pgcryptoBFKey(aesUnitHex(t, v.key))
		state.expand(words[:], nil)
		var plain, cipher, got [8]byte
		copy(plain[:], aesUnitHex(t, v.plain))
		copy(cipher[:], aesUnitHex(t, v.cipher))
		state.encryptBlock(&got, &plain)
		if got != cipher {
			t.Fatalf("primitive encrypt key=%s got=%x want=%x", v.key, got, cipher)
		}
		// Independent literal ciphertext, NOT the result of the call above.
		state.decryptBlock(&got, &cipher)
		if got != plain {
			t.Fatalf("primitive decrypt key=%s got=%x want=%x", v.key, got, plain)
		}
	}
	for _, v := range []struct{ plain, key, iv, typ, cipher string }{
		{"", "666f6f", "", "bf", "1871949bb2311c8e"},
		{"666f6f", "30313233343536373839", "", "blowfish-cbc", "42f58af3b2c03f46"},
		{"666f6f", "30313233343536", "61626364", "bf-cbc", "95c7e89322525d59"},
		{"0000000000000000", "0000000000000000", "ffffffffffffffff", "bf-ecb/pad:none", "4ef997456198dd78"},
		{"37363534333231204e6f77206973207468652074696d6520666f722000", "0123456789abcdeff0e1d2c3b4a59687", "fedcba9876543210", "bf-cfb", "e73214a2822139caf26ecf6d2eb9e76e3da3de04d1517200519d57a6c3"},
	} {
		for _, fn := range []string{"encrypt_iv", "decrypt_iv"} {
			input, expected := v.plain, v.cipher
			if fn == "decrypt_iv" {
				input, expected = v.cipher, v.plain
			}
			got, err := pgcryptoRaw(context.Background(), fn, aesUnitHex(t, input), aesUnitHex(t, v.key), aesUnitHex(t, v.iv), v.typ)
			if err != nil || got != aesUnitHex(t, expected) {
				t.Fatalf("%s %s got=%x err=%v want=%s", fn, v.typ, got, err, expected)
			}
		}
	}
}

func TestPgcryptoBFKeySchedule(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 55, 56, 57, 1048576} {
		raw := make([]byte, n)
		for i := range raw {
			raw[i] = byte(i*73 + 128)
		}
		got := pgcryptoBFKey(string(raw))
		// Independent raw protocol formula: the72 expansion octets cycle
		// unsigned bytes across word boundaries, after clipping the key to56.
		length := min(n, 56)
		for i, word := range got {
			var expected uint32
			for j := range 4 {
				var octet uint32
				if length != 0 {
					octet = uint32(raw[(4*i+j)%length])
				}
				expected |= octet << uint(24-8*j)
			}
			if word != expected {
				t.Fatalf("raw key length%d word%d got=%08x want=%08x", n, i, word, expected)
			}
		}
	}
	words := pgcryptoBFKey(aesUnitHex(t, "8000ff"))
	for i, word := range words {
		expected := [...]uint32{0x8000ff80, 0x00ff8000, 0xff8000ff}[i%3]
		if word != expected {
			t.Fatalf("unsigned/NUL cyclic word%d got=%08x want=%08x", i, word, expected)
		}
	}
	for _, v := range []struct{ key, cipher string }{
		{"", "09a5aa8371843981"}, {"00", "09a5aa8371843981"},
		{"8000ff", "0278e086aa7f22cf"}, {"8000ff00", "68cef14c2cfeb063"},
	} {
		got, err := pgcryptoRaw(context.Background(), "encrypt", aesUnitHex(t, "fedcba9876543210"), aesUnitHex(t, v.key), "", "bf-ecb/pad:none")
		if err != nil || got != aesUnitHex(t, v.cipher) {
			t.Fatalf("raw schedule key=%s got=%x err=%v", v.key, got, err)
		}
	}
	key := aesUnitHex(t, "f0e1d2c3b4a5968778695a4b3c2d1e0f001122334455667704689104c2fd3b2f584023641aba61761f1f1f1f0e0e0e0effffffffffffffff")
	for _, suffix := range []string{"", "\x00", strings.Repeat("\xff", 1048576)} {
		got, err := pgcryptoRaw(context.Background(), "encrypt", aesUnitHex(t, "fedcba9876543210"), key+suffix, "", "bf-ecb/pad:none")
		if err != nil || got != aesUnitHex(t, "c04504012e4e1f53") {
			t.Fatalf("clipped key length%d got=%x err=%v", len(key+suffix), got, err)
		}
	}
}

func TestPgcryptoBFPadding(t *testing.T) {
	for pad := 1; pad <= 8; pad++ {
		block := [8]byte{0xa5, 0xa5, 0xa5, 0xa5, 0xa5, 0xa5, 0xa5, 0xa5}
		for i := 8 - pad; i < 8; i++ {
			block[i] = byte(pad)
		}
		n, valid := pgcryptoRawUnpad(block[:])
		if !valid || n != 8-pad {
			t.Fatal("legal run", pad, n, valid)
		}
		if pad > 1 {
			for _, i := range []int{8 - pad, 8 - pad/2} {
				broken := block
				broken[i] ^= 1
				if _, valid := pgcryptoRawUnpad(broken[:]); valid {
					t.Fatal("incomplete run accepted", pad, i)
				}
			}
		}
	}
	for _, last := range []byte{0, 9, 255} {
		var block [8]byte
		block[7] = last
		if _, valid := pgcryptoRawUnpad(block[:]); valid {
			t.Fatal("bad final pad accepted", last)
		}
	}
	for _, mode := range []string{"bf-cbc", "bf-ecb"} {
		// Observed valid preceding-data witness; the preceding01 must stay.
		cipher := aesUnitHex(t, "cfe05dc46975bc21")
		for _, v := range []struct{ typ, expected string }{
			{mode, "0000000001"}, {mode + "/pad:none", "0000000001030303"},
		} {
			got, err := pgcryptoRaw(context.Background(), "decrypt", cipher, "foo", "", v.typ)
			if err != nil || hex.EncodeToString([]byte(got)) != v.expected {
				t.Fatal("padding boundary", v.typ, got, err)
			}
		}
	}
	for _, v := range []struct{ input, iv, typ string }{
		{"", "", "bf"}, {"00000000000000", "", "bf"}, {"000000000000000000", "", "bf"},
		{"a6ba241e5c1b6cce", "0000000000010303", "bf"},
		{"cca407d8fbafa33b", "", "bf-ecb"},
		// A valid first output block followed by invalid padding must never
		// publish that prefix. These are two independent zero-plaintext blocks.
		{"a6ba241e5c1b6ccea6ba241e5c1b6cce", "", "bf-ecb"},
	} {
		out, err := pgcryptoRaw(context.Background(), "decrypt_iv", aesUnitHex(t, v.input), "foo", aesUnitHex(t, v.iv), v.typ)
		var diagnostic *sqlstate.Error
		if out != "" || !errors.As(err, &diagnostic) || diagnostic.Code != "39000" || diagnostic.Message != "decrypt_iv error: Decryption failed" {
			t.Fatal("invalid padding published bytes or wrong diagnostic", out, err)
		}
	}
}
