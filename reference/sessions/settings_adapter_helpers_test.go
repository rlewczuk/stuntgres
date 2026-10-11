package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"math"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
)

func settingsDefinitionsForTest() []stuntgres.SettingDefinition {
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
	}
}

func settingsTestDriver(t *testing.T) *stuntgres.Driver {
	t.Helper()
	d := stuntgres.NewDriver()
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
	var d *stuntgres.Error
	if !errors.As(err, &d) {
		t.Fatalf("not SQL error: %v", err)
	}
	want := &stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint, Detail: detail, Position: position}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("got %#v want %#v", d, want)
	}
}
func sessionPool(t *testing.T, d *stuntgres.Driver, dsn string) *sql.DB {
	t.Helper()
	connector, err := d.OpenConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	return db
}
func sessionShow(t *testing.T, c *sql.Conn, want string, prepared bool) {
	t.Helper()
	ctx := context.Background()
	var rows *sql.Rows
	var err error
	if prepared {
		s, e := c.PrepareContext(ctx, "SHOW application_name")
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		rows, err = s.QueryContext(ctx)
	} else {
		rows, err = c.QueryContext(ctx, "SHOW application_name")
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(columns, []string{"application_name"}) || len(types) != 1 || types[0].DatabaseTypeName() != "TEXT" {
		t.Fatal(columns, types)
	}
	if !rows.Next() {
		t.Fatal("no SHOW row", rows.Err())
	}
	var got any
	if err = rows.Scan(&got); err != nil || got != want {
		t.Fatalf("SHOW %#v want %q err %v", got, want, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("SHOW EOF", rows.Err())
	}
}
func sessionSet(t *testing.T, c *sql.Conn, q string, prepared bool) {
	t.Helper()
	ctx := context.Background()
	var r sql.Result
	var err error
	if prepared {
		s, e := c.PrepareContext(ctx, q)
		if e != nil {
			t.Fatal(e)
		}
		defer s.Close()
		r, err = s.ExecContext(ctx)
	} else {
		r, err = c.ExecContext(ctx, q)
	}
	if err != nil {
		t.Fatal(err)
	}
	n, err := r.RowsAffected()
	if err != nil || n != 0 {
		t.Fatal(n, err)
	}
}

// Compare the public physical driver object, never an internal token or unsafe pointer.
func settingsPhysical(t *testing.T, c *sql.Conn) any {
	t.Helper()
	var identity any
	if err := c.Raw(func(raw any) error {
		identity = raw
		if validator, ok := raw.(driver.Validator); ok && !validator.IsValid() {
			t.Fatal("physical adapter is invalid")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return identity
}
