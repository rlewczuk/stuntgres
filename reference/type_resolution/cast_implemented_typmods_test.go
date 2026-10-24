package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Same-ID destination sizing owns the released typmod self edges. Syntax owns
// spelling equivalence; each modifier/provider behavior executes only once here.
func TestReferenceCastImplementedTypmods(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
			for _, tc := range []struct {
				source, target, fact, input, output, second, secondOutput string
				length, precision, scale                                  int64
			}{
				{"varchar(6)", "varchar(3)", "varchar", "abcdef", "abc", "é界abcd", "é界a", 3, 0, 0},
				{"bpchar(5)", "text", "text", "xy", "xy", "z", "z", 0, 0, 0},
				{"bpchar(5)", "bpchar(3)", "bpchar", "abcde", "abc", "x", "x  ", 3, 0, 0},
				{"text", "bpchar(5)", "bpchar", "xy", "xy   ", "abcdef", "abcde", 5, 0, 0},
				{"bit varying", "bit(3)", "bit", "10", "100", "1011", "101", 0, 0, 0},
				{"bit(4)", "bit(2)", "bit", "1011", "10", "1", "10", 0, 0, 0},
				{"bit varying", "varbit(2)", "varbit", "1011", "10", "1", "1", 0, 0, 0},
				{"numeric", "numeric(3,1)", "numeric", "3.25", "3.3", "-3.25", "-3.3", 0, 3, 1},
				{"time", "time(2)", "time", "12:34:56.789", "12:34:56.79", "12:34:56.781", "12:34:56.78", 0, 0, 0},
				{"timetz", "timetz(2)", "timetz", "12:34:56.789+00", "12:34:56.79+00", "12:34:56.781+00", "12:34:56.78+00", 0, 0, 0},
				{"timestamp", "timestamp(3)", "timestamp", "2000-01-01 23:59:59.9996", "2000-01-02 00:00:00", "2000-01-01 12:34:56.1234", "2000-01-01 12:34:56.123", 0, 0, 0},
				{"timestamptz", "timestamptz(3)", "timestamptz", "2000-01-01 23:59:59.9996+00", "2000-01-02 00:00:00+00", "2000-01-01 12:34:56.1234+00", "2000-01-01 12:34:56.123+00", 0, 0, 0},
				{"interval", "interval(2)", "interval", "1.234 seconds", "00:00:01.23", "1.236 seconds", "00:00:01.24", 0, 0, 0},
			} {
				q := "SELECT CAST(CAST($1 AS " + tc.source + ") AS " + tc.target + ") AS v WHERE $2"
				fact := castInventoryType(tc.fact)
				makeStep := func(input any, output any, selected bool) fixture.Step {
					s := castResultStep(t, q, fact, output, b.PG, input, selected)
					if !selected {
						s.Rows = nil
					}
					if tc.length != 0 || tc.precision != 0 {
						s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
							cols, e := r.ColumnTypes()
							if e != nil {
								t.Fatal(e)
							}
							if len(cols) != 1 {
								t.Fatal(cols)
							}
							if tc.length != 0 {
								n, ok := cols[0].Length()
								if !ok || n != tc.length {
									t.Fatal("length", n, ok, tc.length)
								}
							}
							if tc.precision != 0 {
								p, s, ok := cols[0].DecimalSize()
								if !ok || p != tc.precision || s != tc.scale {
									t.Fatal("decimal", p, s, ok)
								}
							}
						}
					}
					return s
				}
				castWitnessRoutes(t, p, b, makeStep(tc.input, castExpectedValue(t, fact, tc.output, b.PG), true), makeStep(tc.second, castExpectedValue(t, fact, tc.secondOutput, b.PG), true), makeStep(nil, nil, true), makeStep(tc.input, nil, false))
			}
			for i, tc := range []struct{ target, source, input, code, message string }{{"varchar(3)", "text", "abcdef", "22001", "value too long for type character varying(3)"}, {"bpchar(3)", "text", "abcdef", "22001", "value too long for type character(3)"}, {"bit(3)", "bit varying", "10", "22026", "bit string length 2 does not match type bit(3)"}, {"varbit(2)", "bit varying", "101", "22001", "bit string too long for type bit varying(2)"}} {
				table := fmt.Sprintf("cast_size_%d", i)
				run(fixture.Exec("CREATE TABLE "+table+"(id integer,v "+tc.target+")", 0))
				run(fixture.Exec("INSERT INTO "+table+" VALUES(0,NULL)", 1))
				q := "INSERT INTO " + table + " SELECT 1,CAST($1 AS " + tc.source + ")"
				bad := fixture.ImplicitError(q, fixture.Diagnostic{Code: tc.code, Message: tc.message}, tc.input)
				bad.ErrorStage = "execute"
				castWitnessRoutes(t, p, b, bad)
				run(fixture.Select("SELECT id FROM "+table, []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}}))
			}
			run(fixture.Exec("CREATE TABLE cast_explicit_size(v varchar(3))", 0))
			run(fixture.Exec("INSERT INTO cast_explicit_size VALUES(CAST(CAST($1 AS text) AS varchar(3)))", 1, "abcdef"))
			run(fixture.Exec("INSERT INTO cast_explicit_size SELECT CAST($1 AS text)", 1, "xy   "))
			run(fixture.Select("SELECT v FROM cast_explicit_size ORDER BY v", []string{"v"}, []string{"VARCHAR"}, [][]any{{"abc"}, {"xy "}}))
			q := "SELECT CAST(CAST($1 AS numeric) AS numeric(3,1)) AS v"
			bad := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22003", Message: "numeric field overflow", Detail: "A field with precision 3, scale 1 must round to an absolute value less than 10^2."}, "99.95")
			bad.SelectRows = true
			bad.ErrorStage = "execute"
			castWitnessRoutes(t, p, b, bad, castResultStep(t, q, castInventoryType("numeric"), "99.9", b.PG, "99.94"))
		})
	}
}
