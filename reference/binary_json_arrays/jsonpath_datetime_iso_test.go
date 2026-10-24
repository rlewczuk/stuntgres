package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

var jsonpathDatetimeISO = []struct{ input, method, output, label string }{
	{"2023-08-15", "date", "2023-08-15", "date"},
	{"12:34:56.789", "time", "12:34:56.789", "time without time zone"},
	{"12:34:56.789 +05:30", "time_tz", "12:34:56.789+05:30", "time with time zone"},
	{"2023-08-15 12:34:56.789", "timestamp", "2023-08-15T12:34:56.789", "timestamp without time zone"},
	{"2023-08-15 12:34:56.789 +05:30", "timestamp_tz", "2023-08-15T12:34:56.789+05:30", "timestamp with time zone"},
}

func TestReferenceJSONPathDatetimeISO(t *testing.T) {
	for _, tc := range jsonpathDatetimeISO {
		for _, method := range []string{tc.method, "datetime"} {
			t.Run(tc.method+"/"+method, func(t *testing.T) {
				jsonpathDatetimeValue(t, jsonpathDatetimeString(tc.input), `$.`+method+`()`, jsonpathDatetimeArray(tc.output))
			})
		}
	}
	for _, tc := range []struct{ input, want string }{
		{"12:34:56", "12:34:56"}, {"12:34:56+00", "12:34:56+00:00"},
		{"2023-08-15T12:34:56", "2023-08-15T12:34:56"},
		{"2023-08-15T12:34:56.123456+00", "2023-08-15T12:34:56.123456+00:00"},
		{"2023-08-15 12:34:56+00", "2023-08-15T12:34:56+00:00"},
		{"2023-08-15T12:34:56.123456", "2023-08-15T12:34:56.123456"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			jsonpathDatetimeValue(t, jsonpathDatetimeString(tc.input), `$.datetime()`, jsonpathDatetimeArray(tc.want))
		})
	}
}

func TestReferenceJSONPathDatetimeTypes(t *testing.T) {
	for _, tc := range jsonpathDatetimeISO {
		t.Run(tc.method, func(t *testing.T) {
			jsonpathDatetimeValue(t, jsonpathDatetimeString(tc.input), `$.datetime().type()`, jsonpathDatetimeArray(tc.label))
			jsonpathDatetimeValue(t, jsonpathDatetimeString(tc.input), `$.`+tc.method+`().string()`, jsonpathDatetimeArray(tc.output))
		})
	}
}

func TestReferenceJSONPathDatetimeConversions(t *testing.T) {
	// Rows are source families; columns are the six requested methods. A blank
	// cell is an incompatible family, not an invitation to coerce through text.
	methods := []string{"datetime", "date", "time", "time_tz", "timestamp", "timestamp_tz"}
	outputs := [][]string{
		{"2023-08-15", "2023-08-15", "", "", "2023-08-15T00:00:00", "zone"},
		{"12:34:56.789", "", "12:34:56.789", "zone", "", ""},
		{"12:34:56.789+05:30", "", "zone", "12:34:56.789+05:30", "", ""},
		{"2023-08-15T12:34:56.789", "2023-08-15", "12:34:56.789", "", "2023-08-15T12:34:56.789", "zone"},
		{"2023-08-15T12:34:56.789+05:30", "zone", "zone", "07:04:56.789+00:00", "zone", "2023-08-15T12:34:56.789+05:30"},
	}
	sources := []string{"date", "time", "timetz", "timestamp", "timestamptz"}
	targets := []string{"", "date", "time", "timetz", "timestamp", "timestamptz"}
	for i, tc := range jsonpathDatetimeISO {
		for j, method := range methods {
			t.Run(tc.method+"/"+method, func(t *testing.T) {
				doc, path, want := jsonpathDatetimeString(tc.input), `$.`+method+`()`, outputs[i][j]
				switch want {
				case "zone":
					jsonpathDatetimeDiagnostic(t, doc, path, jsonpathDatetimeZoneError(sources[i], targets[j]))
				case "":
					jsonpathDatetimeDiagnostic(t, doc, path, jsonpathDatetimeFormatError(method, tc.input))
				default:
					jsonpathDatetimeValue(t, doc, path, jsonpathDatetimeArray(want))
				}
			})
		}
	}
}

func TestReferenceJSONPathDatetimeDiagnostics(t *testing.T) {
	for _, method := range []string{"datetime", "date", "time", "time_tz", "timestamp", "timestamp_tz"} {
		for _, doc := range []string{`null`, `1`, `true`, `{}`, `[]`, `["2023-08-15"]`} {
			t.Run(method+"/"+doc, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic("22031", `jsonpath item method .`+method+`() can only be applied to a string`)
				jsonpathDatetimeDiagnostic(t, doc, `strict $.`+method+`()`, d)
			})
		}
		for _, input := range []string{"", "bad", "2023-02-29", "2023-13-01", "2023-08-32", "25:00:00", "2023-08-15junk"} {
			t.Run(method+"/"+input, func(t *testing.T) {
				jsonpathDatetimeDiagnostic(t, jsonpathDatetimeString(input), `$.`+method+`()`, jsonpathDatetimeFormatError(method, input))
			})
		}
		t.Run(method+"/typed-continuation", func(t *testing.T) {
			jsonpathDatetimeDiagnostic(t, `"2023-08-15"`, `$.date().`+method+`()`, fixture.ControlsReferenceDiagnostic("22031", `jsonpath item method .`+method+`() can only be applied to a string`))
		})
	}
}
