package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func settingsReferenceError(q, code, message, hint, detail string) fixture.Step {
	d := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint, Detail: detail}
	s := fixture.ErrorDiagnostic(q, d)
	s.ErrorStage = "execute"
	return s
}
func TestReferenceSettingsSQL(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("prepared=", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, a, backend.Recorder, s) }
					for _, tc := range []struct{ name, value string }{{"application_name", ""}, {"standard_conforming_strings", "on"}, {"statement_timeout", "0"}, {"server_encoding", "UTF8"}, {"log_statement_stats", "off"}} {
						run(fixture.Select("SHOW "+tc.name, []string{tc.name}, []string{"TEXT"}, [][]any{{tc.value}}))
						run(fixture.Select("SELECT current_setting('"+tc.name+"')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{tc.value}}))
					}
					run(fixture.Select("SHOW client_min_messages", []string{"client_min_messages"}, []string{"TEXT"}, [][]any{{"notice"}}))
					for _, tc := range []struct{ input, want string }{{"DEBUG", "debug2"}, {"WARNING", "warning"}, {"info", "info"}, {"notice", "notice"}} {
						run(fixture.Exec("SET client_min_messages TO '"+tc.input+"'", 0))
						run(fixture.Select("SELECT current_setting('client_min_messages')", []string{"current_setting"}, []string{"TEXT"}, [][]any{{tc.want}}))
					}
					run(settingsReferenceError("SET client_min_messages TO bogus", "22023", `invalid value for parameter "client_min_messages": "bogus"`, "Available values: debug5, debug4, debug3, debug2, debug1, log, notice, warning, error.", ""))
					run(fixture.Exec("RESET client_min_messages", 0))
					for _, tc := range []struct{ input, want string }{{"1000", "1s"}, {"1500ms", "1500ms"}, {"1.5s", "1500ms"}, {"0.5ms", "0"}, {"1min", "1min"}, {"0", "0"}} {
						run(fixture.Exec("SET statement_timeout TO '"+tc.input+"'", 0))
						run(fixture.Select("SHOW statement_timeout", []string{"statement_timeout"}, []string{"TEXT"}, [][]any{{tc.want}}))
						run(fixture.Select("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{tc.want}}, "statement_timeout", tc.input, false))
					}
					// Observe 1ms on the arming command, whose budget starts at 0.
					// Subsequent SHOW or reset SQL could legitimately time out, so
					// each route retires its physical owner without another command.
					for _, step := range []fixture.Step{
						fixture.Exec("SET statement_timeout TO '1000us'", 0),
						fixture.Select("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"1ms"}}, "statement_timeout", "1000us", false),
					} {
						func() {
							owner, err := backend.DB.Conn(p.Ctx)
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
							baseline := fixture.Select("SHOW statement_timeout", []string{"statement_timeout"}, []string{"TEXT"}, [][]any{{"0"}})
							baseline.Prepared = prepared
							fixture.RunStep(t, p.Ctx, owner, backend.Recorder, baseline)
							step.Prepared = prepared
							fixture.RunStep(t, p.Ctx, owner, backend.Recorder, step)
						}()
					}
					for _, tc := range []struct{ input, want string }{{"t", "on"}, {"TRUE", "on"}, {"yes", "on"}, {"1", "on"}, {"f", "off"}, {"NO", "off"}, {"0", "off"}, {"off", "off"}} {
						run(fixture.Exec("SET standard_conforming_strings TO '"+tc.input+"'", 0))
						run(fixture.Select("SHOW standard_conforming_strings", []string{"standard_conforming_strings"}, []string{"TEXT"}, [][]any{{tc.want}}))
					}
					run(fixture.Exec("RESET standard_conforming_strings", 0))
					run(fixture.Exec("SET application_name TO retained", 0))
					run(fixture.Select("SELECT current_setting(NULL)", []string{"current_setting"}, []string{"TEXT"}, [][]any{{nil}}))
					run(fixture.Select("SELECT set_config('application_name',NULL,NULL)", []string{"set_config"}, []string{"TEXT"}, [][]any{{""}}))
					run(fixture.Select("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"value"}}, "APPLICATION_NAME", "value", false))
					run(fixture.Exec("SELECT current_setting('application_name')", 1))
					run(fixture.Exec("SELECT set_config('application_name','value',false)", 1))
					for _, s := range []fixture.Step{
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
						run(fixture.Select("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"value"}}))
					}
					unknown := settingsReferenceError("SHOW demo.placeholder", "42704", `unrecognized configuration parameter "demo.placeholder"`, "", "")
					unknown.SelectRows = true
					if prepared {
						unknown.ErrorStage = "prepare"
					}
					run(unknown)
					run(fixture.Exec("RESET demo.placeholder", 0))
					run(fixture.Select("SHOW demo.placeholder", []string{"demo.placeholder"}, []string{"TEXT"}, [][]any{{""}}))
					run(fixture.Exec("SET demo.placeholder TO private", 0))
					unknown.Prepared = prepared
					fixture.RunStep(t, p.Ctx, b, backend.Recorder, unknown)
					run(fixture.Exec("RESET ALL", 0))
					run(fixture.Select("SHOW demo.placeholder", []string{"demo.placeholder"}, []string{"TEXT"}, [][]any{{""}}))
				})
			}
		})
	}
}
func TestReferenceSettingsBuiltinRegistry(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint("prepared=", prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					a, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer a.Close()
					b, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer b.Close()
					run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, a, backend.Recorder, s) }
					run(fixture.Exec("DO $$BEGIN END$$", 0))
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
						"max_parallel_workers_per_gather":     {"2", "Sets the maximum number of parallel processes per executor node."},
						"plpgsql.check_asserts":                {"on", "Perform checks given in ASSERT statements."},
						"quote_all_identifiers":               {"off", "When generating SQL fragments, quote all identifiers."},
						"row_security":                        {"on", "Enables row security."},
						"search_path":                         {`"$user", public`, "Sets the schema search order for names that are not schema-qualified."},
						"server_encoding":                     {"UTF8", "Shows the server (database) character set encoding."},
						"track_commit_timestamp":              {"off", "Collects transaction commit time."},
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
					if backend.Name == "postgres" {
						expected["TimeZone"] = [2]string{"Etc/UTC", "Sets the time zone for displaying and interpreting time stamps."}
						// Project the genuine registry, not fabricated whole-catalog parity.
						query = "SELECT name,setting,short_desc FROM pg_settings WHERE name IN ('DateStyle','TimeZone','timezone_abbreviations','track_commit_timestamp','application_name','bytea_output','client_min_messages','default_transaction_deferrable','default_transaction_isolation','default_transaction_read_only','IntervalStyle','lock_timeout','log_statement_stats','max_parallel_workers_per_gather','plpgsql.check_asserts','quote_all_identifiers','row_security','search_path','server_encoding','standard_conforming_strings','statement_timeout','transaction_deferrable','transaction_isolation','transaction_read_only','transaction_timeout','idle_in_transaction_session_timeout','idle_session_timeout','client_encoding','lc_messages','lc_monetary','lc_numeric','lc_time','icu_validation_level','extra_float_digits') ORDER BY lower(name)"
						run(fixture.Select("SELECT name FROM pg_settings WHERE name='demo.placeholder'", []string{"name"}, []string{"TEXT"}, nil))
					}
					var rows *sql.Rows
					if prepared {
						stmt, e := a.PrepareContext(p.Ctx, query)
						if e != nil {
							t.Fatal(e)
						}
						defer stmt.Close()
						rows, err = stmt.QueryContext(p.Ctx)
					} else {
						rows, err = a.QueryContext(p.Ctx, query)
					}
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.Columns()
					if err != nil || len(columns) != 3 || columns[0] != "name" || columns[1] != "setting" || columns[2] != map[bool]string{false: "description", true: "short_desc"}[backend.Name == "postgres"] {
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
