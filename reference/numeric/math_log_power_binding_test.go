package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceMathLogPowerBinding(t *testing.T) {
	mathLogPowerScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE DOMAIN mlp_numeric AS numeric(20,4)", 0))
		run(fixture.Exec("CREATE DOMAIN mlp_float AS float8", 0))
		for _, tc := range []struct {
			expression, typ string
			want            any
		}{
			{"lgamma(1)", "FLOAT8", float64(0)}, {"lgamma(1::numeric)", "FLOAT8", float64(0)},
			{"ln(2.0)", "NUMERIC", "0.6931471805599453"},
			{"log(100::float8)", "FLOAT8", float64(2)}, {"log(100::numeric)", "NUMERIC", "2.0000000000000000"},
			{"log10(100::float8)", "FLOAT8", float64(2)}, {"log10(100::numeric)", "NUMERIC", "2.0000000000000000"},
			{"log(2,64)", "NUMERIC", "6.0000000000000000"},
			{"power(2,3)", "FLOAT8", float64(8)}, {"power(2::numeric,3::numeric)", "NUMERIC", "8.0000000000000000"},
			{"power(2::numeric,3::float8)", "FLOAT8", float64(8)},
			{"sqrt(4)", "FLOAT8", float64(2)}, {"sqrt(4::numeric)", "NUMERIC", "2.000000000000000"},
			{"sqrt(4::mlp_numeric)", "NUMERIC", "2.000000000000000"}, {"sqrt(4::mlp_float)", "FLOAT8", float64(2)},
			{"width_bucket(2,0,10,5)", "INT4", int64(2)}, {"width_bucket(2.0,0.0,10.0,5)", "INT4", int64(2)},
			{"width_bucket(2,ARRAY[0,2,4])", "INT4", int64(2)},
			{"pg_catalog.ln(1)", "FLOAT8", float64(0)}, {"\"ln\"(1)", "FLOAT8", float64(0)},
		} {
			mathLogPowerBoth(run, fixture.Select("SELECT "+tc.expression+" AS result", []string{"result"}, []string{tc.typ}, [][]any{{tc.want}}))
		}
		for _, expression := range []string{"ln(2::int2)", "ln(2::int4)", "ln(2::int8)", "ln(2::real)", "ln(2::float8)", "ln('2')"} {
			mathLogPowerFloat(t, ctx, conn, "SELECT "+expression+" AS result", 0.6931471805599453, 2)
		}
		for _, tc := range []struct{ query, message, position string }{
			{"SELECT log(2::float8,64::float8)", "function log(double precision, double precision) does not exist", "8"},
			{"/*é*/ SELECT log(2::float8,64::float8)", "function log(double precision, double precision) does not exist", "14"},
			{"SELECT ln()", "function ln() does not exist", "8"}, {"SELECT sqrt(1,2)", "function sqrt(integer, integer) does not exist", "8"},
			{"SELECT power(1)", "function power(integer) does not exist", "8"}, {"SELECT width_bucket(1,2,3)", "function width_bucket(integer, integer, integer) does not exist", "8"},
			{"SELECT lgamma('x'::text)", "function lgamma(text) does not exist", "8"}, {"SELECT \"LN\"(1)", "function LN(integer) does not exist", "8"},
			{"SELECT public.ln(1)", "function public.ln(integer) does not exist", "8"}, {"SELECT ln(VARIADIC ARRAY[1,2])", "function ln(integer[]) does not exist", "8"},
		} {
			mathLogPowerErrors(run, tc.query, fixture.Diagnostic{Code: "42883", Message: tc.message, Position: tc.position, Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}, true)
		}
		mathLogPowerErrors(run, "SELECT ln(DISTINCT 1)", fixture.Diagnostic{Code: "42809", Message: "DISTINCT specified, but ln is not an aggregate function", Position: "8"}, true)
	})
}

func mathLogPowerMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range columns {
		if length, known := column.Length(); length != 0 || known {
			t.Fatalf("%s length=%d/%v", column.Name(), length, known)
		}
		if nullable, known := column.Nullable(); nullable || known {
			t.Fatalf("%s nullability=%v/%v", column.Name(), nullable, known)
		}
		precision, scale, sized := column.DecimalSize()
		want := reflect.TypeFor[float64]()
		switch column.DatabaseTypeName() {
		case "NUMERIC":
			want = reflect.TypeFor[string]()
			if pg {
				want = reflect.TypeFor[any]()
			}
			if pg && (precision != 65535 || scale != 65531 || !sized) {
				t.Fatalf("NUMERIC decimal=%d,%d,%v", precision, scale, sized)
			}
		case "INT4":
			want = reflect.TypeFor[int64]()
			if pg {
				want = reflect.TypeFor[int32]()
			}
		}
		if !(pg && column.DatabaseTypeName() == "NUMERIC") && (precision != 0 || scale != 0 || sized) {
			t.Fatalf("%s inherited decimal=%d,%d,%v", column.Name(), precision, scale, sized)
		}
		if column.ScanType() != want {
			t.Fatalf("%s scan=%v want %v", column.Name(), column.ScanType(), want)
		}
	}
}

