package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceComparisonBoundaries(t *testing.T) {
	cases := []struct {
		expression string
		want       any
	}{
		{"9007199254740993::int8 = 9007199254740993::numeric", true},
		{"9007199254740993::int8 > 9007199254740992::numeric", true},
		{"9007199254740993::numeric = 9007199254740992::float8", true},
		{"9007199254740993::int8 = 9007199254740992::float8", true},
		{"9223372036854775807::int8 < 9223372036854775808::numeric", true},
		{"(-9223372036854775808)::int8 > (-9223372036854775809)::numeric", true},
		{"'NaN'::numeric = 'NaN'::numeric", true}, {"'NaN'::numeric > 'Infinity'::numeric", true},
		{"'NaN'::float4 = 'NaN'::float8", true}, {"'NaN'::float8 > 'Infinity'::float4", true},
		{"'-Infinity'::float8 < '-1e308'::float8", true}, {"'-0'::float8 = '0'::float4", true},
		{"'4294967295'::oid > '2147483648'::oid", true},
		{"'4294967295'::regtype = '4294967295'::regclass", true},
		{"'4294967295'::xid = (-1)::int4", true},
		{"'18446744073709551615'::xid8 > '9223372036854775807'::xid8", true},
		{"'a '::bpchar = 'a'::bpchar", true}, {"'a '::bpchar = 'a'::varchar", true},
		{"'a '::varchar <> 'a'::text", true}, {"'A'::name < 'a'::text", true},
		{"'é'::text > 'z'::varchar", true},
		{"'infinity'::date = 'infinity'::timestamptz", true},
		{"'-infinity'::timestamp < '2000-01-01'::date", true},
		{"'2000-01-01 00:00:00+00'::timestamptz = '1999-12-31 19:00:00-05'::timestamptz", true},
		{"TIME '24:00' = INTERVAL '24 hours'", true},
		{"INTERVAL '1 month' = INTERVAL '30 days'", true},
		{"'00:00+00'::timetz > '00:00+01'::timetz", true},
		{"'10.0.0.0/8'::inet < '10.0.0.0/16'::cidr", true},
		{"'ffffffff-ffff-ffff-ffff-ffffffffffff'::uuid > '00000000-0000-0000-0000-000000000000'::uuid", true},
		{"'[0:1]={1,2}'::int4[] < '[1:2]={1,2}'::int4[]", true},
		{"ARRAY[1,NULL]::int4[] > ARRAY[1,2]::int4[]", true},
		{"ARRAY[[1,2]]::int4[] > ARRAY[1,2]::int4[]", true},
		{"'null'::jsonb < 'false'::jsonb", true},
	}
	prefix := strings.Repeat("x", 63)
	cases = append(cases, struct {
		expression string
		want       any
	}{comparisonSQLString(prefix) + "::name < " + comparisonSQLString(prefix+"y") + "::varchar", true})
	cases = append(cases, struct {
		expression string
		want       any
	}{comparisonSQLString(prefix+"y") + "::varchar > " + comparisonSQLString(prefix) + "::name", true})
	multibyte := strings.Repeat("é", 31)
	cases = append(cases, struct {
		expression string
		want       any
	}{comparisonSQLString(multibyte+"é") + "::name < " + comparisonSQLString(multibyte+"é") + "::varchar", true})
	// All admitted ranks, not only the rank-one seed used by the finite matrix.
	for rank := 1; rank <= 6; rank++ {
		array := comparisonSQLString(strings.Repeat("{", rank)+"1"+strings.Repeat("}", rank)) + "::int4[]"
		cases = append(cases, struct {
			expression string
			want       any
		}{array + " = " + array, true})
		cases = append(cases, struct {
			expression string
			want       any
		}{"'{}'::int4[] < " + array, true})
		if rank < 6 {
			higher := comparisonSQLString(strings.Repeat("{", rank+1)+"1"+strings.Repeat("}", rank+1)) + "::int4[]"
			cases = append(cases, struct {
				expression string
				want       any
			}{array + " < " + higher, true})
		}
	}
	for _, tc := range cases {
		t.Run(tc.expression, func(t *testing.T) {
			comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				comparisonCheckpointBool(t, p, b, tc.expression, tc.want)
			})
		})
	}
	// BIT and range values have executable contextual input codecs without
	// general CastExpr admission. Exercise their actual stored source types.
	for _, tc := range []struct {
		leftType, rightType, left, right, operator string
	}{
		{"bit(1)", "bit varying", "1", "10", "<"},
		{"bit varying", "bit(2)", "10", "01", ">"},
		{"int4range", "int4range", "empty", "[1,2)", "<"},
		{"int4range", "int4range", "[1,2]", "[1,3)", "="},
		{"int4multirange", "int4multirange", "{[1,2),[2,3)}", "{[1,3)}", "="},
	} {
		t.Run(tc.leftType+"_"+tc.operator+"_"+tc.left, func(t *testing.T) {
			comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE comparison_boundary(l "+tc.leftType+",r "+tc.rightType+")", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO comparison_boundary VALUES($1,$2)", 1, tc.left, tc.right))
				comparisonCheckpointRoutes(t, p, b, fixture.Select("SELECT l "+tc.operator+" r AS v FROM comparison_boundary", []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
			})
		})
	}
}

