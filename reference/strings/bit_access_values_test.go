package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitAccessValues(t *testing.T) { fixture.BitAccessScenario(t, bitAccessValues) }
func bitAccessValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	both := func(s fixture.Step) { bitStringFunctionsBoth(t, p, b, c, s) }
	both(fixture.Select("SELECT get_bit(B'101010101010101010',6) AS g,set_bit(B'101010101010101010',6,0) AS s", []string{"g", "s"}, []string{"INT4", "BIT"}, [][]any{{int64(1), "101010001010101010"}}))
	both(fixture.Select(`SELECT get_bit(B'10000000',0) AS g,get_bit('\x80'::bytea,0) AS b,get_bit(X'80',7) AS z`, []string{"g", "b", "z"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(0), int64(0)}}))
	// Independently specified unequal endpoint witnesses, including byte boundaries.
	for _, tc := range []struct {
		source, first, last string
		tail                int
	}{
		{"1", "0", "0", 1},
		{"1001010", "0001010", "1001011", 0},
		{"10010100", "00010100", "10010101", 0},
		{"100000001", "000000001", "100000000", 1},
		{"1001010000010100", "0001010000010100", "1001010000010101", 0},
		{"10010100000101001", "00010100000101001", "10010100000101000", 1},
	} {
		q := fmt.Sprintf("SELECT get_bit(B'%s',0) AS f,get_bit(B'%s',%d) AS l,set_bit(B'%s',0,0) AS s,set_bit(B'%s',%d,%d) AS e", tc.source, tc.source, len(tc.source)-1, tc.source, tc.source, len(tc.source)-1, 1-tc.tail)
		both(fixture.Select(q, []string{"f", "l", "s", "e"}, []string{"INT4", "INT4", "BIT", "BIT"}, [][]any{{int64(1), int64(tc.tail), tc.first, tc.last}}))
	}
	both(fixture.Select("SELECT set_bit(B'000000000',8,1) AS s,get_bit(set_bit(B'100000001',0,0),0) AS g,set_bit(B'001',1,0) AS n", []string{"s", "g", "n"}, []string{"BIT", "INT4", "BIT"}, [][]any{{"000000001", int64(0), "001"}}))
	for _, typ := range []string{"BIT(9)", "VARBIT(9)", "VARBIT"} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ba_values(v "+typ+")", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ba_values VALUES(B'100000001')", 1))
		both(fixture.Select("SELECT get_bit(v,8) AS g,set_bit(v,8,0) AS s FROM ba_values", []string{"g", "s"}, []string{"INT4", "BIT"}, [][]any{{int64(1), "100000000"}}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DROP TABLE ba_values", 0))
	}
}

func TestReferenceBitAccessTyping(t *testing.T) {
	fixture.BitAccessScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ba_typing(v VARBIT)", 0))
		for _, tc := range []struct{ q, signature string }{
			{"SELECT get_bit(B'1',0::bigint)", "get_bit(bit, bigint)"},
			{"SELECT set_bit(B'1',0,1::bigint)", "set_bit(bit, integer, bigint)"},
			{"SELECT get_bit(v,0::bigint) FROM ba_typing", "get_bit(bit varying, bigint)"},
			{"SELECT set_bit(v,0,1::bigint) FROM ba_typing", "set_bit(bit varying, integer, bigint)"},
			{"SELECT get_bit(B'1','0'::text)", "get_bit(bit, text)"},
			{"SELECT get_bit(B'1')", "get_bit(bit)"},
			{"SELECT set_bit(B'1',0)", "set_bit(bit, integer)"},
		} {
			fixture.BinaryAccessAnalysisError(t, p, b, c, tc.q, fixture.Diagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: fixture.BinaryHashHint, Position: "8"})
		}
		bitStringFunctionsBoth(t, p, b, c, fixture.Select("SELECT get_bit(B'10',0::smallint) AS g,set_bit(B'10',1::smallint,1::smallint) AS s,get_bit('101',0) AS u", []string{"g", "s", "u"}, []string{"INT4", "BIT", "INT4"}, [][]any{{int64(1), "11", int64(1)}}))
	})
}
