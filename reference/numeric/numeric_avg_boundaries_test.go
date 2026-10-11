package reference_test

import (
	"database/sql"
	"math"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNumericAvgScale(t *testing.T) {
	cases := []struct {
		name, kind, values, want string
		count                    int64
	}{
		{"one", "integer", "(1)", "1.00000000000000000000", 1},
		{"ones", "numeric", "(1),(1)", "1.00000000000000000000", 2},
		{"third", "integer", "(0),(1),(0)", "0.33333333333333333333", 3},
		{"negative_third", "numeric", "(0),(-1),(0)", "-0.33333333333333333333", 3},
		{"zero", "numeric", "(0)", "0.00000000000000000000", 1},
		{"cancel", "integer", "(1),(-1)", "0.00000000000000000000", 2},
		{"scale", "numeric", "(1.0000000000000000000000000),(3)", "2.0000000000000000000000000", 2},
		{"scaled_zero", "numeric", "(0.0000000000000000000000000)", "0.0000000000000000000000000", 1},
		{"scaled_cancel", "numeric", "(1.0000000000000000000000000),(-1)", "0.0000000000000000000000000", 2},
		{"max_bigint", "bigint", "(9223372036854775807),(9223372036854775807)", "9223372036854775807", 2},
		{"bigint_extremes", "bigint", "(9223372036854775807),(-9223372036854775808)", "-0.50000000000000000000", 2},
		{"beyond_float", "bigint", "(9007199254740993),(9007199254740993)", "9007199254740993.0000", 2},
		{"tiny_below", "numeric", "('4e-1001')", "0." + strings.Repeat("0", 1000), 1},
		{"tiny_tie", "numeric", "('5e-1001')", "0." + strings.Repeat("0", 999) + "1", 1},
		{"negative_tie", "numeric", "('-5e-1001')", "-0." + strings.Repeat("0", 999) + "1", 1},
		{"clipped_zero", "numeric", "('0e-1001')", "0." + strings.Repeat("0", 1000), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			numericAvgScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE t(v "+c.kind+")", 0))
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO t VALUES "+c.values, c.count))
				numericAvgBoth(t, p, conn, recorder, fixture.Select("SELECT avg(v) FROM t", []string{"avg"}, []string{"NUMERIC"}, [][]any{{c.want}}))
			})
		})
	}
}

func numericAvgExecutionError(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, query, message string, queryMode bool) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "22003", Message: message})
		s.SelectRows = queryMode
		s.Prepared = prepared
		s.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, conn, recorder, s)
	}
}

func TestReferenceNumericAvgNumericBoundaries(t *testing.T) {
	cases := []struct {
		name, values, want string
		count              int64
		overflow           bool
	}{
		{"nan", "('NaN'),(1),(NULL)", "NaN", 3, false},
		{"positive", "('Infinity'),('Infinity'),(1),(NULL)", "Infinity", 4, false},
		{"negative", "('-Infinity'),('-Infinity'),(1)", "-Infinity", 3, false},
		{"opposite", "('Infinity'),('-Infinity')", "NaN", 2, false},
		{"nan_infinities", "('Infinity'),('NaN'),('-Infinity')", "NaN", 3, false},
		{"packed_sum", "('9e131071'),('9e131071')", "", 2, true},
		{"special_before_pack", "('9e131071'),('9e131071'),('Infinity')", "Infinity", 3, false},
		{"nan_before_pack", "('9e131071'),('9e131071'),('NaN')", "NaN", 3, false},
		{"transient_sum", "('9e131071'),('9e131071'),('-9e131071'),('-9e131071')", "0.00000000000000000000", 4, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			numericAvgScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE t(v NUMERIC)", 0))
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO t VALUES "+c.values, c.count))
				if c.overflow {
					numericAvgExecutionError(t, p, conn, recorder, "SELECT avg(v) FROM t", "value overflows numeric format", true)
					return
				}
				numericAvgBoth(t, p, conn, recorder, fixture.Select("SELECT avg(v) FROM t", []string{"avg"}, []string{"NUMERIC"}, [][]any{{c.want}}))
			})
		})
	}
	// A singleton at the maximum packed weight is legal. Compare an independently
	// constructed string rather than asking a native codec to generate a golden.
	t.Run("maximum_singleton", func(t *testing.T) {
		numericAvgScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE t(v NUMERIC)", 0))
			fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO t VALUES ('9e131071')", 1))
			numericAvgBoth(t, p, conn, recorder, fixture.Select("SELECT avg(v) FROM t", []string{"avg"}, []string{"NUMERIC"}, [][]any{{"9" + strings.Repeat("0", 131071)}}))
		})
	})
}

