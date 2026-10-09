package reference_test

import "testing"

func datetimeFormattingParseCase(name, input, template, want string) datetimeFormattingCase {
	return datetimeFormattingCase{name: name, expression: "to_date(" + datetimeFormattingQuote(input) + "," + datetimeFormattingQuote(template) + ")::text", want: want}
}

func TestReferenceDatetimeFormattingInputTokens(t *testing.T) {
	cases := []datetimeFormattingCase{
		datetimeFormattingParseCase("manual", "05 Dec 2000", "DD Mon YYYY", "2000-12-05"),
		datetimeFormattingParseCase("ISO week", "2006-42-4", "IYYY-IW-ID", "2006-10-19"),
		datetimeFormattingParseCase("ISO day", "2006-291", "IYYY-IDDD", "2006-10-19"),
		datetimeFormattingParseCase("ISO Monday default", "2006-42", "IYYY-IW", "2006-10-16"),
		datetimeFormattingParseCase("Julian", "2454028", "J", "2006-10-19"),
		datetimeFormattingParseCase("Roman", "2006 X 19", "YYYY RM DD", "2006-10-19"),
		datetimeFormattingParseCase("ignored quarter", "2006 4", "YYYY Q", "2006-01-01"),
		datetimeFormattingParseCase("ignored weekday", "2006-10-19 MONDAY", "YYYY-MM-DD DAY", "2006-10-19"),
		datetimeFormattingParseCase("Gregorian day", "2006 292", "YYYY DDD", "2006-10-19"),
		{name: "timestamp", expression: "to_timestamp('2006-10-19 15:12:02.021230','YYYY-MM-DD HH24:MI:SS.US')::text", want: "2006-10-19 15:12:02.02123+00"},
	}
	// Every keyword has an actual input witness in each shared parsing entry
	// point, including fields ignored by to_date and release-only TZ/OF input.
	for _, token := range datetimeFormattingTokens {
		for _, function := range []string{"to_date", "to_timestamp"} {
			for _, modifier := range []string{"", "FM", "fm", "TM", "tm", "TH", "th"} {
				template := modifier + token.token
				input := token.want
				if modifier == "TH" || modifier == "th" {
					template = token.token + modifier
					input += "xx"
				}
				cases = append(cases, datetimeFormattingCase{name: function + "/" + template, expression: function + "(" + datetimeFormattingQuote(input) + "," + datetimeFormattingQuote(template) + ")::text"})
			}
		}
	}
	for _, c := range []struct{ input, template string }{
		{"2006 october 19", "YYYY MONTH DD"}, {"2006 OCTOBER 19", "YYYY Month DD"}, {"2006 OcT 19", "YYYY mon DD"},
		{"2006 x 19", "YYYY rm DD"}, {"2006 XII 19", "YYYY RM DD"}, {"2006 XI 19", "YYYY RM DD"},
		{"0001-01-01 b.c.", "YYYY-MM-DD B.C."}, {"0001-01-01 ad", "YYYY-MM-DD AD"},
		{"2006-42-4 thursday", "IYYY-IW-ID DAY"}, {"2006-42-4 THU", "IYYY-IW-ID Dy"},
		{"2006-42-5", "IYYY-IW-D"}, {"2006-10-19 15:12 PM", "YYYY-MM-DD HH24:MI PM"},
	} {
		cases = append(cases, datetimeFormattingCase{name: c.input + "/" + c.template, expression: "to_date(" + datetimeFormattingQuote(c.input) + "," + datetimeFormattingQuote(c.template) + ")::text"})
	}
	datetimeFormattingCases(t, cases)
}

