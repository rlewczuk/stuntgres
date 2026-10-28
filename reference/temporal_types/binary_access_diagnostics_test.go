package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

// Expectations use REL_18_0 and the root's pinned PostgreSQL observations.
// Inherited native admission boundaries are asserted separately below.
func TestReferenceBinaryAccessDiagnostics(t *testing.T) {
	binaryAccessScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, name := range []string{"get_byte", "get_bit", "set_byte", "set_bit"} {
			upper := 1
			end := int64(2)
			if strings.HasSuffix(name, "bit") {
				upper = 15
				end = 16
			}
			for _, index := range []int64{-1, end} {
				args := fmt.Sprintf(`'\x0180'::bytea,%d`, index)
				if strings.HasPrefix(name, "set") {
					args += ",2"
				}
				fixture.BinaryAccessBodyError(t, p, b, c, "SELECT "+name+"("+args+")", "2202E", fmt.Sprintf("index %d out of valid range, 0..%d", index, upper))
			}
			args := ` '\x'::bytea,0`
			if strings.HasPrefix(name, "set") {
				args += ",256"
			}
			fixture.BinaryAccessBodyError(t, p, b, c, "SELECT "+name+"("+args+")", "2202E", "index 0 out of valid range, 0..-1")
		}
		for _, n := range []string{"9223372036854775807", "-9223372036854775808"} {
			for _, name := range []string{"get_bit", "set_bit"} {
				args := `'\x00'::bytea,` + n
				if name == "set_bit" {
					args += ",2"
				}
				fixture.BinaryAccessBodyError(t, p, b, c, "SELECT "+name+"("+args+")", "2202E", "index "+n+" out of valid range, 0..7")
			}
		}
		for _, n := range []string{"-1", "2", "2147483647", "-2147483648"} {
			fixture.BinaryAccessBodyError(t, p, b, c, `SELECT set_bit('\x00'::bytea,0,`+n+")", "22023", "new bit must be 0 or 1")
		}
		fixture.BinaryAccessBodyError(t, p, b, c, `SELECT set_bit('\x00'::bytea,8,2)`, "2202E", "index 8 out of valid range, 0..7")
		for _, tc := range []struct {
			q, code, msg string
			pos          int
		}{
			{`SELECT get_byte('\q'::bytea,0)`, "22P02", "invalid input syntax for type bytea", 17},
			{`SELECT get_byte('\x0'::bytea,0)`, "22023", "invalid hexadecimal data: odd number of digits", 17},
			{`SELECT get_byte('\xGG'::bytea,0)`, "22023", `invalid hexadecimal digit: "G"`, 17},
			{`SELECT get_byte(NULL::bytea,'bad')`, "22P02", `invalid input syntax for type integer: "bad"`, 29},
			{`SELECT get_bit(NULL::bytea,'bad')`, "22P02", `invalid input syntax for type bigint: "bad"`, 28},
		} {
			t.Run(tc.q, func(t *testing.T) {
				fixture.BinaryAccessAnalysisError(t, p, b, c, tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.msg, Position: fixture.Position(tc.pos)})
			})
		}
		// Explicit numeric casts fail when executed, not during PostgreSQL Prepare.
		for _, tc := range []struct{ q, msg string }{
			{`SELECT set_byte('\x00'::bytea,0,2147483648::integer)`, "integer out of range"},
			{`SELECT get_bit('\x00'::bytea,9223372036854775808::bigint)`, "bigint out of range"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("22003", tc.msg)
				fixture.BinaryAccessDiagnosticStages(t, p, b, c, tc.q, d, d, "execute")
			})
		}
		q := "SELECT set_bit($1::bytea,$2::bigint,$3::integer)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			offset, newbit     any
			code, msg, context string
		}{
			{int64(8), 2, "2202E", "index 8 out of valid range, 0..7", ""},
			{int64(0), 2, "22023", "new bit must be 0 or 1", ""},
			{"bad", 1, "22P02", `invalid input syntax for type bigint: "bad"`, `unnamed portal parameter $2 = '...'`},
			{int64(0), "bad", "22P02", `invalid input syntax for type integer: "bad"`, `unnamed portal parameter $3 = '...'`},
			{int64(0), "2147483648", "22003", `value "2147483648" is out of range for type integer`, `unnamed portal parameter $3 = '...'`},
		} {
			d := fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)
			d.Context = tc.context
			s := fixture.ErrorDiagnostic(q, d, []byte{0}, tc.offset, tc.newbit)
			s.SelectRows = true
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, h, b.Recorder, fixture.Select(q, []string{"set_bit"}, []string{"BYTEA"}, [][]any{{"\x01"}}, []byte{0}, int64(0), 1))
		}
	})
}

