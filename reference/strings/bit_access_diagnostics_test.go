package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitAccessDiagnostics(t *testing.T) {
	fixture.BitAccessScenario(t, bitAccessDiagnostics)
}
func bitAccessDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	for _, n := range []string{"-1", "3", "-2147483648", "2147483647"} {
		for _, fn := range []string{"get_bit", "set_bit"} {
			args := "B'101'," + n
			if fn == "set_bit" {
				args += ",2"
			}
			fixture.BinaryAccessBodyError(t, p, b, c, "SELECT "+fn+"("+args+")", "2202E", fmt.Sprintf("bit index %s out of valid range (0..2)", n))
		}
	}
	for _, fn := range []string{"get_bit", "set_bit"} {
		args := "B'',0"
		if fn == "set_bit" {
			args += ",2"
		}
		fixture.BinaryAccessBodyError(t, p, b, c, "SELECT "+fn+"("+args+")", "2202E", "bit index 0 out of valid range (0..-1)")
	}
	for _, n := range []string{"-1", "2", "-2147483648", "2147483647"} {
		fixture.BinaryAccessBodyError(t, p, b, c, "SELECT set_bit(B'1',0,"+n+")", "22023", "new bit must be 0 or 1")
	}
	fixture.BinaryAccessBodyError(t, p, b, c, "SELECT set_bit(B'1',1,2)", "2202E", "bit index 1 out of valid range (0..0)")
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ba_diag_null(v BIT)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ba_diag_null VALUES(NULL)", 1))
	for _, tc := range []struct {
		q, code, msg string
		pos          int
	}{
		{"SELECT get_bit('2',NULL::integer)", "22P02", `"2" is not a valid binary digit`, 16},
		{"SELECT set_bit('2',0,NULL::integer)", "22P02", `"2" is not a valid binary digit`, 16},
		{"SELECT get_bit(v,'bad') FROM ba_diag_null", "22P02", `invalid input syntax for type integer: "bad"`, 18},
	} {
		fixture.BinaryAccessAnalysisError(t, p, b, c, tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.msg, Position: fixture.Position(tc.pos)})
	}
	q := "SELECT get_bit(B'1',2147483648::integer)"
	d := fixture.ControlsReferenceDiagnostic("22003", "integer out of range")
	fixture.BinaryAccessDiagnosticStages(t, p, b, c, q, d, d, "execute")
}

func TestReferenceBitAccessHeldStatements(t *testing.T) { fixture.BitAccessScenario(t, bitAccessHeld) }
func bitAccessHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	q := "SELECT get_bit($1,$2::integer) AS g,set_bit($1,$2::integer,$3::integer) AS s"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	h := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	good := func(src, n, v any, row []any) {
		s := fixture.Select(q, []string{"g", "s"}, []string{"INT4", "BIT"}, [][]any{row}, src, n, v)
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
			cols, err := r.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			bitAccessMetadata(t, cols, b.PG)
		}
		fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
	}
	good("100000001", 8, 0, []any{int64(1), "100000000"})
	good("000000000", 0, 1, []any{int64(0), "100000000"})
	good(nil, 0, 1, []any{nil, nil})
	good([]byte(nil), 0, 1, []any{nil, nil})
	good("1", nil, 1, []any{nil, nil})
	good("1", 0, nil, []any{int64(1), nil})
	for _, tc := range []struct {
		src, n, v          any
		code, msg, context string
	}{
		{"", 0, 2, "2202E", "bit index 0 out of valid range (0..-1)", ""},
		{[]byte{}, 0, 1, "2202E", "bit index 0 out of valid range (0..-1)", ""},
		{"1", 0, 2, "22023", "new bit must be 0 or 1", ""},
		{"1", 1, 2, "2202E", "bit index 1 out of valid range (0..0)", ""},
		{"2", nil, 1, "22P02", `"2" is not a valid binary digit`, `unnamed portal parameter $1 = '...'`},
		{nil, "bad", 1, "22P02", `invalid input syntax for type integer: "bad"`, `unnamed portal parameter $2 = '...'`},
		{"1", 0, "bad", "22P02", `invalid input syntax for type integer: "bad"`, `unnamed portal parameter $3 = '...'`},
		{"1", "2147483648", 0, "22003", `value "2147483648" is out of range for type integer`, `unnamed portal parameter $2 = '...'`},
		{"1", 0, "2147483648", "22003", `value "2147483648" is out of range for type integer`, `unnamed portal parameter $3 = '...'`},
	} {
		d := fixture.ControlsReferenceDiagnostic(tc.code, tc.msg)
		d.Context = tc.context
		for _, query := range []bool{false, true} {
			s := fixture.ErrorDiagnostic(q, d, tc.src, tc.n, tc.v)
			s.SelectRows = query
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, h, b.Recorder, s)
		}
		good("100000001", 8, 0, []any{int64(1), "100000000"})
	}
}
