package reference_test

import (
	"database/sql"
	"math"
	"testing"
)

func numericSumFloatResult(t *testing.T, p *referencePair, conn *sql.Conn, pg bool, q, typ string, want, tolerance float64) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		rows, closeRows, err := transactionQuery(p.ctx, conn, q, prepared)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() {
				if err := closeRows(); err != nil {
					t.Error(err)
				}
			}()
			columns, err := rows.Columns()
			if err != nil || len(columns) != 1 || columns[0] != "sum" {
				t.Fatalf("columns: %v/%v", columns, err)
			}
			ct, err := rows.ColumnTypes()
			if err != nil || len(ct) != 1 || ct[0].DatabaseTypeName() != typ {
				t.Fatalf("types: %v/%v", ct, err)
			}
			numericSumMetadata(pg, []string{typ})(t, rows)
			if !rows.Next() {
				t.Fatalf("missing sum: %v", rows.Err())
			}
			var raw any
			if err := rows.Scan(&raw); err != nil {
				t.Fatal(err)
			}
			got, ok := raw.(float64)
			if !ok {
				t.Fatalf("FLOAT result: %T/%v", raw, raw)
			}
			if math.IsNaN(want) {
				if !math.IsNaN(got) {
					t.Errorf("sum %v want NaN", got)
				}
			} else if tolerance != 0 {
				if math.IsNaN(got) || math.IsInf(got, 0) || math.Abs(got-want) > tolerance+tolerance*math.Abs(want) {
					t.Errorf("sum %.17g want %.17g tolerance %g", got, want, tolerance)
				}
			} else if math.Float64bits(got) != math.Float64bits(want) {
				t.Errorf("sum %v bits %x want %v bits %x", got, math.Float64bits(got), want, math.Float64bits(want))
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatalf("extra/error sum: %v", rows.Err())
			}
		}()
	}
}

func TestReferenceNumericSumFloatBoundaries(t *testing.T) {
	for _, kind := range []string{"REAL", "DOUBLE PRECISION"} {
		typ, large, discriminator, tolerance := "FLOAT4", "3.4028235e38", "16777216", 1e-6
		if kind == "DOUBLE PRECISION" {
			typ, large, discriminator, tolerance = "FLOAT8", "1.7976931348623157e308", "9007199254740992", 1e-12
		}
		for _, c := range []struct {
			name, values     string
			count            int64
			want             float64
			approx, overflow bool
		}{
			{"round_each", "(1," + discriminator + "),(2,1),(3,-" + discriminator + ")", 3, 0, false, false},
			{"normal", "(1,0.1),(2,0.2),(3,0.3)", 3, 0.6, true, false},
			{"negative_zero", "(1,'-0')", 1, math.Copysign(0, -1), false, false},
			{"repeated_negative_zero", "(1,NULL),(2,'-0'),(3,'-0'),(4,NULL)", 4, math.Copysign(0, -1), false, false},
			{"positive_inf", "(1,'Infinity'),(2,1)", 2, math.Inf(1), false, false},
			{"negative_inf", "(1,'-Infinity'),(2,1)", 2, math.Inf(-1), false, false},
			{"opposite_inf", "(1,'Infinity'),(2,'-Infinity')", 2, math.NaN(), false, false},
			{"nan", "(1,'NaN'),(2,1),(3,NULL)", 3, math.NaN(), false, false},
			{"overflow", "(1," + large + "),(2," + large + ")", 2, 0, false, true},
			{"no_late_compensation", "(1," + large + "),(2," + large + "),(3,-" + large + ")", 3, 0, false, true},
			{"no_late_inf", "(1," + large + "),(2," + large + "),(3,'Infinity')", 3, 0, false, true},
			{"no_late_nan", "(1," + large + "),(2," + large + "),(3,'NaN')", 3, 0, false, true},
		} {
			t.Run(kind+"_"+c.name, func(t *testing.T) {
				numericSumScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
					referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TABLE t(id INTEGER,v "+kind+")", 0))
					referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO t VALUES "+c.values, c.count))
					q := "SELECT sum(v ORDER BY id) FROM t"
					if c.overflow {
						numericSumExecutionError(t, p, conn, recorder, q, "value out of range: overflow", true)
						return
					}
					tol := float64(0)
					if c.approx {
						tol = tolerance
					}
					numericSumFloatResult(t, p, conn, pg, q, typ, c.want, tol)
				})
			})
		}
	}
	t.Run("real_minimum_subnormal", func(t *testing.T) {
		numericSumScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TABLE t(v REAL)", 0))
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO t VALUES ('1e-45')", 1))
			want := float64(math.Float32frombits(1))
			if pg {
				want = 1e-45
			}
			numericSumFloatResult(t, p, conn, pg, "SELECT sum(v) FROM t", "FLOAT4", want, 0)
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO t VALUES ('1e-45')", 1))
			want = float64(math.Float32frombits(2))
			// pq parses PG's shortest binary32 text at binary64 precision.
			if pg {
				want = 3e-45
			}
			numericSumFloatResult(t, p, conn, pg, "SELECT sum(v) FROM t", "FLOAT4", want, 0)
		})
	})
	t.Run("real_stored_binary32_export", func(t *testing.T) {
		numericSumScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("CREATE TABLE t(v REAL)", 0))
			referenceRunStep(t, p.ctx, conn, recorder, referenceExec("INSERT INTO t VALUES (0.1)", 1))
			want := float64(math.Float32frombits(0x3dcccccd))
			if pg {
				want = 0.1
			}
			numericSumFloatResult(t, p, conn, pg, "SELECT sum(v) FROM t", "FLOAT4", want, 0)
		})
	})
}
