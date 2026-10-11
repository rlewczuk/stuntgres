package reference_test

import (
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimezoneExtremeTimetzCapture(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		for _, tc := range []struct{ zone, input, want string }{{"ABC167:59:60", "10:00", "10:00:00-168"}, {"ABC-167:59:60DEF,M3.2.0,M11.1.0", "2004-07-01 10:00", "10:00:00+169"}} {
			run(fixture.Exec("SET TIME ZONE '"+tc.zone+"'", 0))
			q := "SELECT (TIMETZ '" + tc.input + "')::TEXT AS q FROM tz_witness WHERE id=1"
			held := timezoneHeld(t, p, backend.DB, q)
			run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Select(q, []string{"q"}, []string{"TEXT"}, [][]any{{tc.want}}))
			if err := held.Statement.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestReferenceTimezoneCastsExecutionEnvironment(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	p.Step(t, fixture.Exec("UPDATE tz_witness SET z='2004-10-19 04:00+00',c='2004-10-19 00:00',d='2004-10-19',q='10:00-05' WHERE id=1", 1))
	for _, backend := range p.Backends() {
		query := "SELECT c::TIMESTAMPTZ AS a,d::TIMESTAMPTZ AS b,z::TIMESTAMP::TEXT AS c,z::DATE::TEXT AS d,z::TIME::TEXT AS e,q::TIME::TEXT AS f FROM tz_witness"
		held := timezoneHeld(t, p, backend.DB, query)
		for _, tc := range []struct {
			zone  string
			hour  int
			clock string
		}{{"UTC", 0, "04:00:00"}, {"America/New_York", 4, "00:00:00"}} {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("SET TIME ZONE '"+tc.zone+"'", 0))
			want := time.Date(2004, 10, 19, tc.hour, 0, 0, 0, time.UTC)
			s := fixture.Select(query, []string{"a", "b", "c", "d", "e", "f"}, []string{"TIMESTAMPTZ", "TIMESTAMPTZ", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{want, want, "2004-10-19 " + tc.clock, "2004-10-19", tc.clock, "10:00:00"}})
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
		}
		if err := held.Statement.Close(); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("SET TIME ZONE 'UTC'", 0))
	}
	timezoneBoth(t, p, fixture.Select("SELECT $1::TIMESTAMPTZ=$1::TIMESTAMPTZ AS a,$2::TIMETZ=$2::TIMETZ AS b FROM tz_witness", []string{"a", "b"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, "2004-01-01+02", "10:00-05"))
	s := fixture.ImplicitError("SELECT $1 FROM tz_witness WHERE z=$1", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "timestamp with time zone versus text", Position: "8"})
	s.SelectRows = true
	s.ErrorStage = "prepare"
	p.Step(t, fixture.Prepared(s))
	timezoneBoth(t, p, fixture.Select("SELECT NULL::TIMESTAMPTZ=TIMESTAMPTZ 'epoch' AS a,NULL::TIMETZ<TIMETZ '10:00+00' AS b FROM tz_witness", []string{"a", "b"}, []string{"BOOL", "BOOL"}, [][]any{{nil, nil}}))
	timezoneBoth(t, p, fixture.Exec("INSERT INTO tz_witness(id,z,q) VALUES($1,$2,$3)", 1, int64(2), nil, nil))
}

