package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func mathLogPowerStoredSetup(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE mlp_stored (id integer, x numeric, fallback float8 DEFAULT power(2::float8,3::float8), root numeric GENERATED ALWAYS AS (sqrt(x)) STORED, bucket integer GENERATED ALWAYS AS (width_bucket(x,0::numeric,10::numeric,5)) STORED, CHECK (x >= 0))", 0))
	run(fixture.Exec("INSERT INTO mlp_stored(id,x) VALUES (1,1),(2,4),(3,9),(4,NULL)", 4))
	run(fixture.Exec("CREATE VIEW mlp_stored_view AS SELECT id,root,bucket,fallback,width_bucket(x,ARRAY[0,4,9]::numeric[]) AS threshold,ln(x) AS logarithm,log(2::numeric,x) AS binary_log FROM mlp_stored", 0))
}

func TestReferenceMathLogPowerConsumers(t *testing.T) {
	mathLogPowerScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		mathLogPowerStoredSetup(run)
		mathLogPowerBoth(run, fixture.Select("SELECT * FROM mlp_stored_view ORDER BY id",
			[]string{"id", "root", "bucket", "fallback", "threshold", "logarithm", "binary_log"},
			[]string{"INT4", "NUMERIC", "INT4", "FLOAT8", "INT4", "NUMERIC", "NUMERIC"}, [][]any{
				{int64(1), "1.000000000000000", int64(1), float64(8), int64(1), "0.0000000000000000", "0.0000000000000000"},
				{int64(2), "2.000000000000000", int64(3), float64(8), int64(2), "1.3862943611198906", "2.0000000000000000"},
				{int64(3), "3.000000000000000", int64(5), float64(8), int64(3), "2.1972245773362194", "3.1699250014423124"},
				{int64(4), nil, nil, float64(8), nil, nil, nil},
			}))
		mathLogPowerBoth(run, fixture.Select("SELECT bucket,count(*) AS n FROM mlp_stored GROUP BY bucket ORDER BY bucket NULLS LAST",
			[]string{"bucket", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(3), int64(1)}, {int64(5), int64(1)}, {nil, int64(1)}}))
		mathLogPowerBoth(run, fixture.Select("SELECT id,sqrt(x)::float8 AS result FROM mlp_stored WHERE width_bucket(x,0::numeric,10::numeric,5) > 1 ORDER BY sqrt(x)", []string{"id", "result"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(2), float64(2)}, {int64(3), float64(3)}}))
		mathLogPowerBoth(run, fixture.Select("SELECT id,CASE WHEN x IS NULL THEN 0::numeric ELSE sqrt(x) END AS result FROM mlp_stored ORDER BY id",
			[]string{"id", "result"}, []string{"INT4", "NUMERIC"}, [][]any{{int64(1), "1.000000000000000"}, {int64(2), "2.000000000000000"}, {int64(3), "3.000000000000000"}, {int64(4), "0"}}))
		run(fixture.Exec("CREATE TABLE mlp_source (id integer,x numeric)", 0))
		run(fixture.Exec("INSERT INTO mlp_source VALUES (1,4),(2,-1)", 2))
		run(fixture.Exec("CREATE TABLE mlp_sink (x numeric)", 0))
		run(fixture.Exec("INSERT INTO mlp_sink VALUES (99)", 1))
		for _, tc := range []struct{ expression, code, message string }{
			{"sqrt(x)", "2201F", "cannot take square root of a negative number"},
			{"ln(x)", "2201E", "cannot take logarithm of a negative number"},
			{"power(x,0.5::numeric)", "2201F", "a negative number raised to a non-integer power yields a complex result"},
			{"width_bucket(x,0::numeric,10::numeric,2-id)", "2201G", "count must be greater than zero"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("INSERT INTO mlp_sink SELECT "+tc.expression+" FROM mlp_source ORDER BY id", fixture.Diagnostic{Code: tc.code, Message: tc.message})
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				run(fixture.Select("SELECT x FROM mlp_sink", []string{"x"}, []string{"NUMERIC"}, [][]any{{"99"}}))
			}
		}
		// A runtime NULL must not suppress a demanded failing sibling; unlike a
		// literal NULL, this is not an immutable constant-folding witness.
		mathLogPowerErrors(run, "SELECT power(NULLIF(x,x),ln(0::numeric)) FROM mlp_source ORDER BY id",
			fixture.Diagnostic{Code: "2201E", Message: "cannot take logarithm of zero"}, false)
	})
}

func TestReferenceMathLogPowerFeatureSmoke(t *testing.T) {
	mathLogPowerScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		mathLogPowerBoth(run, fixture.Select("SELECT lgamma(1::float8) AS g,ln(1::float8) AS n,log(100::float8) AS l,log10(1000::float8) AS d,power(2::float8,3::float8) AS p,sqrt(4::float8) AS s,width_bucket(2,0,10,5) AS a,width_bucket(8,10,0,5) AS r,width_bucket(10,ARRAY[0,10,10,20]) AS t", []string{"g", "n", "l", "d", "p", "s", "a", "r", "t"}, []string{"FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "FLOAT8", "INT4", "INT4", "INT4"}, [][]any{{float64(0), float64(0), float64(2), float64(3), float64(8), float64(2), int64(2), int64(2), int64(3)}}))
		mathLogPowerBoth(run, fixture.Select("SELECT log(2::numeric,64::numeric) AS exact", []string{"exact"}, []string{"NUMERIC"}, [][]any{{"6.0000000000000000"}}))
		stmt, err := conn.PrepareContext(ctx, "SELECT sqrt($1::float8) AS result")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for _, tc := range []struct {
			input string
			want  float64
		}{{"4", 2}, {"9", 3}} {
			var got float64
			if err := stmt.QueryRowContext(ctx, tc.input).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("held sqrt(%s)=%g want %g", tc.input, got, tc.want)
			}
		}
		var ignored float64
		fixture.AssertDiagnostic(t, stmt.QueryRowContext(ctx, "-1").Scan(&ignored), &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2201F", Message: "cannot take square root of a negative number"})
		if err := stmt.QueryRowContext(ctx, "4").Scan(&ignored); err != nil || ignored != 2 {
			t.Fatalf("held recovery=%g error=%v", ignored, err)
		}
		mathLogPowerStoredSetup(run)
		mathLogPowerBoth(run, fixture.Select("SELECT id,root,bucket,threshold FROM mlp_stored_view ORDER BY id",
			[]string{"id", "root", "bucket", "threshold"}, []string{"INT4", "NUMERIC", "INT4", "INT4"},
			[][]any{{int64(1), "1.000000000000000", int64(1), int64(1)}, {int64(2), "2.000000000000000", int64(3), int64(2)}, {int64(3), "3.000000000000000", int64(5), int64(3)}, {int64(4), nil, nil, nil}}))
	})
	mathLogPowerControlScenario(t)
}
