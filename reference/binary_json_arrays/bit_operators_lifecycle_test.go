package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitOperatorsMetadataAndHeld(t *testing.T) {
	bitOperatorsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE bop_held(id INTEGER,f "bit",v VARBIT)`, 0))
		run(fixture.Exec("INSERT INTO bop_held VALUES(1,B'10001',B'01101'),(2,B'',B''),(3,NULL,NULL)", 3))
		names := []string{"cat", "a", "o", "x", "n", "l", "r", "eq"}
		types := []string{"VARBIT", "BIT", "BIT", "BIT", "BIT", "BIT", "BIT", "BOOL"}
		q := "SELECT f||v AS cat,f&v AS a,f|v AS o,f#v AS x,~v AS n,v<<2 AS l,v>>2 AS r,f=v AS eq FROM bop_held WHERE id=$1"
		for _, id := range []int{1, 2, 3, 99} {
			want := [][]any{{"1000101101", "00001", "11101", "11100", "10010", "10100", "00011", false}}
			if id == 2 {
				want = [][]any{{"", "", "", "", "", "", "", true}}
			}
			if id == 3 {
				want = [][]any{{nil, nil, nil, nil, nil, nil, nil, nil}}
			}
			if id == 99 {
				want = nil
			}
			for _, prepared := range []bool{false, true} {
				t.Run(q+fixture.Position(id)+map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
					var saved []*sql.ColumnType
					s := fixture.Select(q, names, types, want, id)
					s.Prepared = prepared
					s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
						var err error
						saved, err = r.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						bitOperatorsMetadata(t, saved, names, types)
					}
					s.CheckRaw = func(t *testing.T, col, row int, value any) bool {
						fixture.AssertRawValue(t, b.PG, types[col], value)
						if id == 2 && col < 7 && value == nil {
							t.Fatal("empty bit result became NULL")
						}
						if id == 2 && col < 7 && b.PG {
							if v, ok := value.([]byte); !ok || v == nil {
								t.Fatalf("empty raw bit %#v", value)
							}
						}
						return true
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
					bitOperatorsMetadata(t, saved, names, types)
				})
			}
		}
		// Truthful DB route in addition to the borrowed Conn and SAME held Stmt.
		bitOperatorsBoth(t, p, b, b.DB, bitOperatorsOne("SELECT B'1'||B'0' AS r", "VARBIT", "10"))
		heldSQL := "SELECT f & $1 AS a,f << $2 AS l,f = $1 AS eq FROM bop_held WHERE id=$3"
		stmt, err := c.PrepareContext(p.Ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: heldSQL}
		check := func(bits, count any, id int, want [][]any) {
			t.Helper()
			s := fixture.Select(heldSQL, []string{"a", "l", "eq"}, []string{"BIT", "BIT", "BOOL"}, want, bits, count, id)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
				cols, err := r.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				bitOperatorsMetadata(t, cols, s.Columns, s.Types)
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		initial := []byte("01101")
		var retained *sql.Rows
		var saved []*sql.ColumnType
		if !b.PG {
			retained, err = stmt.QueryContext(p.Ctx, initial, 3, 1)
			if err != nil {
				t.Fatal(err)
			}
			defer retained.Close()
			saved, err = retained.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
		} else {
			check(initial, 3, 1, [][]any{{"00001", "01000", false}})
		}
		initial[0] = '1'
		check("10001", -2, 1, [][]any{{"10001", "00100", true}})
		check(nil, nil, 1, [][]any{{nil, nil, nil}})
		check("01101", nil, 1, [][]any{{"00001", nil, false}})
		check("01101", 3, 99, nil)
		for _, tc := range []struct {
			name                   string
			bits, count            any
			code, message, context string
		}{
			{"bad-bit", "2", 3, "22P02", `"2" is not a valid binary digit`, "unnamed portal parameter $1 = '...'"},
			{"bad-hex", "xG", 3, "22P02", `"G" is not a valid hexadecimal digit`, "unnamed portal parameter $1 = '...'"},
			{"bad-count", "01101", "bad", "22P02", `invalid input syntax for type integer: "bad"`, "unnamed portal parameter $2 = '...'"},
			{"overflow-count", "01101", "2147483648", "22003", `value "2147483648" is out of range for type integer`, "unnamed portal parameter $2 = '...'"},
			{"wrong-width", "1", 3, "22026", "cannot AND bit strings of different sizes", ""},
		} {
			t.Run(tc.name, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Context = tc.context
				fixture.RunStep(t, p.Ctx, held, b.Recorder, bitOperatorsError(heldSQL, d, false, false, true, tc.bits, tc.count, 1))
			})
			check(nil, nil, 1, [][]any{{nil, nil, nil}})
			check("10001", -2, 1, [][]any{{"10001", "00100", true}})
		}
		run(fixture.Exec("INSERT INTO bop_held VALUES(4,B'1',B'0')", 1))
		if retained != nil {
			if !retained.Next() {
				t.Fatal(retained.Err())
			}
			var a, l, eq any
			if err := retained.Scan(&a, &l, &eq); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual([]any{a, l, eq}, []any{"00001", "01000", false}) {
				t.Fatal("retained frame changed", a, l, eq)
			}
			if retained.Next() || retained.Err() != nil {
				t.Fatal("retained snapshot changed", retained.Err())
			}
			bitOperatorsMetadata(t, saved, []string{"a", "l", "eq"}, []string{"BIT", "BIT", "BOOL"})
		}
	})
}
