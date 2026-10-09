package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

func TestReferenceDatetimeFormattingIntervalTokens(t *testing.T) {
	cases := []datetimeFormattingCase{
		{name: "clock", expression: "to_char(INTERVAL '15 hours 2 minutes 12 seconds','HH24:MI:SS')", want: "15:02:12"},
		{name: "unbounded hour", expression: "to_char(INTERVAL '36 hours','HH24 HH12 HH')", want: "36 12 12"},
		{name: "zero hour", expression: "to_char(INTERVAL '0','HH24 HH12 HH')", want: "00 12 12"},
		{name: "separate components", expression: "to_char(INTERVAL '25 months 3 days 36 hours 2 minutes 12.021230 seconds','YYYY MM DD HH24:MI:SS.US')", want: "0002 01 03 36:02:12.021230"},
	}
	for _, source := range []string{"0", "25 months 3 days 36 hours 2 minutes 12.021230 seconds", "-25 months 3 days -36 hours -2 minutes -12.021230 seconds", "-3 days 1000000 hours"} {
		for _, token := range datetimeFormattingTokens {
			cases = append(cases, datetimeFormattingCase{name: source + "/" + token.token, expression: "to_char(INTERVAL " + datetimeFormattingQuote(source) + "," + datetimeFormattingQuote(token.token) + ")"})
		}
	}
	for month := -13; month <= 13; month++ {
		for _, template := range []string{"RM", "rm", "FMRM", "YYYY MM DDD WW W Q J IYYY IYY IY I IW IDDD CC", "MMTH MMth"} {
			cases = append(cases, datetimeFormattingCase{name: fmt.Sprintf("month=%d/%s", month, template), expression: fmt.Sprintf("to_char(INTERVAL '%d months',%s)", month, datetimeFormattingQuote(template))})
		}
	}
	for _, token := range []string{"MONTH", "Mon", "DAY", "Dy", "AD", "TZ", "OF"} {
		cases = append(cases, datetimeFormattingCase{name: "rejection/" + token, expression: "to_char(INTERVAL '1 day'," + datetimeFormattingQuote(token) + ")", diagnostic: datetimeFormattingDiagnostic("22007", "invalid format specification for an interval value", "", "Intervals are not tied to specific calendar dates.")})
	}
	datetimeFormattingCases(t, cases)
}

func TestReferenceDatetimeFormattingNullInfinity(t *testing.T) {
	var cases []datetimeFormattingCase
	for _, source := range []string{"NULL::timestamp", "NULL::timestamptz", "NULL::interval"} {
		cases = append(cases, datetimeFormattingCase{name: source, expression: "to_char(" + source + ",'MONTH')", want: nil})
	}
	for _, source := range []string{"TIMESTAMP '2006-10-19'", "TIMESTAMPTZ '2006-10-19+00'", "INTERVAL '1 day'"} {
		cases = append(cases, datetimeFormattingCase{name: source + "/NULL", expression: "to_char(" + source + ",NULL::text)", want: nil}, datetimeFormattingCase{name: source + "/empty", expression: "to_char(" + source + ",'')", want: nil})
	}
	for _, typ := range []string{"timestamp", "timestamptz", "interval"} {
		for _, value := range []string{"infinity", "-infinity"} {
			cases = append(cases, datetimeFormattingCase{name: typ + "/" + value, expression: "to_char(" + typ + " " + datetimeFormattingQuote(value) + ",'YYYY-MM-DD TZ')", want: nil})
		}
	}
	for _, f := range []string{"to_date", "to_timestamp"} {
		typ := "DATE"
		if f == "to_timestamp" {
			typ = "TIMESTAMPTZ"
		}
		cases = append(cases, datetimeFormattingCase{name: f + "/source null", expression: f + "(NULL::text,'YYYY')", sqlType: typ, want: nil}, datetimeFormattingCase{name: f + "/template null", expression: f + "('not a date',NULL::text)", sqlType: typ, want: nil})
		cases = append(cases, datetimeFormattingCase{name: f + "/unreachable", expression: "CASE WHEN false THEN " + f + "('bad','YYYY')::text ELSE 'safe' END", want: "safe"})
		for _, expr := range []string{f + "(NULL::text,(1/0)::text)", f + "((1/0)::text,NULL::text)", "CASE WHEN true THEN NULL::text ELSE " + f + "('bad','YYYY')::text END"} {
			cases = append(cases, datetimeFormattingCase{name: expr, expression: expr})
		}
	}
	datetimeFormattingCases(t, cases)
}

func TestReferenceDatetimeFormattingRowNullDemand(t *testing.T) {
	datetimeFormattingScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, conn *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
		run(referenceExec("CREATE TABLE dt_null(id integer,input text,template text)", 0))
		run(referenceExec("INSERT INTO dt_null VALUES(1,NULL,'YYYY IYYY'),(2,'bad',NULL),(3,'2000','YYYY')", 3))
		for _, prepared := range []bool{false, true} {
			s := referenceSelect("SELECT id,to_date(input,template)::text AS d,to_timestamp(input,template)::text AS z FROM dt_null ORDER BY id", []string{"id", "d", "z"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), nil, nil}, {int64(2), nil, nil}, {int64(3), "2000-01-01", "2000-01-01 00:00:00+00"}})
			if prepared {
				s = referencePrepared(s)
			}
			run(s)
			datetimeFormattingAssertContract(t, p, b, conn, "SELECT id,to_date(input,CASE WHEN id=1 THEN (1/0)::text ELSE template END) FROM dt_null ORDER BY id", prepared)
			datetimeFormattingAssertContract(t, p, b, conn, "SELECT id,to_date(CASE WHEN id=2 THEN (1/0)::text ELSE input END,template) FROM dt_null ORDER BY id", prepared)
		}
	})
}
