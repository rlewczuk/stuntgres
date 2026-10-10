package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestReferenceStringEncodingValues(t *testing.T) {
	stringEncodingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			expr string
			want int64
		}{
			{"ascii('')", 0}, {"ascii('A')", 65}, {"ascii('é')", 233}, {"ascii('🙂')", 128578}, {"ascii('é')", 101},
			{"ascii(' '::char(1))", 0}, {"ascii(' '::varchar(1))", 32}, {"pg_catalog.ascii('A'::name)", 65},
		} {
			stringEncodingValue(t, p, b, c, tc.expr, "INT4", tc.want)
		}
		for _, tc := range []struct {
			n    int
			want string
		}{
			{1, "\x01"}, {127, "\x7f"}, {128, "\u0080"}, {2047, "\u07ff"}, {2048, "\u0800"}, {65535, "\uffff"}, {65536, "\U00010000"}, {1114111, "\U0010ffff"}, {888, "\u0378"},
		} {
			stringEncodingValue(t, p, b, c, fmt.Sprintf("chr(%d)", tc.n), "TEXT", tc.want)
		}
		for _, expr := range []string{"chr('65')", "chr(65::smallint)", "chr(65::bigint::int)", "pg_catalog.chr(65)"} {
			stringEncodingValue(t, p, b, c, expr, "TEXT", "A")
		}
	})
}

func TestReferenceStringEncodingEncoding(t *testing.T) {
	stringEncodingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		// Independently calculated from REL_18_0 ascii.c byte translation constants.
		for _, tc := range []struct {
			name          string
			id            int
			accent, mixed string
		}{
			{"LATIN1", 8, "AC", "A a      "}, {"LATIN2", 9, "AS", "A a Zd   "}, {"LATIN9", 16, "AC", "A a      "}, {"WIN1250", 29, "AC", "A\"a' dz '"},
		} {
			for _, enc := range []string{"'" + tc.name + "'", fmt.Sprint(tc.id)} {
				stringEncodingValue(t, p, b, c, "to_ascii('é',"+enc+")", "TEXT", tc.accent)
				stringEncodingValue(t, p, b, c, "to_ascii('Ą€🙂',"+enc+")", "TEXT", tc.mixed)
				stringEncodingValue(t, p, b, c, "to_ascii('',"+enc+")", "TEXT", "")
				stringEncodingValue(t, p, b, c, "to_ascii(NULL,"+enc+")", "TEXT", nil)
			}
		}
		for _, enc := range []string{"ISO-8859-1", "iso_8859_1", "l.a-t_i n1", "windows-1250", "WIN1250"} {
			stringEncodingBoth(t, p, b, c, referenceSelect("SELECT to_ascii($1,$2) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"abc\t\n\x01\x7f"}}, "abc\t\n\x01\x7f", enc))
		}
		for _, cast := range []string{"text", "varchar(20)", "char(20)", "name"} {
			stringEncodingValue(t, p, b, c, "to_ascii('é','LATIN1'::"+cast+")", "TEXT", "AC")
		}
		// NAME clips to 63 complete UTF8 bytes before the encoding resolver sees it.
		clipped := "LATIN1" + strings.Repeat("-", 57)
		for _, cast := range []string{"text", "varchar", "name"} {
			stringEncodingBoth(t, p, b, c, referenceSelect("SELECT to_ascii('é',$1::"+cast+") AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"AC"}}, clipped+"nope"))
		}
		q := "SELECT to_ascii($1,$2) AS v"
		d := controlsReferenceDiagnostic("42704", "8 is not a valid encoding name")
		bad := referenceErrorDiagnostic(q, d, "abc", 8)
		bad.selectRows, bad.errorStage = true, "execute"
		stringEncodingHeld(t, p, b, c, q, []referenceStep{
			referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{"AC"}}, "é", "LATIN1"), bad,
			referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{nil}}, "abc", nil),
			referenceSelect(q, []string{"v"}, []string{"TEXT"}, [][]any{{"AC"}}, "é", "LATIN1"),
		})
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE DOMAIN se_encoding AS text", 0))
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE DOMAIN se_number AS smallint", 0))
		stringEncodingValue(t, p, b, c, "to_ascii('é','LATIN1'::se_encoding)", "TEXT", "AC")
		stringEncodingValue(t, p, b, c, "chr(65::se_number)", "TEXT", "A")
	})
}

