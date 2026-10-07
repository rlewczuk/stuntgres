package stuntgres_test

import (
	"context"
	"strings"
	"stuntgres"
	"testing"
)

func TestTimezoneReviewIntervalDiagnosticsAndAtomicity(t *testing.T) {
	ctx := context.Background()
	db := openLocal(t, stuntgres.NewDriver(), t.Name())
	c := timezoneConn(t, db)
	timezoneExec(t, c, "CREATE TABLE tz_witness(v INTEGER)", 0)
	timezoneExec(t, c, "INSERT INTO tz_witness VALUES(1)", 1)
	for _, input := range []string{"@", "@ ", "@ ago", "@  ago", "ago", "nonsense", "9223372036854775808 microseconds"} {
		code, message := "22007", "invalid input syntax for type interval: \""+input+"\""
		if strings.HasPrefix(input, "922337") {
			code, message = "22015", "interval field value out of range: \""+input+"\""
		}
		for _, q := range []string{
			"SET TIME ZONE INTERVAL '" + input + "'",
			"SET timezone='INTERVAL ''" + input + "'''",
			"SELECT set_config('timezone','INTERVAL ''" + input + "''',false)",
		} {
			t.Run(q, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					timezoneExec(t, c, "SET TIME ZONE 'America/New_York'", 0)
					var err error
					if prepared {
						s, prepareErr := c.PrepareContext(ctx, q)
						if prepareErr != nil {
							t.Fatal("validation must occur at execution", prepareErr)
						}
						if strings.HasPrefix(q, "SELECT") {
							rows, queryErr := s.QueryContext(ctx)
							err = queryErr
							if rows != nil {
								rows.Close()
								t.Fatal("failed setting returned a cursor")
							}
						} else {
							_, err = s.ExecContext(ctx)
						}
						s.Close()
					} else {
						if strings.HasPrefix(q, "SELECT") {
							rows, queryErr := c.QueryContext(ctx, q)
							err = queryErr
							if rows != nil {
								rows.Close()
								t.Fatal("failed setting returned a cursor")
							}
						} else {
							_, err = c.ExecContext(ctx, q)
						}
					}
					timeDiagnostic(t, err, stuntgres.Error{Code: code, Message: message})
					timezoneRows(t, c, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"America/New_York"}})
					timezoneRows(t, c, "SELECT ('2004-07-01 12:00'::TEXT::TIMESTAMPTZ AT TIME ZONE 'UTC')::TEXT FROM tz_witness", []string{"TEXT"}, [][]any{{"2004-07-01 16:00:00"}})
				}
			})
		}
	}
	for _, tc := range []struct{ value, detail string }{
		{"No/Such_Zone", ""},
		{"INTERVAL nonsense", ""},
		{"INTERVAL '1 month'", "Cannot specify months in time zone interval."},
		{"INTERVAL '1 day'", "Cannot specify days in time zone interval."},
		{"INTERVAL '168 hours'", "UTC timezone offset is out of range."},
	} {
		for _, prepared := range []bool{false, true} {
			timezoneExec(t, c, "SET TIME ZONE 'America/New_York'", 0)
			q := "SELECT set_config('timezone',$1,false)"
			var err error
			if prepared {
				s, prepareErr := c.PrepareContext(ctx, q)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				rows, queryErr := s.QueryContext(ctx, tc.value)
				err = queryErr
				if rows != nil {
					rows.Close()
					t.Fatal("failed setting returned a cursor")
				}
				s.Close()
			} else {
				rows, queryErr := c.QueryContext(ctx, q, tc.value)
				err = queryErr
				if rows != nil {
					rows.Close()
					t.Fatal("failed setting returned a cursor")
				}
			}
			timeDiagnostic(t, err, stuntgres.Error{Code: "22023", Message: "invalid value for parameter \"TimeZone\": \"" + tc.value + "\"", Detail: tc.detail})
			timezoneRows(t, c, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"America/New_York"}})
		}
	}
	for _, input := range []string{"0 seconds", "@ 0 seconds", "@ 0 seconds ago"} {
		timezoneExec(t, c, "SET TIME ZONE INTERVAL '"+input+"'", 0)
		timezoneRows(t, c, "SHOW TimeZone", []string{"TEXT"}, [][]any{{"<+00>-00"}})
	}
}
