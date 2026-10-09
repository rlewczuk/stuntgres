package reference_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// These independent expectations describe numeric conversions, not quoted float
// input. The reference fixture checks each backend's complete record and phase
// before parity; only real pinned PostgreSQL is used as the external backend.
func TestReferenceNumericConversionFailuresAndAtomicity(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			run := func(s referenceStep) {
				s.prepared = prepared
				if s.state == "" || s.selectRows {
					p.step(t, s)
					return
				}
				for _, backend := range p.backends() {
					ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
					func() {
						var executor referenceExecutor = backend.db
						if prepared {
							stmt, err := backend.db.PrepareContext(ctx, s.query)
							if err != nil {
								t.Fatal("conversion failed Prepare", err)
							}
							defer stmt.Close()
							executor = referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: s.query}
						}
						result, err := executor.ExecContext(ctx, s.query, s.args...)
						if result != nil {
							t.Fatal("failed assignment exposed an affected-count result")
						}
						referenceState(t, err, s.state)
						referenceCheckDiagnostic(t, err, s, "execute")
					}()
				}
			}
			for _, tc := range []struct {
				name, decl, typ, positive, negative, message string
				maximum, minimum                             int64
			}{
				{"i2", "SMALLINT", "INT2", "32767", "32768", "smallint out of range", 32767, -32768},
				{"i4", "INTEGER", "INT4", "2147483647", "2147483648", "integer out of range", 2147483647, -2147483648},
				{"i8", "BIGINT", "INT8", "9223372036854775807", "9223372036854775808", "bigint out of range", 9223372036854775807, -9223372036854775808},
			} {
				run(referenceExec("CREATE TABLE "+tc.name+"(id INT,v "+tc.decl+")", 0))
				run(referenceExec("INSERT INTO "+tc.name+" VALUES(0,99),(1,"+tc.positive+".49),(2,-"+tc.negative+".49)", 3))
				unchanged := referenceSelect("SELECT * FROM "+tc.name+" ORDER BY id", []string{"id", "v"}, []string{"INT4", tc.typ}, [][]any{{int64(0), int64(99)}, {int64(1), tc.maximum}, {int64(2), tc.minimum}})
				run(unchanged)
				for _, literal := range []string{tc.positive + ".5", "-" + tc.negative + ".5"} {
					s := referenceImplicitError("INSERT INTO "+tc.name+" VALUES(3,1.5),(4,"+literal+")", referenceDiagnostic{Code: "22003", Message: tc.message})
					s.errorStage = "execute" // Prepare parses valid NUMERIC; conversion is later.
					run(s)
					run(unchanged)
				}
			}
			run(referenceExec("CREATE TABLE small_compare(id INT,v SMALLINT)", 0))
			run(referenceExec("INSERT INTO small_compare VALUES(0,32767.49),(1,-32768.49),(2,1.5),(3,-1.5),(4,NULL)", 5))
			run(referenceSelect("SELECT * FROM small_compare ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT2"}, [][]any{{int64(0), int64(32767)}, {int64(1), int64(-32768)}, {int64(2), int64(2)}, {int64(3), int64(-2)}, {int64(4), nil}}))
			for _, tc := range []struct {
				literal string
				want    [][]any
			}{
				{"32767.0", [][]any{{int64(32767)}}},
				{"-32768.0", [][]any{{int64(-32768)}}},
				{"32767.1", [][]any{}},
				{"32768.0", [][]any{}},
				{"-32769.0", [][]any{}},
				{"1e100", [][]any{}},
				{"NULL", [][]any{}},
			} {
				run(referenceSelect("SELECT v FROM small_compare WHERE v="+tc.literal, []string{"v"}, []string{"INT2"}, tc.want))
			}
			run(referenceExec("CREATE TABLE floats(id INT,r REAL,d DOUBLE PRECISION)", 0))
			run(referenceExec("INSERT INTO floats VALUES(99,1.0,1.0)", 1))
			unchanged := referenceSelect("SELECT * FROM floats ORDER BY id", []string{"id", "r", "d"}, []string{"INT4", "FLOAT4", "FLOAT8"}, [][]any{{int64(99), float64(1), float64(1)}})
			run(referenceExec("CREATE TABLE float_controls(id INT,r REAL,d DOUBLE PRECISION)", 0))
			for i, literal := range []string{"1.5", "-1.5", "0e-400"} {
				run(referenceExec(fmt.Sprintf("INSERT INTO float_controls VALUES(%d,%s,%s)", i, literal, literal), 1))
			}
			run(referenceSelect("SELECT * FROM float_controls ORDER BY id", []string{"id", "r", "d"}, []string{"INT4", "FLOAT4", "FLOAT8"}, [][]any{{int64(0), float64(1.5), float64(1.5)}, {int64(1), float64(-1.5), float64(-1.5)}, {int64(2), float64(0), float64(0)}}))
			for _, tc := range []struct{ column, literal, text, name string }{
				{"r", "1e40", "1" + strings.Repeat("0", 40), "real"},
				{"r", "1e-50", "0." + strings.Repeat("0", 49) + "1", "real"},
				{"d", "1e400", "1" + strings.Repeat("0", 400), "double precision"},
				{"d", "1e-400", "0." + strings.Repeat("0", 399) + "1", "double precision"},
			} {
				s := referenceImplicitError("INSERT INTO floats(id,"+tc.column+") VALUES(1,.5),(2,"+tc.literal+")", referenceDiagnostic{Code: "22003", Message: `"` + tc.text + `" is out of range for type ` + tc.name})
				s.errorStage = "execute"
				run(s)
				run(unchanged)
			}
			for _, column := range []string{"r", "d"} {
				typ := "FLOAT4"
				if column == "d" {
					typ = "FLOAT8"
				}
				run(referenceSelect("SELECT "+column+" FROM floats WHERE "+column+"=1.0", []string{column}, []string{typ}, [][]any{{float64(1)}}))
				run(referenceSelect("SELECT "+column+" FROM floats WHERE "+column+"=1e100", []string{column}, []string{typ}, [][]any{}))
				for _, tc := range []struct{ literal, text string }{
					{"1e400", "1" + strings.Repeat("0", 400)},
					{"1e-400", "0." + strings.Repeat("0", 399) + "1"},
				} {
					s := referenceImplicitError("SELECT id FROM floats WHERE "+column+"="+tc.literal, referenceDiagnostic{Code: "22003", Message: `"` + tc.text + `" is out of range for type double precision`})
					s.selectRows = true
					s.errorStage = "execute"
					run(s)
					run(unchanged)
				}
			}
		})
	}
}
