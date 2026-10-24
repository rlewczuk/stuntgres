package reference_test

import (
	"fmt"
	"reflect"
	"strconv"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathDatetimePrecision(t *testing.T) {
	fractions := []string{"12:34:57", "12:34:56.8", "12:34:56.79", "12:34:56.789", "12:34:56.789", "12:34:56.789", "12:34:56.789"}
	for _, method := range []string{"time", "time_tz", "timestamp", "timestamp_tz"} {
		for precision, clock := range fractions {
			t.Run(method+"/"+strconv.Itoa(precision), func(t *testing.T) {
				input, want := "12:34:56.789", clock
				if method == "timestamp" || method == "timestamp_tz" {
					input, want = "2023-08-15 "+input, "2023-08-15T"+want
				}
				if method == "time_tz" || method == "timestamp_tz" {
					input, want = input+" +05:30", want+"+05:30"
				}
				jsonpathDatetimeValue(t, jsonpathDatetimeString(input), `$.`+method+`(`+strconv.Itoa(precision)+`)`, jsonpathDatetimeArray(want))
			})
		}
	}
	for _, tc := range []struct{ input, path, want string }{
		{"23:59:59.999999", `$.time(0)`, "24:00:00"},
		{"2023-12-31 23:59:59.999999", `$.timestamp(0)`, "2024-01-01T00:00:00"},
		{"1999-12-31 23:59:59.5", `$.timestamp(0)`, "1999-12-31T23:59:59"},
		{"2000-01-01 00:00:00.5", `$.timestamp(0)`, "2000-01-01T00:00:01"},
		{"0001-01-01", `$.date()`, "0001-01-01"},
		{"9999-12-31 23:59:59.999999", `$.timestamp(6)`, "9999-12-31T23:59:59.999999"},
	} {
		t.Run(tc.input+tc.path, func(t *testing.T) {
			jsonpathDatetimeValue(t, jsonpathDatetimeString(tc.input), tc.path, jsonpathDatetimeArray(tc.want))
		})
	}
	t.Run("24:00 ISO input rejected", func(t *testing.T) {
		jsonpathDatetimeDiagnostic(t, `"24:00:00"`, `$.time()`, jsonpathDatetimeFormatError("time", "24:00:00"))
	})
	for _, method := range []string{"time", "time_tz", "timestamp", "timestamp_tz"} {
		t.Run(method+"/integer-overflow", func(t *testing.T) {
			jsonpathDatetimeDiagnostic(t, `"2023-08-15 12:34:56+00"`, `$.`+method+`(2147483648)`, fixture.ControlsReferenceDiagnostic("22031", `time precision of jsonpath item method .`+method+`() is out of range for type integer`))
		})
	}
}

func TestReferenceJSONPathDatetimePrecisionNotices(t *testing.T) {
	for _, tc := range []struct{ method, input, output, typ string }{
		{"time", "12:34:56.123456", "12:34:56.123456", "TIME(%d)"},
		{"time_tz", "12:34:56.123456+05:30", "12:34:56.123456+05:30", "TIME(%d) WITH TIME ZONE"},
		{"timestamp", "2023-08-15 12:34:56.123456", "2023-08-15T12:34:56.123456", "TIMESTAMP(%d)"},
		{"timestamp_tz", "2023-08-15 12:34:56.123456+05:30", "2023-08-15T12:34:56.123456+05:30", "TIMESTAMP(%d) WITH TIME ZONE"},
	} {
		for _, precision := range []int{7, 10, 2147483647} {
			t.Run(tc.method+"/"+strconv.Itoa(precision), func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						var notices []fixture.Diagnostic
						_, c, record := fixture.NoticeReferencePool(t, p, b.PG, &notices)
						warning := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: fmt.Sprintf(tc.typ, precision) + " precision reduced to maximum allowed, 6"}
						assert := func(phase string, count int) {
							t.Helper()
							var want []fixture.Diagnostic
							for range count {
								want = append(want, warning)
							}
							if !reflect.DeepEqual(notices, want) {
								t.Errorf("%s complete notices=%#v want=%#v", phase, notices, want)
							}
							notices = nil
						}
						path := `$[*].` + tc.method + `(` + strconv.Itoa(precision) + `)`
						valid := jsonpathDatetimeString(tc.input)
						for _, prepared := range []bool{false, true} {
							stmt, err := c.PrepareContext(p.Ctx, jsonpathDatetimeSQL)
							if err != nil {
								t.Fatal(err)
							}
							assert("Prepare", 0)
							func() {
								defer stmt.Close()
								var executor fixture.Executor = c
								if prepared {
									executor = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: jsonpathDatetimeSQL}
								}
								for _, sample := range []struct {
									doc    string
									want   any
									count  int
									silent bool
								}{
									{"[" + valid + "," + valid + "]", "[" + jsonpathDatetimeString(tc.output) + ", " + jsonpathDatetimeString(tc.output) + "]", 2, false},
									{"[]", "[]", 0, false}, {valid, jsonpathDatetimeArray(tc.output), 1, false},
									{"[" + valid + `,"bad",` + valid + "]", jsonpathDatetimeArray(tc.output), 1, true},
									{"null", "[]", 0, true},
								} {
									fixture.RunStep(t, p.Ctx, executor, record, jsonpathNavigateSelect(jsonpathDatetimeSQL, sample.want, sample.doc, path, `{}`, sample.silent))
									assert(fmt.Sprintf("prepared=%t doc=%s", prepared, sample.doc), sample.count)
								}
								s := fixture.ErrorDiagnostic(jsonpathDatetimeSQL, jsonpathDatetimeFormatError(tc.method, "bad"), `"bad"`, path, `{}`, false)
								s.SelectRows = true
								fixture.RunStep(t, p.Ctx, executor, record, s)
								assert("invalid input before precision", 0)
								fixture.RunStep(t, p.Ctx, executor, record, jsonpathNavigateSelect(jsonpathDatetimeSQL, nil, nil, path, `{}`, false))
								assert("SQL NULL", 0)
							}()
						}
						// Literal plans must not emit a warning at Prepare, and an
						// unreachable method must not emit one at execution.
						q := `SELECT jsonb_path_query_array('[]',` + jsonReferenceQuote(path) + `) AS changed`
						stmt, err := c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						assert("literal Prepare", 0)
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, record, jsonpathNavigateSelect(q, "[]"))
						stmt.Close()
						assert("empty literal Execute", 0)
					})
				}
			})
		}
	}
}
