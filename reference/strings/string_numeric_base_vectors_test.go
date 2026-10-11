package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

// PostgreSQL18 Table9.10 and unsigned 32/64-bit arithmetic, independently
// expanded into literals. Never derive expected values with a native formatter.
var stringNumericBaseVectors = []struct {
	typ, input, bin, oct, hex string
}{
	{"integer", "0", "0", "0", "0"},
	{"bigint", "0", "0", "0", "0"},
	{"integer", "1", "1", "1", "1"},
	{"bigint", "1", "1", "1", "1"},
	{"integer", "42", "101010", "52", "2a"},
	{"bigint", "42", "101010", "52", "2a"},
	{"integer", "2147483647", "1111111111111111111111111111111", "17777777777", "7fffffff"},
	{"integer", "-2147483648", "10000000000000000000000000000000", "20000000000", "80000000"},
	{"integer", "-1", "11111111111111111111111111111111", "37777777777", "ffffffff"},
	{"integer", "-1234", "11111111111111111111101100101110", "37777775456", "fffffb2e"},
	{"bigint", "2147483648", "10000000000000000000000000000000", "20000000000", "80000000"},
	{"bigint", "4294967295", "11111111111111111111111111111111", "37777777777", "ffffffff"},
	{"bigint", "9223372036854775807", "111111111111111111111111111111111111111111111111111111111111111", "777777777777777777777", "7fffffffffffffff"},
	{"bigint", "-9223372036854775808", "1000000000000000000000000000000000000000000000000000000000000000", "1000000000000000000000", "8000000000000000"},
	{"bigint", "-1", "1111111111111111111111111111111111111111111111111111111111111111", "1777777777777777777777", "ffffffffffffffff"},
	{"bigint", "-1234", "1111111111111111111111111111111111111111111111111111101100101110", "1777777777777777775456", "fffffffffffffb2e"},
}

func TestReferenceStringNumericBaseVectors(t *testing.T) {
	stringNumericBaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, v := range stringNumericBaseVectors {
			t.Run(v.typ+"/"+v.input, func(t *testing.T) {
				arg := "'" + v.input + "'::" + v.typ
				q := "SELECT to_bin(" + arg + "),to_oct(" + arg + "),to_hex(" + arg + ")"
				stringNumericBaseBoth(t, p, b, c, stringNumericBaseText(q, []string{"to_bin", "to_oct", "to_hex"}, [][]any{{v.bin, v.oct, v.hex}}))
			})
		}
		for _, typ := range []string{"integer", "bigint"} {
			q := "SELECT to_bin(NULL::" + typ + "),to_oct(NULL::" + typ + "),to_hex(NULL::" + typ + ")"
			stringNumericBaseBoth(t, p, b, c, stringNumericBaseText(q, []string{"to_bin", "to_oct", "to_hex"}, [][]any{{nil, nil, nil}}))
		}
		for _, v := range []struct{ input, want string }{
			{"2147483647", "7fffffff"}, {"2147483648", "80000000"}, {"4294967295", "ffffffff"}, {"9223372036854775807", "7fffffffffffffff"}, {"-1", "ffffffff"},
		} {
			stringNumericBaseBoth(t, p, b, c, stringNumericBaseText("SELECT to_hex("+v.input+")", []string{"to_hex"}, [][]any{{v.want}}))
		}
	})
}

// First seven vectors are RFC1321 AppendixA.5. UTF8 and trailing-space
// constants were obtained independently with printf | openssl dgst -md5,
// 2026-10-10, before the pinned PostgreSQL discovery run.
var stringNumericBaseMD5Vectors = []struct{ input, digest string }{
	{"", "d41d8cd98f00b204e9800998ecf8427e"},
	{"a", "0cc175b9c0f1b6a831c399e269772661"},
	{"abc", "900150983cd24fb0d6963f7d28e17f72"},
	{"message digest", "f96b697d7cb7938d525a2f31aaf161d0"},
	{"abcdefghijklmnopqrstuvwxyz", "c3fcd3d76192e4007dfb496cca67e13b"},
	{"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789", "d174ab98d277d9f5a5611c2c9f419d9f"},
	{"12345678901234567890123456789012345678901234567890123456789012345678901234567890", "57edf4a22be3c955ac49da2e2107b67a"},
	{"abc ", "28a53e303da9f5742476fd6b62434540"},
	{"é", "66ddcd97cfdeabb2f6fb8a999b4bc76f"},
	{"e\u0301", "5526861fbb1e71a1bda6ac364310a807"},
	{"🙂", "5c8d6d302301d0e25c0e051418dff305"},
}

func TestReferenceStringNumericBaseMD5Vectors(t *testing.T) {
	stringNumericBaseScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, v := range stringNumericBaseMD5Vectors {
			t.Run(v.input, func(t *testing.T) {
				stringNumericBaseBoth(t, p, b, c, stringNumericBaseText("SELECT md5('"+v.input+"')", []string{"md5"}, [][]any{{v.digest}}))
				stringNumericBaseBoth(t, p, b, c, stringNumericBaseText("SELECT md5($1::text)", []string{"md5"}, [][]any{{v.digest}}, v.input))
			})
		}
		for _, tc := range []struct {
			expr string
			want any
		}{
			{"md5(NULL)", nil}, {"md5(NULL::text)", nil}, {"md5(NULL::bytea)", nil},
			{"md5('abc '::character(4))", "900150983cd24fb0d6963f7d28e17f72"},
			{"md5('abc '::varchar(4))", "28a53e303da9f5742476fd6b62434540"},
			{"md5('abc '::text)", "28a53e303da9f5742476fd6b62434540"},
			{"md5('abc'::name)", "900150983cd24fb0d6963f7d28e17f72"},
			{`md5('a'::"char")`, "0cc175b9c0f1b6a831c399e269772661"},
			{`md5('\x0')`, "d8e6a5626b620a84d69e8052951a080a"},
			{`md5('\x00'::bytea)`, "93b885adfe0da089cdf634904fd59f71"},
			{`md5('\x0080ff'::bytea)`, "edaa0390b22ab88b20932fa7a5e019ff"},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				stringNumericBaseBoth(t, p, b, c, stringNumericBaseText("SELECT "+tc.expr, []string{"md5"}, [][]any{{tc.want}}))
			})
		}
	})
}
