package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// Errorf rather than Fatalf keeps oracle discovery running over the full matrix.
// All fields, including absent fields, and the actual Prepare/Execute boundary
// are asserted. No result from either backend is turned into an expectation.
func largeObjectOwnerDiagnostic(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, prepared bool, stage string, want fixture.Diagnostic) {
	t.Helper()
	for _, query := range []bool{false, true} {
		t.Run(fmt.Sprintf("sql=%s/query=%t", q, query), func(t *testing.T) {
			var stmt *sql.Stmt
			var err error
			gotStage := "execute"
			if prepared {
				stmt, err = c.PrepareContext(p.Ctx, q)
				if err != nil {
					gotStage = "prepare"
					if stmt != nil { stmt.Close(); t.Error("failed Prepare returned statement") }
				} else { defer stmt.Close() }
			}
			if err == nil {
				if query {
					var rows *sql.Rows
					if prepared { rows, err = stmt.QueryContext(p.Ctx) } else { rows, err = c.QueryContext(p.Ctx, q) }
					if rows != nil { rows.Close(); t.Error("failure returned rows") }
				} else {
					var result sql.Result
					if prepared { result, err = stmt.ExecContext(p.Ctx) } else { result, err = c.ExecContext(p.Ctx, q) }
					if result != nil { t.Error("failure returned result") }
				}
			}
			if err == nil { t.Error("expected failure"); return }
			if !prepared { stage = "execute" }
			if gotStage != stage { t.Errorf("stage=%s want=%s", gotStage, stage) }
			if got := fixture.ExtractDiagnostic(t, err); got != want { t.Errorf("diagnostic=%#v want=%#v", got, want) }
		})
	}
}

