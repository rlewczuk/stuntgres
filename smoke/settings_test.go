package smoke_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"math"
	"math/big"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func TestSettingsSmoke(t *testing.T) {
	if err := settingsRun(); err != nil {
		t.Fatal(err)
	}
}
func settingsDefinitions() []stuntgres.SettingDefinition {
	return []stuntgres.SettingDefinition{
		{Name: "demo.flag", Type: stuntgres.SettingBoolean, Default: "off", Description: "flag"},
		{Name: "demo.text", Type: stuntgres.SettingString, Default: "seed", Description: "text"},
		{Name: "demo.count", Type: stuntgres.SettingInteger, Default: "2", IntegerMin: -10, IntegerMax: 10, Description: "count"},
		{Name: "demo.ratio", Type: stuntgres.SettingFloating, Default: "1", FloatMax: 10, Description: "ratio"},
		{Name: "demo.mode", Type: stuntgres.SettingEnum, Default: "safe", EnumValues: []string{"safe", "fast"}, Description: "mode"},
		{Name: "demo.memory", Type: stuntgres.SettingInteger, Unit: stuntgres.SettingKilobytes, Default: "1024", IntegerMax: math.MaxInt32, Description: "memory"},
		{Name: "demo.bytes", Type: stuntgres.SettingInteger, Unit: stuntgres.SettingBytes, Default: "0", IntegerMax: math.MaxInt32, Description: "bytes"},
		{Name: "demo.duration", Type: stuntgres.SettingInteger, Unit: stuntgres.SettingMilliseconds, Default: "1500", IntegerMax: math.MaxInt32, Description: "duration"},
		{Name: "demo.real_time", Type: stuntgres.SettingFloating, Unit: stuntgres.SettingSeconds, Default: "1", FloatMax: 10, Description: "real time"},
		{Name: "demo.fixed", Type: stuntgres.SettingString, Access: stuntgres.SettingReadOnly, Default: "locked", Description: "fixed"},
		{Name: "demo.priv", Type: stuntgres.SettingBoolean, Access: stuntgres.SettingPrivileged, Default: "off", Description: "privileged"},
		{Name: "edge.count", Type: stuntgres.SettingInteger, Default: "0", IntegerMin: -200, IntegerMax: 200, Description: "numeric prefix"},
		{Name: "edge.real", Type: stuntgres.SettingFloating, Default: "0", FloatMin: -200, FloatMax: 200, Description: "real prefix"},
	}
}
func settingsCommand(ctx context.Context, c *sql.Conn, q string, prepared bool, count int64, args ...any) error {
	var r sql.Result
	var err error
	if prepared {
		s, e := c.PrepareContext(ctx, q)
		if e != nil {
			return e
		}
		defer s.Close()
		r, err = s.ExecContext(ctx, args...)
	} else {
		r, err = c.ExecContext(ctx, q, args...)
	}
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != count {
		return fmt.Errorf("%s affected %d want %d", q, n, count)
	}
	return nil
}
func settingsScalar(ctx context.Context, c *sql.Conn, q, column string, want any, prepared bool, args ...any) error {
	var r *sql.Rows
	var err error
	if prepared {
		s, e := c.PrepareContext(ctx, q)
		if e != nil {
			return e
		}
		defer s.Close()
		r, err = s.QueryContext(ctx, args...)
	} else {
		r, err = c.QueryContext(ctx, q, args...)
	}
	if err != nil {
		return err
	}
	defer r.Close()
	cols, err := r.Columns()
	if err != nil {
		return err
	}
	types, err := r.ColumnTypes()
	if err != nil {
		return err
	}
	if len(types) != 1 {
		return fmt.Errorf("%s result types %v", q, types)
	}
	nullable, known := types[0].Nullable()
	if !reflect.DeepEqual(cols, []string{column}) || types[0].DatabaseTypeName() != "TEXT" || types[0].ScanType() != reflect.TypeOf("") || !known || nullable != strings.HasPrefix(q, "SELECT") {
		return fmt.Errorf("metadata %s: %v %v", q, cols, types)
	}
	if !r.Next() {
		return fmt.Errorf("missing row %s: %v", q, r.Err())
	}
	var v any
	if err = r.Scan(&v); err != nil {
		return err
	}
	if !reflect.DeepEqual(v, want) {
		return fmt.Errorf("%s got %#v want %#v", q, v, want)
	}
	if r.Next() || r.Err() != nil {
		return fmt.Errorf("%s EOF: %v", q, r.Err())
	}
	return r.Close()
}
func settingsDiagnostic(err error, code, message, hint string) error {
	return settingsFullDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint})
}
func settingsFullDiagnostic(err error, want stuntgres.Error) error {
	var d *stuntgres.Error
	if !errors.As(err, &d) || *d != want {
		return fmt.Errorf("diagnostic got %#v (%v) want %#v", d, err, want)
	}
	return nil
}
func settingsIdentity(c *sql.Conn) (any, error) {
	var v any
	err := c.Raw(func(raw any) error { v = raw; return nil })
	return v, err
}
func settingsSentinel(ctx context.Context, c *sql.Conn) error {
	rows, err := c.QueryContext(ctx, "SELECT v FROM sentinel")
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		return fmt.Errorf("missing sentinel: %v", rows.Err())
	}
	var value int
	if err = rows.Scan(&value); err != nil || value != 7 {
		return fmt.Errorf("sentinel %d: %v", value, err)
	}
	if rows.Next() || rows.Err() != nil {
		return fmt.Errorf("sentinel cardinality: %v", rows.Err())
	}
	return nil
}
func settingsExactDecimalSubnormal(ctx context.Context) error {
	digits := new(big.Int).Exp(big.NewInt(5), big.NewInt(1023), nil)
	exact := digits.String() + "e-1023"
	inexact := new(big.Int).Add(digits, big.NewInt(1)).String() + "e-1023"
	d := stuntgres.NewDriver()
	if err := d.RegisterSetting(stuntgres.SettingDefinition{Name: "demo.exact", Type: stuntgres.SettingFloating, Default: exact, FloatMax: 10}); err != nil {
		return err
	}
	connector, err := d.OpenConnector("settings-exact-decimal")
	if err != nil {
		return err
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	c, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close()
	for _, prepared := range []bool{false, true} {
		if err = settingsScalar(ctx, c, "SHOW demo.exact", "demo.exact", "1.11254e-308", prepared); err != nil {
			return err
		}
		if err = settingsCommand(ctx, c, "SET demo.exact TO '"+exact+"'", prepared, 0); err != nil {
			return err
		}
		if err = settingsScalar(ctx, c, "SHOW demo.exact", "demo.exact", "1.11254e-308", prepared); err != nil {
			return err
		}
		if err = settingsScalar(ctx, c, "SELECT set_config($1,$2,false)", "set_config", "1.11254e-308", prepared, "demo.exact", exact); err != nil {
			return err
		}
		for _, input := range []string{inexact, "1e-320", "1e-999"} {
			for _, q := range []string{"SET demo.exact TO '" + input + "'", "SELECT set_config('demo.exact','" + input + "',false)"} {
				e := settingsCommand(ctx, c, q, prepared, 0)
				if err = settingsDiagnostic(e, "22023", `invalid value for parameter "demo.exact": "`+input+`"`, ""); err != nil {
					return err
				}
				if err = settingsScalar(ctx, c, "SHOW demo.exact", "demo.exact", "1.11254e-308", prepared); err != nil {
					return err
				}
			}
		}
		if err = settingsCommand(ctx, c, "SET demo.exact TO '1'", prepared, 0); err != nil {
			return err
		}
		if err = settingsCommand(ctx, c, "RESET demo.exact", prepared, 0); err != nil {
			return err
		}
		if err = settingsScalar(ctx, c, "SHOW demo.exact", "demo.exact", "1.11254e-308", prepared); err != nil {
			return err
		}
	}
	return nil
}
func settingsRun() error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := settingsExactDecimalSubnormal(ctx); err != nil {
		return err
	}
	d := stuntgres.NewDriver()
	for _, v := range settingsDefinitions() {
		if err := d.RegisterSetting(v); err != nil {
			return err
		}
	}
	raw, err := d.OpenConnector("settings-smoke")
	if err != nil {
		return err
	}
	var notices []stuntgres.Error
	var physical driver.QueryerContext
	var callbackError error
	connector := raw.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) {
		notices = append(notices, n)
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
			callbackError = fmt.Errorf("callback saw uncommitted application_name: %v", value)
		}
	})
	db := sql.OpenDB(connector)
	defer db.Close()
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	a, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { a.Close() }()
	b, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer b.Close()
	if err = a.Raw(func(v any) error { physical = v.(driver.QueryerContext); return nil }); err != nil {
		return err
	}
	if len(notices) != 0 {
		return errors.New("default initialization emitted a notice")
	}
	if err = settingsCommand(ctx, a, "CREATE TABLE sentinel(v INT NOT NULL)", false, 0); err != nil {
		return err
	}
	if err = settingsCommand(ctx, a, "INSERT INTO sentinel VALUES(7)", false, 1); err != nil {
		return err
	}
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ name, input, want string }{
			{"demo.flag", "yes", "on"}, {"demo.text", "é a b", "é a b"}, {"demo.count", "3.5", "4"}, {"demo.ratio", "2.1234567", "2.12346"}, {"demo.mode", "FAST", "fast"},
			{"demo.memory", "30.1GB", "30822MB"}, {"demo.bytes", "0.5kB", "512B"}, {"demo.duration", "1.5s", "1500ms"}, {"demo.real_time", "0.001s", "1ms"},
			{"edge.count", "010", "8"}, {"edge.count", "0xA", "10"}, {"edge.count", "010.5", "10"}, {"edge.count", "010e1", "100"}, {"edge.count", "0x1.8p1", "3"}, {"edge.count", ".5", "0"}, {"edge.count", "9223372036854775808e-18", "9"},
			{"edge.real", "010", "10"}, {"edge.real", "08.5", "8.5"}, {"edge.real", "0x1p0", "1"}, {"edge.real", "-.5", "-0.5"}, {"edge.real", " .5", "0.5"}, {"edge.real", "0e-999", "0"}, {"edge.real", "0x1p-1074", "4.94066e-324"},
		} {
			if err = settingsCommand(ctx, a, "SET "+strings.ToUpper(tc.name)+" TO '"+tc.input+"'", prepared, 0); err != nil {
				return err
			}
			if err = settingsScalar(ctx, a, "SHOW "+tc.name, tc.name, tc.want, prepared); err != nil {
				return err
			}
			if err = settingsScalar(ctx, a, "SELECT current_setting($1)", "current_setting", tc.want, prepared, tc.name); err != nil {
				return err
			}
			if err = settingsScalar(ctx, a, "SELECT set_config($1,$2,$3)", "set_config", tc.want, prepared, tc.name, tc.input, false); err != nil {
				return err
			}
		}
		for _, name := range []string{"edge.count", "edge.real"} {
			inputs := []string{"0b10", "0o10", "1_000", "1e", "0x", "NaN", "1e-999", "1e999"}
			if name == "edge.count" {
				inputs = append(inputs, "+.5", "-.5", " .5", "08.5", "0x1p0", "Infinity", "-inf")
			} else {
				inputs = append(inputs, "1e-320", "0x1.1p-1074")
			}
			for _, input := range inputs {
				if err = settingsScalar(ctx, a, "SELECT set_config($1,'1',false)", "set_config", "1", prepared, name); err != nil {
					return err
				}
				e := settingsCommand(ctx, a, "SELECT set_config($1,$2,false)", prepared, 1, name, input)
				if err = settingsDiagnostic(e, "22023", `invalid value for parameter "`+name+`": "`+input+`"`, ""); err != nil {
					return err
				}
				if err = settingsScalar(ctx, a, "SHOW "+name, name, "1", prepared); err != nil {
					return err
				}
			}
		}
		for _, input := range []string{"Infinity", "-inf"} {
			prefix := "Infinity"
			if input == "-inf" {
				prefix = "-Infinity"
			}
			e := settingsCommand(ctx, a, "SELECT set_config('demo.ratio',$1,false)", prepared, 1, input)
			if err = settingsDiagnostic(e, "22023", prefix+` is outside the valid range for parameter "demo.ratio" (0 .. 10)`, ""); err != nil {
				return err
			}
			if err = settingsScalar(ctx, a, "SHOW demo.ratio", "demo.ratio", "2.12346", prepared); err != nil {
				return err
			}
		}
		if err = settingsCommand(ctx, a, "RESET ALL", prepared, 0); err != nil {
			return err
		}
		if err = settingsScalar(ctx, a, "SELECT current_setting(NULL)", "current_setting", nil, prepared); err != nil {
			return err
		}
		if err = settingsScalar(ctx, a, "SELECT set_config('demo.text',NULL,NULL)", "set_config", "seed", prepared); err != nil {
			return err
		}
		if err = settingsCommand(ctx, a, "SELECT current_setting('demo.text')", prepared, 1); err != nil {
			return err
		}
		e := settingsCommand(ctx, a, "SELECT set_config($1,$2,$3)", prepared, 1, "application_name", "changed", "maybe")
		if err = settingsDiagnostic(e, "22P02", `invalid input syntax for type boolean: "maybe"`, ""); err != nil {
			return err
		}
	}
	if err = settingsScalar(ctx, b, "SHOW demo.text", "demo.text", "seed", false); err != nil {
		return err
	}
	_, err = a.ExecContext(ctx, "SET demo.count TO '11'")
	if err = settingsDiagnostic(err, "22023", `11 is outside the valid range for parameter "demo.count" (-10 .. 10)`, ""); err != nil {
		return err
	}
	if err = settingsCommand(ctx, a, "SET demo.placeholder TO private", true, 0); err != nil {
		return err
	}
	rows, err := a.QueryContext(ctx, "SHOW ALL")
	if err != nil {
		return err
	}
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(cols, []string{"name", "setting", "description"}) {
		return fmt.Errorf("SHOW ALL columns %v", cols)
	}
	expected := map[string][2]string{
		"application_name": {"", "Sets the application name to be reported in statistics and logs."}, "client_min_messages": {"notice", "Sets the message levels that are sent to the client."},
		"log_statement_stats": {"off", "Writes cumulative performance statistics to the server log."}, "server_encoding": {"UTF8", "Shows the server (database) character set encoding."},
		"standard_conforming_strings": {"on", "Causes '...' strings to treat backslashes literally."}, "statement_timeout": {"0", "Sets the maximum allowed duration of any statement."},
	}
	for _, v := range settingsDefinitions() {
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
			return err
		}
		want, ok := expected[n]
		if n <= last || !ok || want != [2]string{v, desc} {
			return fmt.Errorf("SHOW ALL %s %q %q", n, v, desc)
		}
		last = n
		delete(expected, n)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if len(expected) != 0 {
		return fmt.Errorf("SHOW ALL missing %v", expected)
	}
	original := strings.Repeat("a", 64)
	clipped := strings.Repeat("a", 63)
	notice := stuntgres.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
	for _, prepared := range []bool{false, true} {
		for _, threshold := range []string{"notice", "warning", "error"} {
			if err = settingsCommand(ctx, a, "SET client_min_messages TO "+threshold, false, 0); err != nil {
				return err
			}
			before := len(notices)
			if err = settingsCommand(ctx, a, "SET application_name TO '"+original+"'", prepared, 0); err != nil {
				return err
			}
			if err = settingsScalar(ctx, a, "SELECT set_config($1,$2,false)", "set_config", clipped, prepared, "application_name", original); err != nil {
				return err
			}
			count := 0
			if threshold == "notice" {
				count = 2
			}
			if len(notices) != before+count || callbackError != nil {
				return fmt.Errorf("notice delivery %v: %v", notices, callbackError)
			}
			for _, n := range notices[before:] {
				if n != notice {
					return fmt.Errorf("notice %#v want %#v", n, notice)
				}
			}
			if err = settingsScalar(ctx, a, "SHOW application_name", "application_name", clipped, false); err != nil {
				return err
			}
		}
	}
	if err = settingsCommand(ctx, a, "RESET client_min_messages", false, 0); err != nil {
		return err
	}
	before := len(notices)
	for _, q := range []string{"SET application_name TO DEFAULT", "RESET application_name"} {
		if err = settingsCommand(ctx, a, q, true, 0); err != nil {
			return err
		}
	}
	if err = settingsScalar(ctx, a, "SELECT set_config('application_name',NULL,false)", "set_config", "", true); err != nil {
		return err
	}
	if len(notices) != before {
		return errors.New("default reset emitted notice")
	}
	if err = settingsDefaultsAndUnitFailures(ctx, a); err != nil {
		return err
	}
	if err = settingsParserMode(ctx, a); err != nil {
		return err
	}
	if err = settingsTimeoutStages(ctx, a, b); err != nil {
		return err
	}
	for _, tc := range []struct{ q, code, message string }{{"RESET server_encoding", "55P02", `parameter "server_encoding" cannot be changed`}, {"SET log_statement_stats TO garbage", "42501", `permission denied to set parameter "log_statement_stats"`}, {"SELECT set_config('demo.text','lost',true)", "0A000", "transaction-local settings are unsupported"}} {
		_, err = a.ExecContext(ctx, tc.q)
		if err = settingsDiagnostic(err, tc.code, tc.message, ""); err != nil {
			return err
		}
	}
	_, err = a.ExecContext(ctx, "SET LOCAL demo.text TO lost")
	if err = settingsFullDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "transaction-local settings are unsupported", Position: "5"}); err != nil {
		return err
	}
	if err = settingsTransactionSettings(ctx, a); err != nil {
		return err
	}
	if err = settingsAuthenticatedAccess(ctx, d, a); err != nil {
		return err
	}
	if err = settingsCommand(ctx, a, "SET demo.text TO retained", false, 0); err != nil {
		return err
	}
	beforeID, err := settingsIdentity(a)
	if err != nil {
		return err
	}
	a.Close()
	a, err = db.Conn(ctx)
	if err != nil {
		return err
	}
	afterID, err := settingsIdentity(a)
	if err != nil || afterID != beforeID {
		return fmt.Errorf("reuse identity %v", err)
	}
	if err = settingsScalar(ctx, a, "SHOW demo.text", "demo.text", "retained", false); err != nil {
		return err
	}
	if err = a.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		return err
	}
	a.Close()
	a, err = db.Conn(ctx)
	if err != nil {
		return err
	}
	afterID, err = settingsIdentity(a)
	if err != nil || afterID == beforeID {
		return fmt.Errorf("replacement identity %v", err)
	}
	if err = settingsScalar(ctx, a, "SHOW demo.text", "demo.text", "seed", false); err != nil {
		return err
	}
	_, err = a.ExecContext(ctx, "SHOW demo.placeholder")
	if err = settingsDiagnostic(err, "42704", `unrecognized configuration parameter "demo.placeholder"`, ""); err != nil {
		return err
	}
	if err = settingsScalar(ctx, b, "SHOW demo.text", "demo.text", "seed", false); err != nil {
		return err
	}
	if err = settingsSentinel(ctx, a); err != nil {
		return err
	}
	return nil
}
func settingsParserMode(ctx context.Context, c *sql.Conn) error {
	if err := settingsCommand(ctx, c, "CREATE TABLE literals(id INT,v TEXT)", false, 0); err != nil {
		return err
	}
	captured, err := c.PrepareContext(ctx, `INSERT INTO literals VALUES(1,U&'\0061')`)
	if err != nil {
		return err
	}
	defer captured.Close()
	setting, err := c.PrepareContext(ctx, `SET application_name TO U&'\0061'`)
	if err != nil {
		return err
	}
	defer setting.Close()
	call, err := c.PrepareContext(ctx, `SELECT set_config('application_name',U&'\0061',false)`)
	if err != nil {
		return err
	}
	defer call.Close()
	if err = settingsCommand(ctx, c, "SET standard_conforming_strings TO off", false, 0); err != nil {
		return err
	}
	for _, tc := range []struct{ q, position string }{{`SET application_name TO U&'\0061'`, "25"}, {`SELECT current_setting(U&'application_name')`, "24"}, {`SET application_name TO U&'\ZZZZ' UESCAPE '0'`, "25"}, {`SET application_name TO U&'unterminated`, "25"}, {`/*é*/ SET application_name TO U&'\0061'`, "31"}, {`INSERT INTO literals VALUES(9,U&'\0061')`, "31"}} {
		for _, prepare := range []bool{false, true} {
			if prepare {
				s, e := c.PrepareContext(ctx, tc.q)
				err = e
				if s != nil {
					s.Close()
					return errors.New("unsafe preparation returned a handle")
				}
			} else {
				_, err = c.ExecContext(ctx, tc.q)
			}
			if err = settingsFullDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unsafe use of string constant with Unicode escapes", Detail: `String constants with Unicode escapes cannot be used when "standard_conforming_strings" is off.`, Position: tc.position}); err != nil {
				return err
			}
			if err = settingsScalar(ctx, c, "SHOW application_name", "application_name", "", false); err != nil {
				return err
			}
		}
	}
	if err = settingsCommand(ctx, c, `SET U&"application_name" TO 'x'`, false, 0); err != nil {
		return err
	}
	if err = settingsCommand(ctx, c, `SET application_name TO E'a\nb'`, false, 0); err != nil {
		return err
	}
	if err = settingsScalar(ctx, c, "SHOW application_name", "application_name", `a\x0ab`, false); err != nil {
		return err
	}
	if err = settingsCommand(ctx, c, `SET application_name TO $$a\nb$$`, false, 0); err != nil {
		return err
	}
	if err = settingsScalar(ctx, c, "SHOW application_name", "application_name", `a\nb`, false); err != nil {
		return err
	}
	for _, s := range []*sql.Stmt{setting, call, captured} {
		if _, err = s.ExecContext(ctx); err != nil {
			return err
		}
	}
	if err = settingsScalar(ctx, c, "SHOW application_name", "application_name", "a", false); err != nil {
		return err
	}
	off, err := c.PrepareContext(ctx, `INSERT INTO literals VALUES(2,'a\nb')`)
	if err != nil {
		return err
	}
	defer off.Close()
	if err = settingsCommand(ctx, c, "SET standard_conforming_strings TO on", false, 0); err != nil {
		return err
	}
	if _, err = off.ExecContext(ctx); err != nil {
		return err
	}
	if err = settingsScalar(ctx, c, "SELECT v FROM literals WHERE id=1", "v", "a", false); err != nil {
		return err
	}
	return settingsScalar(ctx, c, "SELECT v FROM literals WHERE id=2", "v", "a\nb", false)
}
func settingsTimeoutStages(ctx context.Context, a, b *sql.Conn) error {
	retained, err := a.QueryContext(ctx, "SELECT v FROM sentinel")
	if err != nil {
		return err
	}
	defer retained.Close()
	for _, stage := range []string{"direct", "prepare", "prepared-execute"} {
		timedOut := false
		for _, size := range []int{16384, 65536, 262144, 1048576} {
			// NULL prevents a fast successful mutation; only a failed or Prepare-only
			// workload may be enlarged. Every timeout uses the actual command timer.
			q := "INSERT INTO sentinel VALUES " + strings.Repeat("(8),", size) + "(NULL)"
			var s *sql.Stmt
			if stage == "prepared-execute" {
				s, err = a.PrepareContext(ctx, q)
				if err != nil {
					return err
				}
			}
			if err = settingsCommand(ctx, a, "SET statement_timeout TO '1ms'", false, 0); err != nil {
				return err
			}
			switch stage {
			case "direct":
				_, err = a.ExecContext(ctx, q)
			case "prepare":
				s, err = a.PrepareContext(ctx, q)
			case "prepared-execute":
				_, err = s.ExecContext(ctx)
			}
			if s != nil {
				s.Close()
			}
			var d *stuntgres.Error
			if errors.As(err, &d) && d.Code == "57014" {
				if err = settingsDiagnostic(err, "57014", "canceling statement due to statement timeout", ""); err != nil {
					return err
				}
				timedOut = true
			} else if stage == "prepare" {
				if err != nil {
					return err
				}
			} else if !errors.As(err, &d) || d.Code != "23502" {
				return fmt.Errorf("timeout %s unexpected %v", stage, err)
			}
			if err = settingsCommand(ctx, a, "RESET statement_timeout", false, 0); err != nil {
				return err
			}
			if err = settingsSentinel(ctx, a); err != nil {
				return err
			}
			if timedOut {
				break
			}
		}
		if !timedOut {
			return fmt.Errorf("bounded real %s workload did not time out", stage)
		}
		if err = settingsScalar(ctx, b, "SHOW statement_timeout", "statement_timeout", "0", false); err != nil {
			return err
		}
	}
	if !retained.Next() {
		return fmt.Errorf("retained snapshot: %v", retained.Err())
	}
	var v int
	if err = retained.Scan(&v); err != nil || v != 7 {
		return fmt.Errorf("retained sentinel %d: %v", v, err)
	}
	if retained.Next() || retained.Err() != nil {
		return fmt.Errorf("retained EOF: %v", retained.Err())
	}
	caller, stop := context.WithCancel(ctx)
	stop()
	_, err = a.ExecContext(caller, "SHOW application_name")
	if !errors.Is(err, context.Canceled) {
		return fmt.Errorf("caller cancellation %v", err)
	}
	return settingsScalar(ctx, a, "SHOW statement_timeout", "statement_timeout", "0", false)
}

