package stuntgres

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"math"
	"math/big"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func settingsDefinitionsForTest() []SettingDefinition {
	return []SettingDefinition{
		{Name: "demo.flag", Type: SettingBoolean, Default: "off", Description: "flag"},
		{Name: "demo.text", Type: SettingString, Default: "seed", Description: "text"},
		{Name: "demo.count", Type: SettingInteger, Default: "2", IntegerMin: -10, IntegerMax: 10, Description: "count"},
		{Name: "demo.ratio", Type: SettingFloating, Default: "1", FloatMax: 10, Description: "ratio"},
		{Name: "demo.mode", Type: SettingEnum, Default: "safe", EnumValues: []string{"safe", "fast"}, Description: "mode"},
		{Name: "demo.memory", Type: SettingInteger, Unit: SettingKilobytes, Default: "1024", IntegerMax: math.MaxInt32, Description: "memory"},
		{Name: "demo.bytes", Type: SettingInteger, Unit: SettingBytes, Default: "0", IntegerMax: math.MaxInt32, Description: "bytes"},
		{Name: "demo.duration", Type: SettingInteger, Unit: SettingMilliseconds, Default: "1500", IntegerMax: math.MaxInt32, Description: "duration"},
		{Name: "demo.real_time", Type: SettingFloating, Unit: SettingSeconds, Default: "1", FloatMax: 10, Description: "real time"},
		{Name: "demo.fixed", Type: SettingString, Access: SettingReadOnly, Default: "locked", Description: "fixed"},
		{Name: "demo.priv", Type: SettingBoolean, Access: SettingPrivileged, Default: "off", Description: "privileged"},
	}
}
func settingsTestDriver(t *testing.T) *Driver {
	t.Helper()
	d := NewDriver()
	for _, v := range settingsDefinitionsForTest() {
		if err := d.RegisterSetting(v); err != nil {
			t.Fatal(err)
		}
	}
	return d
}
func settingsRows(t *testing.T, c *sql.Conn, q string, prepared bool, column string, want any, args ...any) {
	t.Helper()
	ctx := context.Background()
	var r *sql.Rows
	var err error
	if prepared {
		s, e := c.PrepareContext(ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		r, err = s.QueryContext(ctx, args...)
	} else {
		r, err = c.QueryContext(ctx, q, args...)
	}
	if err != nil {
		t.Fatal(q, err)
	}
	defer r.Close()
	cols, err := r.Columns()
	if err != nil {
		t.Fatal(err)
	}
	types, err := r.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	nullable := strings.HasPrefix(q, "SELECT")
	n, ok := types[0].Nullable()
	if !reflect.DeepEqual(cols, []string{column}) || types[0].DatabaseTypeName() != "TEXT" || types[0].ScanType() != reflect.TypeOf("") || !ok || n != nullable {
		t.Fatal(q, cols, types, n, ok)
	}
	if !r.Next() {
		t.Fatal("missing row", q, r.Err())
	}
	var got any
	if err = r.Scan(&got); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("%s got %#v want %#v err %v", q, got, want, err)
	}
	if r.Next() || r.Err() != nil {
		t.Fatal("EOF", r.Err())
	}
	if err = r.Close(); err != nil {
		t.Fatal(err)
	}
}
func settingsDiagnostic(t *testing.T, err error, code, message, hint, detail, position string) {
	t.Helper()
	var d *Error
	if !errors.As(err, &d) {
		t.Fatalf("not SQL error: %v", err)
	}
	want := &Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint, Detail: detail, Position: position}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("got %#v want %#v", d, want)
	}
}
func TestSettingsPublicTypedSQL(t *testing.T) {
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
				if v.Access == SettingSession {
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
			settingsRows(t, a, "SELECT set_config($1,$1,false)", prepared, "set_config", "demo.text", "demo.text")
		})
	}
}

