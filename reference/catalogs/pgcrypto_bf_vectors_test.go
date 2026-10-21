package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

const bfCFBKey = "0123456789abcdeff0e1d2c3b4a59687"
const bfCFBIV = "fedcba9876543210"
const bfCFBPlain = "37363534333231204e6f77206973207468652074696d6520666f722000"
const bfCFBCipher = "e73214a2822139caf26ecf6d2eb9e76e3da3de04d1517200519d57a6c3"
const bfFullKey = "f0e1d2c3b4a5968778695a4b3c2d1e0f001122334455667704689104c2fd3b2f584023641aba61761f1f1f1f0e0e0e0effffffffffffffff"

func bfVector(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, plain, key, iv, typ, ciphertext string) {
	t.Helper()
	for _, direction := range []string{"encrypt", "decrypt"} {
		input, expected := plain, ciphertext
		if direction == "decrypt" {
			input, expected = ciphertext, plain
		}
		name := direction + "_iv"
		q := "SELECT " + name + "($1::bytea,$2::bytea,$3::bytea,$4)"
		aesHeldResult(t, p, c, r, name, q, expected, aesBytes(t, input), aesBytes(t, key), aesBytes(t, iv), typ)
		if iv == "" || strings.Contains(typ, "ecb") {
			q = "SELECT " + direction + "($1::bytea,$2::bytea,$3)"
			aesHeldResult(t, p, c, r, direction, q, expected, aesBytes(t, input), aesBytes(t, key), typ)
		}
	}
}

func TestReferencePgcryptoBFVectors(t *testing.T) {
	// Independent PostgreSQL REL_18_6 expected/blowfish.out and Eric Young's
	// vectors-2.txt (Schneier). Literal ciphertext also supplies decrypt input.
	vectors := []struct{ plain, key, iv, typ, ciphertext string }{
		{"0000000000000000", "0000000000000000", "", "bf-ecb/pad:none", "4ef997456198dd78"},
		{"ffffffffffffffff", "ffffffffffffffff", "", "bf-ecb/pad:none", "51866fd5b85ecb8a"},
		{"1000000000000001", "3000000000000000", "", "bf-ecb/pad:none", "7d856f9a613063f2"},
		{"1111111111111111", "1111111111111111", "", "bf-ecb/pad:none", "2466dd878b963c9d"},
		{"0123456789abcdef", "fedcba9876543210", "", "bf-ecb/pad:none", "0aceab0fc6a0a28d"},
		{"01a1d6d039776742", "fedcba9876543210", "", "bf-ecb/pad:none", "3273b8badc9e9e15"},
		{"ffffffffffffffff", "0000000000000000", "", "bf-ecb/pad:none", "014933e0cdaff6e4"},
		{"fedcba9876543210", "f0", "", "bf-ecb/pad:none", "f9ad597c49db005e"},
		{"fedcba9876543210", "f0e1", "", "bf-ecb/pad:none", "e91d21c1d961a6d6"},
		{"fedcba9876543210", "f0e1d2", "", "bf-ecb/pad:none", "e9c2b70a1bc65cf3"},
		{"fedcba9876543210", bfFullKey, "", "bf-ecb/pad:none", "c04504012e4e1f53"},
		{"01234567890123456789", "33443344334433443344334433443344", "", "bf-ecb", "0d04a43a20456dee5ede6ed9e4dcaaa6"},
		{"", "666f6f", "", "bf", "1871949bb2311c8e"},
		{"666f6f", "30313233343536373839", "", "bf", "42f58af3b2c03f46"},
		{"666f6f", "30313233343536373839303132333435363738393031", "", "bf", "86ab6f0bc72b5f22"},
		{"666f6f", "30313233343536", "61626364", "bf", "95c7e89322525d59"},
		{"4c657473207472792061206c6f6e676572206d6573736167652e", "30313233343536373839", "", "bf", "a76059f7a1b627b5b84080d9beb337714c7a7f8b70300023e5feb6dfa6813536"},
		{"6b77b4d63006dee605b156e27403979358deb9e7154616d959f1652bd5", "37363534333231204e6f77206973207468652074696d6520666f7220", "", "bf-cbc", "4f2beb748c4f689ec755edb9dc252a41b93a3786850b4c75d6a702b6a8e48825"},
		{"6b77b4d63006dee605b156e27403979358deb9e7154616d959f1652bd5ff92cc", "37363534333231204e6f77206973207468652074696d6520666f722000", "", "bf-cbc", "3ea6357a0ee7fad6d0c4b63464f2aafa40c2e91b4b7e1bba8114932fd92b5c8f111e7e50e7b2e541"},
	}
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for i, v := range vectors {
			t.Run(fmt.Sprint(i), func(t *testing.T) { bfVector(t, p, c, r, v.plain, v.key, v.iv, v.typ, v.ciphertext) })
		}
		for _, typ := range []string{"bf", "blowfish", "bf-cbc", "blowfish-cbc", "BF-CBC/PAD:PKCS"} {
			bfVector(t, p, c, r, "666f6f", "30313233343536373839", "", typ, "42f58af3b2c03f46")
		}
		for _, typ := range []string{"bf-ecb/pad:none", "blowfish-ecb/pad:none", "BLOWFISH-ECB/PAD:NONE"} {
			bfVector(t, p, c, r, "0000000000000000", "0000000000000000", "", typ, "4ef997456198dd78")
		}
	})
}

