//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"
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
			p := newReferencePair(t)
			for _, backend := range []struct {
				name   string
				db     *sql.DB
				record *referenceRecorder
			}{{"native", p.memory, p.memoryRecord}, {"postgres", p.postgres, p.postgresRecord}} {
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
					run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, a, backend.record, s) }
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
					for _, tc := range []struct{ input, want string }{{"1000", "1s"}, {"1500ms", "1500ms"}, {"1.5s", "1500ms"}, {"1000us", "1ms"}, {"0.5ms", "0"}, {"1min", "1min"}, {"0", "0"}} {
						run(referenceExec("SET statement_timeout TO '"+tc.input+"'", 0))
						run(referenceSelect("SHOW statement_timeout", []string{"statement_timeout"}, []string{"TEXT"}, [][]any{{tc.want}}))
						run(referenceSelect("SELECT set_config($1,$2,$3)", []string{"set_config"}, []string{"TEXT"}, [][]any{{tc.want}}, "statement_timeout", tc.input, false))
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
					referenceRunStep(t, p.ctx, b, backend.record, unknown)
					run(referenceExec("RESET ALL", 0))
					run(referenceSelect("SHOW demo.placeholder", []string{"demo.placeholder"}, []string{"TEXT"}, [][]any{{""}}))
					// Independent REL_18_0 guc_tables.c short_desc expectations.
					expected := map[string][2]string{
						"DateStyle":                     {"ISO, MDY", "Sets the display format for date and time values."},
						"TimeZone":                      {"UTC", "Sets the time zone for displaying and interpreting time stamps."},
						"application_name":              {"", "Sets the application name to be reported in statistics and logs."},
						"bytea_output":                  {"hex", "Sets the output format for bytea."},
						"IntervalStyle":                 {"postgres", "Sets the display format for interval values."},
						"client_min_messages":           {"notice", "Sets the message levels that are sent to the client."},
						"default_transaction_isolation": {"read committed", "Sets the transaction isolation level of each new transaction."},
						"default_transaction_read_only": {"off", "Sets the default read-only status of new transactions."},
						"lock_timeout":                  {"0", "Sets the maximum allowed duration of any wait for a lock."},
						"log_statement_stats":           {"off", "Writes cumulative performance statistics to the server log."},
						"search_path":                   {`"$user", public`, "Sets the schema search order for names that are not schema-qualified."},
						"server_encoding":               {"UTF8", "Shows the server (database) character set encoding."},
						"standard_conforming_strings":   {"on", "Causes '...' strings to treat backslashes literally."},
						"statement_timeout":             {"0", "Sets the maximum allowed duration of any statement."},
						"transaction_isolation":         {"read committed", "Sets the current transaction's isolation level."},
						"transaction_read_only":         {"off", "Sets the current transaction's read-only status."},
					}
					query := "SHOW ALL"
					if backend.name == "postgres" {
						expected["TimeZone"] = [2]string{"Etc/UTC", "Sets the time zone for displaying and interpreting time stamps."}
						// Project the genuine registry, not fabricated whole-catalog parity.
						query = "SELECT name,setting,short_desc FROM pg_settings WHERE name IN ('DateStyle','TimeZone','application_name','bytea_output','client_min_messages','default_transaction_isolation','default_transaction_read_only','IntervalStyle','lock_timeout','log_statement_stats','search_path','server_encoding','standard_conforming_strings','statement_timeout','transaction_isolation','transaction_read_only') ORDER BY lower(name)"
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

// These are oracle-only builtins with real PostgreSQL consumers. Native custom
// settings are NOT represented as typed PostgreSQL placeholders.
func TestReferenceSettingsNumericOracle(t *testing.T) {
	p := newReferencePair(t)
	a, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, prepared := range []bool{false, true} {
		run := func(s referenceStep) {
			s.prepared = prepared
			referenceRunStep(t, p.ctx, a, p.postgresRecord, s)
			if s.state != "" {
				for _, tc := range []struct{ name, want string }{{"geqo_threshold", "9"}, {"random_page_cost", "4.94066e-324"}, {"work_mem", "30822MB"}, {"bytea_output", "hex"}, {"statement_timeout", "0"}} {
					check := referenceSelect("SHOW "+tc.name, []string{tc.name}, []string{"TEXT"}, [][]any{{tc.want}})
					check.prepared = prepared
					referenceRunStep(t, p.ctx, a, p.postgresRecord, check)
				}
			}
		}
		for _, tc := range []struct{ name, input, want string }{
			{"work_mem", "1024kB", "1MB"}, {"work_mem", "1 MB", "1MB"}, {"work_mem", "1025kB", "1025kB"}, {"work_mem", "30.1GB", "30822MB"},
			{"geqo_threshold", "2", "2"}, {"geqo_threshold", "2.5", "2"}, {"geqo_threshold", "3.5", "4"}, {"geqo_threshold", "010", "8"}, {"geqo_threshold", "0xA", "10"},
			{"geqo_threshold", "010.5", "10"},
			{"geqo_threshold", "010e1", "100"}, {"geqo_threshold", "0x1.8p1", "3"}, {"geqo_threshold", " 2.5 ", "2"}, {"geqo_threshold", "9223372036854775808e-18", "9"},
			{"random_page_cost", "010", "10"}, {"random_page_cost", "0xA", "10"}, {"random_page_cost", "08.5", "8.5"}, {"random_page_cost", "0x1p0", "1"}, {"random_page_cost", " .5", "0.5"}, {"random_page_cost", "0e-999", "0"},
			{"random_page_cost", "1.25", "1.25"}, {"random_page_cost", "2.1234567", "2.12346"}, {"random_page_cost", "1e-3", "0.001"},
			{"random_page_cost", "0x1p-1074", "4.94066e-324"},
			{"vacuum_cost_delay", "1500us", "1500us"}, {"vacuum_cost_delay", "0.0001", "0.1us"}, {"vacuum_cost_delay", "0.0001ms", "0"},
			{"bytea_output", "HEX", "hex"}, {"vacuum_buffer_usage_limit", "0", "0"},
		} {
			run(referenceExec("SET "+tc.name+" TO '"+tc.input+"'", 0))
			run(referenceSelect("SHOW "+tc.name, []string{tc.name}, []string{"TEXT"}, [][]any{{tc.want}}))
		}
		for _, s := range []referenceStep{settingsReferenceError("SET work_mem TO '1 mb'", "22023", `invalid value for parameter "work_mem": "1 mb"`, `Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".`, ""), settingsReferenceError("SET work_mem TO '1kB'", "22023", `1 kB is outside the valid range for parameter "work_mem" (64 kB .. 2147483647 kB)`, "", ""), settingsReferenceError("SET bytea_output TO 'bad'", "22023", `invalid value for parameter "bytea_output": "bad"`, "Available values: escape, hex.", "")} {
			run(s)
		}
		run(settingsReferenceError("SET geqo_threshold TO '1'", "22023", `1 is outside the valid range for parameter "geqo_threshold" (2 .. 2147483647)`, "", ""))
		run(settingsReferenceError("SET geqo_threshold TO '1kB'", "22023", `invalid value for parameter "geqo_threshold": "1kB"`, "", ""))
		run(settingsReferenceError("SET random_page_cost TO 'NaN'", "22023", `invalid value for parameter "random_page_cost": "NaN"`, "", ""))
		for _, input := range []string{"08.5", "0x1p0"} {
			run(settingsReferenceError("SET geqo_threshold TO '"+input+"'", "22023", `invalid value for parameter "geqo_threshold": "`+input+`"`, "", ""))
		}
		run(settingsReferenceError("SET random_page_cost TO '0x1.1p-1074'", "22023", `invalid value for parameter "random_page_cost": "0x1.1p-1074"`, "", ""))
		run(settingsReferenceError("SET random_page_cost TO '1e-320'", "22023", `invalid value for parameter "random_page_cost": "1e-320"`, "", ""))
		run(settingsReferenceError("SET random_page_cost TO 'Infinity'", "22023", `Infinity is outside the valid range for parameter "random_page_cost" (0 .. 1.79769e+308)`, "", ""))
		for _, input := range []string{"+.5", "-.5", " .5", "0b10", "0o10", "1_000", "1e", "0x", "0x1p-1074", "1e999", "Infinity", "-inf"} {
			run(settingsReferenceError("SET geqo_threshold TO '"+input+"'", "22023", `invalid value for parameter "geqo_threshold": "`+input+`"`, "", ""))
		}
		run(settingsReferenceError("SET geqo_threshold TO '.5'", "22023", `0 is outside the valid range for parameter "geqo_threshold" (2 .. 2147483647)`, "", ""))
		for _, input := range []string{"0b10", "0o10", "1_000", "1e", "0x", "1e-999", "1e999"} {
			run(settingsReferenceError("SET random_page_cost TO '"+input+"'", "22023", `invalid value for parameter "random_page_cost": "`+input+`"`, "", ""))
		}
		run(settingsReferenceError("SET random_page_cost TO '-inf'", "22023", `-Infinity is outside the valid range for parameter "random_page_cost" (0 .. 1.79769e+308)`, "", ""))
		for _, input := range []string{"08.5", "0x1p0"} {
			run(settingsReferenceError("SET statement_timeout TO '"+input+"'", "22023", `invalid value for parameter "statement_timeout": "`+input+`"`, `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`, ""))
		}
		for _, name := range []string{"work_mem", "geqo_threshold", "random_page_cost", "vacuum_cost_delay", "bytea_output", "vacuum_buffer_usage_limit"} {
			run(referenceExec("RESET "+name, 0))
		}
	}
}

func TestReferenceSettingsExactDecimalSubnormal(t *testing.T) {
	p := newReferencePair(t)
	c, err := p.postgres.Conn(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Independent integer identity: 5^1023 / 10^1023 = 2^-1023.
	digits := new(big.Int).Exp(big.NewInt(5), big.NewInt(1023), nil)
	exact := digits.String() + "e-1023"
	inexact := new(big.Int).Add(digits, big.NewInt(1)).String() + "e-1023"
	for _, prepared := range []bool{false, true} {
		run := func(s referenceStep) { s.prepared = prepared; referenceRunStep(t, p.ctx, c, p.postgresRecord, s) }
		run(referenceExec("SET random_page_cost TO '"+exact+"'", 0))
		run(referenceSelect("SHOW random_page_cost", []string{"random_page_cost"}, []string{"TEXT"}, [][]any{{"1.11254e-308"}}))
		run(referenceSelect("SELECT set_config($1,$2,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"1.11254e-308"}}, "random_page_cost", exact))
		for _, input := range []string{inexact, "1e-320", "1e-999"} {
			run(settingsReferenceError("SET random_page_cost TO '"+input+"'", "22023", `invalid value for parameter "random_page_cost": "`+input+`"`, "", ""))
			run(referenceSelect("SHOW random_page_cost", []string{"random_page_cost"}, []string{"TEXT"}, [][]any{{"1.11254e-308"}}))
		}
		run(referenceExec("RESET random_page_cost", 0))
	}
}
func TestReferenceSettingsPrivilegeAndTimeoutOracle(t *testing.T) {
	p := newReferencePair(t)
	ctx, cancel := context.WithTimeout(p.ctx, 10*time.Second)
	defer cancel()
	if _, err := referenceAdmin.ExecContext(ctx, "CREATE ROLE settings_oracle_reader LOGIN PASSWORD 'settings_oracle_reader'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := referenceAdmin.ExecContext(context.Background(), "DROP ROLE settings_oracle_reader"); err != nil {
			t.Error(err)
		}
	})
	u, err := url.Parse(referenceDSN)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword("settings_oracle_reader", "settings_oracle_reader")
	reader, err := sql.Open("stuntgres_reference_postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	a, err := reader.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, prepared := range []bool{false, true} {
		for _, q := range []string{"SET log_statement_stats TO garbage", "RESET log_statement_stats", "SET log_statement_stats TO DEFAULT"} {
			s := settingsReferenceError(q, "42501", `permission denied to set parameter "log_statement_stats"`, "", "")
			s.prepared = prepared
			referenceRunStep(t, ctx, a, p.postgresRecord, s)
		}
	}
	c, err := p.postgres.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, prepared := range []bool{false, true} {
		var sleep *sql.Stmt
		recorded := context.WithValue(ctx, referenceRecorderKey{}, p.postgresRecord)
		if prepared {
			sleep, err = c.PrepareContext(recorded, "SELECT pg_sleep(1)")
			if err != nil {
				t.Fatal("prepare at timeout0", err)
			}
		}
		referenceRunStep(t, ctx, c, p.postgresRecord, referenceExec("SET statement_timeout TO '20ms'", 0))
		step := settingsReferenceError("SELECT pg_sleep(1)", "57014", "canceling statement due to statement timeout", "", "")
		step.selectRows = true
		if prepared {
			rows, err := sleep.QueryContext(recorded)
			if rows != nil {
				rows.Close()
			}
			sleep.Close()
			referenceAssertDiagnostic(t, err, step.diagnostic)
			t.Log("prepared pg_sleep: execute57014")
		} else {
			referenceRunStep(t, ctx, c, p.postgresRecord, step)
		}
		referenceRunStep(t, ctx, c, p.postgresRecord, referenceSelect("SHOW statement_timeout", []string{"statement_timeout"}, []string{"TEXT"}, [][]any{{"20ms"}}))
		referenceRunStep(t, ctx, c, p.postgresRecord, referenceExec("RESET statement_timeout", 0))
	}
}
func TestReferenceSettingsParserMode(t *testing.T) {
	p := newReferencePair(t)
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		a, err := db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		b, err := db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.ExecContext(p.ctx, "CREATE TABLE settings_literals(id INT,v TEXT)"); err != nil {
			t.Fatal(err)
		}
		if _, err = a.ExecContext(p.ctx, "SET standard_conforming_strings TO off"); err != nil {
			t.Fatal(err)
		}
		off, err := a.PrepareContext(p.ctx, `INSERT INTO settings_literals VALUES(1,'a\nb')`)
		if err != nil {
			t.Fatal(err)
		}
		on, err := b.PrepareContext(p.ctx, `INSERT INTO settings_literals VALUES(2,'a\nb')`)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = a.ExecContext(p.ctx, "SET standard_conforming_strings TO on"); err != nil {
			t.Fatal(err)
		}
		if _, err = b.ExecContext(p.ctx, "SET standard_conforming_strings TO off"); err != nil {
			t.Fatal(err)
		}
		if _, err = off.ExecContext(p.ctx); err != nil {
			t.Fatal(err)
		}
		if _, err = on.ExecContext(p.ctx); err != nil {
			t.Fatal(err)
		}
		off.Close()
		on.Close()
		if _, err = a.ExecContext(p.ctx, `INSERT INTO settings_literals VALUES(3,E'a\nb')`); err != nil {
			t.Fatal(err)
		}
		record := p.memoryRecord
		if db == p.postgres {
			record = p.postgresRecord
		}
		referenceRunStep(t, p.ctx, a, record, referenceSelect("SELECT v FROM settings_literals ORDER BY id", []string{"v"}, []string{"TEXT"}, [][]any{{"a\nb"}, {`a\nb`}, {"a\nb"}}))
		a.Close()
		b.Close()
	}
}

func TestReferenceSettingsBooleanBindingDiagnostics(t *testing.T) {
	p := newReferencePair(t)
	for _, prepared := range []bool{false, true} {
		native := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: `invalid input syntax for type boolean: "maybe"`}
		pg := native
		pg.Context = "unnamed portal parameter $3 = '...'"
		s := referenceErrorDiagnostic("SELECT set_config($1,$2,$3)", native, "application_name", "changed", "maybe")
		s.postgresDiagnostic = &pg
		s.selectRows = true
		s.prepared = prepared
		s.errorStage = "execute"
		p.step(t, s)
		p.step(t, referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{""}}))
		p.step(t, referenceSelect("SELECT set_config('application_name','healthy',false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"healthy"}}))
		p.step(t, referenceSelect("SELECT set_config('application_name',NULL,NULL)", []string{"set_config"}, []string{"TEXT"}, [][]any{{""}}))
		for _, tc := range []struct{ query, code, message string }{
			{`SELECT set_config('application_name','v','maybe')`, "22P02", `invalid input syntax for type boolean: "maybe"`},
			{`SELECT set_config('application_name','a',)`, "42601", `syntax error at or near ")"`},
		} {
			s := referenceErrorDiagnostic(tc.query, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: "42"})
			s.prepared = prepared
			s.selectRows = true
			s.errorStage = "execute"
			if prepared {
				s.errorStage = "prepare"
			}
			p.step(t, s)
		}
	}
}