func numericAvgFloatResult(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, want float64) {
	t.Helper()
	// The fixture's ordinary row comparison intentionally does not equate NaNs.
	// Inspect this feature's raw FLOAT8 cell without weakening global equality.
	for _, prepared := range []bool{false, true} {
		q := "SELECT avg(v) FROM t"
		var rows *sql.Rows
		var err error
		if prepared {
			stmt, e := conn.PrepareContext(p.Ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			rows, err = stmt.QueryContext(p.Ctx)
			if err != nil {
				stmt.Close()
				t.Fatal(err)
			}
			defer stmt.Close()
		} else {
			rows, err = conn.QueryContext(p.Ctx, q)
		}
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer rows.Close()
			columns, e := rows.Columns()
			if e != nil || len(columns) != 1 || columns[0] != "avg" {
				t.Fatalf("columns: %v/%v", columns, e)
			}
			types, e := rows.ColumnTypes()
			if e != nil || len(types) != 1 || types[0].DatabaseTypeName() != "FLOAT8" {
				t.Fatalf("FLOAT8 metadata: %v/%v", types, e)
			}
			if !rows.Next() {
				t.Fatalf("missing mean: %v", rows.Err())
			}
			var raw any
			if e := rows.Scan(&raw); e != nil {
				t.Fatal(e)
			}
			got, ok := raw.(float64)
			if !ok {
				t.Fatalf("raw FLOAT8: %T", raw)
			}
			if math.IsNaN(want) {
				if !math.IsNaN(got) {
					t.Errorf("mean %v want NaN", got)
				}
			} else if math.Float64bits(got) != math.Float64bits(want) {
				t.Errorf("mean %v bits %x want %v bits %x", got, math.Float64bits(got), want, math.Float64bits(want))
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatalf("extra/error mean: %v", rows.Err())
			}
		}()
	}
}

func TestReferenceNumericAvgFloatBoundaries(t *testing.T) {
	cases := []struct {
		name, kind, values string
		count              int64
		want               float64
		overflow           bool
	}{
		{"sxx", "DOUBLE PRECISION", "(1e200),(0)", 2, 0, true},
		{"equal_large", "DOUBLE PRECISION", "(1e200),(1e200)", 2, 1e200, false},
		{"singleton_large", "DOUBLE PRECISION", "(1e308)", 1, 1e308, false},
		{"sum", "DOUBLE PRECISION", "(1e308),(1e308)", 2, 0, true},
		{"opposite_large", "DOUBLE PRECISION", "(1e308),(-1e308)", 2, 0, true},
		{"real_max", "REAL", "(3.4028235e38),(3.4028235e38)", 2, 3.4028234663852886e38, false},
		{"real_widen", "REAL", "(0.1)", 1, 0.10000000149011612, false},
		{"tiny", "DOUBLE PRECISION", "('5e-324'),(0)", 2, 0, false},
		{"negative_tiny", "DOUBLE PRECISION", "('-5e-324'),(0)", 2, math.Copysign(0, -1), false},
		{"negative_zero", "DOUBLE PRECISION", "('-0')", 1, 0, false},
	}
	for _, kind := range []string{"REAL", "DOUBLE PRECISION"} {
		for _, c := range []struct {
			name, values string
			count        int64
			want         float64
		}{
			{"nan", "('NaN'),(1),(NULL)", 3, math.NaN()},
			{"positive", "('Infinity'),('Infinity'),(1),(NULL)", 4, math.Inf(1)},
			{"negative", "('-Infinity'),('-Infinity'),(1)", 3, math.Inf(-1)},
			{"opposite", "('Infinity'),('-Infinity')", 2, math.NaN()},
		} {
			cases = append(cases, struct {
				name, kind, values string
				count              int64
				want               float64
				overflow           bool
			}{kind + "_" + c.name, kind, c.values, c.count, c.want, false})
		}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			numericAvgScenario(t, func(t *testing.T, p *fixture.Pair, conn *sql.Conn, recorder *fixture.Recorder, pg bool) {
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("CREATE TABLE t(v "+c.kind+")", 0))
				fixture.RunStep(t, p.Ctx, conn, recorder, fixture.Exec("INSERT INTO t VALUES "+c.values, c.count))
				if c.overflow {
					numericAvgExecutionError(t, p, conn, recorder, "SELECT avg(v) FROM t", "value out of range: overflow", true)
					return
				}
				numericAvgFloatResult(t, p, conn, recorder, c.want)
			})
		})
	}
}
