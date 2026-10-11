package reference_test

import (
	"fmt"
	"strings"
	"testing"
)

// One lexical inventory drives timestamp, instant and interval witnesses. Named
// spellings are exact: arbitrary case folding would accept non-PostgreSQL tokens.
var datetimeFormattingTokens = []struct{ token, want string }{
	{"HH", "03"}, {"HH12", "03"}, {"HH24", "15"}, {"MI", "12"}, {"SS", "02"}, {"SSSS", "54722"}, {"SSSSS", "54722"},
	{"AM", "PM"}, {"PM", "PM"}, {"am", "pm"}, {"pm", "pm"}, {"A.M.", "P.M."}, {"P.M.", "P.M."}, {"a.m.", "p.m."}, {"p.m.", "p.m."},
	{"MS", "021"}, {"US", "021230"}, {"FF1", "0"}, {"FF2", "02"}, {"FF3", "021"}, {"FF4", "0212"}, {"FF5", "02123"}, {"FF6", "021230"},
	{"Y,YYY", "2,006"}, {"YYYY", "2006"}, {"YYY", "006"}, {"YY", "06"}, {"Y", "6"}, {"IYYY", "2006"}, {"IYY", "006"}, {"IY", "06"}, {"I", "6"},
	{"AD", "AD"}, {"BC", "AD"}, {"ad", "ad"}, {"bc", "ad"}, {"A.D.", "A.D."}, {"B.C.", "A.D."}, {"a.d.", "a.d."}, {"b.c.", "a.d."}, {"CC", "21"}, {"Q", "4"}, {"J", "2454028"},
	{"MONTH", "OCTOBER  "}, {"Month", "October  "}, {"month", "october  "}, {"MON", "OCT"}, {"Mon", "Oct"}, {"mon", "oct"}, {"MM", "10"}, {"RM", "X   "}, {"rm", "x   "},
	{"DAY", "THURSDAY "}, {"Day", "Thursday "}, {"day", "thursday "}, {"DY", "THU"}, {"Dy", "Thu"}, {"dy", "thu"}, {"DDD", "292"}, {"IDDD", "291"}, {"DD", "19"}, {"D", "5"}, {"ID", "4"},
	{"W", "3"}, {"WW", "42"}, {"IW", "42"}, {"TZ", "UTC"}, {"tz", "utc"}, {"TZH", "+00"}, {"TZM", "00"}, {"OF", "+00"},
	{"hh", "03"}, {"hh12", "03"}, {"hh24", "15"}, {"mi", "12"}, {"ss", "02"}, {"ssss", "54722"}, {"sssss", "54722"},
	{"ms", "021"}, {"us", "021230"}, {"ff1", "0"}, {"ff2", "02"}, {"ff3", "021"}, {"ff4", "0212"}, {"ff5", "02123"}, {"ff6", "021230"},
	{"y,yyy", "2,006"}, {"yyyy", "2006"}, {"yyy", "006"}, {"yy", "06"}, {"y", "6"}, {"iyyy", "2006"}, {"iyy", "006"}, {"iy", "06"}, {"i", "6"},
	{"cc", "21"}, {"q", "4"}, {"j", "2454028"}, {"mm", "10"}, {"ddd", "292"}, {"iddd", "291"}, {"dd", "19"}, {"d", "5"}, {"id", "4"},
	{"w", "3"}, {"ww", "42"}, {"iw", "42"}, {"tzh", "+00"}, {"tzm", "00"}, {"of", "+00"},
}