func TestReferenceSettingsUnicodeOffAndCapture(t *testing.T) {
	p := newReferencePair(t)
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		c, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		recorded := context.WithValue(p.ctx, referenceRecorderKey{}, backend.record)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.record, s) }
		run(referenceExec("CREATE TABLE unicode_capture(id INT,v TEXT)", 0))
		queries := []string{`SET application_name TO U&'\0061'`, `SELECT set_config('application_name',U&'\0061',false)`, `INSERT INTO unicode_capture VALUES(1,U&'\0061')`}
		var captured []*sql.Stmt
		for _, q := range queries {
			s, e := c.PrepareContext(recorded, q)
			if e != nil {
				t.Fatal(e)
			}
			captured = append(captured, s)
		}
		run(referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{""}}))
		run(referenceExec("SET standard_conforming_strings TO off", 0))
		for _, tc := range []struct{ query, position string }{
			{queries[0], "25"}, {`SELECT current_setting(U&'application_name')`, "24"},
			{`SET application_name TO U&'\ZZZZ' UESCAPE '0'`, "25"}, {`SET application_name TO U&'unterminated`, "25"},
			{`/*é*/ SET application_name TO U&'\0061'`, "31"}, {`INSERT INTO unicode_capture VALUES(2,U&'\0061')`, "38"},
		} {
			for _, prepared := range []bool{false, true} {
				s := referenceErrorDiagnostic(tc.query, referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unsafe use of string constant with Unicode escapes", Detail: `String constants with Unicode escapes cannot be used when "standard_conforming_strings" is off.`, Position: tc.position})
				s.prepared = prepared
				s.errorStage = "execute"
				if prepared {
					s.errorStage = "prepare"
				}
				run(s)
				run(referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{""}}))
			}
		}
		run(referenceExec(`SET U&"application_name" TO 'x'`, 0))
		run(referenceExec(`SET application_name TO E'a\nb'`, 0))
		run(referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{`a\x0ab`}}))
		run(referenceExec(`SET application_name TO $$a\nb$$`, 0))
		run(referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{`a\nb`}}))
		for i, s := range captured {
			r, e := s.ExecContext(recorded)
			s.Close()
			if e != nil {
				t.Fatal(e)
			}
			n, e := r.RowsAffected()
			want := int64(0)
			if i > 0 {
				want = 1
			}
			if e != nil || n != want {
				t.Fatal(n, e)
			}
		}
		run(referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"a"}}))
		run(referenceSelect("SELECT v FROM unicode_capture", []string{"v"}, []string{"TEXT"}, [][]any{{"a"}}))
		c.Close()
	}
}