func TestSettingsExactDecimalSubnormal(t *testing.T) {
	// 5^1023 * 10^-1023 is exactly 2^-1023, independently of float formatting.
	digits := new(big.Int).Exp(big.NewInt(5), big.NewInt(1023), nil)
	exact := digits.String() + "e-1023"
	inexact := new(big.Int).Add(digits, big.NewInt(1)).String() + "e-1023"
	d := settingsTestDriver(t)
	if err := d.RegisterSetting(SettingDefinition{Name: "demo.exact", Type: SettingFloating, Default: exact, FloatMax: 10}); err != nil {
		t.Fatal(err)
	}
	db := sessionPool(t, d, "exact-decimal")
	ctx := context.Background()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, prepared := range []bool{false, true} {
		settingsRows(t, c, "SHOW demo.exact", prepared, "demo.exact", "1.11254e-308")
		for _, input := range []string{exact, "+" + exact, " " + exact + " "} {
			sessionSet(t, c, "SET demo.ratio TO '"+input+"'", prepared)
			settingsRows(t, c, "SHOW demo.ratio", prepared, "demo.ratio", "1.11254e-308")
			settingsRows(t, c, "SELECT set_config($1,$2,false)", prepared, "set_config", "1.11254e-308", "demo.ratio", input)
		}
		for _, input := range []string{inexact, "1e-320", "1e-999"} {
			for _, q := range []string{"SET demo.ratio TO '" + input + "'", "SELECT set_config('demo.ratio','" + input + "',false)"} {
				if prepared {
					s, e := c.PrepareContext(ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					_, err = s.ExecContext(ctx)
					s.Close()
				} else {
					_, err = c.ExecContext(ctx, q)
				}
				settingsDiagnostic(t, err, "22023", `invalid value for parameter "demo.ratio": "`+input+`"`, "", "", "")
				settingsRows(t, c, "SHOW demo.ratio", prepared, "demo.ratio", "1.11254e-308")
			}
		}
		sessionSet(t, c, "SET demo.exact TO '2'", prepared)
		for _, q := range []string{"SET demo.exact TO DEFAULT", "RESET demo.exact", "RESET ALL"} {
			sessionSet(t, c, q, prepared)
			settingsRows(t, c, "SHOW demo.exact", prepared, "demo.exact", "1.11254e-308")
		}
		settingsRows(t, c, "SELECT set_config('demo.exact',NULL,false)", prepared, "set_config", "1.11254e-308")
	}
}

func TestSettingsErrorsAndAtomicity(t *testing.T) {
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

func TestSettingsRegistryPublication(t *testing.T) {
	var d Driver
	def := SettingDefinition{Name: "demo.mode", Type: SettingEnum, Default: "safe", EnumValues: []string{"safe", "fast"}, Description: "original"}
	if err := d.RegisterSetting(def); err != nil {
		t.Fatal(err)
	}
	def.EnumValues[0] = "changed"
	def.Description = "changed"
	if err := d.RegisterSetting(SettingDefinition{Name: "DEMO.MODE", Type: SettingString}); err == nil {
		t.Fatal("duplicate accepted")
	}
	for _, bad := range []SettingDefinition{{Name: "plain", Type: SettingString}, {Name: "demo.bad", Type: SettingType(99)}, {Name: "demo.bad", Type: SettingInteger, IntegerMin: 1, IntegerMax: 0}, {Name: "demo.bad", Type: SettingBoolean, Default: "o"}, {Name: "demo.bad", Type: SettingString, Unit: SettingBytes}, {Name: "demo.bad", Type: SettingEnum, EnumValues: []string{"a", "A"}}, {Name: "demo.bad", Type: SettingFloating, FloatMax: math.Inf(1)}} {
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
	if err := d.RegisterSetting(SettingDefinition{Name: "demo.late", Type: SettingString}); err == nil {
		t.Fatal("late registration")
	}
	db2 := sessionPool(t, &d, "second")
	b, err := db2.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	settingsRows(t, b, "SHOW demo.mode", false, "demo.mode", "safe")
	other := sessionPool(t, NewDriver(), "first")
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
	if err = d.RegisterSetting(SettingDefinition{Name: "demo.after_close", Type: SettingString}); err == nil {
		t.Fatal("close unfroze registry")
	}
}
func TestSettingsConcurrentFreeze(t *testing.T) {
	for range 20 {
		d := NewDriver()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = d.RegisterSetting(SettingDefinition{Name: "demo.race", Type: SettingString})
		}()
		go func() { defer wg.Done(); _, _ = d.OpenConnector("race") }()
		wg.Wait()
		if err := d.RegisterSetting(SettingDefinition{Name: "demo.late", Type: SettingString}); err == nil {
			t.Fatal("not frozen")
		}
	}
}

func TestSettingsShowAllPlaceholdersAndLifecycle(t *testing.T) {
	ctx := context.Background()
	d := settingsTestDriver(t)
	db := sessionPool(t, d, "lifecycle")
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
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
		"DateStyle":                     {"ISO, MDY", "Sets the display format for date and time values."},
		"TimeZone":                      {"UTC", "Sets the time zone for displaying and interpreting time stamps."},
		"application_name":              {"", "Sets the application name to be reported in statistics and logs."},
		"client_min_messages":           {"notice", "Sets the message levels that are sent to the client."},
		"bytea_output":                  {"hex", "Sets the output format for bytea."},
		"IntervalStyle":                 {"postgres", "Sets the display format for interval values."},
		"log_statement_stats":           {"off", "Writes cumulative performance statistics to the server log."},
		"server_encoding":               {"UTF8", "Shows the server (database) character set encoding."},
		"standard_conforming_strings":   {"on", "Causes '...' strings to treat backslashes literally."},
		"statement_timeout":             {"0", "Sets the maximum allowed duration of any statement."},
		"lock_timeout":                  {"0", "Sets the maximum allowed duration of any wait for a lock."},
		"search_path":                   {`"$user", public`, "Sets the schema search order for names that are not schema-qualified."},
		"default_transaction_isolation": {"read committed", "Sets the transaction isolation level of each new transaction."},
		"default_transaction_read_only": {"off", "Sets the default read-only status of new transactions."},
		"transaction_isolation":         {"read committed", "Sets the current transaction's isolation level."},
		"transaction_read_only":         {"off", "Sets the current transaction's read-only status."},
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
	before := nativeSession(t, a).Token()
	a.Close()
	a, err = db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if nativeSession(t, a).Token() != before {
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
	if nativeSession(t, a).Token() == before {
		t.Fatal("not replaced")
	}
	settingsRows(t, a, "SHOW demo.text", false, "demo.text", "seed")
	_, err = a.ExecContext(ctx, "SHOW demo.placeholder")
	settingsDiagnostic(t, err, "42704", `unrecognized configuration parameter "demo.placeholder"`, "", "", "")
	var v int
	if err = a.QueryRowContext(ctx, "SELECT v FROM sentinel").Scan(&v); err != nil || v != 7 {
		t.Fatal(v, err)
	}
}

func TestSettingsPreparedLiteralMode(t *testing.T) {
	ctx := context.Background()
	db := sessionPool(t, NewDriver(), "mode")
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err = a.ExecContext(ctx, "CREATE TABLE literals(v TEXT)"); err != nil {
		t.Fatal(err)
	}
	sessionSet(t, a, "SET standard_conforming_strings TO off", false)
	off, err := a.PrepareContext(ctx, `INSERT INTO literals VALUES('a\nb')`)
	if err != nil {
		t.Fatal(err)
	}
	defer off.Close()
	on, err := b.PrepareContext(ctx, `INSERT INTO literals VALUES('a\nb')`)
	if err != nil {
		t.Fatal(err)
	}
	defer on.Close()
	sessionSet(t, a, "SET standard_conforming_strings TO on", false)
	sessionSet(t, b, "SET standard_conforming_strings TO off", false)
	if _, err = off.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = on.ExecContext(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ExecContext(ctx, `INSERT INTO literals VALUES(E'a\nb')`); err != nil {
		t.Fatal(err)
	}
	rows, err := a.QueryContext(ctx, "SELECT v FROM literals ORDER BY v")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var v string
		if err = rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if rows.Err() != nil || !reflect.DeepEqual(got, []string{"a\nb", "a\nb", `a\nb`}) {
		t.Fatal(got, rows.Err())
	}
}

func TestSettingsPreparedBindingAndStages(t *testing.T) {
	ctx := context.Background()
	db := sessionPool(t, settingsTestDriver(t), "binding")
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for _, q := range []string{"SHOW demo.text", "SELECT current_setting('demo.text')", "SELECT current_setting($1)"} {
		s, err := a.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		sessionSet(t, a, "SET demo.text TO changed", false)
		var value string
		if strings.Contains(q, "$1") {
			err = s.QueryRowContext(ctx, "demo.text").Scan(&value)
		} else {
			err = s.QueryRowContext(ctx).Scan(&value)
		}
		s.Close()
		if err != nil || value != "changed" {
			t.Fatal(q, value, err)
		}
		sessionSet(t, a, "RESET demo.text", false)
	}
	for _, q := range []string{"SET demo.text TO prepared", "SELECT set_config('demo.text','prepared',false)"} {
		s, err := a.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		settingsRows(t, a, "SHOW demo.text", false, "demo.text", "seed")
		if _, err = s.ExecContext(ctx); err != nil {
			t.Fatal(err)
		}
		s.Close()
		settingsRows(t, a, "SHOW demo.text", false, "demo.text", "prepared")
		sessionSet(t, a, "RESET demo.text", false)
	}
	for _, q := range []string{"SELECT current_setting()", "SELECT current_setting('demo.text',true)", "SELECT set_config('demo.text','v')", "SELECT current_setting(true)"} {
		_, err = a.PrepareContext(ctx, q)
		settingsDiagnostic(t, err, "0A000", "unsupported setting function signature", "", "", "8")
	}
	_, err = a.PrepareContext(ctx, "SHOW absent_setting")
	settingsDiagnostic(t, err, "42704", `unrecognized configuration parameter "absent_setting"`, "", "", "")
	s, err := a.PrepareContext(ctx, "SET absent_setting TO v")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ExecContext(ctx)
	s.Close()
	settingsDiagnostic(t, err, "42704", `unrecognized configuration parameter "absent_setting"`, "", "", "")
	for _, args := range [][]any{{}, {1, 2}, {sql.Named("name", "demo.text")}} {
		_, err = a.ExecContext(ctx, "SELECT current_setting($1)", args...)
		var d *Error
		if err == nil || errors.As(err, &d) {
			t.Fatalf("adapter arguments %#v: %v", args, err)
		}
	}
}

func TestSettingsEveryUnitBase(t *testing.T) {
	d := NewDriver()
	for _, v := range []SettingDefinition{{Name: "demo.mb", Type: SettingInteger, Unit: SettingMegabytes, Default: "1", IntegerMax: math.MaxInt32}, {Name: "demo.minutes", Type: SettingInteger, Unit: SettingMinutes, Default: "1", IntegerMax: math.MaxInt32}} {
		if err := d.RegisterSetting(v); err != nil {
			t.Fatal(err)
		}
	}
	db := sessionPool(t, d, "units")
	a, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	sessionSet(t, a, "SET demo.mb TO '1GB'", true)
	settingsRows(t, a, "SHOW demo.mb", true, "demo.mb", "1GB")
	sessionSet(t, a, "SET demo.minutes TO '2h'", true)
	settingsRows(t, a, "SHOW demo.minutes", true, "demo.minutes", "2h")
}

func TestSettingsControlsDefaultOnParser(t *testing.T) {
	ctx := context.Background()
	db := sessionPool(t, settingsTestDriver(t), "controls")
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	sessionSet(t, a, "SET standard_conforming_strings TO off", false)
	sessionSet(t, a, "SET demo.text TO retained", false)
	if err = Restore(ctx, db, strings.NewReader(`CREATE TABLE literals(v TEXT); INSERT INTO literals VALUES('a\nb')`)); err != nil {
		t.Fatal(err)
	}
	var v string
	if err = a.QueryRowContext(ctx, "SELECT v FROM literals").Scan(&v); err != nil || v != `a\nb` {
		t.Fatal(v, err)
	}
	var dump bytes.Buffer
	if err = Dump(ctx, db, &dump); err != nil {
		t.Fatal(err)
	}
	if err = Reset(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err = Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	settingsRows(t, a, "SHOW demo.text", false, "demo.text", "retained")
	settingsRows(t, a, "SHOW standard_conforming_strings", false, "standard_conforming_strings", "off")
	if err = a.QueryRowContext(ctx, "SELECT v FROM literals").Scan(&v); err != nil || v != `a\nb` {
		t.Fatal(v, err)
	}
}

func TestSettingsPublicTimeoutStages(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	db := sessionPool(t, NewDriver(), "timeout")
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	before := nativeSession(t, a).Token()
	if _, err = a.ExecContext(ctx, "CREATE TABLE sentinel(v INT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ExecContext(ctx, "INSERT INTO sentinel VALUES(7)"); err != nil {
		t.Fatal(err)
	}
	retained, err := a.QueryContext(ctx, "SELECT v FROM sentinel")
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	for _, stage := range []string{"direct", "prepare", "prepared-execute"} {
		t.Run(stage, func(t *testing.T) {
			timedOut := false
			for _, size := range []int{16384, 65536, 262144, 1048576} {
				// NULL guarantees failed execution if this workload completes before the
				// actual timer. No successful mutation is retried to force a timeout.
				q := "INSERT INTO sentinel VALUES " + strings.Repeat("(8),", size) + "(NULL)"
				var prepared *sql.Stmt
				if stage == "prepared-execute" {
					prepared, err = a.PrepareContext(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
				}
				sessionSet(t, a, "SET statement_timeout TO '1ms'", false)
				switch stage {
				case "direct":
					_, err = a.ExecContext(ctx, q)
				case "prepare":
					prepared, err = a.PrepareContext(ctx, q)
				case "prepared-execute":
					_, err = prepared.ExecContext(ctx)
				}
				if prepared != nil {
					prepared.Close()
				}
				var d *Error
				if errors.As(err, &d) && d.Code == "57014" {
					settingsDiagnostic(t, err, "57014", "canceling statement due to statement timeout", "", "", "")
					timedOut = true
				} else if stage == "prepare" {
					if err != nil {
						t.Fatal(err)
					}
				} else if !errors.As(err, &d) || d.Code != "23502" {
					t.Fatal(stage, err)
				}
				sessionSet(t, a, "RESET statement_timeout", false)
				rows, err := a.QueryContext(ctx, "SELECT v FROM sentinel")
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for rows.Next() {
					var v int
					if err = rows.Scan(&v); err != nil || v != 7 {
						t.Fatal("publication changed", v, err)
					}
					count++
				}
				if rows.Err() != nil || count != 1 {
					t.Fatal("publication cardinality", count, rows.Err())
				}
				rows.Close()
				if timedOut {
					break
				}
			}
			if !timedOut {
				t.Fatal("bounded actual command workload did not time out")
			}
			if nativeSession(t, a).Token() != before {
				t.Fatal("timeout replaced physical session")
			}
			settingsRows(t, b, "SHOW statement_timeout", false, "statement_timeout", "0")
		})
	}
	if !retained.Next() {
		t.Fatal(retained.Err())
	}
	var v int
	if err = retained.Scan(&v); err != nil || v != 7 {
		t.Fatal(v, err)
	}
	if retained.Next() || retained.Err() != nil {
		t.Fatal(retained.Err())
	}
	caller, stop := context.WithCancel(ctx)
	stop()
	_, err = a.ExecContext(caller, "SHOW application_name")
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	deadline, stopDeadline := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer stopDeadline()
	_, err = a.ExecContext(deadline, "SHOW application_name")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	settingsRows(t, a, "SHOW statement_timeout", false, "statement_timeout", "0")
}

func TestSettingsBooleanBindingDiagnostics(t *testing.T) {
	ctx := context.Background()
	db := sessionPool(t, NewDriver(), "bool-binding")
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sessionSet(t, c, "SET application_name TO retained", false)
	for _, prepared := range []bool{false, true} {
		q := "SELECT set_config($1,$2,$3)"
		if prepared {
			s, e := c.PrepareContext(ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			_, err = s.QueryContext(ctx, "application_name", "changed", "maybe")
			s.Close()
		} else {
			_, err = c.QueryContext(ctx, q, "application_name", "changed", "maybe")
		}
		var diagnostic *Error;want:=Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22P02",Message:`invalid input syntax for type boolean: "maybe"`,Context:"unnamed portal parameter $3 = '...'"};if !errors.As(err,&diagnostic)||*diagnostic!=want{t.Fatal(diagnostic,err,want)}
		settingsRows(t, c, "SHOW application_name", false, "application_name", "retained")
	}
	for _, q := range []string{`SELECT set_config('application_name','v','maybe')`, `SELECT set_config('application_name','a',)`} {
		for _, prepare := range []bool{false, true} {
			if prepare {
				s, e := c.PrepareContext(ctx, q)
				err = e
				if s != nil {
					s.Close()
					t.Fatal("failed preparation returned a handle")
				}
			} else {
				_, err = c.QueryContext(ctx, q)
			}
			if strings.Contains(q, "maybe") {
				settingsDiagnostic(t, err, "22P02", `invalid input syntax for type boolean: "maybe"`, "", "", "42")
			} else {
				settingsDiagnostic(t, err, "42601", `syntax error at or near ")"`, "", "", "42")
			}
			settingsRows(t, c, "SHOW application_name", false, "application_name", "retained")
		}
	}
}

func TestSettingsUnicodeModeCapture(t *testing.T) {
	ctx := context.Background()
	db := sessionPool(t, NewDriver(), "unicode-mode")
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sessionSet(t, c, "CREATE TABLE unicode_capture(id INT,v TEXT)", false)
	queries := []string{`SET application_name TO U&'\0061'`, `SELECT set_config('application_name',U&'\0061',false)`, `INSERT INTO unicode_capture VALUES(1,U&'\0061')`}
	var captured []*sql.Stmt
	for _, q := range queries {
		s, e := c.PrepareContext(ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		captured = append(captured, s)
		defer s.Close()
	}
	settingsRows(t, c, "SHOW application_name", false, "application_name", "")
	sessionSet(t, c, "SET standard_conforming_strings TO off", false)
	for _, tc := range []struct{ query, position string }{
		{queries[0], "25"}, {`SELECT current_setting(U&'application_name')`, "24"},
		{`SET application_name TO U&'\ZZZZ' UESCAPE '0'`, "25"}, {`SET application_name TO U&'unterminated`, "25"},
		{`/*é*/ SET application_name TO U&'\0061'`, "31"},
		{`INSERT INTO unicode_capture VALUES(2,U&'\0061')`, "38"},
	} {
		for _, prepare := range []bool{false, true} {
			if prepare {
				s, e := c.PrepareContext(ctx, tc.query)
				err = e
				if s != nil {
					s.Close()
					t.Fatal("unsafe Prepare returned a handle")
				}
			} else {
				_, err = c.ExecContext(ctx, tc.query)
			}
			settingsDiagnostic(t, err, "0A000", "unsafe use of string constant with Unicode escapes", "", `String constants with Unicode escapes cannot be used when "standard_conforming_strings" is off.`, tc.position)
			settingsRows(t, c, "SHOW application_name", false, "application_name", "")
		}
	}
	sessionSet(t, c, `SET U&"application_name" TO 'x'`, false)
	sessionSet(t, c, `SET application_name TO E'a\nb'`, false)
	settingsRows(t, c, "SHOW application_name", false, "application_name", `a\x0ab`)
	sessionSet(t, c, `SET application_name TO $$a\nb$$`, false)
	settingsRows(t, c, "SHOW application_name", false, "application_name", `a\nb`)
	for i, s := range captured {
		r, e := s.ExecContext(ctx)
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
	settingsRows(t, c, "SHOW application_name", false, "application_name", "a")
	settingsRows(t, c, "SELECT v FROM unicode_capture", false, "v", "a")
}

func TestSettingsNumericPrefixEdges(t *testing.T) {
	ctx := context.Background()
	d := settingsTestDriver(t)
	for _, def := range []SettingDefinition{{Name: "edge.count", Type: SettingInteger, Default: "0", IntegerMin: -200, IntegerMax: 200}, {Name: "edge.real", Type: SettingFloating, Default: "0", FloatMin: -200, FloatMax: 200}} {
		if err := d.RegisterSetting(def); err != nil {
			t.Fatal(err)
		}
	}
	db := sessionPool(t, d, "numeric-prefix")
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ name, input, want string }{
			{"edge.count", "010e1", "100"}, {"edge.count", "0x1.8p1", "3"}, {"edge.count", ".5", "0"}, {"edge.count", " 2.5 ", "2"}, {"edge.count", "9223372036854775808e-18", "9"}, {"edge.count", "0e-999", "0"},
			{"edge.real", "010", "10"}, {"edge.real", "0xA", "10"}, {"edge.real", "010.5", "10.5"}, {"edge.real", "010e1", "100"}, {"edge.real", "08.5", "8.5"}, {"edge.real", "0x1p0", "1"}, {"edge.real", "+.5", "0.5"}, {"edge.real", "-.5", "-0.5"}, {"edge.real", " .5", "0.5"}, {"edge.real", "0e-999", "0"}, {"edge.real", "0x1p-1074", "4.94066e-324"},
		} {
			sessionSet(t, c, "SET "+tc.name+" TO '"+tc.input+"'", prepared)
			settingsRows(t, c, "SHOW "+tc.name, prepared, tc.name, tc.want)
			settingsRows(t, c, "SELECT set_config($1,$2,false)", prepared, "set_config", tc.want, tc.name, tc.input)
		}
		for _, name := range []string{"edge.count", "edge.real"} {
			inputs := []string{"0b10", "0o10", "1_000", "1e", "0x", "1e-999", "1e999", "NaN"}
			if name == "edge.count" {
				inputs = append(inputs, "+.5", "-.5", " .5", "08.5", "0x1p0", "Infinity", "-inf", "0x1p-1074")
			} else {
				inputs = append(inputs, "0x1.1p-1074", "1e-320")
			}
			for _, input := range inputs {
				settingsRows(t, c, "SELECT set_config($1,'1',false)", prepared, "set_config", "1", name)
				q := "SELECT set_config($1,$2,false)"
				if prepared {
					s, e := c.PrepareContext(ctx, q)
					if e != nil {
						t.Fatal(e)
					}
					_, err = s.ExecContext(ctx, name, input)
					s.Close()
				} else {
					_, err = c.ExecContext(ctx, q, name, input)
				}
				settingsDiagnostic(t, err, "22023", `invalid value for parameter "`+name+`": "`+input+`"`, "", "", "")
				settingsRows(t, c, "SHOW "+name, prepared, name, "1")
			}
		}
		for _, input := range []string{"08.5", "0x1p0"} {
			_, err = c.ExecContext(ctx, "SELECT set_config('statement_timeout',$1,false)", input)
			settingsDiagnostic(t, err, "22023", `invalid value for parameter "statement_timeout": "`+input+`"`, `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`, "", "")
		}
		_, err = c.ExecContext(ctx, "SELECT set_config('demo.ratio','-inf',false)")
		settingsDiagnostic(t, err, "22023", `-Infinity is outside the valid range for parameter "demo.ratio" (0 .. 10)`, "", "", "")
		settingsRows(t, c, "SHOW demo.ratio", prepared, "demo.ratio", "1")
	}
}

func TestSettingsSetConfigNoticesAndReentry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, prepared := range []bool{false, true} {
		d := NewDriver()
		raw, err := d.OpenConnector("set-config-notices")
		if err != nil {
			t.Fatal(err)
		}
		var physical *conn
		var notices []Error
		var callbackError error
		configured := raw.(*Connector).WithNoticeHandler(func(n Error) {
			notices = append(notices, n)
			// Reenter the same admitted adapter, not database/sql's held Conn mutex.
			// This witnesses completion of the lease as well as publication.
			rows, e := physical.QueryContext(ctx, "SHOW application_name", nil)
			if e != nil {
				callbackError = e
				return
			}
			defer rows.Close()
			value := make([]driver.Value, 1)
			if e = rows.Next(value); e != nil {
				callbackError = e
				return
			}
			if value[0] != strings.Repeat("a", 63) {
				callbackError = errors.New("callback observed unpublished value")
			}
		})
		db := sql.OpenDB(configured)
		c, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.Raw(func(v any) error { physical = v.(*conn); return nil }); err != nil {
			t.Fatal(err)
		}
		original := strings.Repeat("a", 64)
		clipped := strings.Repeat("a", 63)
		want := Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
		for _, threshold := range []string{"notice", "warning", "error"} {
			sessionSet(t, c, "SET client_min_messages TO "+threshold, false)
			before := len(notices)
			q := "SELECT set_config($1,$2,false)"
			if prepared {
				s, e := c.PrepareContext(ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				if len(notices) != before {
					t.Fatal("prepare emitted notice")
				}
				settingsRows(t, c, "SHOW application_name", false, "application_name", map[bool]string{true: clipped, false: ""}[before > 0])
				rows, e := s.QueryContext(ctx, "application_name", original)
				if e != nil {
					t.Fatal(e)
				}
				rows.Close()
				s.Close()
			} else {
				settingsRows(t, c, q, false, "set_config", clipped, "application_name", original)
			}
			count := 0
			if threshold == "notice" {
				count = 1
			}
			if len(notices) != before+count || callbackError != nil {
				t.Fatal(notices, callbackError)
			}
			if count == 1 && notices[len(notices)-1] != want {
				t.Fatal(notices)
			}
			settingsRows(t, c, "SHOW application_name", false, "application_name", clipped)
		}
		sessionSet(t, c, "RESET client_min_messages", false)
		before := len(notices)
		settingsRows(t, c, "SELECT set_config('application_name',NULL,false)", prepared, "set_config", "")
		sessionSet(t, c, "SET application_name TO DEFAULT", prepared)
		sessionSet(t, c, "RESET application_name", prepared)
		if len(notices) != before {
			t.Fatal("default restore emitted notice", notices)
		}
		c.Close()
		db.Close()
	}
}

func TestSettingsRegistryValidationAndLinearization(t *testing.T) {
	for _, bad := range []SettingDefinition{
		{Name: "demo.value", Type: SettingString, IntegerMax: 1},
		{Name: "demo.value", Type: SettingInteger, FloatMax: 1},
		{Name: "demo.value", Type: SettingFloating, FloatMin: math.NaN()},
		{Name: "demo.value", Type: SettingBoolean, Default: "o"},
		{Name: "demo.value", Type: SettingInteger, Default: "1"},
		{Name: "demo.value", Type: SettingFloating, Default: "NaN", FloatMax: 10},
		{Name: "demo.value", Type: SettingEnum, Default: "bad", EnumValues: []string{"ok"}},
		{Name: "demo.value", Type: SettingEnum, EnumValues: []string{"ok", ""}},
		{Name: "demo.value", Type: SettingString, Description: "bad\x00description"},
		{Name: "demo.value", Type: SettingString, Default: "bad\x00default"},
		{Name: "demo.value", Type: SettingString, Description: string([]byte{0xff})},
	} {
		d := NewDriver()
		err := d.RegisterSetting(bad)
		var diagnostic *Error
		if err == nil || errors.As(err, &diagnostic) {
			t.Fatal("registration boundary", bad, err)
		}
		if err = d.RegisterSetting(SettingDefinition{Name: "demo.value", Type: SettingString, Default: "owned"}); err != nil {
			t.Fatal("failed publication reserved name", err)
		}
		db := sessionPool(t, d, t.Name())
		c, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		settingsRows(t, c, "SHOW demo.value", false, "demo.value", "owned")
		c.Close()
	}
	d := NewDriver()
	for _, name := range []string{"démo.值", "demo.deep.$bad", "demo.deep.a$1"} {
		err := d.RegisterSetting(SettingDefinition{Name: name, Type: SettingString, Default: "seed"})
		if name == "demo.deep.$bad" {
			if err == nil {
				t.Fatal("invalid component initial accepted")
			}
		} else if err != nil {
			t.Fatal(name, err)
		}
	}
	for range 20 {
		d := NewDriver()
		start := make(chan struct{})
		registered := make(chan error, 1)
		opened := make(chan driver.Connector, 1)
		go func() {
			<-start
			registered <- d.RegisterSetting(SettingDefinition{Name: "demo.race", Type: SettingString, Default: "winner"})
		}()
		go func() {
			<-start
			c, err := d.OpenConnector("linearized")
			if err != nil {
				opened <- nil
				return
			}
			opened <- c
		}()
		close(start)
		publication := <-registered
		connector := <-opened
		if connector == nil {
			t.Fatal("connector failed")
		}
		db := sql.OpenDB(connector)
		c, err := db.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if publication == nil {
			settingsRows(t, c, "SHOW demo.race", false, "demo.race", "winner")
		} else {
			_, err = c.ExecContext(context.Background(), "SHOW demo.race")
			settingsDiagnostic(t, err, "42704", `unrecognized configuration parameter "demo.race"`, "", "", "")
		}
		c.Close()
		db.Close()
	}
}

func TestSettingsBooleanByteInputAndHealthySession(t *testing.T) {
	db := sessionPool(t, NewDriver(), "boolean-bytes")
	ctx := context.Background()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, prepared := range []bool{false, true} {
		sessionSet(t, c, "SET application_name TO retained", false)
		q := "SELECT set_config($1,$2,$3)"
		if prepared {
			s, e := c.PrepareContext(ctx, q)
			if e != nil {
				t.Fatal(e)
			}
			_, err = s.QueryContext(ctx, "application_name", "changed", []byte("maybe"))
			s.Close()
		} else {
			_, err = c.QueryContext(ctx, q, "application_name", "changed", []byte("maybe"))
		}
		var diagnostic *Error;want:=Error{Severity:"ERROR",SeverityUnlocalized:"ERROR",Code:"22P02",Message:`invalid input syntax for type boolean: "maybe"`,Context:"unnamed portal parameter $3 = '...'"};if !errors.As(err,&diagnostic)||*diagnostic!=want{t.Fatal(diagnostic,err,want)}
		settingsRows(t, c, "SHOW application_name", false, "application_name", "retained")
		settingsRows(t, c, q, prepared, "set_config", "healthy", "application_name", "healthy", false)
		settingsRows(t, c, q, prepared, "set_config", "", "application_name", nil, nil)
	}
}

func TestSettingsShowAllCurrentOverrides(t *testing.T) {
	db := sessionPool(t, settingsTestDriver(t), "show-all-current")
	ctx := context.Background()
	c, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, prepared := range []bool{false, true} {
		expected := map[string]string{"demo.flag": "on", "demo.text": "current", "demo.count": "4", "demo.ratio": "2.12346", "demo.mode": "fast", "demo.memory": "2MB"}
		for name, value := range map[string]string{
			"application_name": "", "client_min_messages": "notice",
			"lock_timeout": "0", "log_statement_stats": "off",
			"search_path": `"$user", public`, "server_encoding": "UTF8",
			"standard_conforming_strings": "on", "statement_timeout": "0",
		} {
			expected[name] = value
		}
		for _, tc := range []struct{ name, input string }{{"demo.flag", "YES"}, {"demo.text", "current"}, {"demo.count", "3.5"}, {"demo.ratio", "2.1234567"}, {"demo.mode", "FAST"}, {"demo.memory", "2048"}} {
			sessionSet(t, c, "SET "+tc.name+" TO '"+tc.input+"'", prepared)
		}
		var rows *sql.Rows
		if prepared {
			s, e := c.PrepareContext(ctx, "SHOW ALL")
			if e != nil {
				t.Fatal(e)
			}
			defer s.Close()
			r, e := s.ExecContext(ctx)
			if e != nil {
				t.Fatal(e)
			}
			n, e := r.RowsAffected()
			if e != nil || n != 0 {
				t.Fatal(n, e)
			}
			rows, err = s.QueryContext(ctx)
		} else {
			r, e := c.ExecContext(ctx, "SHOW ALL")
			if e != nil {
				t.Fatal(e)
			}
			n, e := r.RowsAffected()
			if e != nil || n != 0 {
				t.Fatal(n, e)
			}
			rows, err = c.QueryContext(ctx, "SHOW ALL")
		}
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name, value, description string
			if err = rows.Scan(&name, &value, &description); err != nil {
				t.Fatal(err)
			}
			if want, ok := expected[name]; ok {
				if value != want {
					t.Fatal(name, value, want)
				}
				delete(expected, name)
			}
		}
		if rows.Err() != nil || len(expected) != 0 {
			t.Fatal(rows.Err(), expected)
		}
		rows.Close()
		sessionSet(t, c, "RESET ALL", prepared)
	}
}

func TestSettingsAuthenticatedAccessClasses(t *testing.T) {
	ctx := context.Background()
	d := settingsTestDriver(t)
	adminPool := rolePool(t, d, t.Name(), "postgres")
	admin, err := adminPool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	rolePublicCommand(t, admin, "CREATE USER settings_reader", 0, false)
	readerPool := rolePool(t, d, t.Name(), "settings_reader")
	reader, err := readerPool.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	for _, principal := range []struct {
		name string
		conn *sql.Conn
	}{{"postgres", admin}, {"settings_reader", reader}} {
		rolePublicIdentity(t, principal.conn, principal.name, nil)
		for _, definition := range []struct{ name, value, code, message string }{
			{"server_encoding", "UTF8", "55P02", `parameter "server_encoding" cannot be changed`},
			{"log_statement_stats", "off", "42501", `permission denied to set parameter "log_statement_stats"`},
			{"demo.fixed", "locked", "55P02", `parameter "demo.fixed" cannot be changed`},
			{"demo.priv", "off", "42501", `permission denied to set parameter "demo.priv"`},
		} {
			for _, prepared := range []bool{false, true} {
				for _, query := range []string{
					"SET " + definition.name + " TO garbage",
					"SET " + definition.name + " TO DEFAULT",
					"RESET " + definition.name,
					"SELECT set_config('" + definition.name + "','garbage',false)",
				} {
					if prepared {
						statement, prepareErr := principal.conn.PrepareContext(ctx, query)
						if prepareErr != nil {
							t.Fatal(prepareErr)
						}
						_, err = statement.ExecContext(ctx)
						statement.Close()
					} else {
						_, err = principal.conn.ExecContext(ctx, query)
					}
					settingsDiagnostic(t, err, definition.code, definition.message, "", "", "")
					settingsRows(t, principal.conn, "SHOW "+definition.name, prepared, definition.name, definition.value)
				}
			}
		}
		rolePublicCommand(t, principal.conn, "RESET ALL", 0, true)
		settingsRows(t, principal.conn, "SHOW log_statement_stats", false, "log_statement_stats", "off")
		settingsRows(t, principal.conn, "SHOW demo.priv", false, "demo.priv", "off")
		rolePublicIdentity(t, principal.conn, principal.name, nil)
	}
}