func TestReferencePgcryptoBFCFB64(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		// Full 29-byte CFB64 vector distinguishes OFB and CFB8; each prefix is
		// independently implied by the published streaming construction.
		for _, typ := range []string{"bf-cfb", "blowfish-cfb", "BF-CFB/PAD:PKCS", "bf-cfb/pad:none"} {
			for _, n := range []int{0, 1, 7, 8, 9, 16, 29} {
				t.Run(fmt.Sprintf("%s/%d", typ, n), func(t *testing.T) {
					bfVector(t, p, c, r, bfCFBPlain[:n*2], bfCFBKey, bfCFBIV, typ, bfCFBCipher[:n*2])
				})
			}
		}
		for _, direction := range []string{"encrypt", "decrypt"} {
			q := "SELECT " + direction + "($1::bytea,$2::bytea,$3)"
			pgcryptoResult(t, p, c, r, direction, q, "", []byte{}, aesBytes(t, bfCFBKey), "bf-cfb")
		}
		// Pinned18.6 observations: corruption is unauthenticated and changes
		// the current segment and next feedback block, not the whole tail.
		changed := aesBytes(t, bfCFBCipher)
		changed[0] ^= 1
		aesHeldResult(t, p, c, r, "decrypt_iv", "SELECT decrypt_iv($1::bytea,$2::bytea,$3::bytea,$4)", "3636353433323120a40f76d2ec913f9568652074696d6520666f722000", changed, aesBytes(t, bfCFBKey), aesBytes(t, bfCFBIV), "bf-cfb")
		ivCiphertexts := map[int]string{
			0:  "30c6ce1ab13da990407f5761de0f41a7c6bc3106d084c5dea26e2f7ad0",
			1:  "30c6ce1ab13da990407f5761de0f41a7c6bc3106d084c5dea26e2f7ad0",
			7:  "51ff8cc6e220e1867b07cfa6def5c05cfa4c814eb9173cf72d713e08e1",
			8:  "1abcd31c1d9dbe2a865afa261e7a8a687e074b5c12e5c422369aae4ce9",
			9:  "1abcd31c1d9dbe2a865afa261e7a8a687e074b5c12e5c422369aae4ce9",
			64: "1abcd31c1d9dbe2a865afa261e7a8a687e074b5c12e5c422369aae4ce9",
		}
		for _, n := range []int{0, 1, 7, 8, 9, 64} {
			iv := aesSequence(n)
			effective := make([]byte, 8)
			copy(effective, iv)
			for _, value := range [][]byte{iv, effective} {
				for _, typ := range []string{"bf-cfb", "bf-cfb/pad:pkcs", "bf-cfb/pad:none"} {
					bfVector(t, p, c, r, bfCFBPlain, bfCFBKey, fmt.Sprintf("%x", value), typ, ivCiphertexts[n])
				}
			}
		}
		for _, typ := range []string{"bf-cfb", "bf-cfb/pad:pkcs", "bf-cfb/pad:none"} {
			aesHeldResult(t, p, c, r, "encrypt", "SELECT encrypt($1::bytea,$2::bytea,$3)", ivCiphertexts[0], aesBytes(t, bfCFBPlain), aesBytes(t, bfCFBKey), typ)
			aesHeldResult(t, p, c, r, "decrypt", "SELECT decrypt($1::bytea,$2::bytea,$3)", bfCFBPlain, aesBytes(t, ivCiphertexts[0]), aesBytes(t, bfCFBKey), typ)
		}
	})
}