func TestReferenceBinaryAccessResolution(t *testing.T) {
	fixture.BitAccessScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE t(v bytea)", 0))
		for _, tc := range []struct{ q, msg string }{
			{`SELECT get_byte('\x00'::bytea,0::bigint)`, "get_byte(bytea, bigint)"},
			{`SELECT set_bit('\x00'::bytea,0,1::bigint)`, "set_bit(bytea, integer, bigint)"},
			{"SELECT bit_count(1)", "bit_count(integer)"},
			{"SELECT get_byte()", "get_byte()"},
			{"SELECT set_byte(v,0) FROM t", "set_byte(bytea, integer)"},
			{"SELECT public.bit_count(v) FROM t", "public.bit_count(bytea)"},
			{`SELECT "BIT_COUNT"(v) FROM t`, "BIT_COUNT(bytea)"},
			{`SELECT get_byte('\x00'::bytea,'0'::text)`, "get_byte(bytea, text)"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				fixture.BinaryAccessAnalysisError(t, p, b, c, tc.q, fixture.Diagnostic{Code: "42883", Message: "function " + tc.msg + " does not exist", Hint: fixture.BinaryHashHint, Position: "8"})
			})
		}
		for _, q := range []string{"SELECT bit_count('101')", "SELECT bit_count(NULL)", "SELECT get_bit('101',0::smallint)", "SELECT get_bit('101','0')", "SELECT get_bit('101',NULL)"} {
			name, signature := "bit_count", "unknown"
			if strings.Contains(q, "get_bit") {
				name = "get_bit"
				signature = "unknown, unknown"
				if strings.Contains(q, "smallint") {
					signature = "unknown, smallint"
				}
			}
			t.Run(q, func(t *testing.T) {
				fixture.BinaryAccessAnalysisError(t, p, b, c, q, fixture.Diagnostic{Code: "42725", Message: "function " + name + "(" + signature + ") is not unique", Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: "8"})
			})
		}
		t.Run("SELECT bit_count($1)", func(t *testing.T) {
			prepared := fixture.Diagnostic{Code: "42725", Message: "function bit_count(unknown) is not unique", Hint: "Could not choose a best candidate function. You might need to add explicit type casts.", Position: "8"}
			direct := fixture.Diagnostic{Code: "42P02", Message: "there is no parameter $1", Position: "18"}
			// Native analyzes before supplied-count validation, as documented by
			// TestReferenceBinaryConversionPhases. Do not widen generic admission.
			if !b.PG {
				direct = prepared
			}
			fixture.BinaryAccessDiagnosticStages(t, p, b, c, "SELECT bit_count($1)", direct, prepared, "prepare")
		})
		for _, tc := range []struct {
			q, name, typ string
			value        any
		}{{"SELECT get_bit('101',0)", "get_bit", "INT4", int64(1)}, {"SELECT set_bit('101',0,0)", "set_bit", "BIT", "001"}} {
			binaryAccessBoth(t, p, b, c, fixture.Select(tc.q, []string{tc.name}, []string{tc.typ}, [][]any{{tc.value}}))
		}
		binaryAccessBoth(t, p, b, c, fixture.Select("SELECT get_bit('10',1::bigint),set_bit('101',0::bigint,0)", []string{"get_bit", "set_bit"}, []string{"INT4", "BYTEA"}, [][]any{{int64(0), "001"}}))
		for _, tc := range []struct {
			q, code, msg string
			pos          int
		}{
			{"SELECT absent.bit_count(v) FROM t", "3F000", `schema "absent" does not exist`, 8},
			{"SELECT absent.bit_count(missing) FROM t", "42703", `column "missing" does not exist`, 25},
			{"SELECT get_byte(missing,0) FROM t", "42703", `column "missing" does not exist`, 17},
			{`SELECT bit_count(v COLLATE "C") FROM t`, "42804", `collations are not supported by type bytea`, 20},
			{"SELECT bit_count(" + strings.TrimSuffix(strings.Repeat("v,", 101), ",") + ") FROM t", "54023", "cannot pass more than 100 arguments to a function", 8},
			{"SELECT bit_count(missing," + strings.TrimSuffix(strings.Repeat("v,", 100), ",") + ") FROM t", "42703", `column "missing" does not exist`, 18},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.Diagnostic{Code: tc.code, Message: tc.msg, Position: fixture.Position(tc.pos)}
				// Shared bindFunction checks namespace visibility before dispatching
				// to bindBinaryAccess; PostgreSQL instead diagnoses this child first.
				if !b.PG && tc.q == "SELECT absent.bit_count(missing) FROM t" {
					d = fixture.Diagnostic{Code: "3F000", Message: `schema "absent" does not exist`, Position: "8"}
				}
				fixture.BinaryAccessAnalysisError(t, p, b, c, tc.q, d)
			})
		}
	})
}
