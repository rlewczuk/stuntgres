package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func arrayOrderScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func arrayOrderVector(left, right string, order int) fixture.Step {
	ops := []string{"=", "<>", "!=", "<", "<=", ">", ">="}
	expressions := make([]string, len(ops))
	for i, op := range ops {
		expressions[i] = "(" + left + ") " + op + " (" + right + ")"
	}
	return arrayCompareBool("SELECT "+strings.Join(expressions, ","), []any{order == 0, order != 0, order != 0, order < 0, order <= 0, order > 0, order >= 0})
}

func TestReferenceArrayOrderValues(t *testing.T) {
	arrayOrderScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE TYPE ao_enum AS ENUM ('z','a')",
			"CREATE DOMAIN ao_int AS INT", "CREATE DOMAIN ao_chain AS ao_int",
			"CREATE DOMAIN ao_outer AS INT[]", "CREATE DOMAIN ao_nested AS INT[]",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for _, tc := range []struct {
			name, left, right string
			order             int
		}{
			{"first", "'{1,9}'::int[]", "'{2,0}'::int[]", -1},
			{"element_before_count", "'{2}'::int[]", "'{1,99}'::int[]", 1},
			{"prefix_count", "'{1}'::int[]", "'{1,0}'::int[]", -1},
			{"row_major", "'{{1,2},{3,4}}'::int[]", "'{{1,2},{3,5}}'::int[]", -1},
			{"element_before_rank", "'{2,0,0,0}'::int[]", "'{{1,9},{9,9}}'::int[]", 1},
			{"bounds", "'[0:1]={1,2}'::int[]", "'{1,2}'::int[]", -1},
			{"rank", "'{1,2,3,4}'::int[]", "'{{1,2},{3,4}}'::int[]", -1},
			{"lengths_before_bounds", "'[9:10][9:11]={{1,2,3},{4,5,6}}'::int[]", "'[0:2][0:1]={{1,2},{3,4},{5,6}}'::int[]", -1},
			{"later_length_before_first_bound", "'[9:10][1:2][1:3]={{{1,2,3},{4,5,6}},{{7,8,9},{10,11,12}}}'::int[]", "'[0:1][1:3][1:2]={{{1,2},{3,4},{5,6}},{{7,8},{9,10},{11,12}}}'::int[]", -1},
			{"later_bound", "'[0:1][2:3]={{1,2},{3,4}}'::int[]", "'[0:1][3:4]={{1,2},{3,4}}'::int[]", -1},
			{"null_equal", "'{NULL}'::int[]", "'{NULL}'::int[]", 0},
			{"null_last", "'{NULL}'::int[]", "'{1}'::int[]", 1},
			{"null_not_unknown", "'{1,NULL}'::int[]", "'{1,2}'::int[]", 1},
			{"null_prefix_equal", "'{1,NULL}'::int[]", "'{1,NULL}'::int[]", 0},
			{"empty_equal", "'{}'::int[]", "'{}'::int[]", 0},
			{"empty_first", "'{}'::int[]", "'{NULL}'::int[]", -1},
			{"bigint_exact", "'{9007199254740992}'::bigint[]", "'{9007199254740993}'::bigint[]", -1},
			{"numeric_exact", "'{9007199254740992}'::numeric[]", "'{9007199254740993}'::numeric[]", -1},
			{"numeric_nan", "'{NaN}'::numeric[]", "'{NaN}'::numeric[]", 0},
			{"float_nan", "'{NaN}'::float8[]", "'{1}'::float8[]", 1},
			{"bpchar_padding", "ARRAY['a '::char(3)]", "ARRAY['a'::char(1)]", 0},
			{"text_c", "ARRAY['10'] COLLATE \"C\"", "ARRAY['2'] COLLATE \"C\"", -1},
			{"enum_rank", "ARRAY['z'::ao_enum]", "ARRAY['a'::ao_enum]", -1},
			{"domain_chain", "ARRAY[1::ao_chain]", "ARRAY[2::ao_chain]", -1},
			{"outer_domain", "'{1}'::ao_outer", "'{2}'::int[]", -1},
			{"nested_domain", "ARRAY['{1}'::ao_nested]", "ARRAY['{2}'::ao_nested]", -1},
			{"registry_identity", "ARRAY[0::regclass]", "ARRAY[0::regclass]", 0},
			{"jsonb", "ARRAY['1'::jsonb]", "ARRAY['2'::jsonb]", -1},
			{"uuid", "ARRAY['00000000-0000-0000-0000-000000000001'::uuid]", "ARRAY['00000000-0000-0000-0000-000000000002'::uuid]", -1},
			{"date", "ARRAY['2000-01-01'::date]", "ARRAY['2000-01-02'::date]", -1},
			{"timestamp", "ARRAY['2000-01-01'::timestamp]", "ARRAY['2000-01-02'::timestamp]", -1},
			{"interval", "ARRAY['1 day'::interval]", "ARRAY['2 days'::interval]", -1},
			{"bytea", "ARRAY['\\x00'::bytea]", "ARRAY['\\x01'::bytea]", -1},
			{"boolean", "ARRAY[false]", "ARRAY[true]", -1},
		} {
			t.Run(tc.name, func(t *testing.T) { arrayCompareBoth(t, p, b, c, arrayOrderVector(tc.left, tc.right, tc.order)) })
		}
		for _, right := range []string{"'{1}'::int[]", "NULL::int[]"} {
			for _, op := range []string{"=", "<>", "!=", "<", "<=", ">", ">="} {
				arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT NULL::int[]"+op+right, []any{nil}))
			}
		}
		arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT '{1}'::int[]='{1}','{10}'<'{2}',2147483648::bigint>ALL(ARRAY[1,2147483647]),2=ANY('[-1:0][3:4]={{1,NULL},{2,3}}'::int[])", []any{true, true, true, true}))
		for _, tc := range []struct {
			rhs  string
			want []any
		}{
			{"'{}'::int[]", []any{false, false, true}}, {"NULL::int[]", []any{nil, nil, nil}},
			{"'{1}'::int[]", []any{true, true, true}}, {"'{2}'::int[]", []any{false, false, false}},
			{"'{NULL}'::int[]", []any{nil, nil, nil}}, {"'{1,NULL}'::int[]", []any{true, true, nil}},
			{"'{2,NULL}'::int[]", []any{nil, nil, false}}, {"'{1,2,NULL}'::int[]", []any{true, true, false}},
		} {
			arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT 1=ANY("+tc.rhs+"),1=SOME("+tc.rhs+"),1=ALL("+tc.rhs+")", tc.want))
		}
		arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT NULL::int=ANY('{}'::int[]),NULL::int=SOME('{}'::int[]),NULL::int=ALL('{}'::int[]),NULL::int=ANY('{1}'::int[]),NULL::int=ALL(NULL::int[])", []any{false, false, true, nil, nil}))
	})
}
