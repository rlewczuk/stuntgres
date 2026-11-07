package reference_test

import (
	"database/sql"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSelectedCallOperandsValues(t *testing.T) {
	selectedCallScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			expression, name, typ string
			value                 any
		}{
			{"random()::text IS NOT NULL", "?column?", "BOOL", true},
			{"abs(-1)::text", "abs", "TEXT", "1"},
			{"abs(-1) * 2", "?column?", "INT4", int64(2)},
			{"1 + abs(-1)", "?column?", "INT4", int64(2)},
			{"CAST(abs(-1) AS text)", "abs", "TEXT", "1"},
			{"pg_catalog.abs(-1)::text", "abs", "TEXT", "1"},
			{"round(2.5)::int", "round", "INT4", int64(3)},
			{"floor(random() * 10) >= 0", "?column?", "BOOL", true},
			{"10 / power(2,3)", "?column?", "FLOAT8", float64(1.25)},
			{"sqrt(4)::int", "sqrt", "INT4", int64(2)},
			{"upper('a')::text", "upper", "TEXT", "A"},
			{"(abs(-1)) + 1", "?column?", "INT4", int64(2)},
			{"abs(-round(-1.5)::int)", "abs", "INT4", int64(2)},
		} {
			selectedCallBoth(t, p, b, c, fixture.Select("SELECT "+tc.expression, []string{tc.name}, []string{tc.typ}, [][]any{{tc.value}}))
		}
		selectedCallReadModes(t, p, b, c, "SELECT random()", "random", "FLOAT8", func(t *testing.T, v any) { selectedCallRange(t, v, 1) })
		selectedCallReadModes(t, p, b, c, "SELECT floor(random()*10)", "floor", "FLOAT8", func(t *testing.T, v any) {
			selectedCallRange(t, v, 10)
			if v.(float64) != math.Trunc(v.(float64)) {
				t.Fatal("floor returned noninteger", v)
			}
		})
		for _, ordered := range []bool{false, true} {
			q := "SELECT x FROM (VALUES(-1),(0),(1),(2),(NULL)) AS selected_x(x) WHERE abs(x)+0=1"
			if ordered {
				q += " ORDER BY x"
			}
			step := fixture.Select(q, []string{"x"}, []string{"INT4"}, [][]any{{int64(-1)}, {int64(1)}})
			step.Bag = !ordered
			selectedCallBoth(t, p, b, c, step)
		}
	})
}

