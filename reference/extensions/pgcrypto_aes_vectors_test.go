package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// NIST SP800-38A Appendix F.1/F.2, four blocks, independently specified
// encryption and decryption inputs. Never learn expected bytes from native AES.
const aesPlain = "6bc1bee22e409f96e93d7e117393172aae2d8a571e03ac9c9eb76fac45af8e5130c81c46a35ce411e5fbc1191a0a52eff69f2445df4f9b17ad2b417be66c3710"
const aesIV = "000102030405060708090a0b0c0d0e0f"

var aesNIST = []struct{ key, ecb, cbc string }{
	{"2b7e151628aed2a6abf7158809cf4f3c", "3ad77bb40d7a3660a89ecaf32466ef97f5d3d58503b9699de785895a96fdbaaf43b1cd7f598ece23881b00e3ed0306887b0c785e27e8ad3f8223207104725dd4", "7649abac8119b246cee98e9b12e9197d5086cb9b507219ee95db113a917678b273bed6b8e3c1743b7116e69e222295163ff1caa1681fac09120eca307586e1a7"},
	{"8e73b0f7da0e6452c810f32b809079e562f8ead2522c6b7b", "bd334f1d6e45f25ff712a214571fa5cc974104846d0ad3ad7734ecb3ecee4eefef7afd2270e2e60adce0ba2face6444e9a4b41ba738d6c72fb16691603c18e0e", "4f021db243bc633d7178183a9fa071e8b4d9ada9ad7dedf4e5e738763f69145a571b242012fb7ae07fa9baac3df102e008b0e27988598881d920a9e64f5615cd"},
	{"603deb1015ca71be2b73aef0857d77811f352c073b6108d72d9810a30914dff4", "f3eed1bdb5d2a03c064b5a7e3db181f8591ccb10d410ed26dc5ba74a31362870b6ed21b99ca6f4f9f153e7b1beafed1d23304b7a39f9f3ff067d8d8f9e24ecc7", "f58c4c04d6e5f1ba779eabfb5f7bfbd69cfc4e967edb808d679f777bc6702c7d39f23369a9d9bacfa530e26304231461b2eb05e2c39be9fcda6c19078c6a9d1b"},
}

func aesBytes(t *testing.T, h string) []byte { return []byte(pgcryptoBytes(t, h)) }

func aesHeldResult(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, name, q, expected string, args ...any) {
	t.Helper()
	pgcryptoResult(t, p, c, r, name, q, expected, args...)
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Statement: stmt, SQL: q}, r, fixture.Select(q, []string{name}, []string{"BYTEA"}, [][]any{{pgcryptoBytes(t, expected)}}, args...))
}

func TestReferencePgcryptoAESNISTVectors(t *testing.T) {
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, v := range aesNIST {
			for _, mode := range []string{"ecb", "cbc"} {
				ct := v.ecb
				if mode == "cbc" {
					ct = v.cbc
				}
				for _, direction := range []string{"encrypt", "decrypt"} {
					input, expected := aesPlain, ct
					if direction == "decrypt" {
						input, expected = ct, aesPlain
					}
					args := []any{aesBytes(t, input), aesBytes(t, v.key), aesBytes(t, aesIV), "aes-" + mode + "/pad:none"}
					aesHeldResult(t, p, c, r, direction+"_iv", "SELECT "+direction+"_iv($1::bytea,$2::bytea,$3::bytea,$4)", expected, args...)
					if mode == "ecb" {
						aesHeldResult(t, p, c, r, direction, "SELECT "+direction+"($1::bytea,$2::bytea,$3)", expected, args[0], args[1], args[3])
					}
				}
			}
		}
	})
}

func TestReferencePgcryptoAESPostgresFixtures(t *testing.T) {
	// PostgreSQL contrib/pgcrypto expected/rijndael.out, independent fixed bytes.
	vectors := []struct{ data, key, iv, ct, typ string }{
		{"00112233445566778899aabbccddeeff", "000102030405060708090a0b0c0d0e0f", "", "69c4e0d86a7b0430d8cdb78070b4c55a", "aes-ecb/pad:none"},
		{"00112233445566778899aabbccddeeff", "000102030405060708090a0b0c0d0e0f1011121314151617", "", "dda97ca4864cdfe06eaf70a0ec0d7191", "aes-ecb/pad:none"},
		{"00112233445566778899aabbccddeeff", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", "", "8ea2b7ca516745bfeafc49904b496089", "aes-ecb/pad:none"},
		{"0011223344", "000102030405", "", "189a28932213f017b246678dbc28655f", "aes"},
		{"0011223344", "000102030405060708090a0b0c0d0e0f10111213", "", "3b02279162d15580e069d3a71407a556", "aes"},
		{"0011223344", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b", "", "4facb6a041d53e0a5a73289170901fe7", "aes"},
		{"", "666f6f", "", "b48cc3338a2eb293b6007ef72c360d48", "aes"},
		{"666f6f", "30313233343536373839", "", "f397f03d2819b7172b68d0706fda4693", "aes"},
		{"666f6f", "30313233343536373839303132333435363738393031", "", "5c9db77af02b4678117bcd8a71ae7f53", "aes"},
		{"666f6f", "30313233343536", "61626364", "2c24cb7da91d6d5699801268b0f5adad", "aes"},
		{"4c657473207472792061206c6f6e676572206d6573736167652e", "30313233343536373839", "", "d9beb785dd5403ed02f66b755bb191b93ed93ca54930153f2c3b9ec7785056ad", "aes"},
	}
	pgcryptoCase(t, true, func(t *testing.T, p *fixture.Pair, c *sql.Conn, r *fixture.Recorder, pg bool) {
		for _, v := range vectors {
			for _, direction := range []string{"encrypt", "decrypt"} {
				input, expected := v.data, v.ct
				if direction == "decrypt" {
					input, expected = v.ct, v.data
				}
				name := direction
				q := "SELECT " + name + "($1::bytea,$2::bytea,$3)"
				args := []any{aesBytes(t, input), aesBytes(t, v.key), v.typ}
				if v.iv != "" {
					name += "_iv"
					q = "SELECT " + name + "($1::bytea,$2::bytea,$3::bytea,$4)"
					args = []any{args[0], args[1], aesBytes(t, v.iv), v.typ}
				}
				aesHeldResult(t, p, c, r, name, q, expected, args...)
			}
		}
	})
}