func TestReferenceMathLogPowerMetadata(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE DOMAIN mlp_meta_number AS numeric(12,4)", 0))
			run(fixture.Exec("CREATE TABLE mlp_meta (n mlp_meta_number, f float8, a numeric[])", 0))
			projection := "SELECT ln(n) AS n,log(n) AS l,log10(n) AS d,log(2::numeric,n) AS b,power(n,n) AS p,sqrt(n) AS s,lgamma(f) AS g,width_bucket(n,0::numeric,10::numeric,5) AS w,width_bucket(n,a) AS t FROM mlp_meta"
			names := []string{"n", "l", "d", "b", "p", "s", "g", "w", "t"}
			types := []string{"NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "NUMERIC", "FLOAT8", "INT4", "INT4"}
			check := func(query string, names, types []string, want [][]any) {
				s := fixture.Select(query, names, types, want)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { mathLogPowerMetadata(t, rows, backend.PG) }
				mathLogPowerBoth(run, s)
			}
			check(projection, names, types, nil)
			run(fixture.Exec("INSERT INTO mlp_meta VALUES (NULL,NULL,NULL)", 1))
			check(projection, names, types, [][]any{{nil, nil, nil, nil, nil, nil, nil, nil, nil}})
			run(fixture.Exec("INSERT INTO mlp_meta VALUES (4,1,ARRAY[0,4,10])", 1))
			check(projection+" ORDER BY n NULLS LAST", names, types, [][]any{{"1.3862943611198906", "0.6020599913279624", "0.6020599913279624", "2.0000000000000000", "256.00000000000000", "2.000000000000000", float64(0), int64(3), int64(2)}, {nil, nil, nil, nil, nil, nil, nil, nil, nil}})
			check("SELECT ln(NULL::float8) AS n,log(NULL::float8) AS l,log10(NULL::float8) AS d,power(NULL::float8,NULL::float8) AS p,sqrt(NULL::float8) AS s,width_bucket(NULL::float8,0::float8,1::float8,1) AS w", []string{"n", "l", "d", "p", "s", "w"}, []string{"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "INT4"}, [][]any{{nil, nil, nil, nil, nil, nil}})
		})
	}
}

type mathLogPowerHeldInput struct {
	args                   []any
	want                   any
	code, message, context string
}

func TestReferenceMathLogPowerHeldParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			portal := "unnamed portal parameter $1 = '...'"
			for _, tc := range []struct {
				query, typ string
				inputs     []mathLogPowerHeldInput
			}{
				{"SELECT ln($1) AS result", "FLOAT8", []mathLogPowerHeldInput{
					{[]any{"1"}, float64(0), "", "", ""}, {[]any{"2"}, float64(0.6931471805599453), "", "", ""}, {[]any{nil}, nil, "", "", ""},
					{[]any{"0"}, nil, "2201E", "cannot take logarithm of zero", ""}, {[]any{"1"}, float64(0), "", "", ""},
					{[]any{"oops"}, nil, "22P02", "invalid input syntax for type double precision: \"oops\"", portal}, {[]any{"1"}, float64(0), "", "", ""}}},
				{"SELECT log($1,$2) AS result", "NUMERIC", []mathLogPowerHeldInput{
					{[]any{"2", "64"}, "6.0000000000000000", "", "", ""}, {[]any{nil, "0"}, nil, "", "", ""}, {[]any{"1", "2"}, nil, "22012", "division by zero", ""},
					{[]any{"2", "64"}, "6.0000000000000000", "", "", ""}, {[]any{"oops", "2"}, nil, "22P02", "invalid input syntax for type numeric: \"oops\"", portal}, {[]any{"2", "64"}, "6.0000000000000000", "", "", ""}}},
				{"SELECT power($1,$2) AS result", "FLOAT8", []mathLogPowerHeldInput{{[]any{"2", "3"}, float64(8), "", "", ""}, {[]any{"0", "-1"}, nil, "2201F", "zero raised to a negative power is undefined", ""}, {[]any{"2", "3"}, float64(8), "", "", ""}}},
				{"SELECT width_bucket($1,$2,$3,$4) AS result", "INT4", []mathLogPowerHeldInput{{[]any{"2", "0", "10", "5"}, int64(2), "", "", ""}, {[]any{"2", "10", "0", "5"}, int64(5), "", "", ""}, {[]any{"2", "0", "10", "0"}, nil, "2201G", "count must be greater than zero", ""}, {[]any{"2", "0", "10", "5"}, int64(2), "", "", ""}}},
				{"SELECT width_bucket($1,$2::numeric[]) AS result", "INT4", []mathLogPowerHeldInput{{[]any{"10", "{0,10,10,20}"}, int64(3), "", "", ""}, {[]any{nil, "{NULL}"}, nil, "", "", ""}, {[]any{"10", "{0,NULL}"}, nil, "22004", "thresholds array must not contain NULLs", ""}, {[]any{"10", "{0,10,10,20}"}, int64(3), "", "", ""}}},
			} {
				t.Run(tc.query, func(t *testing.T) {
					stmt, err := conn.PrepareContext(p.Ctx, tc.query)
					if err != nil {
						t.Fatalf("quiet prepare: %v", err)
					}
					defer stmt.Close()
					held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: tc.query}
					for _, input := range tc.inputs {
						s := fixture.Select(tc.query, []string{"result"}, []string{tc.typ}, [][]any{{input.want}}, input.args...)
						if input.code != "" {
							s = fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: input.code, Message: input.message, Context: input.context}, input.args...)
							s.SelectRows, s.ErrorStage = true, "execute"
						}
						fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
					}
				})
			}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			mathLogPowerErrors(run, "SELECT width_bucket($1,0,10,$1)", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "double precision versus integer", Position: "29"}, true, "2")
			mathLogPowerErrors(run, "SELECT width_bucket($1,$1)", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "text versus text[]", Position: "24"}, true, "m")
			mathLogPowerBoth(run, fixture.Select("SELECT width_bucket($1,$2)", []string{"width_bucket"}, []string{"INT4"}, [][]any{{int64(2)}}, "1", "{0,1}"))
		})
	}
}

func TestReferenceMathLogPowerSQLPrepared(t *testing.T) {
	mathLogPowerScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		for _, query := range []string{
			"PREPARE mlp_ln(float8) AS SELECT ln($1) AS result",
			"PREPARE mlp_log(numeric,numeric) AS SELECT log($1,$2) AS result",
			"PREPARE mlp_bucket(numeric,numeric,numeric,int4) AS SELECT width_bucket($1,$2,$3,$4) AS result",
			"PREPARE mlp_threshold(numeric,numeric[]) AS SELECT width_bucket($1,$2) AS result",
		} {
			run(fixture.Exec(query, 0))
		}
		for _, tc := range []struct {
			query, typ    string
			want          any
			code, message string
		}{
			{"EXECUTE mlp_ln(1)", "FLOAT8", float64(0), "", ""}, {"EXECUTE mlp_ln(0)", "", nil, "2201E", "cannot take logarithm of zero"}, {"EXECUTE mlp_ln(1)", "FLOAT8", float64(0), "", ""},
			{"EXECUTE mlp_log(2,64)", "NUMERIC", "6.0000000000000000", "", ""}, {"EXECUTE mlp_log(1,2)", "", nil, "22012", "division by zero"}, {"EXECUTE mlp_log(2,64)", "NUMERIC", "6.0000000000000000", "", ""},
			{"EXECUTE mlp_bucket(2,0,10,5)", "INT4", int64(2), "", ""}, {"EXECUTE mlp_bucket(2,0,10,0)", "", nil, "2201G", "count must be greater than zero"}, {"EXECUTE mlp_bucket(2,0,10,5)", "INT4", int64(2), "", ""},
			{"EXECUTE mlp_threshold(10,ARRAY[0,10,10,20])", "INT4", int64(3), "", ""}, {"EXECUTE mlp_threshold(10,ARRAY[0,NULL])", "", nil, "22004", "thresholds array must not contain NULLs"}, {"EXECUTE mlp_threshold(10,ARRAY[0,10,10,20])", "INT4", int64(3), "", ""},
		} {
			if tc.code != "" {
				mathLogPowerErrors(run, tc.query, fixture.Diagnostic{Code: tc.code, Message: tc.message}, false)
				continue
			}
			mathLogPowerBoth(run, fixture.Select(tc.query, []string{"result"}, []string{tc.typ}, [][]any{{tc.want}}))
		}
	})
}
