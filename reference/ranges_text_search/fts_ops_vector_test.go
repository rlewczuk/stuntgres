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

func TestReferenceFTSOpsVectorConcat(t *testing.T) {
	ftsOpsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ left, right, want string }{
			{"a:1 b:2", "c:1 d:2 b:3", "'a':1 'b':2,5 'c':3 'd':4"},
			{"a:3A b:2a", "ba:1234 a:1B", "'a':3A,4B 'b':2A 'ba':1237"},
			{"a:1 z:10", "a:2B", "'a':1,12B 'z':10"},
			{"a z", "a:2B b:1", "'a':2B 'b':1 'z'"},
			{"a:2A", "a b", "'a':2A 'b'"},
			{"z a", "b a", "'a' 'b' 'z'"},
			{"z:16382", "a:1D,2A", "'a':16383 'z':16382"},
			{"a:16383D", "a:1A", "'a':16383"},
			{"", "a:2B", "'a':2B"}, {"a:2B", "", "'a':2B"}, {"", "", ""},
		} {
			ftsOpsValue(t, p, b, "$1::tsvector || $2::tsvector", "TSVECTOR", tc.want, tc.left, tc.right)
		}
		positions := make([]string, 256)
		for i := range positions {
			positions[i] = fmt.Sprint(i + 1)
		}
		for _, n := range []int{255, 256} {
			left := "a:" + strings.Join(positions[:n], ",")
			want := "'a':" + strings.Join(positions, ",")
			if n == 255 {
				want += "B"
			}
			want += fmt.Sprintf(" 'b':%dA", n+1)
			ftsOpsValue(t, p, b, "$1::tsvector || $2::tsvector", "TSVECTOR", want, left, "a:1B,2A b:1A")
		}
		// Each input is below the storage ceiling; only composition reaches it.
		// 511 * (2040 lexeme bytes + 2 count bytes + 4*2 positions) = 1047550.
		words := make([]string, 511)
		canonical := make([]string, 511)
		for i := range words {
			word := fmt.Sprintf("%04d", i) + strings.Repeat("x", 2036)
			words[i] = word + ":1,2,3,4"
			canonical[i] = "'" + word + "':1,2,3,4"
		}
		left := strings.Join(words, " ")
		for _, size := range []int{1048575, 1048576} {
			t.Run(fmt.Sprintf("storage_%d", size), func(t *testing.T) {
				tail := strings.Repeat("z", size-1047550)
				ctx, cancel := context.WithTimeout(p.Ctx, 3*time.Second)
				defer cancel()
				conn, err := b.DB.Conn(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				if b.PG {
					defer conn.Raw(func(any) error { return driver.ErrBadConn })
				}
				var raw any
				err = conn.QueryRowContext(ctx, "SELECT $1::tsvector || $2::tsvector", left, tail).Scan(&raw)
				if size == 1048576 {
					d := fixture.ControlsReferenceDiagnostic("54000", "string is too long for tsvector (1048576 bytes, max 1048575 bytes)")
					if err != nil {
						fixture.AssertDiagnostic(t, err, &d)
					} else {
						t.Error("overflow unexpectedly accepted")
					}
					return
				}
				if err != nil {
					t.Fatal(ftsTypesObserveError(t, err))
				}
				fixture.AssertRawValue(t, b.PG, "TSVECTOR", raw)
				if fixture.Normalize(raw) != strings.Join(canonical, " ")+" '"+tail+"'" {
					t.Error("exact storage boundary output mismatch")
				}
			})
		}
		// Released generated-query traversal serializes the right operand first.
		// Its final word starts at MAXSTRPOS (accepted), then MAXSTRPOS+1 (rejected).
		for _, offset := range []int{1048575, 1048576} {
			t.Run(fmt.Sprintf("generated_query_start_%d", offset), func(t *testing.T) {
				rightWords := repeatFTSWord(512, strings.Repeat("x", 2046))
				rightWords = append(rightWords, strings.Repeat("y", offset-512*2047-1))
				right := strings.Join(rightWords, " & ")
				var raw any
				err := b.DB.QueryRowContext(p.Ctx, "SELECT $1::tsquery && $2::tsquery", "z", right).Scan(&raw)
				if offset == 1048576 {
					d := fixture.ControlsReferenceDiagnostic("54000", "tsquery is too large")
					fixture.AssertDiagnostic(t, err, &d)
					return
				}
				if err != nil {
					t.Fatal(ftsTypesObserveError(t, err))
				}
				expected := make([]string, len(rightWords))
				for i, w := range rightWords {
					expected[i] = "'" + w + "'"
				}
				fixture.AssertRawValue(t, b.PG, "TSQUERY", raw)
				if fixture.Normalize(raw) != "'z' & "+strings.Join(expected, " & ") {
					t.Error("generated query boundary output mismatch")
				}
			})
		}
	})
}
