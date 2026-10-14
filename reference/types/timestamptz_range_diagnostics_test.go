package reference_test

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

type timestamptzRangeFailure struct{ input, code, message, detail, hint string }

// Independently expected from PG18 range and subtype contracts: the REL_18_0 grammar/subtype expectations below. Omitted
// logical diagnostic fields are expected EMPTY, not unknown/ignored.
func timestamptzRangeFailures() []timestamptzRangeFailure {
	var cases []timestamptzRangeFailure
	for _, tc := range []struct{ input, detail string }{
		{"", "Missing left parenthesis or bracket."},
		{"epoch,epoch)", "Missing left parenthesis or bracket."},
		{"emptyx", `Junk after "empty" key word.`},
		{"[epoch)", "Missing comma after lower bound."},
		{"[epoch,epoch,epoch]", "Too many commas."},
		{"[epoch,epoch", "Unexpected end of input."},
		{`["epoch,epoch)`, "Unexpected end of input."},
		{`[epoch,epoch\`, "Unexpected end of input."},
		{"[epoch,epoch)junk", "Junk after right parenthesis or bracket."},
		{"[bad,epoch)junk", "Junk after right parenthesis or bracket."},
	} {
		cases = append(cases, timestamptzRangeFailure{input: tc.input, code: "22P02", message: `malformed range literal: "` + tc.input + `"`, detail: tc.detail})
	}
	for _, tc := range []struct{ input, decoded string }{
		{"[bad,epoch)", "bad"}, {`["",epoch)`, ""}, {"[ ,epoch)", " "}, {"[NULL,epoch)", "NULL"},
		{`["2000""01",epoch)`, `2000"01`}, {`["2000\"01",epoch)`, `2000"01`},
		{"[bad,294277-01-01)", "bad"}, {"[epoch,bad)", "bad"}, {"[, )", " "},
	} {
		cases = append(cases, timestamptzRangeFailure{input: tc.input, code: "22007", message: `invalid input syntax for type timestamp with time zone: "` + tc.decoded + `"`})
	}
	for _, tc := range []struct{ endpoint, hint string }{
		{"1900-02-29", ""}, {"0000-01-01", ""}, {"2000-01-01 25:00:00+00", ""},
		{"2001-13-01", `Perhaps you need a different "DateStyle" setting.`},
		{"2001-01-32", `Perhaps you need a different "DateStyle" setting.`},
	} {
		cases = append(cases, timestamptzRangeFailure{input: "[" + tc.endpoint + ",infinity)", code: "22008", message: `date/time field value out of range: "` + tc.endpoint + `"`, hint: tc.hint})
	}
	for _, endpoint := range []string{"294277-01-01", "4714-11-23 BC"} {
		cases = append(cases, timestamptzRangeFailure{input: "[" + endpoint + ",infinity)", code: "22008", message: `timestamp out of range: "` + endpoint + `"`})
	}
	endpoint := "2000-01-01 12:00:00+16"
	cases = append(cases, timestamptzRangeFailure{input: "[" + endpoint + ",infinity)", code: "22009", message: `time zone displacement out of range: "` + endpoint + `"`})
	endpoint = "2000-01-01 12:00:00 no/such_zone"
	cases = append(cases, timestamptzRangeFailure{input: "[" + endpoint + ",infinity)", code: "22023", message: `time zone "no/such_zone" not recognized`})
	for _, endpoint := range []string{"4714-11-24 00:00:00+01 BC", "294276-12-31 23:59:59.999999-01"} {
		cases = append(cases, timestamptzRangeFailure{input: "[" + endpoint + ",infinity)", code: "22008", message: `timestamp out of range: "` + endpoint + `"`})
	}
	for _, input := range []string{"[2000-01-02,2000-01-01)", "[infinity,-infinity]", "[2010-01-01 01:00:00-08,2010-01-01 02:00:00-05)"} {
		cases = append(cases, timestamptzRangeFailure{input: input, code: "22000", message: "range lower bound must be less than or equal to range upper bound"})
	}
	return cases
}

func TestReferenceTimestamptzRangeDiagnostics(t *testing.T) {
	for i, tc := range timestamptzRangeFailures() {
		t.Run(fmt.Sprintf("%02d", i), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				conn, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer conn.Close()
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE r(v tstzrange)", 0))
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Detail, want.Hint, want.Position = tc.detail, tc.hint, "29"
				q := "/*é*/ INSERT INTO r VALUES (" + rangeLiteral(tc.input) + ")"
				// Common helper uses nonfatal full-record/stage mismatches so all routes
				// remain observable, and checks that failed calls expose no Rows/Stmt/Result.
				fixture.CompositeReferenceError(t, p, conn, q, "prepare", want)
				want.Position = ""
				want.Context = `unnamed portal parameter $1 = '...'`
				if tc.input == "" {
					want.Context = `unnamed portal parameter $1 = ''`
				}
				fixture.CompositeReferenceError(t, p, conn, "INSERT INTO r VALUES ($1)", "execute", want, tc.input)
				run(fixture.Exec("INSERT INTO r VALUES ('[epoch,epoch]')", 1))
				atomic := "INSERT INTO r VALUES ('[2000-01-01,2000-01-02)'),(" + rangeLiteral(tc.input) + ")"
				want.Context = ""
				want.Position = strconv.Itoa(utf8.RuneCountInString(atomic[:strings.LastIndex(atomic, "('")+1]) + 1)
				fixture.CompositeReferenceError(t, p, conn, atomic, "prepare", want)
				want.Position, want.Context = "", `unnamed portal parameter $2 = '...'`
				if tc.input == "" {
					want.Context = `unnamed portal parameter $2 = ''`
				}
				fixture.CompositeReferenceError(t, p, conn, "INSERT INTO r VALUES ($1),($2)", "execute", want, "[2000-01-01,2000-01-02)", tc.input)
				run(rangeSelect(b, "SELECT v FROM r", []string{"v"}, []string{"TSTZRANGE"}, [][]any{{`["1970-01-01 00:00:00+00","1970-01-01 00:00:00+00"]`}}, []bool{true}))
			}
		})
	}
}

func TestReferenceTimestamptzRangeModifiers(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		for _, typ := range []string{"tstzrange", "pg_catalog.tstzrange", `"tstzrange"`} {
			for _, modifier := range []string{"2", "-1", "2147483648", "2,3", "'x'"} {
				want := fixture.ControlsReferenceDiagnostic("42601", `type modifier is not allowed for type "tstzrange"`)
				if typ == "pg_catalog.tstzrange" {
					want.Message = `type modifier is not allowed for type "pg_catalog.tstzrange"`
				}
				want.Position = "18"
				q := "CREATE TABLE m(v " + typ + "(" + modifier + "))"
				if modifier == "'x'" && !b.PG {
					want.Message = `syntax error at or near "'x'"`
					want.Position = strconv.Itoa(utf8.RuneCountInString("CREATE TABLE m(v "+typ+"(") + 1)
					fixture.CompositeReferenceError(t, p, conn, q, "prepare", want)
				} else {
					fixture.CompositeReferenceError(t, p, conn, q, "execute", want)
				}
			}
		}
		fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("CREATE TABLE m(v tstzrange)", 0))
	}
}
