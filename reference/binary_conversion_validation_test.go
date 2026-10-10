package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceBinaryConversionValidation(t *testing.T) {
	binaryConversionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ name, raw string }{
			{"UTF8", "c2"}, {"UTF8", "c220"}, {"UTF8", "c080"}, {"UTF8", "eda080"}, {"UTF8", "f4908080"}, {"UTF8", "e282"},
			{"EUC_JP", "8fa1"}, {"EUC_JP", "a120"}, {"EUC_JP", "8e80"}, {"EUC_CN", "a120"}, {"EUC_KR", "a120"}, {"EUC_TW", "8ea2a1"}, {"EUC_TW", "a120"}, {"EUC_JIS_2004", "8fa120"},
			{"MULE_INTERNAL", "81"}, {"MULE_INTERNAL", "8120"}, {"MULE_INTERNAL", "92a1"}, {"MULE_INTERNAL", "9cf0a1"},
			{"SJIS", "817f"}, {"SHIFT_JIS_2004", "817f"}, {"BIG5", "a1"}, {"GBK", "81"}, {"UHC", "81"}, {"GB18030", "813081"}, {"GB18030", "81302030"}, {"JOHAB", "8120"},
		} {
			t.Run(tc.name+"_"+tc.raw, func(t *testing.T) {
				raw := binaryConversionBytes(t, tc.raw)
				var words []string
				for i := range raw {
					words = append(words, fmt.Sprintf("0x%02x", raw[i]))
				}
				d := controlsReferenceDiagnostic("22021", `invalid byte sequence for encoding "`+tc.name+`": `+strings.Join(words, " "))
				stringEncodingFailure(t, p, b, c, "SELECT convert($1,$2,$2)", d, false, []byte(raw), tc.name)
			})
		}
		// Release verifiers accept these complete byte pairs on identity paths,
		// even though destination mapping rules can be stricter.
		for _, tc := range []struct{ name, raw string }{{"BIG5", "a120"}, {"GBK", "8120"}, {"UHC", "8120"}} {
			raw := binaryConversionBytes(t, tc.raw)
			binaryConversionBoth(t, p, b, c, referenceSelect("SELECT convert($1,$2,$2) AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{raw}}, []byte(raw), tc.name))
		}
		// pg_convert validates the entire source before encountering an unmappable
		// euro, even though conversion would otherwise fail earlier in the payload.
		stringEncodingFailure(t, p, b, c, "SELECT convert($1,'UTF8','LATIN1')", controlsReferenceDiagnostic("22021", `invalid byte sequence for encoding "UTF8": 0xff`), false, []byte("€\xff"))
		stringEncodingFailure(t, p, b, c, "SELECT convert($1,'EUC_JP','LATIN1')", controlsReferenceDiagnostic("22021", `invalid byte sequence for encoding "EUC_JP": 0xa1`), false, []byte{0xa1})
		for _, tc := range []struct{ alias, raw, want string }{
			{"unicode", "c3a9", "é"}, {"ISO-8859-1", "e9", "é"}, {"windows950", "a140", "　"}, {"mskanji", "8140", "　"}, {"ShiftJIS", "8140", "　"}, {"windows932", "8140", "　"}, {"ABC", "a0", " "}, {"TCVN", "a0", " "}, {"VSCII", "a0", " "},
		} {
			t.Run(tc.alias, func(t *testing.T) {
				binaryConversionBoth(t, p, b, c, referenceSelect("SELECT convert_from($1,$2) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, []byte(binaryConversionBytes(t, tc.raw)), tc.alias))
			})
		}
		for _, name := range []string{"CP1250", "UTF16", "8", "csISOLatin1"} {
			t.Run(name, func(t *testing.T) {
				stringEncodingFailure(t, p, b, c, "SELECT convert_from('a'::bytea,$1)", controlsReferenceDiagnostic("22023", `invalid source encoding name "`+name+`"`), false, name)
			})
		}
	})
}
