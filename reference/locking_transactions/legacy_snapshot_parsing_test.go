package reference_test

import (
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLegacySnapshotParsing(t *testing.T) {
	for i, tc := range []struct{ input, canonical string }{
		{"12:16:14,", "12:16:14"}, {"010:020:014", "10:20:14"},
		{"+12:+16:+14", "12:16:14"}, {" 12: 16: 14", "12:16:14"},
		{"1:18446744073709551616:", "1:18446744073709551615:"},
		{"1:-1:", "1:18446744073709551615:"}, {"-18446744073709551615:2:", "1:2:"},
		{"1:-18446744073709551616:", "1:18446744073709551615:"},
	} {
		t.Run(fmt.Sprintf("valid-%d", i), func(t *testing.T) {
			legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				identityBoth(t, p, b, b.DB, legacySnapshotStep(b, "SELECT "+snapshotQuote(tc.input)+"::txid_snapshot AS s", []string{"s"}, []string{"TXID_SNAPSHOT"}, [][]any{{tc.canonical}}))
				identityBoth(t, p, b, b.DB, legacySnapshotStep(b, "SELECT $1::txid_snapshot AS s", []string{"s"}, []string{"TXID_SNAPSHOT"}, [][]any{{tc.canonical}}, tc.input))
			})
		})
	}
	for i, input := range []string{"", "garbage", "31:12:", "0:1:", "1:0:", "12:13:0", "12:13:13", "12:16:14,13", "12", "12:16", "12:16::", "12:16:14x", "12 :16:", "12:16:14 ", "12:16:14,,", "1:4294967296:3", "4294967296:4294967297:", "1:9223372036854775808:3"} {
		t.Run(fmt.Sprintf("invalid-%d", i), func(t *testing.T) {
			legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := snapshotConn(t, p, b)
				message := "invalid input syntax for type pg_snapshot: \"" + input + "\""
				identityError(t, p, b, c, "SELECT "+snapshotQuote(input)+"::txid_snapshot", fixture.Diagnostic{Code: "22P02", Message: message, Position: "8"}, "prepare")
				parameter := "..."
				if input == "" {
					parameter = ""
				}
				for _, q := range []string{"SELECT $1::txid_snapshot", "SELECT $1::bigint,$2::txid_snapshot"} {
					index := "$1"
					args := []any{input}
					if q == "SELECT $1::bigint,$2::txid_snapshot" {
						index = "$2"
						args = []any{int64(1), input}
					}
					want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: message, Context: "unnamed portal parameter " + index + " = '" + parameter + "'"}
					identityBoth(t, p, b, c, legacySnapshotError(q, want, args...))
					stmt, err := c.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatalf("parameter Prepare must succeed: %v", err)
					}
					x := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					fixture.RunStep(t, p.Ctx, x, b.Recorder, legacySnapshotError(q, want, args...))
					if index == "$1" {
						fixture.RunStep(t, p.Ctx, x, b.Recorder, legacySnapshotStep(b, q, []string{"txid_snapshot"}, []string{"TXID_SNAPSHOT"}, [][]any{{"12:13:"}}, "12:13:"))
					}
					if err := stmt.Close(); err != nil {
						t.Fatal(err)
					}
				}
			})
		})
	}
}

func legacySnapshotError(q string, want fixture.Diagnostic, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(q, want, args...)
	s.ErrorStage = "execute"
	return s
}

func TestReferenceLegacySnapshotModernBounds(t *testing.T) {
	for _, input := range []string{"1:4294967296:3", "4294967296:4294967297:", "1:9223372036854775808:3"} {
		t.Run(input, func(t *testing.T) {
			legacySnapshotScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := snapshotConn(t, p, b)
				message := "invalid input syntax for type pg_snapshot: \"" + input + "\""
				identityError(t, p, b, c, "SELECT "+snapshotQuote(input)+"::pg_snapshot", fixture.Diagnostic{Code: "22P02", Message: message, Position: "8"}, "prepare")
				identityBoth(t, p, b, c, legacySnapshotError("SELECT $1::pg_snapshot", fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: message, Context: "unnamed portal parameter $1 = '...'"}, input))
			})
		})
	}
}
