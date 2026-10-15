package reference_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"math"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSettingsPublicTypedSQL(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SHOW standard_conforming_strings", []string{"standard_conforming_strings"}, []string{"TEXT"}, [][]any{{"on"}}))
	}
	if p.Postgres != nil {
		settingsPostgresNumericConsumers(t, p)
	}
	// Native host registration/control phase; PostgreSQL has no RegisterSetting API.
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			db := sessionPool(t, settingsTestDriver(t), "typed")
			a, err := db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := db.Conn(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			for _, tc := range []struct{ name, input, want string }{
				{"demo.flag", "yes", "on"},
				{"demo.flag", "TRUE", "on"}, {"demo.flag", "f", "off"}, {"demo.text", "é a b", "é a b"}, {"demo.text", "DEFAULT", "DEFAULT"}, {"demo.text", "", ""},
				{"demo.count", "2.5", "2"}, {"demo.count", "3.5", "4"}, {"demo.count", "010", "8"}, {"demo.count", "0xA", "10"}, {"demo.count", "-10", "-10"},
				{"demo.count", "010.5", "10"}, {"demo.ratio", "0x1p-1074", "4.94066e-324"},
				{"demo.ratio", "1e-3", "0.001"}, {"demo.ratio", "2.1234567", "2.12346"}, {"demo.mode", "FAST", "fast"},
				{"demo.memory", "1 MB", "1MB"}, {"demo.memory", "1025kB", "1025kB"}, {"demo.memory", "30.1GB", "30822MB"}, {"demo.memory", "0.5kB", "0"},
				{"demo.bytes", "0.5kB", "512B"}, {"demo.duration", "1.5s", "1500ms"}, {"demo.duration", "1000us", "1ms"}, {"demo.duration", "0.5ms", "0"}, {"demo.duration", "1min", "1min"},
				{"demo.real_time", "1500ms", "1500ms"}, {"demo.real_time", "0.001s", "1ms"},
			} {
				sessionSet(t, a, "SET "+strings.ToUpper(tc.name)+" TO '"+tc.input+"'", prepared)
				settingsRows(t, a, "SHOW "+tc.name, prepared, tc.name, tc.want)
				settingsRows(t, a, "SELECT current_setting($1)", prepared, "current_setting", tc.want, strings.ToUpper(tc.name))
				settingsRows(t, a, "SELECT set_config($1,$2,$3)", prepared, "set_config", tc.want, tc.name, []byte(tc.input), false)
			}
			settingsRows(t, b, "SHOW demo.text", prepared, "demo.text", "seed")
			sessionSet(t, a, "RESET ALL", prepared)
			defaults := map[string]string{"demo.memory": "1MB", "demo.duration": "1500ms", "demo.real_time": "1s"}
			for _, v := range settingsDefinitionsForTest() {
				want := v.Default
				if n, ok := defaults[v.Name]; ok {
					want = n
				}
				settingsRows(t, a, "SHOW "+v.Name, prepared, v.Name, want)
				if v.Access == stuntgres.SettingSession {
					sessionSet(t, a, "SET "+v.Name+" TO DEFAULT", prepared)
					sessionSet(t, a, "RESET "+v.Name, prepared)
					settingsRows(t, a, "SELECT set_config($1,NULL,NULL)", prepared, "set_config", want, v.Name)
				}
			}
			settingsRows(t, a, "SELECT current_setting(NULL)", prepared, "current_setting", nil)
			settingsRows(t, a, "SELECT set_config('demo.text','shared',false)", prepared, "set_config", "shared")
			result, err := a.ExecContext(context.Background(), "SELECT current_setting('demo.text')")
			if err != nil {
				t.Fatal(err)
			}
			count, err := result.RowsAffected()
			if err != nil || count != 1 {
				t.Fatal(count, err)
			}
			if prepared {
				statement, prepareErr := a.PrepareContext(context.Background(), "SELECT current_setting('demo.text')")
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				result, err = statement.ExecContext(context.Background())
				statement.Close()
				if err != nil {
					t.Fatal(err)
				}
				count, err = result.RowsAffected()
				if err != nil || count != 1 {
					t.Fatal(count, err)
				}
			}
			settingsRows(t, a, "SELECT set_config($1,$1,false)", prepared, "set_config", "demo.text", "demo.text")
		})
	}
}

func TestReferenceSettingsErrorsAndAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SHOW standard_conforming_strings", []string{"standard_conforming_strings"}, []string{"TEXT"}, [][]any{{"on"}}))
	}
	// Native host registration/control phase; PostgreSQL has no RegisterSetting API.
	for _, prepared := range []bool{false, true} {
		db := sessionPool(t, settingsTestDriver(t), "errors")
		a, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		sessionSet(t, a, "SET demo.text TO retained", prepared)
		for _, tc := range []struct{ query, code, message, hint, detail string }{
			{"SET standard_conforming_strings TO 'o'", "22023", `parameter "standard_conforming_strings" requires a Boolean value`, "", ""},
			{"SET standard_conforming_strings TO ' true '", "22023", `parameter "standard_conforming_strings" requires a Boolean value`, "", ""},
			{"SET statement_timeout TO '-1'", "22023", `-1 ms is outside the valid range for parameter "statement_timeout" (0 ms .. 2147483647 ms)`, "", ""},
			{"SET demo.duration TO '1 MS'", "22023", `invalid value for parameter "demo.duration": "1 MS"`, `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`, ""},
			{"SET statement_timeout TO '1 MS'", "22023", `invalid value for parameter "statement_timeout": "1 MS"`, `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`, ""},
			{"SET statement_timeout TO '2147483648'", "22023", `invalid value for parameter "statement_timeout": "2147483648"`, "Value exceeds integer range.", ""},
			{"SET demo.count TO '11'", "22023", `11 is outside the valid range for parameter "demo.count" (-10 .. 10)`, "", ""},
			{"SET demo.flag TO 'o'", "22023", `parameter "demo.flag" requires a Boolean value`, "", ""},
			{"SET demo.count TO '08.5'", "22023", `invalid value for parameter "demo.count": "08.5"`, "", ""},
			{"SET demo.count TO '0x1p0'", "22023", `invalid value for parameter "demo.count": "0x1p0"`, "", ""},
			{"SET demo.count TO '1_000'", "22023", `invalid value for parameter "demo.count": "1_000"`, "", ""},
			{"SET demo.count TO 'NaN'", "22023", `invalid value for parameter "demo.count": "NaN"`, "", ""},
			{"SET demo.ratio TO 'NaN'", "22023", `invalid value for parameter "demo.ratio": "NaN"`, "", ""},
			{"SET demo.ratio TO '1e999'", "22023", `invalid value for parameter "demo.ratio": "1e999"`, "", ""},
			{"SET demo.ratio TO '1e-320'", "22023", `invalid value for parameter "demo.ratio": "1e-320"`, "", ""},
			{"SET demo.ratio TO '0x1.1p-1074'", "22023", `invalid value for parameter "demo.ratio": "0x1.1p-1074"`, "", ""},
			{"SET demo.ratio TO 'Infinity'", "22023", `Infinity is outside the valid range for parameter "demo.ratio" (0 .. 10)`, "", ""},
			{"SET demo.mode TO bad", "22023", `invalid value for parameter "demo.mode": "bad"`, "Available values: safe, fast.", ""},
			{"SET demo.memory TO '1 mb'", "22023", `invalid value for parameter "demo.memory": "1 mb"`, `Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".`, ""},
			{"SET demo.ratio TO '11'", "22023", `11 is outside the valid range for parameter "demo.ratio" (0 .. 10)`, "", ""},
			{"SET demo.real_time TO '11s'", "22023", `11 s is outside the valid range for parameter "demo.real_time" (0 s .. 10 s)`, "", ""},
			{`SET "x..y" TO 'v'`, "42602", `invalid configuration parameter name "x..y"`, "", "Custom parameter names must be two or more simple identifiers separated by dots."},
			{"SET absent_setting TO a,b", "22023", "SET absent_setting takes only one argument", "", ""},
			{"SELECT set_config(NULL,'x',false)", "22004", "SET requires parameter name", "", ""},
		} {
			if prepared {
				s, e := a.PrepareContext(context.Background(), tc.query)
				if e != nil {
					t.Fatal("unexpected prepare", tc.query, e)
				}
				_, err = s.ExecContext(context.Background())
				s.Close()
			} else {
				_, err = a.ExecContext(context.Background(), tc.query)
			}
			settingsDiagnostic(t, err, tc.code, tc.message, tc.hint, tc.detail, "")
			settingsRows(t, a, "SHOW demo.text", prepared, "demo.text", "retained")
			for _, prior := range []struct{ name, value string }{{"standard_conforming_strings", "on"}, {"statement_timeout", "0"}, {"demo.count", "2"}, {"demo.flag", "off"}, {"demo.ratio", "1"}, {"demo.mode", "safe"}, {"demo.memory", "1MB"}, {"demo.real_time", "1s"}} {
				if strings.HasPrefix(tc.query, "SET "+prior.name+" ") {
					settingsRows(t, a, "SHOW "+prior.name, prepared, prior.name, prior.value)
				}
			}
		}
		for _, tc := range []struct{ name, input, message, hint, want string }{
			{"demo.memory", "1 mb", `invalid value for parameter "demo.memory": "1 mb"`, `Valid units for this parameter are "B", "kB", "MB", "GB", and "TB".`, "1MB"},
			{"demo.duration", "1 MS", `invalid value for parameter "demo.duration": "1 MS"`, `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`, "1500ms"},
			{"statement_timeout", "08.5", `invalid value for parameter "statement_timeout": "08.5"`, `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`, "0"},
			{"statement_timeout", "0x1p0", `invalid value for parameter "statement_timeout": "0x1p0"`, `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`, "0"},
			{"statement_timeout", "2147483648", `invalid value for parameter "statement_timeout": "2147483648"`, "Value exceeds integer range.", "0"},
			{"demo.real_time", "11s", `11 s is outside the valid range for parameter "demo.real_time" (0 s .. 10 s)`, "", "1s"},
			{"demo.mode", "bad", `invalid value for parameter "demo.mode": "bad"`, "Available values: safe, fast.", "safe"},
		} {
			const query = "SELECT set_config($1,$2,false)"
			if prepared {
				statement, prepareErr := a.PrepareContext(context.Background(), query)
				if prepareErr != nil {
					t.Fatal(prepareErr)
				}
				_, err = statement.ExecContext(context.Background(), tc.name, tc.input)
				statement.Close()
			} else {
				_, err = a.ExecContext(context.Background(), query, tc.name, tc.input)
			}
			settingsDiagnostic(t, err, "22023", tc.message, tc.hint, "", "")
			settingsRows(t, a, "SHOW "+tc.name, prepared, tc.name, tc.want)
		}
		for _, name := range []string{"server_encoding", "demo.fixed", "log_statement_stats", "demo.priv"} {
			code := "55P02"
			message := `parameter "` + name + `" cannot be changed`
			if name == "log_statement_stats" || name == "demo.priv" {
				code = "42501"
				message = `permission denied to set parameter "` + name + `"`
			}
			for _, q := range []string{"SET " + name + " TO garbage", "SET " + name + " TO DEFAULT", "RESET " + name, "SELECT set_config('" + name + "','garbage',false)"} {
				var e error
				if prepared {
					s, err := a.PrepareContext(context.Background(), q)
					if err != nil {
						t.Fatal(err)
					}
					_, e = s.ExecContext(context.Background())
					s.Close()
				} else {
					_, e = a.ExecContext(context.Background(), q)
				}
				settingsDiagnostic(t, e, code, message, "", "", "")
			}
		}
		for _, tc := range []struct{ q, code, message string }{{"SELECT current_setting($2)", "42P18", "could not determine data type of parameter $1"}, {"SELECT set_config($1,'v',$1)", "0A000", "parameter reused with different destination types"}} {
			_, err := a.PrepareContext(context.Background(), tc.q)
			settingsDiagnostic(t, err, tc.code, tc.message, "", "", "")
		}
	}
}

