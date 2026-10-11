package reference_test

import (
	"math"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFloatingArithmeticValues(t *testing.T) {
	referenceFloatingScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { referenceFloatingRun(t, p, b, s) }
		run(fixture.Exec("CREATE TABLE f(id INT,r REAL,s REAL,d DOUBLE PRECISION,i INT,h SMALLINT,b BIGINT,n NUMERIC)", 0))
		run(fixture.Exec("INSERT INTO f VALUES(1,'7.5','2','2',2,2,2,'2'),(2,NULL,NULL,NULL,NULL,NULL,NULL,NULL),(3,'16777216','1','1',1,1,1,'1')", 3))
		cases := []referenceScalarFloatCase{}
		for _, col := range []struct {
			name, typ string
			v         float64
		}{{"r", "FLOAT4", 7.5}, {"d", "FLOAT8", 2}} {
			for _, op := range []struct {
				op string
				v  float64
			}{{"+", col.v * 2}, {"-", 0}, {"*", col.v * col.v}, {"/", 1}} {
				cases = append(cases, referenceScalarFloatCase{col.name + op.op, "SELECT " + col.name + op.op + col.name + " AS result FROM f WHERE id=1", col.typ, op.v, 0})
			}
			cases = append(cases, referenceScalarFloatCase{"unary_plus", "SELECT +" + col.name + " AS result FROM f WHERE id=1", col.typ, col.v, 0}, referenceScalarFloatCase{"unary_minus", "SELECT -" + col.name + " AS result FROM f WHERE id=1", col.typ, -col.v, 0})
		}
		for _, right := range []string{"d", "i", "h", "b", "n"} {
			for _, tc := range []struct {
				op               string
				forward, reverse float64
				ulps             uint64
			}{{"+", 9.5, 9.5, 0}, {"-", 5.5, -5.5, 0}, {"*", 15, 15, 0}, {"/", 3.75, 0.26666666666666666, 2}} {
				cases = append(cases, referenceScalarFloatCase{right + tc.op, "SELECT r" + tc.op + right + " AS result FROM f WHERE id=1", "FLOAT8", tc.forward, tc.ulps}, referenceScalarFloatCase{right + tc.op + "reverse", "SELECT " + right + tc.op + "r AS result FROM f WHERE id=1", "FLOAT8", tc.reverse, tc.ulps})
			}
		}
		for _, right := range []string{"i", "h", "b", "n"} {
			for _, tc := range []struct {
				op string
				v  float64
			}{{"+", 4}, {"-", 0}, {"*", 4}, {"/", 1}} {
				for _, expr := range []string{"d" + tc.op + right, right + tc.op + "d"} {
					cases = append(cases, referenceScalarFloatCase{expr, "SELECT " + expr + " AS result FROM f WHERE id=1", "FLOAT8", tc.v, 0})
				}
			}
		}
		for _, tc := range []struct {
			expr, typ string
			v         float64
		}{{"r+s", "FLOAT4", 16777216}, {"r+i", "FLOAT8", 16777217}, {"r+n", "FLOAT8", 16777217}, {"r+'1'", "FLOAT4", 16777216}} {
			cases = append(cases, referenceScalarFloatCase{tc.expr, "SELECT " + tc.expr + " AS result FROM f WHERE id=3", tc.typ, tc.v, 0})
		}
		realSum := 0.30000001192092896
		if b.PG {
			realSum = 0.3
		}
		cases = append(cases, referenceScalarFloatCase{"binary32_sum", "SELECT '0.1'::real+'0.2'::real AS result FROM f WHERE id=1", "FLOAT4", realSum, 0}, referenceScalarFloatCase{"binary64_sum", "SELECT '0.1'::double precision+'0.2'::double precision AS result FROM f WHERE id=1", "FLOAT8", 0.30000000000000004, 0})
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					referenceScalarFloatQuery(t, p.Ctx, b.DB, b.Recorder, tc, prepared)
				}
			})
		}
		for _, condition := range []string{"id=2", "FALSE"} {
			rows := [][]any{}
			if condition == "id=2" {
				rows = [][]any{{nil, nil, nil, nil}}
			}
			s := fixture.Select("SELECT r+r,r+i,d+d,r^d FROM f WHERE "+condition, []string{"?column?", "?column?", "?column?", "?column?"}, []string{"FLOAT4", "FLOAT8", "FLOAT8", "FLOAT8"}, rows)
			for _, prepared := range []bool{false, true} {
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, referenceArithmeticMetadataExecutor{b.DB, t, b.PG, s.Types}, b.Recorder, s)
			}
		}
		for _, q := range []string{"SELECT r+$1 AS result FROM f WHERE id=3", "SELECT $1+r AS result FROM f WHERE id=3", "SELECT r^$1 AS result FROM f WHERE id=1", "SELECT $1^r AS result FROM f WHERE id=1"} {
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				for _, arg := range []any{"1", nil, "NaN", "Infinity", "-Infinity", "1"} {
					var want any
					typ := "FLOAT4"
					switch q {
					case "SELECT r+$1 AS result FROM f WHERE id=3", "SELECT $1+r AS result FROM f WHERE id=3":
						switch arg {
						case "1":
							want = float64(16777216)
						case "NaN":
							want = math.NaN()
						case "Infinity":
							want = math.Inf(1)
						case "-Infinity":
							want = math.Inf(-1)
						}
					case "SELECT r^$1 AS result FROM f WHERE id=1":
						typ = "FLOAT8"
						switch arg {
						case "1":
							want = float64(7.5)
						case "NaN":
							want = math.NaN()
						case "Infinity":
							want = math.Inf(1)
						case "-Infinity":
							want = float64(0)
						}
					default:
						typ = "FLOAT8"
						switch arg {
						case "1":
							want = float64(1)
						case "NaN":
							want = math.NaN()
						case "Infinity":
							want = math.Inf(1)
						case "-Infinity":
							continue
						}
					}
					// IEEE values are asserted from the actual held QueryRow route, not equality-normalized goldens.
					var got any
					if err := stmt.QueryRowContext(p.Ctx, arg).Scan(&got); err != nil {
						t.Fatal(q, arg, err)
					}
					if want == nil {
						if got != nil {
							t.Fatal(got)
						}
					} else {
						v, ok := fixture.Normalize(got).(float64)
						if !ok || !referenceScalarFloatMatches(v, want.(float64), 0) {
							t.Fatal(q, arg, got, want, typ)
						}
					}
				}
			}()
		}
	})
}