func TestReferenceDatetimeFormattingOutputTokens(t *testing.T) {
	var cases []datetimeFormattingCase
	for _, source := range []string{"TIMESTAMP '2006-10-19 15:12:02.021230'", "TIMESTAMPTZ '2006-10-19 15:12:02.021230+00'"} {
		for _, token := range datetimeFormattingTokens {
			want := token.want
			if strings.HasPrefix(source, "TIMESTAMP ") && (token.token == "TZ" || token.token == "tz") {
				want = ""
			}
			cases = append(cases, datetimeFormattingCase{name: source + "/" + token.token, expression: "to_char(" + source + "," + datetimeFormattingQuote(token.token) + ")", want: want})
			// Every token has prefix and ordinal suffix witnesses, including
			// PostgreSQL's ignored or inapplicable modifier combinations.
			for _, modifier := range []string{"FM", "fm", "TM", "tm", "TH", "th"} {
				template := modifier + token.token
				if modifier == "TH" || modifier == "th" {
					template = token.token + modifier
				}
				cases = append(cases, datetimeFormattingCase{name: source + "/" + template, expression: "to_char(" + source + "," + datetimeFormattingQuote(template) + ")"})
			}
		}
	}
	datetimeFormattingCases(t, cases)
}

func TestReferenceDatetimeFormattingLiteralsModifiers(t *testing.T) {
	var cases []datetimeFormattingCase
	for _, c := range []struct{ template, want string }{
		{`"Hello Year "YYYY`, "Hello Year 2006"}, {"IYYY-IW-ID HH24:MI:SS.US", "2006-42-4 15:12:02.021230"},
		{"FMMonth DDTH", "October 19TH"}, {"FMDD DD", "19 19"}, {"FMMS FMUS FMFF6", "021 021230 021230"},
		{`"雪 YYYY" YYYY`, "雪 YYYY 2006"}, {"TMMONTH TMMonth TMmonth", "OCTOBER October october"},
	} {
		cases = append(cases, datetimeFormattingCase{name: c.template, expression: "to_char(TIMESTAMP '2006-10-19 15:12:02.021230'," + datetimeFormattingQuote(c.template) + ")", want: c.want})
	}
	for _, template := range []string{`"quoted \"YYYY\" \\" YYYY`, `\"YYYY\"`, `"unterminated YYYY`, "Year Day MI", "FMFMDD", "FMTMMonth", "TMFMMonth", "FM", "TH", "FX", "SP", "DDSP", "hh24 mi ss yyyy mm dd", "Hh24 MoNtH", "SSSSSHH24IDDDIYYY", "FXYYYY-MM-DD"} {
		cases = append(cases, datetimeFormattingCase{name: template, expression: "to_char(TIMESTAMP '2006-10-19 15:12:02.021230'," + datetimeFormattingQuote(template) + ")"})
	}
	for _, day := range []int{1, 2, 3, 4, 11, 12, 13, 21, 22, 23, 31} {
		for _, suffix := range []string{"TH", "th"} {
			ending := "TH"
			if day%100 < 11 || day%100 > 13 {
				switch day % 10 {
				case 1:
					ending = "ST"
				case 2:
					ending = "ND"
				case 3:
					ending = "RD"
				}
			}
			if suffix == "th" {
				ending = strings.ToLower(ending)
			}
			cases = append(cases, datetimeFormattingCase{name: fmt.Sprintf("ordinal/%d/%s", day, suffix), expression: fmt.Sprintf("to_char(TIMESTAMP '2006-10-%02d','FMDD%s')", day, suffix), want: fmt.Sprint(day) + ending})
		}
	}
	cases = append(cases, datetimeFormattingCase{name: "FM next only", expression: "to_char(TIMESTAMP '2006-01-02','FMDD DD FMMM MM')", want: "2 02 1 01"})
	datetimeFormattingCases(t, cases)
}