func TestReferenceDatetimeFormattingInputPrecedence(t *testing.T) {
	var cases []datetimeFormattingCase
	for _, c := range []struct{ input, template string }{
		{"2006 2006", "YYYY YYYY"}, {"2006 2007", "YYYY YYYY"}, {"0 2006", "YYYY YYYY"}, {"2006 0", "YYYY YYYY"},
		{"2006 01 02", "YYYY MM DD"}, {"2006 0 10 19", "YYYY MM MM DD"}, {"2006 10 0 19", "YYYY MM MM DD"},
		{"2006 292 01 01", "YYYY DDD MM DD"}, {"2006 292 02 03", "YYYY DDD MM DD"}, {"2006 02 03 292", "YYYY MM DD DDD"},
		{"2006 42", "YYYY WW"}, {"2006 10 3", "YYYY MM W"}, {"2006 42 19", "YYYY WW DD"}, {"2006 10 3 19", "YYYY MM W DD"},
		{"2000 2454028", "YYYY J"}, {"2454028 2000", "J YYYY"}, {"2454028 01 01", "J MM DD"},
		{"21 06", "CC YY"}, {"21 6", "CC Y"}, {"21", "CC"}, {"20 2006", "CC YYYY"}, {"20 006", "CC YYY"}, {"20 2,006", "CC Y,YYY"},
		{"54722 01 02 03", "SSSS HH24 MI SS"}, {"54722 00 00 00", "SSSS HH24 MI SS"},
		{"2006 2006 42", "YYYY IYYY IW"}, {"2006 10 19 4", "YYYY MM DD ID"}, {"2006 42 5", "IYYY IW D"},
		{"292", "DDD"}, {"291", "IDDD"}, {"2006 1 1 292", "YYYY MM DD DDD"},
	} {
		for _, f := range []string{"to_date", "to_timestamp"} {
			cases = append(cases, datetimeFormattingCase{name: f + "/" + c.input + "/" + c.template, expression: f + "(" + datetimeFormattingQuote(c.input) + "," + datetimeFormattingQuote(c.template) + ")::text"})
		}
	}
	cases = append(cases, datetimeFormattingCase{name: "conventions", expression: "to_date('2006 2006','YYYY IYYY')", diagnostic: datetimeFormattingDiagnostic("22007", "invalid combination of date conventions", "", "Do not mix Gregorian and ISO week date conventions in a formatting template.")},
		datetimeFormattingCase{name: "conflicting year", expression: "to_date('2006 2007','YYYY YYYY')", diagnostic: datetimeFormattingDiagnostic("22007", `conflicting values for "YYYY" field in formatting string`, `This value contradicts a previous setting for the same field type.`, "")})
	datetimeFormattingCases(t, cases)
}

func TestReferenceDatetimeFormattingShortYears(t *testing.T) {
	var cases []datetimeFormattingCase
	for _, c := range []struct{ input, token, want string }{
		{"69", "YY", "2069-01-01"}, {"70", "YY", "1970-01-01"}, {"519", "YYY", "2519-01-01"}, {"520", "YYY", "1520-01-01"},
		{"6", "Y", "2006-01-01"}, {"2006", "YY", "2006-01-01"}, {"2006", "YYY", "2006-01-01"}, {"0000", "YYYY", "0001-01-01 BC"},
	} {
		cases = append(cases, datetimeFormattingParseCase(c.input+"/"+c.token, c.input, c.token, c.want))
	}
	for _, c := range []struct{ input, token string }{
		{"-1", "YYYY"}, {"-1 BC", "YYYY BC"}, {"0 BC", "YYYY BC"}, {"-69", "YY"}, {"-520", "YYY"}, {"12345-01-02", "YYYY-MM-DD"},
		{"123450102", "YYYYMMDD"}, {"1,234,567", "Y,YYY"}, {"2147483648", "YYYY"}, {"-2147483649", "YYYY"}, {"2147483647", "CC"},
		{"2006 2147483647", "YYYY WW"}, {"2006 10 2147483647", "YYYY MM W"},
	} {
		cases = append(cases, datetimeFormattingCase{name: c.input + "/" + c.token, expression: "to_date(" + datetimeFormattingQuote(c.input) + "," + datetimeFormattingQuote(c.token) + ")::text"})
	}
	datetimeFormattingCases(t, cases)
}