func TestReferenceComparisonStructural(t *testing.T) {
	comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{
			"CREATE DOMAIN comparison_number AS bigint CHECK(VALUE>0)",
			"CREATE DOMAIN comparison_number_chain AS comparison_number",
			"CREATE DOMAIN comparison_vector AS integer[]",
			"CREATE TYPE comparison_enum AS ENUM ('z','a')",
			"CREATE DOMAIN comparison_enum_domain AS comparison_enum",
			"CREATE TYPE comparison_struct AS (a integer,b integer)",
			"CREATE TYPE comparison_struct_other AS (a integer,b integer)",
			"CREATE TYPE comparison_struct_nested AS (p comparison_struct,z integer)",
			"CREATE DOMAIN comparison_struct_domain AS comparison_struct",
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
		}
		pairs := [][2]string{
			{"1::comparison_number_chain", "2::numeric"},
			{"1::numeric", "2::comparison_number_chain"},
			{"ARRAY[1]::comparison_vector", "ARRAY[2]::integer[]"},
			{"ARRAY[1::comparison_number]", "ARRAY[2::comparison_number]"},
			{"'z'::comparison_enum", "'a'::comparison_enum"},
			{"ARRAY['z'::comparison_enum]", "ARRAY['a'::comparison_enum]"},
			{"ROW(1,NULL)::comparison_struct", "ROW(2,NULL)::comparison_struct_other"},
			{"ARRAY[ROW(1,NULL)::comparison_struct]", "ARRAY[ROW(2,NULL)::comparison_struct]"},
			{"ROW(ROW(1,NULL)::comparison_struct,0)::comparison_struct_nested", "ROW(ROW(2,NULL)::comparison_struct,0)::comparison_struct_nested"},
			{"(ROW(1,NULL)::comparison_struct)::comparison_struct_domain", "(ROW(2,NULL)::comparison_struct)::comparison_struct_domain"},
		}
		for _, pair := range pairs {
			// A cast RowExpr on the left of ANY(subquery) is expanded into
			// fields by transformSubLink. A scalar subquery preserves one
			// ordinary composite operand against the single composite column.
			subqueryLeft := pair[0]
			if strings.Contains(pair[0], "comparison_struct") && !strings.HasPrefix(pair[0], "ARRAY") {
				subqueryLeft = "(SELECT " + pair[0] + ")"
			}
			for op, spelling := range comparisonSpellings {
				want := comparisonTruth(op, -1)
				comparisonCheckpointBool(t, p, b, pair[0]+" "+spelling+" "+pair[1], want)
				for _, quantifier := range []string{"ANY", "SOME", "ALL"} {
					comparisonCheckpointBool(t, p, b, subqueryLeft+" "+spelling+" "+quantifier+"(SELECT "+pair[1]+")", want)
					if !strings.HasPrefix(pair[1], "ARRAY") {
						comparisonCheckpointBool(t, p, b, pair[0]+" "+spelling+" "+quantifier+"(ARRAY["+pair[1]+"])", want)
					}
				}
			}
			comparisonCheckpointBool(t, p, b, pair[0]+" IN ("+pair[1]+")", false)
			// No common scalar type selects pairwise comparison, not 42804.
			comparisonCheckpointBool(t, p, b, pair[0]+" IN ("+pair[1]+","+pair[1]+")", false)
			comparisonCheckpointBool(t, p, b, subqueryLeft+" NOT IN (SELECT "+pair[1]+")", true)
			comparisonCheckpointBool(t, p, b, pair[0]+" IS DISTINCT FROM "+pair[1], true)
			comparisonCheckpointBool(t, p, b, pair[0]+" IS NOT DISTINCT FROM "+pair[1], false)
			for form, spelling := range comparisonBetweenForms {
				want := form%2 == 0
				comparisonCheckpointBool(t, p, b, pair[0]+" "+spelling+" "+pair[0]+" AND "+pair[1], want)
				comparisonCheckpointBool(t, p, b, pair[0]+" "+spelling+" "+pair[1]+" AND "+pair[0], (form >= 4) == want)
			}
		}
		// Supported ordinary-composite and field-wise row subqueries must
		// retain different NULL-field semantics, not just different syntax.
		for _, tc := range []struct {
			expr string
			want any
		}{
			{"(SELECT ROW(1,NULL)::comparison_struct) = ANY(SELECT ROW(1,NULL)::comparison_struct_other)", true},
			{"ROW(1,NULL::integer) = ANY(SELECT 1,NULL::integer)", nil},
			{"ROW(1,NULL)::comparison_struct = ANY(SELECT 1,NULL::integer)", nil},
			{"ROW(1,NULL)::comparison_struct < ANY(SELECT 2,0)", true},
			{"ROW(1,NULL)::comparison_struct_domain = ANY(SELECT ROW(1,NULL)::comparison_struct)", true},
			{"(SELECT ROW(1,NULL)::comparison_struct) > ALL(SELECT ROW(1,0)::comparison_struct_other)", true},
			{"ROW(1,NULL::integer) > ALL(SELECT 1,0)", nil},
		} {
			comparisonCheckpointBool(t, p, b, tc.expr, tc.want)
		}
		arity := "SELECT ROW(1,NULL)::comparison_struct = ANY(SELECT ROW(1,NULL)::comparison_struct_other) AS v"
		comparisonCheckpointAnalysis(t, p, b, arity, fixture.Diagnostic{Code: "42601", Message: "subquery has too few columns", Position: comparisonCheckpointPosition(arity, "= ANY")})
		// ANYENUM does not admit domains over enum; retain the existing released
		// generic-identity restriction rather than treating domain peeling globally.
		for _, spelling := range comparisonSpellings {
			q := "SELECT 'z'::comparison_enum_domain " + spelling + " 'a'::comparison_enum AS v"
			canonical := spelling
			if canonical == "!=" {
				canonical = "<>"
			}
			comparisonCheckpointAnalysis(t, p, b, q, fixture.Diagnostic{Code: "42883", Message: "operator does not exist: comparison_enum_domain " + canonical + " comparison_enum", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: comparisonCheckpointPosition(q, spelling+" 'a'")})
		}
		for _, value := range []string{"ROW(NULL,NULL)::comparison_struct", "ROW(1,2)::comparison_struct", "ROW(1,NULL)::comparison_struct", "NULL::comparison_struct"} {
			null := value == "ROW(NULL,NULL)::comparison_struct" || value == "NULL::comparison_struct"
			notnull := value == "ROW(1,2)::comparison_struct"
			for _, form := range []string{"IS NULL", "ISNULL"} {
				comparisonCheckpointBool(t, p, b, value+" "+form, null)
			}
			for _, form := range []string{"IS NOT NULL", "NOTNULL"} {
				comparisonCheckpointBool(t, p, b, value+" "+form, notnull)
			}
		}
	})
}