func TestReferenceStringEncodingEscapes(t *testing.T) {
	stringEncodingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ source, want string }{
			{"", ""}, {"raw é🙂", "raw é🙂"}, {`\0041`, "A"}, {`\+0000E9`, "é"}, {`\u00e9`, "é"}, {`\U0001F642`, "🙂"},
			{`\0041F`, "AF"}, {`\u0041f`, "Af"}, {`\+000041F`, "AF"}, {`\U00000041F`, "AF"},
			{`\\`, `\`}, {`a\\b`, `a\b`}, {`\D83D\DE42`, "🙂"}, {`\D83D\uDE42`, "🙂"}, {`\+00D83D\U0000DE42`, "🙂"}, {`\U0000D83D\+00DE42`, "🙂"},
		} {
			t.Run(tc.source, func(t *testing.T) {
				stringEncodingValue(t, p, b, c, "unistr($$"+tc.source+"$$)", "TEXT", tc.want)
				stringEncodingBoth(t, p, b, c, referenceSelect("SELECT unistr($1) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.source))
			})
		}
	})
}

func TestReferenceStringEncodingAssigned(t *testing.T) {
	stringEncodingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct {
			source string
			want   bool
		}{
			{"", true}, {"ASCII", true}, {"\x01", true}, {"\u0301", true}, {"\ue000", true}, {"\U000f0123", true}, {"\U00100123", true},
			{"\u4e00\u5abc\u9fff", true}, {"\uac00\ub123\ud7a3", true}, {"\U00020000\U00023456\U0002a6df", true}, {"\U0001fae9", true},
			{"\u0378", false}, {"\ufdd0", false}, {"\ufffe", false}, {"\uffff", false}, {"\U0010ffff", false}, {"A\u0378🙂", false},
		} {
			stringEncodingBoth(t, p, b, c, referenceSelect("SELECT unicode_assigned($1) AS v", []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.source))
		}
		stringEncodingValue(t, p, b, c, "unicode_assigned(chr(888))", "BOOL", false)
	})
}

func TestReferenceStringEncodingNulls(t *testing.T) {
	stringEncodingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		for _, tc := range []struct{ expr, typ string }{
			{"ascii(NULL)", "INT4"}, {"chr(NULL)", "TEXT"}, {"unistr(NULL)", "TEXT"}, {"unicode_assigned(NULL)", "BOOL"},
			{"to_ascii(NULL)", "TEXT"}, {"to_ascii(NULL,'nope')", "TEXT"}, {"to_ascii(NULL,42)", "TEXT"}, {"to_ascii('abc',NULL)", "TEXT"},
			{"to_ascii('abc',NULL::text)", "TEXT"}, {"to_ascii('abc',NULL::int)", "TEXT"},
		} {
			stringEncodingValue(t, p, b, c, tc.expr, tc.typ, nil)
		}
		q := "SELECT ascii($1) AS a,chr($2) AS c,to_ascii($1,$3) AS t,unistr($4) AS u,unicode_assigned($1) AS assigned"
		names, types := []string{"a", "c", "t", "u", "assigned"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "BOOL"}
		valid := referenceSelect(q, names, types, [][]any{{int64(233), "🙂", "AC", "🙂", true}}, "é", 128578, "LATIN1", `\D83D\DE42`)
		bad := referenceErrorDiagnostic(q, controlsReferenceDiagnostic("54000", "null character not permitted"), "é", 0, "LATIN1", `\0041`)
		bad.selectRows, bad.errorStage = true, "execute"
		stringEncodingHeld(t, p, b, c, q, []referenceStep{valid, referenceSelect(q, names, types, [][]any{{nil, nil, nil, nil, nil}}, nil, nil, nil, nil), bad, valid})
		referenceRunStep(t, p.ctx, c, b.recorder, referenceExec("CREATE TABLE se_empty(s text,n int)", 0))
		empty := referenceSelect("SELECT ascii(s) AS a,chr(n) AS c,unicode_assigned(s) AS assigned FROM se_empty", []string{"a", "c", "assigned"}, []string{"INT4", "TEXT", "BOOL"}, nil)
		empty.checkMetadata = stringEncodingMetadata(b.pg, empty.columns, empty.types)
		stringEncodingBoth(t, p, b, c, empty)
	})
}
