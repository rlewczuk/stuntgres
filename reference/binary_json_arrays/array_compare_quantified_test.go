package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayCompareQuantifiedTruth(t *testing.T) {
	arrayCompareScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, left, array string
			want              []any
		}{
			{"empty", "1", "'{}'::INT[]", []any{false, false, true, false, false, true}},
			{"null_array", "1", "NULL::INT[]", []any{nil, nil, nil, nil, nil, nil}},
			{"null_element", "1", "ARRAY[NULL::INT]", []any{nil, nil, nil, nil, nil, nil}},
			{"match_null_last", "1", "ARRAY[1,NULL]", []any{true, true, nil, nil, nil, false}},
			{"match_null_first", "1", "ARRAY[NULL,1]", []any{true, true, nil, nil, nil, false}},
			{"miss_null_last", "1", "ARRAY[2,NULL]", []any{nil, nil, false, true, true, nil}},
			{"miss_null_first", "1", "ARRAY[NULL,2]", []any{nil, nil, false, true, true, nil}},
			{"all_match", "1", "ARRAY[1,1]", []any{true, true, true, false, false, false}},
			{"mixed", "1", "ARRAY[1,2]", []any{true, true, false, true, true, false}},
			{"null_left_empty", "NULL::INT", "'{}'::INT[]", []any{false, false, true, false, false, true}},
			{"null_left_nonempty", "NULL::INT", "ARRAY[1]", []any{nil, nil, nil, nil, nil, nil}},
			{"null_left_null", "NULL::INT", "NULL::INT[]", []any{nil, nil, nil, nil, nil, nil}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := "SELECT " + tc.left + "=ANY(" + tc.array + ")," + tc.left + "=SOME(" + tc.array + ")," + tc.left + "=ALL(" + tc.array + ")," + tc.left + "<>ANY(" + tc.array + ")," + tc.left + "!=SOME(" + tc.array + ")," + tc.left + "<>ALL(" + tc.array + ")"
				arrayCompareBoth(t, p, b, c, arrayCompareBool(q, tc.want))
			})
		}
		for _, tc := range []struct {
			op       string
			any, all any
		}{
			{"=", true, false}, {"<>", true, false}, {"!=", true, false},
			{"<", nil, false}, {"<=", true, false}, {">", true, false}, {">=", true, nil},
		} {
			t.Run("operator_"+tc.op, func(t *testing.T) {
				arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT 2"+tc.op+"ANY(ARRAY[1,2,NULL]),2"+tc.op+"SOME(ARRAY[1,2,NULL]),2"+tc.op+"ALL(ARRAY[1,2,NULL])", []any{tc.any, tc.any, tc.all}))
				arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT NULL::INT"+tc.op+"ANY(ARRAY[1]),NULL::INT"+tc.op+"SOME('{}'::INT[]),NULL::INT"+tc.op+"ALL('{}'::INT[])", []any{nil, false, true}))
			})
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ac_quant(id INT,x INT,a INT[])", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ac_quant VALUES(1,1,'{1,NULL}'),(2,3,'{1,NULL}'),(3,NULL,'{}'),(4,1,NULL),(5,1,'{1,NULL}')", 5))
		s := fixture.Select("SELECT x=ANY(a),x<>ALL(a) FROM ac_quant", []string{"?column?", "?column?"}, []string{"BOOL", "BOOL"}, [][]any{{true, false}, {nil, nil}, {false, true}, {nil, nil}, {true, false}})
		s.Bag = true
		arrayCompareBoth(t, p, b, c, s)
		for _, tc := range []struct {
			expr string
			ids  [][]any
		}{
			{"x=ANY(a)", [][]any{{int64(1)}, {int64(5)}}},
			{"x=SOME(a)", [][]any{{int64(1)}, {int64(5)}}},
			{"x<>ALL(a)", [][]any{{int64(3)}}},
			{"x<ANY(a)", nil},
			{"x<=SOME(a)", [][]any{{int64(1)}, {int64(5)}}},
			{"x>ANY(a)", [][]any{{int64(2)}}},
			{"x>=ALL(a)", [][]any{{int64(3)}}},
		} {
			arrayCompareBoth(t, p, b, c, fixture.Select("SELECT id FROM ac_quant WHERE "+tc.expr+" ORDER BY id", []string{"id"}, []string{"INT4"}, tc.ids))
		}
		arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT 2=ANY('[-1:0][3:4]={{1,NULL},{2,3}}'::INT[]),4<>ALL('[-1:0][3:4]={{1,NULL},{2,3}}'::INT[]),2=ANY($1::INT[]),1 OPERATOR(pg_catalog.=) SOME(ARRAY[1])", []any{true, nil, true, true}, "{1,2,NULL}"))
	})
}
