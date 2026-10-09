package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceDatetimeFormattingSeparatorsFX(t *testing.T) {
	cases := []datetimeFormattingCase{
		datetimeFormattingParseCase("optional separators", "2000JUN", "YYYY///MON", "2000-06-01"),
		datetimeFormattingParseCase("separator mismatch", "2000/JUN", "YYYY MON", "2000-06-01"),
		datetimeFormattingParseCase("FX separator not equality", "2000/JUN", "FXYYYY MON", "2000-06-01"),
	}
	for _, c := range []struct{ input, template string }{
		{"2000//JUN", "YYYY/MON"}, {"2000    JUN", "FXYYYY MON"}, {"  2000   JUN", "YYYY MON"}, {" 2000/JUN", "FXYYYY MON"},
		{"2000abc06def19", "YYYY\"xyz\"MM\"abc\"DD"}, {"2000XYZ06", "YYYYabcMM"}, {"2000雪06", "YYYY雪MM"}, {"2000/06", "FXYYYY雪MM"},
		{"19971", "YYYYMMDD"}, {"19971)24", "YYYYMMDD"}, {"19971)24", "YYYYFMMMDD"}, {"2006119", "YYYYFMMMDD"},
		{"2000-01", "YYYY-MM-DD"}, {"2000-01", "FXYYYY-MM-DD"}, {"2000-01-02trailing", "YYYY-MM-DD"}, {"2000-01-02trailing", "FXYYYY-MM-DD"},
		{"", "YYYY-MM-DD"}, {"", "FXYYYY-MM-DD"}, {"", ""}, {"2000", ""}, {"20", "YYYYMMDD"}, {"2x06", "YYYY"},
		{"2000 -10", "YYYY TZH"}, {"2000 -10", "YYYY  TZH"}, {"2000 +10", "FXYYYY TZH"},
		{"2000-01-02xx", "YYYY-MM-DDTH"}, {"2000-01-02nd", "YYYY-MM-DDth"}, {"2000-01-02", "FMFMYYYY-MM-DD"},
		{"2000-01-02", "\"unterminated YYYY"}, {"2000-01-02", "FMTMYYYY-MM-DD"},
	} {
		cases = append(cases, datetimeFormattingCase{name: c.input + "/" + c.template, expression: "to_date(" + datetimeFormattingQuote(c.input) + "," + datetimeFormattingQuote(c.template) + ")::text"})
	}
	datetimeFormattingCases(t, cases)
}

func TestReferenceDatetimeFormattingFractionPrecision(t *testing.T) {
	var cases []datetimeFormattingCase
	for _, input := range []string{"12.3", "12.30", "12.300", "12.003"} {
		want := "0001-01-01 00:00:12.3+00 BC"
		if input == "12.003" {
			want = "0001-01-01 00:00:12.003+00 BC"
		}
		cases = append(cases, datetimeFormattingCase{name: "MS/" + input, expression: "to_timestamp(" + datetimeFormattingQuote(input) + ",'SS.MS')::text", want: want})
	}
	cases = append(cases, datetimeFormattingCase{name: "add MS US", expression: "to_timestamp('2006-10-19 15:12:02.020.001230','YYYY-MM-DD HH24:MI:SS.MS.US')::text", want: "2006-10-19 15:12:02.02123+00"})
	for width := 1; width <= 6; width++ {
		for _, digits := range []string{"1", "01", "021230", "0212309", "999999", "-1", "+1", "0000000"} {
			for _, adjacent := range []bool{false, true} {
				input := "2006-10-19 15:12:02." + digits
				template := fmt.Sprintf("YYYY-MM-DD HH24:MI:SS.FF%d", width)
				if adjacent {
					input += "03"
					template += "MI"
				}
				cases = append(cases, datetimeFormattingCase{name: fmt.Sprintf("FF%d/%s/adjacent=%t", width, digits, adjacent), expression: "to_timestamp(" + datetimeFormattingQuote(input) + "," + datetimeFormattingQuote(template) + ")::text"})
			}
		}
		cases = append(cases, datetimeFormattingCase{name: fmt.Sprintf("combined precision/FF%d", width), expression: fmt.Sprintf("to_timestamp('2006-10-19 15:12:02.020.001230.1','YYYY-MM-DD HH24:MI:SS.MS.US.FF%d')::text", width)})
	}
	for _, c := range []struct{ input, template string }{
		{"02.1000", "SS.MS"}, {"02.1000000", "SS.US"}, {"02.-1", "SS.MS"}, {"02.999.999999", "SS.MS.US"}, {"02.999999.9", "SS.US.FF1"},
	} {
		cases = append(cases, datetimeFormattingCase{name: c.input + "/" + c.template, expression: "to_timestamp(" + datetimeFormattingQuote(c.input) + "," + datetimeFormattingQuote(c.template) + ")::text"})
	}
	datetimeFormattingCases(t, cases)
}

func TestReferenceDatetimeFormattingParseDomains(t *testing.T) {
	var cases []datetimeFormattingCase
	for _, c := range []struct{ input, template string }{
		{"2006-02-29", "YYYY-MM-DD"}, {"2000-02-30", "YYYY-MM-DD"}, {"2006-13-01", "YYYY-MM-DD"}, {"2006-00-00", "YYYY-MM-DD"},
		{"2006-10-19 24:00:00", "YYYY-MM-DD HH24:MI:SS"}, {"2006-10-19 23:59:60", "YYYY-MM-DD HH24:MI:SS"},
		{"2006-10-19 0 PM", "YYYY-MM-DD HH PM"}, {"2006-10-19 13 AM", "YYYY-MM-DD HH12 AM"}, {"2006-10-19 86400", "YYYY-MM-DD SSSSS"},
		{"2006-10-19 +16:00", "YYYY-MM-DD TZH:TZM"}, {"2006-10-19 +15:59", "YYYY-MM-DD OF"},
		{"4713-01-01 BC", "YYYY-MM-DD BC"}, {"4714-01-01 BC", "YYYY-MM-DD BC"},
		{"5874897-12-31", "YYYY-MM-DD"}, {"5874898-01-01", "YYYY-MM-DD"}, {"294276-12-31 23:59:59.999999", "YYYY-MM-DD HH24:MI:SS.US"},
		{"294277-01-01", "YYYY-MM-DD"}, {"infinity", "YYYY"}, {"-infinity", "YYYY"}, {"epoch", "YYYY"},
	} {
		for _, f := range []string{"to_date", "to_timestamp"} {
			cases = append(cases, datetimeFormattingCase{name: f + "/" + c.input, expression: f + "(" + datetimeFormattingQuote(c.input) + "," + datetimeFormattingQuote(c.template) + ")::text"})
		}
	}
	cases = append(cases, datetimeFormattingCase{name: "12hour diagnostic", expression: "to_timestamp('13','HH12')", diagnostic: datetimeFormattingDiagnostic("22007", `hour "13" is invalid for the 12-hour clock`, "", "Use the 24-hour clock, or give an hour between 1 and 12.")},
		datetimeFormattingCase{name: "date range diagnostic", expression: "to_date('2006-02-29','YYYY-MM-DD')", diagnostic: datetimeFormattingDiagnostic("22008", `date/time field value out of range: "2006-02-29"`, "", "")})
	datetimeFormattingCases(t, cases)
}
