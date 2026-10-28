package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Complete diagnostics and public API stages were pinned by root's PostgreSQL
// 18.0 discovery. Every unspecified logical field must remain absent.
func bitstringReferenceError(t *testing.T, p *fixture.Pair, backend fixture.Backend, db fixture.Executor, q string, args []any, prepared, query bool, state, message, position string) {
	t.Helper()
	want := fixture.ControlsReferenceDiagnostic(state, message)
	want.Position = position
	if state == "22P02" && len(args) > 0 {
		want.Context = fmt.Sprintf("unnamed portal parameter $%d = '...'", len(args))
	}
	s := fixture.ErrorDiagnostic(q, want, args...)
	s.Prepared, s.SelectRows, s.ErrorStage = prepared, query, "execute"
	if prepared && len(args) == 0 && state != "22026" && state != "22001" && !strings.HasPrefix(q, "CREATE TABLE") {
		s.ErrorStage = "prepare"
	}
	fixture.RunStep(t, p.Ctx, db, backend.Recorder, s)
}

func TestReferenceBitstringDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("CREATE TABLE bs_errors(f BIT(3),v BIT VARYING(5),d BIT)", 0))
		cases := []struct {
			q, state, message, position string
			query                       bool
		}{
			{"INSERT INTO bs_errors(f) VALUES(B'')", "22026", "bit string length 0 does not match type bit(3)", "", false},
			{"INSERT INTO bs_errors(f) VALUES(B'10')", "22026", "bit string length 2 does not match type bit(3)", "", false},
			{"INSERT INTO bs_errors(f) VALUES('1010')", "22026", "bit string length 4 does not match type bit(3)", "", false},
			{"INSERT INTO bs_errors(v) VALUES(B'101010')", "22001", "bit string too long for type bit varying(5)", "", false},
			{"INSERT INTO bs_errors(d) VALUES(B'10')", "22026", "bit string length 2 does not match type bit(1)", "", false},
			{"SELECT B'102'", "22P02", `"2" is not a valid binary digit`, "8", true},
			{"SELECT X'0G'", "22P02", `"G" is not a valid hexadecimal digit`, "8", true},
			{"/*é*/ SELECT B'2'", "22P02", `"2" is not a valid binary digit`, "14", true},
			{"SELECT B' 1'", "22P02", `" " is not a valid binary digit`, "8", true},
			{"SELECT X' F'", "22P02", `" " is not a valid hexadecimal digit`, "8", true},
		}
		for _, value := range []string{"'2'", "B'2'", "X'G'", "' 01'", "'01 '", "'xG'", "'B2'"} {
			q := "INSERT INTO bs_errors(f) VALUES(" + value + ")"
			digit, family := "2", "binary"
			if value == "X'G'" || value == "'xG'" {
				digit, family = "G", "hexadecimal"
			}
			if value == "' 01'" || value == "'01 '" {
				digit = " "
			}
			pos := 33 // Literal prefix/quote after this independently specified INSERT prefix.
			cases = append(cases, struct {
				q, state, message, position string
				query                       bool
			}{q, "22P02", fmt.Sprintf("%q is not a valid %s digit", digit, family), fixture.Position(pos), false})
		}
		for _, digit := range []string{"é", "界", "😀"} {
			for _, prefix := range []string{"B", "X"} {
				family := "binary"
				if prefix == "X" {
					family = "hexadecimal"
				}
				cases = append(cases, struct {
					q, state, message, position string
					query                       bool
				}{"SELECT " + prefix + "'" + digit + "'", "22P02", fmt.Sprintf("%q is not a valid %s digit", digit, family), "8", true})
			}
		}
		for _, tc := range cases {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					bitstringReferenceError(t, p, backend, backend.DB, tc.q, nil, prepared, tc.query, tc.state, tc.message, tc.position)
				})
			}
		}
		// Scanner and adjacency diagnostics include the complete source-context
		// message, not merely a bare scanner error.
		for _, tc := range []struct{ q, state, message, position string }{
			{"SELECT B'10", "42601", "unterminated bit string literal at or near \"B'10\"", "8"},
			{"SELECT X'0f", "42601", "unterminated hexadecimal string literal at or near \"X'0f\"", "8"},
			{"SELECT B'10'\n'01", "42601", "unterminated bit string literal at or near \"B'10'\n'01\"", "8"},
			{"SELECT X'0'\n'f", "42601", "unterminated hexadecimal string literal at or near \"X'0'\n'f\"", "8"},
			{"SELECT B'1' '0'", "42601", `syntax error at or near "'0'"`, "13"},
			{"SELECT X'0' 'f'", "42601", `syntax error at or near "'f'"`, "13"},
			{"SELECT B'1''0'", "42601", `syntax error at or near "'0'"`, "12"},
			{"SELECT X'a''b'", "42601", `syntax error at or near "'b'"`, "12"},
			{"SELECT B$$01$$", "42703", `column "b$$01$$" does not exist`, "8"},
			{"SELECT B'10'/*\n*/'01'", "42601", `syntax error at or near "'01'"`, "18"},
			{"SELECT X'0' /* comment */\n'f'", "42601", `syntax error at or near "'f'"`, "27"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					bitstringReferenceError(t, p, backend, backend.DB, tc.q, nil, prepared, true, tc.state, tc.message, tc.position)
				})
			}
		}
		// A separated prefix is a generic typed-literal attempt, not a B/X
		// constant. Preserve the existing native typed-literal capability
		// boundary and independently pin PostgreSQL's missing-type diagnostic.
		for _, tc := range []struct{ q, pgMessage string }{
			{"SELECT b '01'", `type "b" does not exist`},
			{"SELECT x 'a'", `type "x" does not exist`},
		} {
			state, message := "42704", tc.pgMessage
			if !backend.PG {
				state, message = "0A000", "typed literals are unsupported"
			}
			for _, prepared := range []bool{false, true} {
				t.Run(tc.q+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
					bitstringReferenceError(t, p, backend, backend.DB, tc.q, nil, prepared, true, state, message, "8")
				})
			}
		}
	}
}