// Each row is an independently specified existing signature. Both casts and
// operators must contain the literal terminal name, qualified and unqualified.
func TestReferenceSelectedCallOperandsNames(t *testing.T) {
	type nameCase struct {
		name, args, cast, typ  string
		want                   any
		operator, operatorType string
		operatorWant           any
		comparison             string
		ulps                   uint64
	}
	cases := []nameCase{
		{name: "cbrt", args: "8", cast: "float8", typ: "FLOAT8", want: float64(2)},
		{name: "degrees", args: "0", cast: "float8", typ: "FLOAT8", want: float64(0)},
		{name: "radians", args: "0", cast: "float8", typ: "FLOAT8", want: float64(0)},
		{name: "pi", cast: "float8", typ: "FLOAT8", want: 3.141592653589793, comparison: "> 0"},
		{name: "erf", args: "0", cast: "float8", typ: "FLOAT8", want: float64(0)},
		{name: "erfc", args: "0", cast: "float8", typ: "FLOAT8", want: float64(1)},
		{name: "gamma", args: "1", cast: "float8", typ: "FLOAT8", want: float64(1)},
		{name: "pg_table_is_visible", args: "0::oid", cast: "bool", typ: "BOOL", operator: " = FALSE", operatorType: "BOOL"},
		{name: "pg_type_is_visible", args: "0::oid", cast: "bool", typ: "BOOL", operator: " = FALSE", operatorType: "BOOL"},
		{name: "pg_collation_is_visible", args: "0::oid", cast: "bool", typ: "BOOL", operator: " = FALSE", operatorType: "BOOL"},
		{name: "pg_conversion_is_visible", args: "0::oid", cast: "bool", typ: "BOOL", operator: " = FALSE", operatorType: "BOOL"},
		{name: "pg_ts_config_is_visible", args: "0::oid", cast: "bool", typ: "BOOL", operator: " = FALSE", operatorType: "BOOL"},
		{name: "pg_ts_dict_is_visible", args: "0::oid", cast: "bool", typ: "BOOL", operator: " = FALSE", operatorType: "BOOL"},
		{name: "pg_ts_parser_is_visible", args: "0::oid", cast: "bool", typ: "BOOL", operator: " = FALSE", operatorType: "BOOL"},
		{name: "pg_ts_template_is_visible", args: "0::oid", cast: "bool", typ: "BOOL", operator: " = FALSE", operatorType: "BOOL"},
		{name: "pg_statistics_obj_is_visible", args: "0::oid", cast: "bool", typ: "BOOL", operator: " = FALSE", operatorType: "BOOL"},
		{name: "col_description", args: "0::oid,1", cast: "text", typ: "TEXT", operator: " = 'absent'", operatorType: "BOOL"},
		{name: "obj_description", args: "0::oid,'pg_class'", cast: "text", typ: "TEXT", operator: " = 'absent'", operatorType: "BOOL"},
		{name: "shobj_description", args: "0::oid,'pg_database'", cast: "text", typ: "TEXT", operator: " = 'absent'", operatorType: "BOOL"},
		{name: "num_nulls", args: "NULL,1", cast: "int4", typ: "INT4", want: int64(1)},
		{name: "num_nonnulls", args: "NULL,1", cast: "int4", typ: "INT4", want: int64(1)},
		{name: "current_schemas", args: "false", cast: "name[]", typ: "_NAME", want: "{pg_catalog,public}", operator: " = ARRAY['pg_catalog','public']::name[]", operatorType: "BOOL", operatorWant: true},
		{name: "random", cast: "float8", typ: "FLOAT8", operator: " >= 0", operatorType: "BOOL", operatorWant: true},
		{name: "random_normal", args: "7,0", cast: "float8", typ: "FLOAT8", want: float64(7), operator: " + 1::float8", operatorType: "FLOAT8", operatorWant: float64(8)},
		{name: "setseed", args: "0", cast: "text", typ: "TEXT", want: "", operator: "::text = ''", operatorType: "BOOL", operatorWant: true},
		{name: "min_scale", args: "8.4100", cast: "int4", typ: "INT4", want: int64(2)},
		{name: "round", args: "2.5", cast: "numeric", typ: "NUMERIC", want: "3"},
		{name: "scale", args: "8.4100", cast: "int4", typ: "INT4", want: int64(4)},
		{name: "trim_scale", args: "8.4100", cast: "numeric", typ: "NUMERIC", want: "8.41"},
		{name: "trunc", args: "1.9", cast: "numeric", typ: "NUMERIC", want: "1"},
		{name: "sign", args: "-2.0", cast: "numeric", typ: "NUMERIC", want: "-1"},
		{name: "mod", args: "9,4", cast: "int4", typ: "INT4", want: int64(1)},
		{name: "abs", args: "-1", cast: "int4", typ: "INT4", want: int64(1)},
		{name: "ceil", args: "1.2", cast: "numeric", typ: "NUMERIC", want: "2"},
		{name: "ceiling", args: "1.2", cast: "numeric", typ: "NUMERIC", want: "2"},
		{name: "div", args: "9,4", cast: "numeric", typ: "NUMERIC", want: "2"},
		{name: "exp", args: "0::float8", cast: "float8", typ: "FLOAT8", want: float64(1)},
		{name: "factorial", args: "5", cast: "numeric", typ: "NUMERIC", want: "120"},
		{name: "floor", args: "1.8", cast: "numeric", typ: "NUMERIC", want: "1"},
		{name: "gcd", args: "12,18", cast: "int4", typ: "INT4", want: int64(6)},
		{name: "lcm", args: "12,18", cast: "int4", typ: "INT4", want: int64(36)},
		{name: "acos", args: "1", cast: "float8", typ: "FLOAT8", want: float64(0)},
		{name: "acosd", args: "1", cast: "float8", typ: "FLOAT8", want: float64(0)},
		{name: "asin", args: "0", cast: "float8", typ: "FLOAT8", want: float64(0)},
		{name: "asind", args: "0", cast: "float8", typ: "FLOAT8", want: float64(0)},
		{name: "atan", args: "0", cast: "float8", typ: "FLOAT8", want: float64(0)},
		{name: "atand", args: "0", cast: "float8", typ: "FLOAT8", want: float64(0)},
		{name: "atan2", args: "0,1", cast: "float8", typ: "FLOAT8", want: float64(0)},
		{name: "atan2d", args: "0,1", cast: "float8", typ: "FLOAT8", want: float64(0)},
		{name: "cos", args: "0", cast: "float8", typ: "FLOAT8", want: float64(1)},
		{name: "cosd", args: "0", cast: "float8", typ: "FLOAT8", want: float64(1)},
		{name: "cot", args: "1", cast: "float8", typ: "FLOAT8", want: .6420926159343306, comparison: "> 0", ulps: 2},
		{name: "cotd", args: "45", cast: "float8", typ: "FLOAT8", want: float64(1)},
		{name: "sin", args: "0", cast: "float8", typ: "FLOAT8", want: float64(0)},
		{name: "sind", args: "0", cast: "float8", typ: "FLOAT8", want: float64(0)},
		{name: "tan", args: "0", cast: "float8", typ: "FLOAT8", want: float64(0)},
		{name: "tand", args: "0", cast: "float8", typ: "FLOAT8", want: float64(0)},
	}
	selectedCallScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		selectedCallRun(t, p, b, c, fixture.Exec("SET search_path TO pg_catalog,public", 0))
		for _, tc := range cases {
			for _, qualifier := range []string{"", "pg_catalog."} {
				t.Run(qualifier+tc.name, func(t *testing.T) {
					call := qualifier + tc.name + "(" + tc.args + ")"
					castSQL := "SELECT CAST(" + call + " AS " + tc.cast + ") AS cast_value"
					if tc.name == "random" {
						selectedCallReadModes(t, p, b, c, castSQL, "cast_value", "FLOAT8", func(t *testing.T, v any) { selectedCallRange(t, v, 1) })
					} else if tc.typ == "FLOAT8" {
						selectedCallReadModes(t, p, b, c, castSQL, "cast_value", tc.typ, func(t *testing.T, v any) { selectedCallFloat(t, v, tc.want.(float64), tc.ulps) })
					} else {
						selectedCallBoth(t, p, b, c, fixture.Select(castSQL, []string{"cast_value"}, []string{tc.typ}, [][]any{{tc.want}}))
					}
					op, typ, want := tc.operator, tc.operatorType, tc.operatorWant
					if op == "" {
						op, typ, want = " + 0", tc.typ, tc.want
						if tc.typ == "FLOAT8" {
							op = " + 0::float8"
						}
					}
					q := "SELECT " + call + op + " AS operator_value"
					if typ == "FLOAT8" {
						selectedCallReadModes(t, p, b, c, q, "operator_value", typ, func(t *testing.T, v any) { selectedCallFloat(t, v, want.(float64), tc.ulps) })
					} else {
						selectedCallBoth(t, p, b, c, fixture.Select(q, []string{"operator_value"}, []string{typ}, [][]any{{want}}))
					}
					if tc.operator == "" || tc.name == "random_normal" {
						comparison := tc.comparison
						if comparison == "" {
							comparison = "= " + selectedCallNumber(tc.want)
						}
						selectedCallBoth(t, p, b, c, fixture.Select("SELECT "+call+" "+comparison+" AS comparison_value", []string{"comparison_value"}, []string{"BOOL"}, [][]any{{true}}))
					}
				})
			}
		}
		for _, qualifier := range []string{"", "pg_catalog."} {
			selectedCallBoth(t, p, b, c, fixture.Select("SELECT "+qualifier+"pg_type_is_visible('pg_catalog.int4'::regtype::oid) = TRUE AS visible", []string{"visible"}, []string{"BOOL"}, [][]any{{true}}))
			selectedCallReadModes(t, p, b, c, "SELECT CAST("+qualifier+"random_normal() AS float8) AS normal", "normal", "FLOAT8", func(t *testing.T, v any) {
				f, ok := v.(float64)
				if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
					t.Fatalf("normal finite FLOAT8: %#v", v)
				}
			})
		}
		// Side effects are asserted within this physical session, never by
		// comparing independent backends' draws.
		for _, prepared := range []bool{false, true} {
			seed := fixture.Select("SELECT setseed(0)::text = '' AS seeded", []string{"seeded"}, []string{"BOOL"}, [][]any{{true}})
			seed.Prepared = prepared
			selectedCallRun(t, p, b, c, seed)
			first := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
			seed = fixture.Select("SELECT CAST(pg_catalog.setseed(0) AS text) AS seeded", []string{"seeded"}, []string{"TEXT"}, [][]any{{""}})
			seed.Prepared = prepared
			selectedCallRun(t, p, b, c, seed)
			second := randomScalar(t, p, c, b, "SELECT random()", "random", "FLOAT8")
			selectedCallRange(t, first, 1)
			if first != second {
				t.Fatal("nested setseed did not reseed session", first, second)
			}
		}
		selectedCallBoth(t, p, b, c, fixture.Select("SELECT CAST(setseed(NULL) AS text) AS seeded", []string{"seeded"}, []string{"TEXT"}, [][]any{{nil}}))
	})
}