func TestReferenceSettingsRegistryPublication(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SHOW standard_conforming_strings", []string{"standard_conforming_strings"}, []string{"TEXT"}, [][]any{{"on"}}))
	}
	// Native host registration/control phase; PostgreSQL has no RegisterSetting API.
	var d stuntgres.Driver
	def := stuntgres.SettingDefinition{Name: "demo.mode", Type: stuntgres.SettingEnum, Default: "safe", EnumValues: []string{"safe", "fast"}, Description: "original"}
	if err := d.RegisterSetting(def); err != nil {
		t.Fatal(err)
	}
	def.EnumValues[0] = "changed"
	def.Description = "changed"
	if err := d.RegisterSetting(stuntgres.SettingDefinition{Name: "DEMO.MODE", Type: stuntgres.SettingString}); err == nil {
		t.Fatal("duplicate accepted")
	}
	for _, bad := range []stuntgres.SettingDefinition{{Name: "plain", Type: stuntgres.SettingString}, {Name: "demo.bad", Type: stuntgres.SettingType(99)}, {Name: "demo.bad", Type: stuntgres.SettingInteger, IntegerMin: 1, IntegerMax: 0}, {Name: "demo.bad", Type: stuntgres.SettingBoolean, Default: "o"}, {Name: "demo.bad", Type: stuntgres.SettingString, Unit: stuntgres.SettingBytes}, {Name: "demo.bad", Type: stuntgres.SettingEnum, EnumValues: []string{"a", "A"}}, {Name: "demo.bad", Type: stuntgres.SettingFloating, FloatMax: math.Inf(1)}} {
		if err := d.RegisterSetting(bad); err == nil {
			t.Fatal("invalid accepted", bad)
		}
	}
	db := sessionPool(t, &d, "first")
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	settingsRows(t, c, "SHOW demo.mode", false, "demo.mode", "safe")
	if err := d.RegisterSetting(stuntgres.SettingDefinition{Name: "demo.late", Type: stuntgres.SettingString}); err == nil {
		t.Fatal("late registration")
	}
	db2 := sessionPool(t, &d, "second")
	b, err := db2.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	settingsRows(t, b, "SHOW demo.mode", false, "demo.mode", "safe")
	other := sessionPool(t, stuntgres.NewDriver(), "first")
	_, err = other.Exec("SHOW demo.mode")
	settingsDiagnostic(t, err, "42704", `unrecognized configuration parameter "demo.mode"`, "", "", "")
	rows, err := c.QueryContext(context.Background(), "SHOW ALL")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, value, description string
		if err = rows.Scan(&name, &value, &description); err != nil {
			t.Fatal(err)
		}
		if name == "demo.mode" && (value != "safe" || description != "original") {
			t.Fatal(name, value, description)
		}
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = d.RegisterSetting(stuntgres.SettingDefinition{Name: "demo.after_close", Type: stuntgres.SettingString}); err == nil {
		t.Fatal("close unfroze registry")
	}
}

