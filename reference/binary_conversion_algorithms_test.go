package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceBinaryConversionCombined(t *testing.T) {
	// Both directional expectations transcribed from the two REL_18_0
	// *_to_utf8.map combined arrays; no host or production codec is consulted.
	vectors := []struct{ euc, sjis, utf string }{
		{"a4f7", "82f5", "e3818be3829a"}, {"a4f8", "82f6", "e3818de3829a"}, {"a4f9", "82f7", "e3818fe3829a"}, {"a4fa", "82f8", "e38191e3829a"}, {"a4fb", "82f9", "e38193e3829a"},
		{"a5f7", "8397", "e382abe3829a"}, {"a5f8", "8398", "e382ade3829a"}, {"a5f9", "8399", "e382afe3829a"}, {"a5fa", "839a", "e382b1e3829a"}, {"a5fb", "839b", "e382b3e3829a"}, {"a5fc", "839c", "e382bbe3829a"}, {"a5fd", "839d", "e38384e3829a"}, {"a5fe", "839e", "e38388e3829a"}, {"a6f8", "83f6", "e387b7e3829a"},
		{"abc4", "8663", "c3a6cc80"}, {"abc8", "8667", "c994cc80"}, {"abc9", "8668", "c994cc81"}, {"abca", "8669", "ca8ccc80"}, {"abcb", "866a", "ca8ccc81"}, {"abcc", "866b", "c999cc80"}, {"abcd", "866c", "c999cc81"}, {"abce", "866d", "c99acc80"}, {"abcf", "866e", "c99acc81"}, {"abe5", "8685", "cba9cba5"}, {"abe6", "8686", "cba5cba9"},
	}
	binaryConversionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, v := range vectors {
			for _, w := range []struct{ name, raw string }{{"EUC_JIS_2004", v.euc}, {"SHIFT_JIS_2004", v.sjis}} {
				t.Run(w.name+"_"+w.raw, func(t *testing.T) {
					raw, utf := binaryConversionBytes(t, w.raw), binaryConversionBytes(t, v.utf)
					binaryConversionBoth(t, p, b, c, referenceSelect("SELECT convert_from($1,$2) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{utf}}, []byte(raw), w.name))
					binaryConversionBoth(t, p, b, c, referenceSelect("SELECT convert_to($1,$2) AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{raw}}, utf, w.name))
				})
			}
		}
		binaryConversionValue(t, p, b, c, `convert_to('かA','EUC_JIS_2004')`, "BYTEA", "\xa4\xabA")
	})
}
func TestReferenceBinaryConversionAlgorithmEdges(t *testing.T) {
	binaryConversionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		// GB18030 algorithm first range and supplementary-plane boundary.
		for _, v := range []struct{ raw, utf string }{{"81308130", "c280"}, {"90308130", "f0908080"}, {"e3329a35", "f48fbfbf"}} {
			t.Run(v.raw, func(t *testing.T) {
				binaryConversionBoth(t, p, b, c, referenceSelect("SELECT convert($1,'GB18030','UTF8') AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{binaryConversionBytes(t, v.utf)}}, []byte(binaryConversionBytes(t, v.raw))))
				binaryConversionBoth(t, p, b, c, referenceSelect("SELECT convert($1,'UTF8','GB18030') AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{binaryConversionBytes(t, v.raw)}}, []byte(binaryConversionBytes(t, v.utf))))
			})
		}
		for _, v := range []struct{ source, dest, input, output string }{{"SJIS", "EUC_JP", "fa40", "8ff3f3"}, {"SJIS", "EUC_JP", "f040", "f5a1"}, {"SJIS", "EUC_JP", "eb40", "a2ae"}, {"EUC_JP", "SJIS", "8fa1a1", "81ac"}} {
			t.Run(v.source+v.input, func(t *testing.T) {
				binaryConversionBoth(t, p, b, c, referenceSelect("SELECT convert($1,$2,$3) AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{binaryConversionBytes(t, v.output)}}, []byte(binaryConversionBytes(t, v.input)), v.source, v.dest))
			})
		}
		for _, v := range []struct{ source, dest, input, message string }{
			{"WIN866", "WIN1251", "b0", `character with byte sequence 0xb0 in encoding "WIN866" has no equivalent in encoding "WIN1251"`},
			{"MULE_INTERNAL", "LATIN1", "82a0", `character with byte sequence 0x82 0xa0 in encoding "MULE_INTERNAL" has no equivalent in encoding "LATIN1"`},
			{"EUC_TW", "BIG5", "8ea3a1a1", `character with byte sequence 0x8e 0xa3 0xa1 0xa1 in encoding "EUC_TW" has no equivalent in encoding "BIG5"`},
		} {
			t.Run(v.source+v.input, func(t *testing.T) {
				stringEncodingFailure(t, p, b, c, "SELECT convert($1,$2,$3)", controlsReferenceDiagnostic("22P05", v.message), false, []byte(binaryConversionBytes(t, v.input)), v.source, v.dest)
			})
		}
	})
}