func TestReferenceLargeObjectOwnerDiagnostics(t *testing.T) {
	largeObjectOwnerScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		defer largeObjectCleanup(t, p, c, 910251, 4294967295)
		largeObjectOwnerCreate(run, 910251)
		old := largeObjectOwnerOID(t, p, c, "lo_old")
		for _, oid := range []uint32{4294967295} { run(largeObjectValue(fmt.Sprintf("lo_create(%d::oid)", oid), "OID", int64(oid))) }
		for _, operand := range []string{"910251", "+910251", "0910251", "910_251"} {
			run(fixture.Exec("ALTER LARGE OBJECT "+operand+" OWNER TO lo_old;", 0))
		}
		for _, operand := range []string{"-1", "+4294967295", "4294967295"} { run(fixture.Exec("ALTER LARGE OBJECT "+operand+" OWNER TO lo_new", 0)) }
		for _, tc := range []struct{ operand string; oid uint64 }{
			{"0", 0}, {"-0", 0}, {"2147483647", 2147483647}, {"2147483648", 2147483648}, {"-2147483648", 2147483648},
			{"0xDE3F9", 0xde3f9}, {"0o3361771", 0o3361771}, {"0b11011110001111111001", 0b11011110001111111001},
			{"+0xDE3F9", 0xde3f9}, {"-0x1", 4294967295},
			{"-0x80000000", 2147483648}, {"+0x80000000", 2147483648},
		} {
			q := "ALTER LARGE OBJECT "+tc.operand+" OWNER TO lo_new"
			if tc.oid == 4294967295 { run(fixture.Exec(q, 0)); continue }
			largeObjectOwnerDiagnostic(t, p, c, q, prepared, "execute", fixture.ControlsReferenceDiagnostic("42704", fmt.Sprintf("large object %d does not exist", tc.oid)))
		}
		// NumericOnly preserves float-token text. oidparse is called by object
		// address resolution at Execute, not by raw parsing/Describe.
		for _, text := range []string{"-2147483649", "4294967296", "-4294967295", "999999999999999999999999999999", "+4294967296", "0x100000000", "-0x80000001", "040000000000"} {
			largeObjectOwnerDiagnostic(t, p, c, "ALTER LARGE OBJECT "+text+" OWNER TO lo_new", prepared, "execute", fixture.ControlsReferenceDiagnostic("22003", `value "`+strings.TrimPrefix(text, "+")+`" is out of range for type oid`))
		}
		for _, text := range []string{"1.0", ".5", "1e0", "1e100", "-1.0", "+.5", "-1e100", "0x80000000", "0xFFFFFFFF", "4_294_967_295", "-2_147_483_648", "0o20000000000", "0b10000000000000000000000000000000"} {
			// Oversized radix/underscore scanner tokens enter the Float node path
			// with their original spelling. Source predicts base-0 radix input
			// acceptance but not underscore acceptance.
			q := "ALTER LARGE OBJECT "+text+" OWNER TO lo_new"
			if text == "0x80000000" {
				largeObjectOwnerDiagnostic(t, p, c, q, prepared, "execute", fixture.ControlsReferenceDiagnostic("42704", "large object 2147483648 does not exist"))
				continue
			}
			if text == "0xFFFFFFFF" { run(fixture.Exec(q, 0)); continue }
			largeObjectOwnerDiagnostic(t, p, c, q, prepared, "execute", fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type oid: "`+strings.TrimPrefix(text, "+")+`"`))
		}
		for _, tc := range []struct{ sql, token string }{
			{"ALTER LARGE OBJECT '910251' OWNER TO lo_new", "'910251'"},
			{"ALTER LARGE OBJECT foo OWNER TO lo_new", "foo"},
			{"ALTER LARGE OBJECT (910251) OWNER TO lo_new", "("},
			{"ALTER LARGE OBJECT $1 OWNER TO lo_new", "$1"},
			{"ALTER LARGE OBJECT 910251::oid OWNER TO lo_new", "::"},
			{"ALTER LARGE OBJECT 910251+0 OWNER TO lo_new", "+"},
			{"ALTER LARGE OBJECT 910251,910252 OWNER TO lo_new", ","},
			{"ALTER LARGE OBJECT OWNER TO lo_new", "OWNER"},
			{"ALTER LARGE OBJECT 910251 TO lo_new", "TO"},
			{"ALTER LARGE OBJECT 910251 OWNER lo_new", "lo_new"},
			{"ALTER LARGE OBJECT IF EXISTS 910251 OWNER TO lo_new", "IF"},
			{"ALTER LARGE OBJECT 910251 OWNER TO lo_new garbage", "garbage"},
			{"ALTER LARGE OBJECT 910251 OWNER TO lo_new OWNER TO lo_old", "OWNER"},
			{"/* λ雪 */ ALTER LARGE OBJECT $1 OWNER TO lo_new", "$1"},
			{"ALTER LARGE OBJECT", ""}, {"ALTER LARGE OBJECT 910251", ""}, {"ALTER LARGE OBJECT 910251 OWNER", ""}, {"ALTER LARGE OBJECT 910251 OWNER TO", ""},
		} {
			message := "syntax error at end of input"
			position := utf8.RuneCountInString(tc.sql)+1
			if tc.token != "" {
				at := strings.Index(tc.sql, tc.token)
				if strings.Count(tc.sql, tc.token) > 1 { at = strings.LastIndex(tc.sql, tc.token) }
				position = utf8.RuneCountInString(tc.sql[:at])+1
				message = `syntax error at or near "`+tc.token+`"`
			}
			want := fixture.ControlsReferenceDiagnostic("42601", message)
			want.Position = strconv.Itoa(position)
			largeObjectOwnerDiagnostic(t, p, c, tc.sql, prepared, "prepare", want)
			largeObjectOwnerMetadata(run, 910251, old, nil)
		}
		for _, tc := range []struct{ q, code, message string }{
			{largeObjectOwnerAlter(910259, "lo_missing"), "42704", `role "lo_missing" does not exist`},
			{largeObjectOwnerAlter(910251, "lo_missing"), "42704", `role "lo_missing" does not exist`},
			{"ALTER LARGE OBJECT 1.0 OWNER TO lo_missing", "42704", `role "lo_missing" does not exist`},
			{"ALTER LARGE OBJECT 4294967296 OWNER TO lo_missing", "42704", `role "lo_missing" does not exist`},
		} { largeObjectOwnerDiagnostic(t, p, c, tc.q, prepared, "execute", fixture.ControlsReferenceDiagnostic(tc.code, tc.message)) }
		largeObjectOwnerMetadata(run, 910251, old, nil)
	})
}