func TestReferenceDatetimeFormattingISOEraBounds(t *testing.T) {
	var cases []datetimeFormattingCase
	for _, c := range []struct{ date, template, want string }{
		{"2005-01-01", "IYYY-IW-ID", "2004-53-6"}, {"2007-12-31", "IYYY-IW-ID", "2008-01-1"}, {"2006-01-01", "IYYY-IW-ID", "2005-52-7"},
		{"2000-02-29", "YYYY DDD", "2000 060"}, {"2000-01-01", "CC", "20"}, {"2001-01-01", "CC", "21"},
		{"0001-01-01 BC", "YYYY BC", "0001 BC"}, {"0001-01-01", "YYYY BC", "0001 AD"}, {"12345-01-01", "YYYY Y,YYY", "12345 12,345"},
		{"2006-10-19 00:00", "HH HH24 AM", "12 00 AM"}, {"2006-10-19 12:00", "HH HH24 AM", "12 12 PM"},
	} {
		cases = append(cases, datetimeFormattingCase{name: c.date + "/" + c.template, expression: "to_char(TIMESTAMP " + datetimeFormattingQuote(c.date) + "," + datetimeFormattingQuote(c.template) + ")", want: c.want})
	}
	for _, date := range []string{"4713-01-01 BC", "294276-12-31 23:59:59.999999", "2006-12-29", "2006-12-30", "2006-12-31", "2007-01-01", "2007-01-02", "2007-01-03", "2007-01-04"} {
		cases = append(cases, datetimeFormattingCase{name: date, expression: "to_char(TIMESTAMP " + datetimeFormattingQuote(date) + ",'YYYY BC IYYY IW ID IDDD J')"})
	}
	datetimeFormattingCases(t, cases)
}

func TestReferenceDatetimeFormattingLowerModifiers(t *testing.T) {
	var cases []datetimeFormattingCase
	for _, template := range []string{"fmMonth ddth", "tmMonth tmDay", "fxYYYY-MM-DD", "ddsp", "sp", "fmfmDD", "fmtmMonth", "tmfmMonth", "fm", "tm", "th", "fx"} {
		cases = append(cases, datetimeFormattingCase{name: template, expression: "to_char(TIMESTAMP '2006-10-19'," + datetimeFormattingQuote(template) + ")"})
	}
	datetimeFormattingCases(t, cases)
}

func TestReferenceDatetimeFormattingNameValues(t *testing.T) {
	var cases []datetimeFormattingCase
	for i, month := range []string{"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"} {
		date := fmt.Sprintf("2006-%02d-01", i+1)
		roman := []string{"I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X", "XI", "XII"}[i]
		for _, c := range []struct{ template, want string }{
			{"Month", fmt.Sprintf("%-9s", month)}, {"MONTH", fmt.Sprintf("%-9s", strings.ToUpper(month))}, {"month", fmt.Sprintf("%-9s", strings.ToLower(month))},
			{"Mon", month[:3]}, {"MON", strings.ToUpper(month[:3])}, {"mon", strings.ToLower(month[:3])}, {"RM", fmt.Sprintf("%-4s", roman)},
		} {
			cases = append(cases, datetimeFormattingCase{name: date + "/" + c.template, expression: "to_char(TIMESTAMP " + datetimeFormattingQuote(date) + "," + datetimeFormattingQuote(c.template) + ")", want: c.want})
		}
		cases = append(cases, datetimeFormattingParseCase("parse/"+month, "2006 "+strings.ToUpper(month)+" 01", "YYYY Month DD", date), datetimeFormattingParseCase("parse/"+roman, "2006 "+roman+" 01", "YYYY RM DD", date))
	}
	for i, day := range []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"} {
		date := fmt.Sprintf("2006-01-%02d", i+1)
		for _, c := range []struct{ template, want string }{
			{"Day", fmt.Sprintf("%-9s", day)}, {"DAY", fmt.Sprintf("%-9s", strings.ToUpper(day))}, {"day", fmt.Sprintf("%-9s", strings.ToLower(day))},
			{"Dy", day[:3]}, {"DY", strings.ToUpper(day[:3])}, {"dy", strings.ToLower(day[:3])},
		} {
			cases = append(cases, datetimeFormattingCase{name: date + "/" + c.template, expression: "to_char(TIMESTAMP " + datetimeFormattingQuote(date) + "," + datetimeFormattingQuote(c.template) + ")", want: c.want})
		}
		cases = append(cases, datetimeFormattingParseCase("parse/"+day, date+" "+strings.ToLower(day), "YYYY-MM-DD DAY", date))
	}
	datetimeFormattingCases(t, cases)
}
