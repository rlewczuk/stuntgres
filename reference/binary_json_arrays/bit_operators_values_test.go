package reference_test

import (
	"database/sql"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitOperatorsValues(t *testing.T) {
	bitOperatorsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE bop_values(id INTEGER,f "bit",v VARBIT)`, 0))
		run(fixture.Exec("INSERT INTO bop_values VALUES(1,B'10001',B'01101'),(2,B'',B''),(3,NULL,NULL)", 3))
		for _, pair := range [][2]string{{"f", "f"}, {"f", "v"}, {"v", "f"}, {"v", "v"}} {
			a, z := pair[0], pair[1]
			want := []any{"1000101101", "00001", "11101", "11100", "01110"}
			if a == "v" && z == "f" {
				want = []any{"0110110001", "00001", "11101", "11100", "10010"}
			}
			if a == z {
				v, not := "10001", "01110"
				if a == "v" {
					v, not = "01101", "10010"
				}
				want = []any{v + v, v, v, "00000", not}
			}
			q := "SELECT " + a + "||" + z + " AS cat," + a + "&" + z + " AS a," + a + "|" + z + " AS o," + a + "#" + z + " AS x,~" + a + " AS n FROM bop_values WHERE id=$1"
			for _, id := range []int{1, 2, 3, 99} {
				rows := [][]any{want}
				if id == 2 {
					rows = [][]any{{"", "", "", "", ""}}
				}
				if id == 3 {
					rows = [][]any{{nil, nil, nil, nil, nil}}
				}
				if id == 99 {
					rows = nil
				}
				bitOperatorsBoth(t, p, b, c, fixture.Select(q, []string{"cat", "a", "o", "x", "n"}, []string{"VARBIT", "BIT", "BIT", "BIT", "BIT"}, rows, id))
			}
		}
		for _, tc := range []struct{ expr, typ, want string }{
			{"B'10001'||B'011'", "VARBIT", "10001011"},
			{"B''||B'00100'", "VARBIT", "00100"}, {"B'00100'||B''", "VARBIT", "00100"},
			{"X'0f'||B'1'", "VARBIT", "000011111"},
			{"B'101010101'&B'010101010'", "BIT", "000000000"},
			{"B'101010101'|B'010101010'", "BIT", "111111111"},
			{"B'101010101'#B'010101010'", "BIT", "111111111"},
			{"~B'000000000'", "BIT", "111111111"},
			{"B'000000000'||B'1'", "VARBIT", "0000000001"},
		} {
			t.Run(tc.expr, func(t *testing.T) {
				bitOperatorsBoth(t, p, b, c, bitOperatorsOne("SELECT "+tc.expr+" AS r", tc.typ, tc.want))
			})
		}
		// Explicit independent wide vectors: the first and last digits both matter.
		a, z := "1"+strings.Repeat("0", 63)+"1", "0"+strings.Repeat("1", 63)+"0"
		for _, tc := range []struct{ op, typ, want string }{
			{"&", "BIT", strings.Repeat("0", 65)}, {"|", "BIT", strings.Repeat("1", 65)},
			{"#", "BIT", strings.Repeat("1", 65)}, {"||", "VARBIT", a + z},
		} {
			bitOperatorsBoth(t, p, b, c, bitOperatorsOne("SELECT B'"+a+"'"+tc.op+"B'"+z+"' AS r", tc.typ, tc.want))
		}
		bitOperatorsBoth(t, p, b, c, bitOperatorsOne("SELECT ~B'"+strings.Repeat("0", 65)+"' AS r", "BIT", strings.Repeat("1", 65)))
	})
}

func TestReferenceBitOperatorsShifts(t *testing.T) {
	bitOperatorsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			n           int64
			left, right string
		}{
			{0, "10001", "10001"}, {1, "00010", "01000"}, {2, "00100", "00100"}, {3, "01000", "00010"}, {4, "10000", "00001"},
			{5, "00000", "00000"}, {6, "00000", "00000"}, {32, "00000", "00000"}, {64, "00000", "00000"},
			{2147483647, "00000", "00000"}, {-2147483648, "00000", "00000"},
			{-1, "01000", "00010"}, {-2, "00100", "00100"}, {-3, "00010", "01000"}, {-4, "00001", "10000"},
			{-5, "00000", "00000"}, {-6, "00000", "00000"}, {-32, "00000", "00000"}, {-64, "00000", "00000"},
		} {
			t.Run(fmt.Sprint(tc.n), func(t *testing.T) {
				bitOperatorsBoth(t, p, b, c, fixture.Select("SELECT B'10001' << $1 AS l,B'10001' >> $1 AS r,B'' << $1 AS e,B'' >> $1 AS f", []string{"l", "r", "e", "f"}, []string{"BIT", "BIT", "BIT", "BIT"}, [][]any{{tc.left, tc.right, "", ""}}, tc.n))
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE bop_counts(s SMALLINT,i INTEGER,b INTEGER)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO bop_counts VALUES(3,32,64)", 1))
		bitOperatorsBoth(t, p, b, c, fixture.Select("SELECT B'10001'<<s AS l,B'10001'>>s AS r,1<<i AS i,CAST(1 AS BIGINT)<<b AS b FROM bop_counts", []string{"l", "r", "i", "b"}, []string{"BIT", "BIT", "INT4", "INT8"}, [][]any{{"01000", "00010", int64(1), int64(1)}}))
		wide := "1" + strings.Repeat("0", 63) + "1"
		bitOperatorsBoth(t, p, b, c, fixture.Select("SELECT B'"+wide+"'<<64 AS l,B'"+wide+"'>>64 AS r", []string{"l", "r"}, []string{"BIT", "BIT"}, [][]any{{"1" + strings.Repeat("0", 64), strings.Repeat("0", 64) + "1"}}))
	})
}

func TestReferenceBitOperatorsComparisons(t *testing.T) {
	bitOperatorsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Ascending witnesses asserted as predicates, never ORDER BY a bit key.
		for _, pair := range [][2]string{{"", "0"}, {"0", "00"}, {"00", "001"}, {"001", "01"}, {"01", "1"}, {"1", "10"}, {"10", "100"}} {
			q := "SELECT B'" + pair[0] + "'=B'" + pair[1] + "' AS eq,B'" + pair[0] + "'<>B'" + pair[1] + "' AS ne,B'" + pair[0] + "'!=B'" + pair[1] + "' AS alias,B'" + pair[0] + "'<B'" + pair[1] + "' AS lt,B'" + pair[0] + "'<=B'" + pair[1] + "' AS le,B'" + pair[0] + "'>B'" + pair[1] + "' AS gt,B'" + pair[0] + "'>=B'" + pair[1] + "' AS ge"
			bitOperatorsBoth(t, p, b, c, fixture.Select(q, []string{"eq", "ne", "alias", "lt", "le", "gt", "ge"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{false, true, true, true, true, false, false}}))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE bop_compare(f "bit",v VARBIT)`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO bop_compare VALUES(B'1',B'01'),(B'10',B'10'),(NULL,NULL)", 3))
		for _, op := range []string{"=", "<>", "!=", "<", "<=", ">", ">="} {
			want := map[string][]any{"=": {false, true, nil}, "<>": {true, false, nil}, "!=": {true, false, nil}, "<": {false, false, nil}, "<=": {false, true, nil}, ">": {true, false, nil}, ">=": {true, true, nil}}[op]
			s := fixture.Select("SELECT f"+op+"v AS r FROM bop_compare", []string{"r"}, []string{"BOOL"}, [][]any{{want[0]}, {want[1]}, {want[2]}})
			s.Bag = true
			bitOperatorsBoth(t, p, b, c, s)
		}
		bitOperatorsBoth(t, p, b, c, fixture.Select("SELECT B'1' IS DISTINCT FROM B'10' AS d,NULL IS DISTINCT FROM B'1' AS n,NULL IS NOT DISTINCT FROM NULL AS e,B'1'=B'1' AS eq,B'1'<=B'1' AS le,B'1'>=B'1' AS ge", []string{"d", "n", "e", "eq", "le", "ge"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true, true, true}}))
	})
}