func TestReferenceTimezoneDeclarationPrecisionDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		var notices []fixture.Diagnostic
		postgres := backend.PG
		_, conn, record := fixture.NoticeReferencePool(t, p, postgres, &notices)
		record.RawBackend = fixture.RawNative
		if postgres {
			record.RawBackend = fixture.RawPostgres
		}
		fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("CREATE TABLE tz_one(id INTEGER)", 0))
		fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("INSERT INTO tz_one VALUES(1)", 1))
		for _, tc := range []struct{ typ, label, value, dbtype string }{
			{"TIMESTAMP(7) WITH TIME ZONE", "TIMESTAMP(7) WITH TIME ZONE", "2004-01-01 00:00:00.123456+00", "TIMESTAMPTZ"},
			{"TIME(7) WITH TIME ZONE", "TIME(7) WITH TIME ZONE", "10:00:00.123456-05", "TIMETZ"},
			{"TIMESTAMPTZ(7)", "TIMESTAMP(7) WITH TIME ZONE", "2004-01-01 00:00:00.123456+00", "TEXT"},
			{"TIMETZ(7)", "TIME(7) WITH TIME ZONE", "12:00:00.123456+02", "TIMETZ"},
		} {
			for _, prepared := range []bool{false, true} {
				q := "SELECT " + tc.typ + " '" + tc.value + "' AS v FROM tz_one"
				if tc.dbtype == "TEXT" {
					q = "SELECT " + tc.typ + " '" + tc.value + "'::TEXT AS v FROM tz_one"
				}
				var want any = tc.value
				if tc.dbtype == "TIMESTAMPTZ" {
					want = time.Date(2004, 1, 1, 0, 0, 0, 123456000, time.UTC)
				} else if postgres && tc.dbtype == "TIMETZ" {
					// The fixture normalizes pq's year-zero TIMETZ to UTC.
					// 10:00-05 is independently the exact 15:00 UTC instant.
					want = time.Date(0, 1, 1, 15, 0, 0, 123456000, time.UTC)
					if tc.value == "12:00:00.123456+02" {
						want = time.Date(0, 1, 1, 10, 0, 0, 123456000, time.UTC)
					}
				}
				s := fixture.Select(q, []string{"v"}, []string{tc.dbtype}, [][]any{{want}})
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, record, s)
				expected := []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: tc.label + " precision reduced to maximum allowed, 6", Position: "8"}}
				if !reflect.DeepEqual(notices, expected) {
					t.Fatal(notices, expected)
				}
				notices = nil
			}
		}
		for _, tc := range []struct{ query, message, token string }{
			{"CREATE TABLE bad(v TIMESTAMP(-1) WITH TIME ZONE)", `syntax error at or near "-"`, "-"},
			{"CREATE TABLE bad(v TIME(-1) WITH TIME ZONE)", `syntax error at or near "-"`, "-"},
			{`CREATE TABLE bad(v "timestamptz"(-1))`, "TIMESTAMP(-1) WITH TIME ZONE precision must not be negative", `"timestamptz"`},
			{`CREATE TABLE bad(v "timetz"(-1))`, "TIME(-1) WITH TIME ZONE precision must not be negative", `"timetz"`},
		} {
			for _, prepared := range []bool{false, true} {
				code := "42601"
				stage := "execute"
				if prepared {
					stage = "prepare"
				}
				if strings.HasPrefix(tc.token, `"`) {
					code = "22023"
					stage = "execute"
				}
				s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: code, Message: tc.message, Position: strconv.Itoa(strings.Index(tc.query, tc.token) + 1)})
				s.ErrorStage = stage
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, conn, record, s)
			}
		}
		fixture.RunStep(t, p.Ctx, conn, record, fixture.Exec("DROP TABLE tz_one", 0))
		conn.Close()
	}
}

