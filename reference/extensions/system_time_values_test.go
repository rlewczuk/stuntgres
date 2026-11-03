package reference_test

import (
	"database/sql"
	"math"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSystemTimeValues(t *testing.T) {
	stCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stValues(t, p, b, c)
		for _, source := range []string{"sr", "sr_empty"} {
			for _, argument := range []string{"0", "'-0'::float8", "0::integer", "0::real"} {
				srBoth(t, p, b, c, srMetadata(fixture.Select("SELECT id,payload FROM "+source+" TABLESAMPLE system_time("+argument+")", srNames, srTypes, nil), b.PG))
			}
		}
		for _, argument := range []string{"0.001", "0.25", "1.5", "1000000", "'1.7976931348623157e308'::float8", "1::integer", "1.5::real"} {
			stSampleBoth(t, p, c, "SELECT id,payload FROM sr TABLESAMPLE system_time("+argument+")", srRows())
			stSampleBoth(t, p, c, "SELECT id,payload FROM sr_empty TABLESAMPLE system_time("+argument+")", nil)
		}
		for _, cast := range []string{"", "::float8"} {
			q := "SELECT id,payload FROM sr TABLESAMPLE system_time($1"+cast+") ORDER BY id"
			h := srHeld(t, p, c, q)
			for _, value := range []any{float64(0), math.Copysign(0, -1), 0.001, 0.25, 1.5, math.MaxFloat64, math.Inf(1)} {
				stSample(t, p.Ctx, h, q, srRows(), value)
				if value.(float64) == 0 {
					srRun(t, p, b, h, srMetadata(fixture.Select(q, srNames, srTypes, nil, value), b.PG))
				}
			}
			for _, item := range []struct { value any; message string }{
				{nil, "TABLESAMPLE parameter cannot be null"},
				{-0.25, "sample collection time must not be negative"},
				{math.Inf(-1), "sample collection time must not be negative"},
				{math.NaN(), "sample collection time must not be negative"},
			} {
				srQueryDiagnostic(t, p, h, q, fixture.ControlsReferenceDiagnostic("2202H", item.message), item.value)
				srRun(t, p, b, h, srMetadata(fixture.Select(q, srNames, srTypes, srRows(), math.Inf(1)), b.PG))
			}
		}
	})
}

func stValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, argument := range []string{"0", "'-0'::float8", "'Infinity'"} {
		var want [][]any
		if argument == "'Infinity'" { want = srRows() }
		srBoth(t, p, b, c, srMetadata(fixture.Select("SELECT id,payload FROM sr TABLESAMPLE system_time("+argument+") ORDER BY id", srNames, srTypes, want), b.PG))
	}
	q := "SELECT id,payload FROM sr TABLESAMPLE system_time($1) ORDER BY id"
	for _, value := range []float64{0, math.Inf(1)} {
		var want [][]any
		if math.IsInf(value, 1) { want = srRows() }
		srBoth(t, p, b, c, srMetadata(fixture.Select(q, srNames, srTypes, want, value), b.PG))
	}
	stSampleBoth(t, p, c, "SELECT id,payload FROM sr TABLESAMPLE system_time($1)", srRows(), 0.25)
	srBoth(t, p, b, c, srMetadata(fixture.Select("SELECT id,payload FROM sr_empty TABLESAMPLE system_time('Infinity')", srNames, srTypes, nil), b.PG))
}

func TestReferenceSystemTimeMetadata(t *testing.T) {
	stCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stValues(t, p, b, c)
		srRun(t, p, b, c, fixture.Exec("INSERT INTO sr VALUES(11,NULL)", 1))
		want := append(srRows(), []any{int64(11), nil})
		q := "SELECT id,payload FROM sr TABLESAMPLE system_time($1) ORDER BY id"
		h := srHeld(t, p, c, q)
		for _, argument := range []float64{0, math.Inf(1), 0, math.Inf(1)} {
			var rows [][]any
			if argument != 0 { rows = want }
			srRun(t, p, b, h, srMetadata(fixture.Select(q, srNames, srTypes, rows, argument), b.PG))
		}
		srRun(t, p, b, c, fixture.Exec("CREATE VIEW st_metadata AS SELECT id,payload FROM sr TABLESAMPLE system_time('Infinity')", 0))
		s := fixture.Select("SELECT id,payload FROM st_metadata ORDER BY id", srNames, srTypes, want)
		s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			columns, err := rows.ColumnTypes()
			if err != nil { t.Fatal(err) }
			fixture.TableDDLColumnTypes(t, b.PG, columns, srNames, srTypes, []bool{true,true})
		}
		srBoth(t, p, b, c, s)
	})
}
