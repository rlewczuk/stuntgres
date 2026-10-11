package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func numericSumExecutionError(t *testing.T, p *fixture.Pair, conn fixture.Executor, recorder *fixture.Recorder, q, message string, queryMode bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: message})
		s.SelectRows, s.Prepared, s.ErrorStage = queryMode, prepared, "execute"
		fixture.RunStep(t, p.Ctx, conn, recorder, s)
	}
}

func TestReferenceNumericSumIntegralBoundaries(t *testing.T) {
	for _, c := range []struct {
		name, kind, values, typ string
		want                    any
	}{
		{"small_positive", "SMALLINT", "(32767),(32767)", "INT8", int64(65534)},
		{"small_negative", "SMALLINT", "(-32768),(-32768)", "INT8", int64(-65536)},
		{"int_positive", "INTEGER", "(2147483647),(2147483647)", "INT8", int64(4294967294)},
		{"int_negative", "INTEGER", "(-2147483648),(-2147483648)", "INT8", int64(-4294967296)},
		{"big_positive", "BIGINT", "(9223372036854775807),(9223372036854775807)", "NUMERIC", "18446744073709551614"},
		{"big_negative", "BIGINT", "(-9223372036854775808),(-9223372036854775808)", "NUMERIC", "-18446744073709551616"},
		{"big_cancel", "BIGINT", "(9223372036854775807),(-9223372036854775808)", "NUMERIC", "-1"},
		{"beyond_float", "BIGINT", "(9007199254740993),(1)", "NUMERIC", "9007199254740994"},
	} {
		t.Run(c.name, func(t *testing.T) {
			numericSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE t(v "+c.kind+")", 0))
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO t VALUES "+c.values, 2))
				s := fixture.Select("SELECT sum(v) FROM t", []string{"sum"}, []string{c.typ}, [][]any{{c.want}})
				s.CheckMetadata = numericSumMetadata(pg, s.Types)
				numericSumBoth(t, p, conn, recorder, s)
			})
		})
	}
}

func TestReferenceNumericSumNumericBoundaries(t *testing.T) {
	for _, c := range []struct {
		name, values, want string
		count              int64
		overflow           bool
	}{
		{"scale", "(1.20),(3.456),(NULL)", "4.656", 3, false},
		{"scaled_zero", "(1.20),(-1.20),(0.0000)", "0.0000", 3, false},
		{"negative_exponents", "('-1.2e3'),('2.5e-2')", "-1199.975", 2, false},
		{"tiny", "('1e-1001'),('2e-1001')", "0." + strings.Repeat("0", 1000) + "3", 2, false},
		{"nan", "('NaN'),(1),(NULL)", "NaN", 3, false},
		{"special_only", "('Infinity'),(NULL)", "Infinity", 2, false},
		{"negative", "('-Infinity'),(1)", "-Infinity", 2, false},
		{"opposite", "('Infinity'),('-Infinity')", "NaN", 2, false},
		{"nan_infinities", "('Infinity'),('NaN'),('-Infinity')", "NaN", 3, false},
		{"final_overflow", "('9e131071'),('9e131071')", "", 2, true},
		{"special_before_pack", "('9e131071'),('9e131071'),('Infinity')", "Infinity", 3, false},
		{"nan_before_pack", "('9e131071'),('9e131071'),('NaN')", "NaN", 3, false},
		{"transient_sum", "('9e131071'),('9e131071'),('-9e131071'),('-9e131071')", "0", 4, false},
		{"maximum_singleton", "('9e131071')", "9" + strings.Repeat("0", 131071), 1, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			numericSumScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE t(id INTEGER,v NUMERIC)", 0))
				// Stable id explicitly orders transitions, including transient overflow.
				values := strings.Split(c.values, "),(")
				for i, v := range values {
					v = strings.TrimPrefix(strings.TrimSuffix(v, ")"), "(")
					fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO t VALUES ($1,"+v+")", 1, int64(i+1)))
				}
				q := "SELECT sum(v ORDER BY id) FROM t"
				if c.overflow {
					numericSumExecutionError(t, p, conn, recorder, q, "value overflows numeric format", true)
					return
				}
				numericSumBoth(t, p, conn, recorder, fixture.Select(q, []string{"sum"}, []string{"NUMERIC"}, [][]any{{c.want}}))
			})
		})
	}
}