func TestReferenceTimezoneBuiltinRegistryDescriptions(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	for _, backend := range p.Backends() {
		for _, prepared := range []bool{false, true} {
			query := "SHOW ALL"
			if backend.Name == "postgres" {
				query = "SELECT name,setting,short_desc FROM pg_settings WHERE name IN ('DateStyle','TimeZone','timezone_abbreviations','default_transaction_deferrable','transaction_deferrable','client_encoding','lc_messages','lc_monetary','lc_numeric','lc_time','icu_validation_level','extra_float_digits','transaction_timeout','idle_in_transaction_session_timeout','idle_session_timeout') ORDER BY name"
			}
			var rows *sql.Rows
			var err error
			if prepared {
				stmt, e := backend.DB.PrepareContext(p.Ctx, query)
				if e != nil {
					t.Fatal(e)
				}
				defer stmt.Close()
				rows, err = stmt.QueryContext(p.Ctx)
			} else {
				rows, err = backend.DB.QueryContext(p.Ctx, query)
			}
			if err != nil {
				t.Fatal(err)
			}
			columns, err := rows.Columns()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			third := "description"
			if backend.Name == "postgres" {
				third = "short_desc"
			}
			if !reflect.DeepEqual(columns, []string{"name", "setting", third}) {
				rows.Close()
				t.Fatal(columns)
			}
			types, err := rows.ColumnTypes()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			for _, typ := range types {
				if typ.DatabaseTypeName() != "TEXT" {
					rows.Close()
					t.Fatal(typ)
				}
			}
			want := map[string][2]string{"DateStyle": {"ISO, MDY", "Sets the display format for date and time values."}, "TimeZone": {"UTC", "Sets the time zone for displaying and interpreting time stamps."}}
			want["timezone_abbreviations"] = [2]string{"Default", "Selects a file of time zone abbreviations."}
			want["default_transaction_deferrable"] = [2]string{"off", "Sets the default deferrable status of new transactions."}
			want["transaction_deferrable"] = [2]string{"off", "Whether to defer a read-only serializable transaction until it can be executed with no possible serialization failures."}
			want["client_encoding"] = [2]string{"UTF8", "Sets the client's character set encoding."}
			want["lc_messages"] = [2]string{"C", "Sets the language in which messages are displayed."}
			want["lc_monetary"] = [2]string{"C", "Sets the locale for formatting monetary amounts."}
			want["lc_numeric"] = [2]string{"C", "Sets the locale for formatting numbers."}
			want["lc_time"] = [2]string{"C", "Sets the locale for formatting date and time values."}
			want["icu_validation_level"] = [2]string{"warning", "Log level for reporting invalid ICU locale strings."}
			want["extra_float_digits"] = [2]string{"1", "Sets the number of digits displayed for floating-point values."}
			// Independent REL_18_0 guc_tables.c short_desc, not native registry data.
			want["transaction_timeout"] = [2]string{"0", "Sets the maximum allowed duration of any transaction within a session (not a prepared transaction)."}
			want["idle_in_transaction_session_timeout"] = [2]string{"0", "Sets the maximum allowed idle time between queries, when in a transaction."}
			want["idle_session_timeout"] = [2]string{"0", "Sets the maximum allowed idle time between queries, when not in a transaction."}
			expectedNames := map[string]bool{
				"DateStyle": true, "IntervalStyle": true, "TimeZone": true, "application_name": true,
				"bytea_output": true, "client_min_messages": true, "default_transaction_isolation": true,
				"default_transaction_read_only": true, "lock_timeout": true, "log_statement_stats": true,
				"search_path": true, "server_encoding": true, "standard_conforming_strings": true,
				"statement_timeout": true, "transaction_isolation": true, "transaction_read_only": true,
				"default_transaction_deferrable": true, "transaction_deferrable": true,
				"timezone_abbreviations": true,
				"client_encoding":        true, "lc_messages": true, "lc_monetary": true, "lc_numeric": true,
				"lc_time": true, "icu_validation_level": true, "extra_float_digits": true,
				"transaction_timeout": true, "idle_in_transaction_session_timeout": true, "idle_session_timeout": true,
			}
			count, last := 0, ""
			for rows.Next() {
				var name, value, description string
				if err := rows.Scan(&name, &value, &description); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if !backend.PG {
					key := strings.ToLower(name)
					if !expectedNames[name] || key <= last {
						rows.Close()
						t.Fatal("unexpected, repeated or unsorted builtin", name)
					}
					delete(expectedNames, name)
					last = key
					count++
				}
				if expected, ok := want[name]; ok {
					if value != expected[0] || description != expected[1] {
						rows.Close()
						t.Fatal(name, value, description, expected)
					}
					delete(want, name)
				}
			}
			if rows.Err() != nil || len(want) != 0 {
				rows.Close()
				t.Fatal("registry completeness", rows.Err(), want)
			}
			if !backend.PG && (count != 29 || len(expectedNames) != 0) {
				rows.Close()
				t.Fatal("builtin cardinality/set", count, expectedNames)
			}
			rows.Close()
		}
	}
}
