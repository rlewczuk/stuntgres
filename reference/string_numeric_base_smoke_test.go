package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceStringNumericBaseFeatureSmoke(t *testing.T) {
	t.Run("independent values and text-bytea boundary", func(t *testing.T) {
		stringNumericBaseScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
			for _, tc := range []struct{ typ, bin, oct, hex string }{
				{"integer", "11111111111111111111101100101110", "37777775456", "fffffb2e"},
				{"bigint", "1111111111111111111111111111111111111111111111111111101100101110", "1777777777777777775456", "fffffffffffffb2e"},
			} {
				arg := "'-1234'::" + tc.typ
				stringNumericBaseBoth(t, p, b, c, stringNumericBaseText("SELECT to_bin("+arg+"),to_oct("+arg+"),to_hex("+arg+")", []string{"to_bin", "to_oct", "to_hex"}, [][]any{{tc.bin, tc.oct, tc.hex}}))
				stringNumericBaseBoth(t, p, b, c, stringNumericBaseText("SELECT to_bin(0::"+tc.typ+"),to_oct(0::"+tc.typ+"),to_hex(0::"+tc.typ+")", []string{"to_bin", "to_oct", "to_hex"}, [][]any{{"0", "0", "0"}}))
				stringNumericBaseBoth(t, p, b, c, stringNumericBaseText("SELECT to_bin(NULL::"+tc.typ+"),to_oct(NULL::"+tc.typ+"),to_hex(NULL::"+tc.typ+")", []string{"to_bin", "to_oct", "to_hex"}, [][]any{{nil, nil, nil}}))
			}
			stringNumericBaseBoth(t, p, b, c, stringNumericBaseText("SELECT md5('abc'),md5(''),md5(NULL::text)", []string{"md5", "md5", "md5"}, [][]any{{"900150983cd24fb0d6963f7d28e17f72", "d41d8cd98f00b204e9800998ecf8427e", nil}}))
			stringNumericBaseBoth(t, p, b, c, stringNumericBaseText(`SELECT md5('\x0'),md5('\x00'::bytea)`, []string{"md5", "md5"}, [][]any{{"d8e6a5626b620a84d69e8052951a080a", "93b885adfe0da089cdf634904fd59f71"}}))
		})
	})
	// Reuse the exact behavioral bodies, not shortened or backend-specific copies.
	t.Run("held integer parameters and recovery", TestReferenceStringNumericBaseBinding)
	t.Run("held inferred text parameters and recovery", TestReferenceStringNumericBaseMD5Binding)
	t.Run("empty null and retained metadata", TestReferenceStringNumericBaseMetadata)
	t.Run("complete analysis diagnostics", TestReferenceStringNumericBaseDiagnostics)
	t.Run("write atomicity", TestReferenceStringNumericBaseWriteAtomicity)
	t.Run("stored widths and symbolic md5 with emitted replay", TestReferenceStringNumericBaseControls)
	t.Run("public cancellation and recovery", TestReferenceStringNumericBaseCancellation)
}
