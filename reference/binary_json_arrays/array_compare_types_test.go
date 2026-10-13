package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayCompareTypesAndFrames(t *testing.T) {
	arrayCompareScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE TABLE ac_types(i INT,j BIGINT)",
			"CREATE DOMAIN ac_element AS INT", "CREATE DOMAIN ac_chain AS ac_element",
			"CREATE DOMAIN ac_whole AS INT[]", "CREATE TYPE ac_enum AS ENUM('low','high')",
			"SET TIME ZONE 'UTC'",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO ac_types VALUES(1,2147483648)", 1))
		for _, tc := range []struct {
			name, expr string
			want       any
		}{
			{"lhs_common_widening", "'2147483648' IN(1,2147483648)", true},
			{"width_exact", "9007199254740993 IN(9007199254740992,9007199254740993)", true},
			{"width_miss", "9007199254740993 IN(9007199254740992)", false},
			{"int8_max", "9223372036854775807=ANY(ARRAY[9223372036854775806,9223372036854775807])", true},
			{"int8_min", "(-9223372036854775808)::BIGINT<ALL(ARRAY[-2147483648::BIGINT,0])", true},
			{"smallint", "1::SMALLINT=ANY('{1,2}'::SMALLINT[])", true},
			{"cross_width", "2147483648::BIGINT>ALL(ARRAY[1,2147483647])", true},
			{"numeric_exact", "9007199254740993::NUMERIC>ALL(ARRAY[9007199254740992::NUMERIC])", true},
			{"numeric_nan", "'NaN'::NUMERIC=ANY(ARRAY['NaN'::NUMERIC])", true},
			{"float_nan", "'NaN'::FLOAT8>ALL(ARRAY['Infinity'::FLOAT8])", true},
			{"real", "1::REAL=ANY(ARRAY[1::REAL,2::REAL])", true},
			{"bool", "FALSE<ALL(ARRAY[TRUE])", true},
			{"text_collation", "'a'::TEXT COLLATE \"C\"<ALL(ARRAY['z'::TEXT,'é'::TEXT])", true},
			{"name", "'a'::NAME=ANY(ARRAY['a'::NAME])", true},
			{"bpchar_padding", "'a'::CHAR(2)=ANY(ARRAY['a '::CHAR(2)])", true},
			{"varchar", "'abc'::VARCHAR(3) IN('abc'::VARCHAR(3),'def'::VARCHAR(3))", true},
			{"bytea", "'\\x0080'::BYTEA=ANY(ARRAY['\\x0080'::BYTEA,'\\xff'::BYTEA])", true},
			{"oid", "1::OID=ANY(ARRAY[1::OID,2::OID])", true},
			{"cross_oid", "1=ANY(ARRAY[1::OID])", true},
			{"regclass", "'ac_types'::REGCLASS=ANY(ARRAY['ac_types'::REGCLASS])", true},
			{"regtype", "'integer'::REGTYPE=ANY(ARRAY['int4'::REGTYPE])", true},
			{"uuid", "'00000000-0000-0000-0000-000000000001'::UUID<ALL(ARRAY['00000000-0000-0000-0000-000000000002'::UUID])", true},
			{"date", "DATE '2000-01-01'<ALL(ARRAY[DATE '2000-01-02'])", true},
			{"time", "TIME '01:00'=ANY(ARRAY[TIME '01:00'])", true},
			{"timetz", "TIMETZ '01:00+00'=ANY(ARRAY[TIMETZ '02:00+01'])", false},
			{"time_timetz", "TIME '01:00'=ANY(ARRAY[TIMETZ '01:00+00'])", true},
			{"timestamp", "TIMESTAMP '2000-01-01'=ANY(ARRAY[TIMESTAMP '2000-01-01'])", true},
			{"timestamptz", "TIMESTAMPTZ '2000-01-01 00:00+00'=ANY(ARRAY[TIMESTAMPTZ '1999-12-31 19:00-05'])", true},
			{"interval", "INTERVAL '1 day'=ANY(ARRAY[INTERVAL '24 hours'])", true},
			{"money_list", "'1.00'::MONEY IN('1.00'::MONEY,'2.00'::MONEY)", true},
			{"inet_list", "'192.0.2.1'::INET IN('192.0.2.1'::INET,'192.0.2.2'::INET)", true},
			{"cidr_inet_list", "'192.0.2.0/24'::CIDR IN('192.0.2.0/24'::INET)", true},
			{"enum", "'low'::ac_enum<ALL(ARRAY['high'::ac_enum])", true},
			{"jsonb", "'{\"x\":1}'::JSONB=ANY(ARRAY['{\"x\":1}'::JSONB])", true},
			{"array_domain", "1=ANY('{1,NULL}'::ac_whole)", true},
			{"element_domain", "1=ANY('{1,NULL}'::ac_element[])", true},
			{"domain_chain", "1=ANY('{1,NULL}'::ac_chain[])", true},
			{"nonvar_expression", "'2147483648' IN(1+0,2147483648+0)", true},
			{"mixed_known_bigint", "2147483648::BIGINT IN(1,2147483648,i,j) FROM ac_types", true},
			{"child_local_not_var", "'2147483648' IN((SELECT i FROM ac_types),2147483648) FROM ac_types", true},
		} {
			t.Run(tc.name, func(t *testing.T) { arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT "+tc.expr, []any{tc.want})) })
		}
		for _, tc := range []struct {
			name string
			step fixture.Step
		}{
			{"batch_int8", fixture.Select("SELECT $1 IN(1,2147483648),$1", []string{"?column?", "?column?"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(2147483648)}}, "2147483648")},
			// Source-derived: batch coercion updates this original Param occurrence
			// before Var pairs copy it; unknown literal occurrences remain unknown.
			{"mixed_param_var", fixture.Select("SELECT $1 IN(1,2147483648,i),$1 FROM ac_types", []string{"?column?", "?column?"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(2147483648)}}, "2147483648")},
			{"mixed_repeated_param_var", fixture.Select("SELECT $1 IN($1,2147483648,i),$1 FROM ac_types", []string{"?column?", "?column?"}, []string{"BOOL", "INT8"}, [][]any{{true, int64(2147483648)}}, "2147483648")},
			{"singleton_text", fixture.Select("SELECT $1 IN($2),$1,$2", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "TEXT", "TEXT"}, [][]any{{true, "word", "word"}}, "word", "word")},
			{"repeated_unknown_singleton", fixture.Select("SELECT $1 IN($1),$1", []string{"?column?", "?column?"}, []string{"BOOL", "TEXT"}, [][]any{{true, "word"}}, "word")},
			{"repeated_unknown_batch", fixture.Select("SELECT $1 IN($1,1),$1", []string{"?column?", "?column?"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(2)}}, "2")},
			{"known_array_left", fixture.Select("SELECT $1=ANY(ARRAY[1,2]),$1", []string{"?column?", "?column?"}, []string{"BOOL", "INT4"}, [][]any{{true, int64(2)}}, "2")},
			{"unknown_array", fixture.ArrayReferenceSelect("SELECT $1=ANY($2),$1,$2", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "TEXT", "_TEXT"}, []string{"BOOL", "TEXT", "_TEXT"}, [][]any{{true, "word", "{word,other}"}}, b.PG, "word", "{word,other}")},
			{"known_scalar_array", fixture.ArrayReferenceSelect("SELECT 1=ANY($1),$1", []string{"?column?", "?column?"}, []string{"BOOL", "_INT4"}, []string{"BOOL", "_INT4"}, [][]any{{true, "{1,2}"}}, b.PG, "{1,2}")},
			{"repeated_known", fixture.Select("SELECT $1::INT,$1 IN(1,2147483648),$1=ANY(ARRAY[1::BIGINT]),$1", []string{"int4", "?column?", "?column?", "?column?"}, []string{"INT4", "BOOL", "BOOL", "INT4"}, [][]any{{int64(1), true, true, int64(1)}}, "1")},
			{"repeated_batch", fixture.Select("SELECT $1 IN(1,2147483648),$1 IN($1),$1", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "BOOL", "INT8"}, [][]any{{true, true, int64(2147483648)}}, "2147483648")},
			{"param_nonvar", fixture.Select("SELECT $1 IN($2,2147483648),$1,$2", []string{"?column?", "?column?", "?column?"}, []string{"BOOL", "INT8", "INT8"}, [][]any{{true, int64(2147483648), int64(1)}}, "2147483648", "1")},
		} {
			t.Run(tc.name, func(t *testing.T) { arrayCompareBoth(t, p, b, c, tc.step) })
		}
		// These scalar providers are reachable, but their array declarations are
		// an existing prerequisite boundary, not owned by scalar comparisons.
		// ARRAY constructors retain the ARRAY token position, not '['.
		for _, tc := range []struct{ name, q, position string }{
			{"money_array_boundary", "SELECT '1.00'::MONEY<ALL(ARRAY['2.00'::MONEY])", "26"},
			{"inet_array_boundary", "SELECT '192.0.2.1'::INET=ANY(ARRAY['192.0.2.1'::INET])", "30"},
			{"cidr_inet_array_boundary", "SELECT '192.0.2.0/24'::CIDR=ANY(ARRAY['192.0.2.0/24'::INET])", "33"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				if b.PG {
					arrayCompareBoth(t, p, b, c, arrayCompareBool(tc.q, []any{true}))
					return
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "0A000", Message: "unsupported array element type", Position: tc.position})
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					if prepared {
						s.ErrorStage = "prepare"
					}
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
		arrayCompareHeldFrames(t, p, b, c)
	})
}