func TestReferenceSettingsSetConfigNotices(t *testing.T) {
	p := newReferencePair(t)
	for _, postgres := range []bool{false, true} {
		t.Run(fmt.Sprint(postgres), func(t *testing.T) {
			var notices []referenceDiagnostic
			_, c, record := noticeReferencePool(t, p, postgres, &notices)
			original := strings.Repeat("a", 64)
			clipped := strings.Repeat("a", 63)
			want := referenceDiagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
			for _, prepared := range []bool{false, true} {
				for _, threshold := range []string{"notice", "warning", "error"} {
					referenceRunStep(t, p.ctx, c, record, referenceExec("SET client_min_messages TO "+threshold, 0))
					before := len(notices)
					s := referenceSelect("SELECT set_config($1,$2,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{clipped}}, "application_name", original)
					s.prepared = prepared
					referenceRunStep(t, p.ctx, c, record, s)
					got := referenceNoticeWitnesses(t, postgres, notices[before:], want)
					count := 0
					if threshold == "notice" {
						count = 1
					}
					if len(got) != count {
						t.Fatal(notices[before:])
					}
					referenceRunStep(t, p.ctx, c, record, referenceSelect("SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{clipped}}))
				}
				referenceRunStep(t, p.ctx, c, record, referenceExec("RESET client_min_messages", 0))
				before := len(notices)
				s := referenceSelect("SELECT set_config('application_name',NULL,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{""}})
				s.prepared = prepared
				referenceRunStep(t, p.ctx, c, record, s)
				if len(notices) != before {
					t.Fatal("reset emitted notice", notices[before:])
				}
			}
		})
	}
}
