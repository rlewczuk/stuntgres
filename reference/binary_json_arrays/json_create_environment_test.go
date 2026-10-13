package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonCreateEnvironment(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	q := `SELECT to_json($1::timestamptz) AS v`
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, tc := range []struct{ zone, want string }{
		{"UTC", `"2020-01-02T03:04:05.1234+00:00"`},
		{"America/Los_Angeles", `"2020-01-01T19:04:05.1234-08:00"`},
		{"UTC", `"2020-01-02T03:04:05.1234+00:00"`},
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TimeZone = '"+tc.zone+"'", 0))
		s := jsonCreateSelect(q, "JSON", tc.want, "2020-01-02 03:04:05.1234+00")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		jsonObjectBoth(t, p, b, c, jsonCreateSelect(`SELECT jsonb_build_array(TIMESTAMPTZ '2020-01-02 03:04:05.1234+00') AS v`, "JSONB", "["+tc.want+"]"))
		jsonObjectBoth(t, p, b, c, jsonCreateSelect(`SELECT array_to_json(ARRAY[TIMESTAMPTZ '2020-01-02 03:04:05.1234+00']) AS v`, "JSON", "["+tc.want+"]"))
		jsonObjectBoth(t, p, b, c, jsonCreateSelect(`SELECT row_to_json(ROW(TIMESTAMPTZ '2020-01-02 03:04:05.1234+00')) AS v`, "JSON", `{"f1":`+tc.want+`}`))
	}
	for _, style := range []string{"ISO, MDY", "SQL, DMY", "German, DMY"} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET DateStyle = '"+style+"'", 0))
		jsonCreateCases(t, p, b, c, []struct {
			expr, typ string
			want      any
		}{
			{`to_json(DATE '2020-01-02')`, "JSON", `"2020-01-02"`},
			{`to_jsonb(TIMESTAMP '2020-01-02 03:04:05.1234')`, "JSONB", `"2020-01-02T03:04:05.1234"`},
			{`row_to_json(ROW(DATE '2020-01-02',ARRAY[TIMESTAMP '2020-01-02 03:04:05']))`, "JSON", `{"f1":"2020-01-02","f2":["2020-01-02T03:04:05"]}`},
		})
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET DateStyle = 'ISO, MDY'", 0))
	jsonCreateCases(t, p, b, c, []struct {
		expr, typ string
		want      any
	}{
		{`to_json(DATE '0001-01-02 BC')`, "JSON", `"0001-01-02 BC"`},
		{`to_jsonb(TIMESTAMP '0001-01-02 03:04:05 BC')`, "JSONB", `"0001-01-02T03:04:05 BC"`},
		{`json_build_array(DATE 'infinity',TIMESTAMP '-infinity',TIMESTAMPTZ 'infinity')`, "JSON", `["infinity", "-infinity", "infinity"]`},
		{`jsonb_build_array('NaN'::float8,'Infinity'::numeric,'-Infinity'::float4)`, "JSONB", `["NaN", "Infinity", "-Infinity"]`},
		{`to_json(TIME '03:04:05.1234')`, "JSON", `"03:04:05.1234"`},
		{`to_json(TIMETZ '03:04:05+05:30')`, "JSON", `"03:04:05+05:30"`},
		{`to_json('$12.34'::money)`, "JSON", `"$12.34"`},
	})
	for _, tc := range []struct{ setting, expr, want string }{
		{`SET bytea_output='hex'`, `to_json('\x00ff'::bytea)`, `"\\x00ff"`},
		{`SET bytea_output='escape'`, `to_json('\x00ff'::bytea)`, `"\\000\\377"`},
		{`SET IntervalStyle='postgres'`, `to_json(INTERVAL '1 day 02:03:04')`, `"1 day 02:03:04"`},
		{`SET IntervalStyle='iso_8601'`, `to_json(INTERVAL '1 day 02:03:04')`, `"P1DT2H3M4S"`},
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(tc.setting, 0))
		jsonObjectBoth(t, p, b, c, jsonCreateSelect("SELECT "+tc.expr+" AS v", "JSON", tc.want))
	}
	fq := `SELECT to_json($1::float8) AS v`
	fs, err := c.PrepareContext(p.Ctx, fq)
	if err != nil {
		t.Fatal(err)
	}
	defer fs.Close()
	fh := fixture.HeldStatement{Executor: c, Statement: fs, SQL: fq}
	for _, tc := range []struct{ digits, want string }{{"0", "1.23456789012346"}, {"3", "1.2345678901234567"}} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET extra_float_digits="+tc.digits, 0))
		fixture.RunStep(t, p.Ctx, fh, b.Recorder, jsonCreateSelect(fq, "JSON", tc.want, "1.2345678901234567"))
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET TIME ZONE INTERVAL '+05:30:15'`, 0))
	jsonObjectBoth(t, p, b, c, jsonCreateSelect(`SELECT to_json(TIMESTAMPTZ '2020-01-02 00:00:00+00') AS v`, "JSON", `"2020-01-02T05:30:15+05:30:15"`))
	for _, tc := range []struct{ offset, want string }{
		{"+15:59:59", `"2020-01-02T15:59:59+15:59:59"`},
		{"-15:59:59", `"2020-01-01T08:00:01-15:59:59"`},
	} {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET TIME ZONE INTERVAL '"+tc.offset+"'", 0))
		jsonObjectBoth(t, p, b, c, jsonCreateSelect(`SELECT to_jsonb(TIMESTAMPTZ '2020-01-02 00:00:00+00') AS v`, "JSONB", tc.want))
	}
}

func TestReferenceJSONCreateEnvironment(t *testing.T) {
	jsonCreateScenario(t, jsonCreateEnvironment)
}
