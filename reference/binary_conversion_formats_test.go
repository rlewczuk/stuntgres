package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceBinaryConversionFormats(t *testing.T) {
	binaryConversionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, v := range []struct{ raw, b64 string }{{"", ""}, {"f", "Zg=="}, {"fo", "Zm8="}, {"foo", "Zm9v"}, {"foob", "Zm9vYg=="}, {"fooba", "Zm9vYmE="}, {"foobar", "Zm9vYmFy"}, {"123\x00\x01", "MTIzAAE="}} {
			binaryConversionBoth(t, p, b, c, referenceSelect("SELECT encode($1,'base64') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{v.b64}}, []byte(v.raw)))
			binaryConversionBoth(t, p, b, c, referenceSelect("SELECT decode($1,'base64') AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{v.raw}}, v.b64))
		}
		for _, n := range []int{55, 56, 57, 58, 113, 114, 115} {
			// Every full three-zero group is independently AAAA; final one/two
			// zeros are AA==/AAA=. Only complete three-byte groups trigger LF;
			// a padded tail reaching column76 does not emit a terminal newline.
			want := strings.Repeat("AAAA", n/3)
			var wrapped strings.Builder
			for len(want) >= 76 {
				wrapped.WriteString(want[:76])
				wrapped.WriteByte('\n')
				want = want[76:]
			}
			wrapped.WriteString(want)
			if n%3 == 1 {
				wrapped.WriteString("AA==")
			}
			if n%3 == 2 {
				wrapped.WriteString("AAA=")
			}
			binaryConversionBoth(t, p, b, c, referenceSelect("SELECT encode($1,'base64') AS v", []string{"v"}, []string{"TEXT"}, [][]any{{wrapped.String()}}, make([]byte, n)))
		}
		all := make([]byte, 256)
		var hx, esc strings.Builder
		for i := range all {
			all[i] = byte(i)
			fmt.Fprintf(&hx, "%02x", i)
			if i == 0 || i >= 128 {
				fmt.Fprintf(&esc, "\\%03o", i)
			} else if i == 92 {
				esc.WriteString(`\\`)
			} else {
				esc.WriteByte(byte(i))
			}
		}
		for _, format := range []string{"hex", "escape", "base64"} {
			s := referenceSelect("SELECT decode(encode($1,$2),$2) AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{string(all)}}, all, format)
			binaryConversionBoth(t, p, b, c, s)
		}
		for _, tc := range []struct{ f, want string }{{"hex", hx.String()}, {"escape", esc.String()}} {
			binaryConversionBoth(t, p, b, c, referenceSelect("SELECT encode($1,$2) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, all, tc.f))
		}
		for _, mode := range []string{"hex", "escape"} {
			referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET bytea_output='"+mode+"'", 0))
			binaryConversionValue(t, p, b, c, `encode('\x00011f205c7f80ff'::bytea,'escape')`, "TEXT", "\\000\x01\x1f \\\\\x7f\\200\\377")
			binaryConversionValue(t, p, b, c, `decode('00 AA bb ff','HEX')`, "BYTEA", "\x00\xaa\xbb\xff")
		}
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("SET bytea_output='hex'", 0))
		for _, tc := range []struct{ input, format, want string }{{"00 \t\r\nAA", "hex", "\x00\xaa"}, {" Z m 9 v \r\n", "base64", "foo"}, {`\000\377\\é`, "escape", "\x00\xff\\é"}} {
			binaryConversionBoth(t, p, b, c, referenceSelect("SELECT decode($1,$2) AS v", []string{"v"}, []string{"BYTEA"}, [][]any{{tc.want}}, tc.input, tc.format))
		}
	})
}
func TestReferenceBinaryConversionFeatureSmoke(t *testing.T) {
	binaryConversionScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			expr, typ string
			want      any
		}{
			{`convert('\xe9'::bytea,'LATIN1','UTF8')`, "BYTEA", "é"}, {`convert_from('\xe9'::bytea,'LATIN1')`, "TEXT", "é"}, {`convert_to('€','WIN1252')`, "BYTEA", "\x80"},
			{`encode('\x3132330001'::bytea,'base64')`, "TEXT", "MTIzAAE="}, {`decode('3132330001','hex')`, "BYTEA", "123\x00\x01"},
			{`convert_from('\xa4f7'::bytea,'EUC_JIS_2004')`, "TEXT", "か゚"}, {`convert_to('か゚','EUC_JIS_2004')`, "BYTEA", "\xa4\xf7"},
			{`convert('\xe9'::bytea,'ISO-8859-1','unicode')`, "BYTEA", "é"}, {`encode(NULL::bytea,'bogus')`, "TEXT", nil}, {`decode('','hex')`, "BYTEA", ""},
		} {
			binaryConversionValue(t, p, b, c, tc.expr, tc.typ, tc.want)
		}
		q := "SELECT decode($1,$2) AS v"
		valid := referenceSelect(q, []string{"v"}, []string{"BYTEA"}, [][]any{{"\xff"}}, "ff", "hex")
		null := referenceSelect(q, []string{"v"}, []string{"BYTEA"}, [][]any{{nil}}, nil, "hex")
		bad := referenceErrorDiagnostic(q, controlsReferenceDiagnostic("22023", `unrecognized encoding: "nope"`), "ff", "nope")
		bad.selectRows = true
		bad.errorStage = "execute"
		binaryConversionHeld(t, p, b, c, q, []referenceStep{valid, null, bad, valid})
		binaryConversionValue(t, p, b, c, `encode('\x005c80ff'::bytea,'escape')`, "TEXT", `\000\\\200\377`)
		stringEncodingFailure(t, p, b, c, `SELECT convert_to('€','LATIN1')`, controlsReferenceDiagnostic("22P05", `character with byte sequence 0xe2 0x82 0xac in encoding "UTF8" has no equivalent in encoding "LATIN1"`), false)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE bc_smoke(id integer,b bytea)", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("INSERT INTO bc_smoke VALUES(0,'sentinel')", 1))
		late := referenceErrorDiagnostic("INSERT INTO bc_smoke VALUES(1,decode('61','hex')),(2,decode('0q','hex'))", controlsReferenceDiagnostic("22023", `invalid hexadecimal digit: "q"`))
		late.errorStage = "execute"
		referenceRunStep(t, p.ctx, c, b.recorder, late)
		referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT id,b FROM bc_smoke ORDER BY id", []string{"id", "b"}, []string{"INT4", "BYTEA"}, [][]any{{int64(0), "sentinel"}}))
	})
}
