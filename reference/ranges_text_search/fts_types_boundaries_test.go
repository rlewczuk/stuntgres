package reference_test

import (
	"context"
	"database/sql/driver"
	"fmt"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceFTSTypesBoundaries(t *testing.T) {
	ftsTypesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		// The pinned pq adapter cannot decode the observed oversized query
		// ErrorResponses. Keep large-input probes on separate connections and
		// observe server soft-input fields for that PG-only transport boundary.
		scalar := func(query string, args ...any) (any, error) {
			ctx, cancel := context.WithTimeout(p.Ctx, 2*time.Second)
			defer cancel()
			conn, err := b.DB.Conn(ctx)
			if err != nil {
				return nil, err
			}
			defer conn.Close()
			if b.PG {
				defer conn.Raw(func(any) error { return driver.ErrBadConn })
			}
			var raw any
			err = conn.QueryRowContext(ctx, query, args...).Scan(&raw)
			return raw, err
		}
		probe := func(label, typ, input, want string, diagnostic *fixture.Diagnostic) {
			t.Helper()
			raw, err := scalar("SELECT $1::"+typ, input)
			if diagnostic != nil {
				fixture.AssertDiagnostic(t, err, diagnostic)
				return
			}
			if err != nil {
				t.Fatalf("%s: %s", label, ftsTypesObserveError(t, err))
			}
			fixture.AssertRawValue(t, b.PG, strings.ToUpper(typ), raw)
			got, ok := fixture.Normalize(raw).(string)
			if !ok || got != want {
				t.Fatalf("%s canonical mismatch: raw=%T outputBytes=%d wantBytes=%d", label, raw, len(got), len(want))
			}
		}
		boundError := func(code, message string) *fixture.Diagnostic {
			d := fixture.ControlsReferenceDiagnostic(code, message)
			d.Context = "unnamed portal parameter $1 = '...'"
			return &d
		}
		queryOverflow := func(label, input string) {
			t.Helper()
			message := "value is too big in tsquery: \"" + input + "\""
			if !b.PG {
				probe(label, "tsquery", input, "", boundError("54000", message))
				return
			}
			ctx, cancel := context.WithTimeout(p.Ctx, 2*time.Second)
			defer cancel()
			conn, err := b.DB.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.Raw(func(any) error { return driver.ErrBadConn })
			var gotMessage, detail, hint, code any
			err = conn.QueryRowContext(ctx, "SELECT message,detail,hint,sql_error_code FROM pg_input_error_info($1,'tsquery')", input).Scan(&gotMessage, &detail, &hint, &code)
			if err != nil {
				t.Fatalf("%s soft input: %s", label, ftsTypesObserveError(t, err))
			}
			if fixture.Normalize(gotMessage) != message || detail != nil || hint != nil || fixture.Normalize(code) != "54000" {
				t.Fatalf("%s soft diagnostic mismatch: messageBytes=%d detail=%#v hint=%#v code=%#v", label, len(fmt.Sprint(fixture.Normalize(gotMessage))), detail, hint, fixture.Normalize(code))
			}
		}
		for _, typ := range []string{"tsvector", "tsquery"} {
			for _, n := range []int{2046, 2047} {
				for _, word := range []string{strings.Repeat("x", n), strings.Repeat("é", n/2) + strings.Repeat("x", n%2)} {
					var d *fixture.Diagnostic
					if n == 2047 {
						message := "word is too long (2047 bytes, max 2046 bytes)"
						if typ == "tsquery" {
							message = "word is too long in tsquery: \"" + word + "\""
						}
						d = boundError("54000", message)
					}
					probe(typ+" word bytes "+fmt.Sprint(n), typ, word, "'"+word+"'", d)
				}
			}
		}
		for _, pos := range []string{"1", "16382", "16383", "16384", "2147483647", "2147483648", "4294967295", "4294967296", "18446744073709551615", "18446744073709551616", "999999999999999999999999999999999999"} {
			var d *fixture.Diagnostic
			want := "'a':16383"
			if pos == "1" || pos == "16382" {
				want = "'a':" + pos
			}
			if pos == "2147483648" || pos == "4294967296" {
				d = boundError("42601", "wrong position info in tsvector: \"a:"+pos+"\"")
			}
			probe("position "+pos, "tsvector", "a:"+pos, want, d)
		}
		for _, input := range []string{"a:1DDa", "a:1A2", "a:1*"} {
			probe(input, "tsvector", input, "'a':1A", nil)
		}
		for _, input := range []string{"a:16383D,16383A", "a:16383A,16383D", "a:16384D,16384A", "a:16384A,16384D", "a:16383D a:16383A", "a:16383A a:16383D"} {
			probe(input, "tsvector", input, "'a':16383A", nil)
		}
		ascending := make([]string, 256)
		for i := range ascending {
			ascending[i] = fmt.Sprint(i + 1)
		}
		for _, n := range []int{256, 257} {
			positions := make([]string, n)
			for i := range n {
				positions[i] = fmt.Sprint(n - i)
			}
			probe("descending "+fmt.Sprint(n), "tsvector", "a:"+strings.Join(positions, ","), "'a':"+strings.Join(ascending, ","), nil)
		}
		for _, tc := range []struct{ tail, last string }{{"256D,256A", "256"}, {"256A,256D", "256A"}, {"257D,257A", "257"}, {"257A,257D", "257A"}} {
			prefix := strings.Join(ascending[:255], ",")
			probe("cutoff "+tc.tail, "tsvector", "a:"+prefix+","+tc.tail, "'a':"+prefix+","+tc.last, nil)
		}
		word := strings.Repeat("x", 2046)
		for _, n := range []int{512, 513, 514, 515} {
			var d *fixture.Diagnostic
			if n >= 514 {
				d = boundError("54000", "string is too long for tsvector (1049598 bytes, max 1048575 bytes)")
			}
			probe("vector raw words "+fmt.Sprint(n), "tsvector", strings.Repeat(word+" ", n), "'"+word+"'", d)
			input := strings.Join(repeatFTSWord(n, word), " & ")
			if n >= 514 {
				queryOverflow("query words "+fmt.Sprint(n), input)
			} else {
				probe("query words "+fmt.Sprint(n), "tsquery", input, strings.Join(repeatFTSWord(n, "'"+word+"'"), " & "), nil)
			}
		}
		for _, n := range []int{511, 512} {
			for _, k := range []int{1, 2, 3, 4, 5} {
				words := make([]string, n)
				canonical := make([]string, n)
				positions := strings.Join(ascending[:k], ",")
				for i := range words {
					lexeme := fmt.Sprintf("%04d", i) + strings.Repeat("x", 2036)
					words[i] = lexeme + ":" + positions
					canonical[i] = "'" + lexeme + "':" + positions
				}
				size := n * (2042 + 2*k)
				var d *fixture.Diagnostic
				if size > 1048575 {
					d = boundError("54000", fmt.Sprintf("string is too long for tsvector (%d bytes, max 1048575 bytes)", size))
				}
				probe(fmt.Sprintf("vector aligned n=%d k=%d bytes=%d", n, k, size), "tsvector", strings.Join(words, " "), strings.Join(canonical, " "), d)
			}
		}
		for _, offset := range []int{1048574, 1048575, 1048576} {
			words := repeatFTSWord(512, word)
			extra := offset - 1048064 - 1
			queryWords := append(append([]string(nil), words...), strings.Repeat("y", extra), "z")
			input := strings.Join(queryWords, " & ")
			if offset >= 1048575 {
				queryOverflow("query exact offset "+fmt.Sprint(offset), input)
			} else {
				canonical := repeatFTSWord(512, "'"+word+"'")
				canonical = append(canonical, "'"+strings.Repeat("y", extra)+"'", "'z'")
				probe("query exact offset", "tsquery", input, strings.Join(canonical, " & "), nil)
			}
			extra = offset - 512*2046
			vectorWords := append(append([]string(nil), words...), strings.Repeat("y", extra), "z")
			var d *fixture.Diagnostic
			if offset > 1048575 {
				d = boundError("54000", "string is too long for tsvector (1048576 bytes, max 1048575 bytes)")
			}
			probe("vector exact offset "+fmt.Sprint(offset), "tsvector", strings.Join(vectorWords, " "), "'"+word+"' '"+strings.Repeat("y", extra)+"' 'z'", d)
		}
		for _, size := range []int{1048574, 1048575, 1048576} {
			words := make([]string, 511)
			canonical := make([]string, 511)
			for i := range words {
				lexeme := fmt.Sprintf("%04d", i) + strings.Repeat("x", 2036)
				words[i] = lexeme + ":1,2,3,4"
				canonical[i] = "'" + lexeme + "':1,2,3,4"
			}
			tail := strings.Repeat("z", size-511*2050)
			words = append(words, tail)
			canonical = append(canonical, "'"+tail+"'")
			var d *fixture.Diagnostic
			if size > 1048575 {
				d = boundError("54000", "string is too long for tsvector (1048576 bytes, max 1048575 bytes)")
			}
			probe("vector exact storage "+fmt.Sprint(size), "tsvector", strings.Join(words, " "), strings.Join(canonical, " "), d)
		}
		for _, typ := range []string{"tsvector", "tsquery"} {
			input := strings.Repeat("x", 2047)
			for _, prefix := range []string{"", "/*é*/ "} {
				message := "word is too long (2047 bytes, max 2046 bytes)"
				if typ == "tsquery" {
					message = "word is too long in tsquery: \"" + input + "\""
				}
				d := fixture.ControlsReferenceDiagnostic("54000", message)
				d.Position = "8"
				if prefix != "" {
					d.Position = "14"
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(prefix+"SELECT "+ftsTypesQuote(input)+"::"+typ, d)
					s.SelectRows = true
					s.Prepared = prepared
					s.ErrorStage = "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
			}
		}
	})
}

func repeatFTSWord(n int, word string) []string {
	words := make([]string, n)
	for i := range words {
		words[i] = word
	}
	return words
}
