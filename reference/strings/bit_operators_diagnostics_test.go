package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitOperatorsDiagnostics(t *testing.T) {
	bitOperatorsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE bop_errors(id INTEGER,f "bit",v VARBIT,g "bit",w VARBIT)`, 0))
		run(fixture.Exec("INSERT INTO bop_errors VALUES(1,B'1',B'1',B'00',B'00'),(2,B'',B'',B'0',B'0'),(3,NULL,NULL,B'00',B'00')", 3))
		// Actual width, not declared typmod/family, is the runtime constraint.
		for _, tc := range []struct{ op, message string }{
			{"&", "cannot AND bit strings of different sizes"}, {"|", "cannot OR bit strings of different sizes"}, {"#", "cannot XOR bit strings of different sizes"},
		} {
			for _, pair := range [][2]string{{"f", "g"}, {"f", "w"}, {"v", "g"}, {"v", "w"}, {"g", "f"}, {"w", "f"}, {"g", "v"}, {"w", "v"}} {
				q := "SELECT " + pair[0] + tc.op + pair[1] + " AS r FROM bop_errors WHERE id=$1"
				for _, prepared := range []bool{false, true} {
					for _, id := range []int{1, 2} {
						t.Run(q+fmt.Sprint(prepared, id), func(t *testing.T) {
							d := fixture.ControlsReferenceDiagnostic("22026", tc.message)
							s := fixture.ErrorDiagnostic(q, d, id)
							s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
							fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
						})
					}
				}
			}
			for _, suffix := range []string{"", " WHERE FALSE"} {
				q := "SELECT B'1'" + tc.op + "B'00' AS r" + suffix
				for _, prepared := range []bool{false, true} {
					t.Run(q+fmt.Sprint(prepared), func(t *testing.T) {
						d := fixture.ControlsReferenceDiagnostic("22026", tc.message)
						fixture.RunStep(t, p.Ctx, c, b.Recorder, bitOperatorsError(q, d, prepared, false, true))
					})
				}
			}
			bitOperatorsBoth(t, p, b, c, fixture.Select("SELECT f"+tc.op+"g AS r FROM bop_errors WHERE id=99", []string{"r"}, []string{"BIT"}, nil))
			bitOperatorsBoth(t, p, b, c, bitOperatorsOne("SELECT f"+tc.op+"g AS r FROM bop_errors WHERE id=3", "BIT", nil))
		}
		for _, tc := range []struct {
			q, code, message string
			position         int
		}{
			{"SELECT B'1'&1 AS r", "42883", "operator does not exist: bit & integer", 12},
			{"SELECT B'1'<<CAST(1 AS BIGINT) AS r", "42883", "operator does not exist: bit << bigint", 12},
			{"SELECT B'1'>>1.5 AS r", "42883", "operator does not exist: bit >> numeric", 12},
			{"SELECT B'1'&'2' AS r", "22P02", `"2" is not a valid binary digit`, 13},
			{"SELECT B'1'&'xG' AS r", "22P02", `"G" is not a valid hexadecimal digit`, 13},
			{"SELECT B'1'<<'bad' AS r", "22P02", `invalid input syntax for type integer: "bad"`, 14},
			{"SELECT B'1'<<'2147483648' AS r", "22003", `value "2147483648" is out of range for type integer`, 14},
			{"SELECT /* π🙂 */ B'1' & 1 AS r", "42883", "operator does not exist: bit & integer", 22},
			{"SELECT B'1' & NULL & '2' AS r", "22P02", `"2" is not a valid binary digit`, 22},
			{"SELECT B'1' & '2' & NULL AS r", "22P02", `"2" is not a valid binary digit`, 15},
			{"SELECT ~NULL AS r", "42725", "operator is not unique: ~ unknown", 8},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.q+fmt.Sprint(prepared), func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Position = fixture.Position(tc.position)
					if tc.code == "42883" {
						d.Hint = bitOperatorsBinaryHint
					}
					if tc.code == "42725" {
						d.Hint = bitOperatorsAmbiguousHint
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, bitOperatorsError(tc.q, d, prepared, true, true))
				})
			}
		}
		// Strict operators still evaluate row siblings; malformed constants fail
		// analysis even with NULL data or no selected rows.
		for _, tc := range []struct {
			q, code, message string
			position         int
			analysis         bool
		}{
			{"SELECT f & (g & B'1') AS r FROM bop_errors WHERE id=3", "22026", "cannot AND bit strings of different sizes", 0, false},
			{"SELECT (g & B'1') & f AS r FROM bop_errors WHERE id=3", "22026", "cannot AND bit strings of different sizes", 0, false},
			{"SELECT f & '2' AS r FROM bop_errors WHERE id=3", "22P02", `"2" is not a valid binary digit`, 12, true},
			{"SELECT f & '2' AS r FROM bop_errors WHERE FALSE", "22P02", `"2" is not a valid binary digit`, 12, true},
			{"SELECT f << 'bad' AS r FROM bop_errors WHERE FALSE", "22P02", `invalid input syntax for type integer: "bad"`, 13, true},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.q+fmt.Sprint(prepared), func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Position = fixture.Position(tc.position)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, bitOperatorsError(tc.q, d, prepared, tc.analysis, true))
				})
			}
		}
		run(fixture.Exec("CREATE TABLE bop_fixed(v BIT(3))", 0))
		run(fixture.Exec("CREATE TABLE bop_varying(v VARBIT(3))", 0))
		for _, tc := range []struct{ q, code, message string }{
			{"INSERT INTO bop_fixed VALUES(B'10'||B'10')", "22026", "bit string length 4 does not match type bit(3)"},
			{"INSERT INTO bop_fixed VALUES(~B'10')", "22026", "bit string length 2 does not match type bit(3)"},
			{"INSERT INTO bop_varying VALUES(B'10'||B'10')", "22001", "bit string too long for type bit varying(3)"},
		} {
			for _, prepared := range []bool{false, true} {
				t.Run(tc.q+fmt.Sprint(prepared), func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					fixture.RunStep(t, p.Ctx, c, b.Recorder, bitOperatorsError(tc.q, d, prepared, false, false))
				})
			}
		}
		run(fixture.Exec(`CREATE TABLE bop_atomic_source(id INTEGER,a "bit",b VARBIT)`, 0))
		run(fixture.Exec("INSERT INTO bop_atomic_source VALUES(1,B'10',B'11'),(2,B'10',B'1')", 2))
		run(fixture.Exec(`CREATE TABLE bop_atomic_target(id INTEGER,v "bit")`, 0))
		for _, prepared := range []bool{false, true} {
			t.Run("atomic"+fmt.Sprint(prepared), func(t *testing.T) {
				q := "INSERT INTO bop_atomic_target SELECT id,a&b FROM bop_atomic_source ORDER BY id"
				d := fixture.ControlsReferenceDiagnostic("22026", "cannot AND bit strings of different sizes")
				fixture.RunStep(t, p.Ctx, c, b.Recorder, bitOperatorsError(q, d, prepared, false, false))
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT id,v FROM bop_atomic_target ORDER BY id", []string{"id", "v"}, []string{"INT4", "BIT"}, nil))
			})
		}
		run(fixture.Exec("INSERT INTO bop_atomic_target VALUES(3,B'01')", 1))
		run(fixture.Select("SELECT id,v FROM bop_atomic_target ORDER BY id", []string{"id", "v"}, []string{"INT4", "BIT"}, [][]any{{int64(3), "01"}}))
	})
}