func TestReferenceSettingsShowAllPlaceholdersAndLifecycle(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Select("SHOW standard_conforming_strings", []string{"standard_conforming_strings"}, []string{"TEXT"}, [][]any{{"on"}}))
	}
	// Native host registration/control phase; PostgreSQL has no RegisterSetting API.
	ctx := context.Background()
	d := settingsTestDriver(t)
	for _, definition := range []stuntgres.SettingDefinition{
		{Name: "edge.count", Type: stuntgres.SettingInteger, Default: "0", IntegerMin: -200, IntegerMax: 200, Description: "numeric prefix"},
		{Name: "edge.real", Type: stuntgres.SettingFloating, Default: "0", FloatMin: -200, FloatMax: 200, Description: "real prefix"},
	} {
		if err := d.RegisterSetting(definition); err != nil {
			t.Fatal(err)
		}
	}
	db := sessionPool(t, d, "lifecycle")
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = a.Close() }()
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	_, err = a.ExecContext(ctx, "SHOW demo.placeholder")
	settingsDiagnostic(t, err, "42704", `unrecognized configuration parameter "demo.placeholder"`, "", "", "")
	sessionSet(t, a, "RESET demo.placeholder", false)
	settingsRows(t, a, "SHOW demo.placeholder", false, "demo.placeholder", "")
	sessionSet(t, a, "SET demo.placeholder TO local", true)
	_, err = b.ExecContext(ctx, "SHOW demo.placeholder")
	settingsDiagnostic(t, err, "42704", `unrecognized configuration parameter "demo.placeholder"`, "", "", "")
	rows, err := a.QueryContext(ctx, "SHOW ALL")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	types, _ := rows.ColumnTypes()
	if !reflect.DeepEqual(cols, []string{"name", "setting", "description"}) {
		t.Fatal(cols)
	}
	for _, v := range types {
		n, ok := v.Nullable()
		if v.DatabaseTypeName() != "TEXT" || v.ScanType() != reflect.TypeOf("") || !ok || n {
			t.Fatal(v)
		}
	}
	expected := map[string][2]string{
		// PostgreSQL REL_18_0 guc_tables.c short_desc, independent of the registry.
		"DateStyle":                           {"ISO, MDY", "Sets the display format for date and time values."},
		"TimeZone":                            {"UTC", "Sets the time zone for displaying and interpreting time stamps."},
		"timezone_abbreviations":              {"Default", "Selects a file of time zone abbreviations."},
		"application_name":                    {"", "Sets the application name to be reported in statistics and logs."},
		"client_min_messages":                 {"notice", "Sets the message levels that are sent to the client."},
		"bytea_output":                        {"hex", "Sets the output format for bytea."},
		"IntervalStyle":                       {"postgres", "Sets the display format for interval values."},
		"log_statement_stats":                 {"off", "Writes cumulative performance statistics to the server log."},
		"max_parallel_workers_per_gather":     {"2", "Sets the maximum number of parallel processes per executor node."},
		"quote_all_identifiers":               {"off", "When generating SQL fragments, quote all identifiers."},
		"server_encoding":                     {"UTF8", "Shows the server (database) character set encoding."},
		"track_commit_timestamp":              {"off", "Collects transaction commit time."},
		"standard_conforming_strings":         {"on", "Causes '...' strings to treat backslashes literally."},
		"statement_timeout":                   {"0", "Sets the maximum allowed duration of any statement."},
		"lock_timeout":                        {"0", "Sets the maximum allowed duration of any wait for a lock."},
		"transaction_timeout":                 {"0", "Sets the maximum allowed duration of any transaction within a session (not a prepared transaction)."},
		"idle_in_transaction_session_timeout": {"0", "Sets the maximum allowed idle time between queries, when in a transaction."},
		"idle_session_timeout":                {"0", "Sets the maximum allowed idle time between queries, when not in a transaction."},
		"search_path":                         {`"$user", public`, "Sets the schema search order for names that are not schema-qualified."},
		"default_transaction_deferrable":      {"off", "Sets the default deferrable status of new transactions."},
		"default_transaction_isolation":       {"read committed", "Sets the transaction isolation level of each new transaction."},
		"default_transaction_read_only":       {"off", "Sets the default read-only status of new transactions."},
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
	for _, v := range settingsDefinitionsForTest() {
		value := v.Default
		switch v.Name {
		case "demo.memory":
			value = "1MB"
		case "demo.duration":
			value = "1500ms"
		case "demo.real_time":
			value = "1s"
		}
		expected[v.Name] = [2]string{value, v.Description}
	}
	expected["edge.count"] = [2]string{"0", "numeric prefix"}
	expected["edge.real"] = [2]string{"0", "real prefix"}
	last := ""
	for rows.Next() {
		var n, v, desc string
		if err = rows.Scan(&n, &v, &desc); err != nil {
			t.Fatal(err)
		}
		want, ok := expected[n]
		if strings.ToLower(n) <= last || !ok || want[0] != v || want[1] != desc {
			t.Fatal(n, v, desc, want)
		}
		last = strings.ToLower(n)
		delete(expected, n)
	}
	if rows.Err() != nil || len(expected) != 0 {
		t.Fatal(rows.Err(), expected)
	}
	rows.Close()
	sessionSet(t, a, "RESET ALL", true)
	settingsRows(t, a, "SHOW demo.placeholder", false, "demo.placeholder", "")
	sessionSet(t, a, "SET demo.text TO retained", false)
	before := settingsPhysical(t, a)
	a.Close()
	a, err = db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settingsPhysical(t, a) != before {
		t.Fatal("not reused")
	}
	settingsRows(t, a, "SHOW demo.text", false, "demo.text", "retained")
	if _, err = a.ExecContext(ctx, "CREATE TABLE sentinel(v INT)"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ExecContext(ctx, "INSERT INTO sentinel VALUES(7)"); err != nil {
		t.Fatal(err)
	}
	if err = a.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	a.Close()
	a, err = db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if settingsPhysical(t, a) == before {
		t.Fatal("not replaced")
	}
	settingsRows(t, a, "SHOW demo.text", false, "demo.text", "seed")
	settingsRows(t, b, "SHOW demo.text", false, "demo.text", "seed")
	_, err = a.ExecContext(ctx, "SHOW demo.placeholder")
	settingsDiagnostic(t, err, "42704", `unrecognized configuration parameter "demo.placeholder"`, "", "", "")
	var v int
	if err = a.QueryRowContext(ctx, "SELECT v FROM sentinel").Scan(&v); err != nil || v != 7 {
		t.Fatal(v, err)
	}
}