func settingsDefaultsAndUnitFailures(ctx context.Context, c *sql.Conn) error {
	for _, prepared := range []bool{false, true} {
		for _, def := range settingsDefinitions() {
			want := def.Default
			switch def.Name {
			case "demo.memory":
				want = "1MB"
			case "demo.duration":
				want = "1500ms"
			case "demo.real_time":
				want = "1s"
			}
			if def.Access == stuntgres.SettingSession {
				if err := settingsCommand(ctx, c, "SET "+def.Name+" TO DEFAULT", prepared, 0); err != nil {
					return err
				}
				if err := settingsCommand(ctx, c, "RESET "+def.Name, prepared, 0); err != nil {
					return err
				}
				if err := settingsScalar(ctx, c, "SELECT set_config($1,NULL,NULL)", "set_config", want, prepared, def.Name); err != nil {
					return err
				}
			}
			if err := settingsScalar(ctx, c, "SHOW "+def.Name, def.Name, want, prepared); err != nil {
				return err
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
			err := settingsCommand(ctx, c, "SELECT set_config($1,$2,false)", prepared, 1, tc.name, tc.input)
			if err = settingsDiagnostic(err, "22023", tc.message, tc.hint); err != nil {
				return err
			}
			if err = settingsScalar(ctx, c, "SHOW "+tc.name, tc.name, tc.want, prepared); err != nil {
				return err
			}
		}
		if err := settingsCommand(ctx, c, "RESET ALL", prepared, 0); err != nil {
			return err
		}
	}
	return nil
}

func settingsTransactionSettings(ctx context.Context, c *sql.Conn) error {
	before, err := settingsIdentity(c)
	if err != nil {
		return err
	}
	var original string
	if err = c.QueryRowContext(ctx, "SHOW demo.text").Scan(&original); err != nil {
		return err
	}
	if err = settingsCommand(ctx, c, "SET demo.text TO retained", false, 0); err != nil {
		return err
	}
	for _, query := range []string{
		"SET demo.text TO lost", "RESET demo.text", "RESET ALL", "SHOW ALL",
		"SELECT current_setting('demo.text')", "SELECT set_config('demo.text','lost',false)",
	} {
		for _, prepared := range []bool{false, true} {
			var statement *sql.Stmt
			if prepared {
				statement, err = c.PrepareContext(ctx, query)
				if err != nil {
					return err
				}
			}
			tx, beginErr := c.BeginTx(ctx, nil)
			if beginErr != nil {
				if statement != nil {
					statement.Close()
				}
				return beginErr
			}
			if prepared {
				bound := tx.StmtContext(ctx, statement)
				_, err = bound.ExecContext(ctx)
				bound.Close()
				statement.Close()
			} else {
				_, err = tx.ExecContext(ctx, query)
			}
			if check := settingsDiagnostic(err, "0A000", "statement is unsupported in an explicit transaction", ""); check != nil {
				tx.Rollback()
				return check
			}
			_, err = tx.ExecContext(ctx, "SHOW demo.text")
			if check := settingsDiagnostic(err, "25P02", "current transaction is aborted, commands ignored until end of transaction block", ""); check != nil {
				tx.Rollback()
				return check
			}
			if err = tx.Rollback(); err != nil {
				return err
			}
			after, identityErr := settingsIdentity(c)
			if identityErr != nil || after != before {
				return fmt.Errorf("settings transaction changed physical identity: %v", identityErr)
			}
			if err = settingsScalar(ctx, c, "SHOW demo.text", "demo.text", "retained", false); err != nil {
				return err
			}
		}
	}
	return settingsScalar(ctx, c, "SELECT set_config('demo.text',$1,false)", "set_config", original, false, original)
}

func settingsAuthenticatedAccess(ctx context.Context, d *stuntgres.Driver, admin *sql.Conn) error {
	if err := settingsCommand(ctx, admin, "CREATE USER settings_reader", false, 0); err != nil {
		return err
	}
	connector, err := d.OpenConnectorAs("settings-smoke", "settings_reader")
	if err != nil {
		return err
	}
	pool := sql.OpenDB(connector)
	defer pool.Close()
	reader, err := pool.Conn(ctx)
	if err != nil {
		return err
	}
	defer reader.Close()
	for _, principal := range []struct {
		name string
		conn *sql.Conn
	}{{"postgres", admin}, {"settings_reader", reader}} {
		var sessionUser, currentUser string
		if err = principal.conn.QueryRowContext(ctx, "SELECT session_user,current_user").Scan(&sessionUser, &currentUser); err != nil {
			return err
		}
		if sessionUser != principal.name || currentUser != principal.name {
			return fmt.Errorf("settings authenticated identity: %q %q want %q", sessionUser, currentUser, principal.name)
		}
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
					err = settingsCommand(ctx, principal.conn, query, prepared, 0)
					if err = settingsDiagnostic(err, definition.code, definition.message, ""); err != nil {
						return err
					}
					if err = settingsScalar(ctx, principal.conn, "SHOW "+definition.name, definition.name, definition.value, prepared); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
