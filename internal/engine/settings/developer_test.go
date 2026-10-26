package settings

import (
	"errors"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestDeveloperSettingsParsers(t *testing.T) {
	registry := NewRegistry(nil, false)
	for _, tc := range []struct{ name, input, show, raw string }{
		{"post_auth_delay", "60s", "1min", "60"}, {"post_auth_delay", "1000ms", "1s", "1"}, {"post_auth_delay", "2147", "2147s", "2147"}, {"pre_auth_delay", "60", "1min", "60"},
		{"debug_io_direct", "", "", ""}, {"debug_io_direct", "data", "data", "data"}, {"debug_io_direct", "DATA,wal,wal_init,DATA", "DATA,wal,wal_init,DATA", "DATA,wal,wal_init,DATA"}, {"debug_io_direct", ` "DaTa" , "wal" `, ` "DaTa" , "wal" `, ` "DaTa" , "wal" `},
		{"backtrace_functions", "foo,, 9bad,", "foo,, 9bad,", "foo,, 9bad,"}, {"backtrace_functions", " \n\t", " \n\t", " \n\t"},
		{"wal_consistency_checking", `"HeAp", BTREE`, `"HeAp", BTREE`, `"HeAp", BTREE`}, {"wal_consistency_checking", "all,heap,heap", "all,heap,heap", "all,heap,heap"},
		{"debug_discard_caches", "0", "0", "0"},
	} {
		d, err := registry.Lookup(tc.name)
		if err != nil {
			t.Fatal(err)
		}
		v, err := d.Parse(tc.input)
		if err != nil || d.Format(v) != tc.show || d.Raw(v) != tc.raw {
			t.Fatalf("%s %q: %+v %v show=%s raw=%s", tc.name, tc.input, v, err, d.Format(v), d.Raw(v))
		}
	}
	for _, tc := range []struct{ name, input, message, detail, hint string }{
		{"post_auth_delay", "2148", `2148 s is outside the valid range for parameter "post_auth_delay" (0 s .. 2147 s)`, "", ""},
		{"post_auth_delay", "-1", `-1 s is outside the valid range for parameter "post_auth_delay" (0 s .. 2147 s)`, "", ""},
		{"pre_auth_delay", "61", `61 s is outside the valid range for parameter "pre_auth_delay" (0 s .. 60 s)`, "", ""},
		{"debug_discard_caches", "1", `1 is outside the valid range for parameter "debug_discard_caches" (0 .. 0)`, "", ""},
		{"debug_discard_caches", "-1", `-1 is outside the valid range for parameter "debug_discard_caches" (0 .. 0)`, "", ""},
		{"debug_discard_caches", "2147483648", `invalid value for parameter "debug_discard_caches": "2147483648"`, "", "Value exceeds integer range."},
		{"post_auth_delay", "2147483648", `invalid value for parameter "post_auth_delay": "2147483648"`, "", "Value exceeds integer range."},
		{"post_auth_delay", "broken", `invalid value for parameter "post_auth_delay": "broken"`, "", ""},
		{"pre_auth_delay", "-1", `-1 s is outside the valid range for parameter "pre_auth_delay" (0 s .. 60 s)`, "", ""},
		{"pre_auth_delay", "1fortnight", `invalid value for parameter "pre_auth_delay": "1fortnight"`, "", `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`},
		{"debug_io_direct", "bogus,", `invalid value for parameter "debug_io_direct": "bogus,"`, `Invalid list syntax in parameter "debug_io_direct".`, ""},
		{"debug_io_direct", `"wa""l"`, `invalid value for parameter "debug_io_direct": ""wa""l""`, `Invalid option "wa"l".`, ""},
		{"debug_io_direct", "bogus", `invalid value for parameter "debug_io_direct": "bogus"`, `Invalid option "bogus".`, ""},
		{"debug_io_direct", "data,", `invalid value for parameter "debug_io_direct": "data,"`, `Invalid list syntax in parameter "debug_io_direct".`, ""},
		{"debug_io_direct", "data wal", `invalid value for parameter "debug_io_direct": "data wal"`, `Invalid list syntax in parameter "debug_io_direct".`, ""},
		{"debug_io_direct", `" data "`, `invalid value for parameter "debug_io_direct": "" data ""`, `Invalid option " data ".`, ""},
		{"wal_consistency_checking", `"BOGUS"`, `invalid value for parameter "wal_consistency_checking": ""BOGUS""`, `Unrecognized key word: "BOGUS".`, ""},
		{"wal_consistency_checking", "heap,", `invalid value for parameter "wal_consistency_checking": "heap,"`, `List syntax is invalid.`, ""},
	} {
		d, _ := registry.Lookup(tc.name)
		_, err := d.Parse(tc.input)
		var e *sqlstate.Error
		if !errors.As(err, &e) || e.Code != "22023" || e.Message != tc.message || e.Detail != tc.detail || e.Hint != tc.hint {
			t.Fatalf("%s %q: %+v", tc.name, tc.input, err)
		}
	}
	for _, input := range []string{"foo-bar", "é", "foo\r", "foo\f", `"foo"`} {
		d, _ := registry.Lookup("backtrace_functions")
		_, err := d.Parse(input)
		var e *sqlstate.Error
		if !errors.As(err, &e) || e.Detail != "Invalid character." {
			t.Fatal(input, err)
		}
	}
	for _, access := range []Access{Backend, SuperuserBackend} {
		if _, err := Validate(Definition{Name: "developer.custom", Type: Boolean, Access: access, BootDefault: "off"}); err == nil {
			t.Fatal("custom backend access accepted", access)
		}
	}
}
