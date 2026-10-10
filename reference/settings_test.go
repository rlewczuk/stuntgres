package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func settingsReferenceError(q, code, message, hint, detail string) referenceStep {
	d := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint, Detail: detail}
	s := referenceErrorDiagnostic(q, d)
	s.errorStage = "execute"
	return s
}
func TestReferenceSettingsSQL(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("prepared=", prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					a, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, a, backend.recorder, s) }
					for _, tc := range []struct{ name, value string }{{"application_name", ""}, {"standard_conforming_strings", "on"}, {"statement_timeout", "0"}, {"server_encoding", "UTF8"}, {"log_statement_stats", "off"}} {
						run(referenceSelect("SHOW "+tc.name, []string{tc.name}, []string{"TEXT"}, [][]any{{tc.value}}))
						run(referenceSelect("SELECT current_setting('"+tc.name+"')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{tc.value}}))
					}
					run(referenceSelect("SHOW client_min_messages", []string{"client_min_messages"}, []string{"TEXT"}, [][]any{{"notice"}}))
					for _, tc := range []struct{ input, want string }{{"DEBUG", "debug2"}, {"WARNING", "warning"}, {"info", "info"}, {"notice", "notice"}} {
						run(referenceExec("SET client_min_messages TO '"+tc.input+"'", 0))
						run(referenceSelect("SELECT current_setting('client_min_messages')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{tc.want}}))
					}
					run(settingsReferenceError("SET client_min_messages TO bogus", "22023", `invalid value for parameter "client_min_messages": "bogus"`, "Available values: debug5, debug4, debug3, debug2, debug1, log, notice, warning, error.", ""))
					run(referenceExec("RESET client_min_messages", 0))
					for _, tc := range []struct{ input, want string }{{"1000", "1s"}, {"1500ms", "1500ms"}, {"1.5s", "1500ms"}, {"0.5ms", "0"}, {"1min", "1min"}, {"0", "0"}} {
						run(referenceExec("SET statement_timeout TO '"+tc.input+"'", 0))
						run(referenceSelect("SHOW statement_timeout", []string{"statement_timeout"}, []string{"TEXT"}, [][]any{{tc.want}}))
						run(referenceSelect("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{tc.want}}, "statement_timeout", tc.input, false))
					}
					// Observe 1ms on the arming command, whose budget starts at 0.
					// Subsequent SHOW or reset SQL could legitimately time out, so
					// each route retires its physical owner without another command.
					for _, step := range []referenceStep{
						referenceExec("SET statement_timeout TO '1000us'", 0),
						referenceSelect("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"1ms"}}, "statement_timeout", "1000us", false),
					} {
						func() {
							owner, err := backend.db.Conn(p.ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer func() {
								if err := owner.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
									t.Error("retiring armed settings owner", err)
								}
								if err := owner.Close(); !errors.Is(err, sql.ErrConnDone) {
									t.Error("closing retired settings owner", err)
								}
							}()
							baseline := referenceSelect("SHOW statement_timeout", []string{"statement_timeout"}, []string{"TEXT"}, [][]any{{"0"}})
							baseline.prepared = prepared
							referenceRunStep(t, p.ctx, owner, backend.recorder, baseline)
							step.prepared = prepared
							referenceRunStep(t, p.ctx, owner, backend.recorder, step)
						}()
					}
					for _, tc := range []struct{ input, want string }{{"t", "on"}, {"TRUE", "on"}, {"yes", "on"}, {"1", "on"}, {"f", "off"}, {"NO", "off"}, {"0", "off"}, {"off", "off"}} {
						run(referenceExec("SET standard_conforming_strings TO '"+tc.input+"'", 0))
						run(referenceSelect("SHOW standard_conforming_strings", []string{"standard_conforming_strings"}, []string{"TEXT"}, [][]any{{tc.want}}))
					}
					run(referenceExec("RESET standard_conforming_strings", 0))
					run(referenceExec("SET application_name TO retained", 0))
					run(referenceSelect("SELECT current_setting(NULL)", []string{"current_setting"}, []string{"TEXT"}, [][]any{{nil}}))
					run(referenceSelect("SELECT set_config('application_name',NULL,NULL)", []string{"set_config"}, []string{"TEXT"}, [][]any{{""}}))
					run(referenceSelect("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"value"}}, "APPLICATION_NAME", "value", false))
					run(referenceExec("SELECT current_setting('application_name')", 1))
					run(referenceExec("SELECT set_config('application_name','value',false)", 1))
					for _, s := range []referenceStep{
						settingsReferenceError("SET server_encoding TO 'UTF8'", "55P02", `parameter "server_encoding" cannot be changed`, "", ""),
						settingsReferenceError("RESET server_encoding", "55P02", `parameter "server_encoding" cannot be changed`, "", ""),
						settingsReferenceError("SET standard_conforming_strings TO 'o'", "22023", `parameter "standard_conforming_strings" requires a Boolean value`, "", ""),
						settingsReferenceError("SET standard_conforming_strings TO ' true '", "22023", `parameter "standard_conforming_strings" requires a Boolean value`, "", ""),
						settingsReferenceError("SET statement_timeout TO '-1'", "22023", `-1 ms is outside the valid range for parameter "statement_timeout" (0 ms .. 2147483647 ms)`, "", ""),
						settingsReferenceError("SET statement_timeout TO '1 MS'", "22023", `invalid value for parameter "statement_timeout": "1 MS"`, `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`, ""),
						settingsReferenceError("SET statement_timeout TO '2147483648'", "22023", `invalid value for parameter "statement_timeout": "2147483648"`, "Value exceeds integer range.", ""),
						settingsReferenceError(`SET "x..y" TO 'v'`, "42602", `invalid configuration parameter name "x..y"`, "", "Custom parameter names must be two or more simple identifiers separated by dots."),
						settingsReferenceError("SELECT set_config(NULL,'v',false)", "22004", "SET requires parameter name", "", ""),
					} {
						run(s)
						run(referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"value"}}))
					}
					unknown := settingsReferenceError("SHOW demo.placeholder", "42704", `unrecognized configuration parameter "demo.placeholder"`, "", "")
					unknown.selectRows = true
					if prepared {
						unknown.errorStage = "prepare"
					}
					run(unknown)
					run(referenceExec("RESET demo.placeholder", 0))
					run(referenceSelect("SHOW demo.placeholder", []string{"demo.placeholder"}, []string{"TEXT"}, [][]any{{""}}))
					run(referenceExec("SET demo.placeholder TO private", 0))
					unknown.prepared = prepared
					referenceRunStep(t, p.ctx, b, backend.recorder, unknown)
					run(referenceExec("RESET ALL", 0))
					run(referenceSelect("SHOW demo.placeholder", []string{"demo.placeholder"}, []string{"TEXT"}, [][]any{{""}}))
				})
			}
		})
	}
}
func TestReferenceSettingsBuiltinRegistry(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("prepared=", prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					a, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, a, backend.recorder, s) }
					// Independent REL_18_0 guc_tables.c short_desc expectations.
					expected := map[string][2]string{
						"DateStyle":                           {"ISO, MDY", "Sets the display format for date and time values."},
						"TimeZone":                            {"UTC", "Sets the time zone for displaying and interpreting time stamps."},
						"timezone_abbreviations":              {"Default", "Selects a file of time zone abbreviations."},
						"application_name":                    {"", "Sets the application name to be reported in statistics and logs."},
						"bytea_output":                        {"hex", "Sets the output format for bytea."},
						"IntervalStyle":                       {"postgres", "Sets the display format for interval values."},
						"client_min_messages":                 {"notice", "Sets the message levels that are sent to the client."},
						"default_transaction_deferrable":      {"off", "Sets the default deferrable status of new transactions."},
						"default_transaction_isolation":       {"read committed", "Sets the transaction isolation level of each new transaction."},
						"default_transaction_read_only":       {"off", "Sets the default read-only status of new transactions."},
						"lock_timeout":                        {"0", "Sets the maximum allowed duration of any wait for a lock."},
						"log_statement_stats":                 {"off", "Writes cumulative performance statistics to the server log."},
						"search_path":                         {`"$user", public`, "Sets the schema search order for names that are not schema-qualified."},
						"server_encoding":                     {"UTF8", "Shows the server (database) character set encoding."},
						"standard_conforming_strings":         {"on", "Causes '...' strings to treat backslashes literally."},
						"statement_timeout":                   {"0", "Sets the maximum allowed duration of any statement."},
						"transaction_timeout":                 {"0", "Sets the maximum allowed duration of any transaction within a session (not a prepared transaction)."},
						"idle_in_transaction_session_timeout": {"0", "Sets the maximum allowed idle time between queries, when in a transaction."},
						"idle_session_timeout":                {"0", "Sets the maximum allowed idle time between queries, when not in a transaction."},
						"transaction_deferrable":              {"off", "Whether to defer a read-only serializable transaction until it can be executed with no possible serialization failures."},
						"transaction_isolation":               {"read committed", "Sets the current transaction's isolation level."},
						"transaction_read_only":               {"off", "Sets the current transaction's read-only status."},
						"client_encoding":                     {"UTF8", "Sets the client's character set encoding."},
						"lc_messages":                         {"C", "Sets the language in which messages are displayed."},
						"lc_monetary":                         {"C", "Sets the locale for formatting monetary amounts."},
						"lc_numeric":                          {"C", "Sets the locale for formatting numbers."},
						"lc_time":                             {"C", "Sets the locale for formatting date and time values."},
						"icu_validation_level":                {"warning", "Log level for reporting invalid ICU locale strings."},
						"extra_float_digits":                  {"1", "Sets the number of digits displayed for floating-point values."},
					}
					query := "SHOW ALL"
					if backend.name == "postgres" {
						expected["TimeZone"] = [2]string{"Etc/UTC", "Sets the time zone for displaying and interpreting time stamps."}
						// Project the genuine registry, not fabricated whole-catalog parity.
						query = "SELECT name,setting,short_desc FROM pg_settings WHERE name IN ('DateStyle','TimeZone','timezone_abbreviations','application_name','bytea_output','client_min_messages','default_transaction_deferrable','default_transaction_isolation','default_transaction_read_only','IntervalStyle','lock_timeout','log_statement_stats','search_path','server_encoding','standard_conforming_strings','statement_timeout','transaction_deferrable','transaction_isolation','transaction_read_only','transaction_timeout','idle_in_transaction_session_timeout','idle_session_timeout','client_encoding','lc_messages','lc_monetary','lc_numeric','lc_time','icu_validation_level','extra_float_digits') ORDER BY lower(name)"
						run(referenceSelect("SELECT name FROM pg_settings WHERE name='demo.placeholder'", []string{"name"}, []string{"TEXT"}, nil))
					}
					var rows *sql.Rows
					if prepared {
						stmt, e := a.PrepareContext(p.ctx, query)
						if e != nil {
							t.Fatal(e)
						}
						defer stmt.Close()
						rows, err = stmt.QueryContext(p.ctx)
					} else {
						rows, err = a.QueryContext(p.ctx, query)
					}
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.Columns()
					if err != nil || len(columns) != 3 || columns[0] != "name" || columns[1] != "setting" || columns[2] != map[bool]string{false: "description", true: "short_desc"}[backend.name == "postgres"] {
						t.Fatal(columns, err)
					}
					types, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					for _, typ := range types {
						if typ.DatabaseTypeName() != "TEXT" {
							t.Fatal(typ)
						}
					}
					last := ""
					for rows.Next() {
						var name, value, description string
						if err := rows.Scan(&name, &value, &description); err != nil {
							t.Fatal(err)
						}
						want, ok := expected[name]
						if strings.ToLower(name) <= last || !ok || value != want[0] || description != want[1] {
							t.Fatal(name, value, description, want)
						}
						last = strings.ToLower(name)
						delete(expected, name)
					}
					if rows.Err() != nil || len(expected) != 0 {
						t.Fatal(rows.Err(), expected)
					}
					rows.Close()
				})
			}
		})
	}
}
