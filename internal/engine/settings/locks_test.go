package settings

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
)

var lockDefinitions = []struct {
	name, raw, show, description, extra string
	access Access
	unit Unit
	min, max int32
}{
	{"deadlock_timeout", "1000", "1s", "Sets the time to wait on a lock before checking for deadlock.", "", Privileged, Milliseconds, 1, 2147483647},
	{"max_locks_per_transaction", "64", "64", "Sets the maximum number of locks per transaction.", `The shared lock table is sized on the assumption that at most "max_locks_per_transaction" objects per server process or prepared transaction will need to be locked at any one time.`, StartupOnly, UnitNone, 10, 2147483647},
	{"max_pred_locks_per_page", "2", "2", "Sets the maximum number of predicate-locked tuples per page.", "If more than this number of tuples on the same page are locked by a connection, those locks are replaced by a page-level lock.", Sighup, UnitNone, 0, 2147483647},
	{"max_pred_locks_per_relation", "-2", "-2", "Sets the maximum number of predicate-locked pages and tuples per relation.", "If more than this total of pages and tuples in the same relation are locked by a connection, those locks are replaced by a relation-level lock.", Sighup, UnitNone, -2147483648, 2147483647},
	{"max_pred_locks_per_transaction", "64", "64", "Sets the maximum number of predicate locks per transaction.", `The shared predicate lock table is sized on the assumption that at most "max_pred_locks_per_transaction" objects per server process or prepared transaction will need to be locked at any one time.`, StartupOnly, UnitNone, 10, 2147483647},
}

func TestLockSettingsDefinitions(t *testing.T) {
	r := NewRegistry(nil, false)
	for _, tc := range lockDefinitions {
		t.Run(tc.name, func(t *testing.T) {
			d, err := r.Lookup(tc.name)
			if err != nil { t.Fatal(err) }
			if d.Name != tc.name || d.Type != Integer || d.Access != tc.access || d.Unit != tc.unit || d.IntegerMin != tc.min || d.IntegerMax != tc.max || d.Category != "Lock Management" || d.Description != tc.description || d.ExtraDescription != tc.extra || d.ReadRestricted || d.BootDefaultNull || d.NativeStartup != nil || d.EnumValues != nil { t.Fatalf("descriptor %+v want %+v", d, tc) }
			if d.BootDefault != tc.show || d.Format(d.BootValue()) != tc.show || d.Format(d.DefaultValue()) != tc.show || d.Raw(d.BootValue()) != tc.raw || d.CurrentRaw(d.DefaultValue()) != tc.raw || !reflect.DeepEqual(d.BootValue(), d.DefaultValue()) { t.Fatalf("typed defaults %+v", d) }
		})
	}
}

func TestLockSettingsParsing(t *testing.T) {
	r := NewRegistry(nil, false)
	for _, tc := range lockDefinitions {
		t.Run(tc.name, func(t *testing.T) {
			d, err := r.Lookup(tc.name)
			if err != nil { t.Fatal(err) }
			for _, input := range []string{fmt.Sprint(tc.min), fmt.Sprint(tc.max), tc.raw} {
				v, err := d.Parse(input)
				if err != nil || d.Raw(v) != input { t.Fatal(input, v, err) }
			}
			if tc.name == "max_pred_locks_per_relation" {
				for _, input := range []string{"-1", "0"} { if _, err := d.Parse(input); err != nil { t.Fatal(err) } }
			}
			check := func(input, message, hint string) {
				t.Helper()
				_, err := d.Parse(input)
				var got *sqlstate.Error
				want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: message, Hint: hint}
				if !errors.As(err, &got) || !reflect.DeepEqual(got, want) { t.Fatalf("%q: got %#v want %#v", input, err, want) }
			}
			for _, input := range []string{"2147483648", "-2147483649"} { check(input, fmt.Sprintf("invalid value for parameter %q: %q", tc.name, input), "Value exceeds integer range.") }
			if tc.min != -2147483648 {
				label := ""
				if tc.unit == Milliseconds { label = " ms" }
				input := fmt.Sprint(int64(tc.min)-1)
				check(input, fmt.Sprintf("%s%s is outside the valid range for parameter %q (%d%s .. %d%s)", input, label, tc.name, tc.min, label, tc.max, label), "")
			}
			if tc.name == "deadlock_timeout" {
				for _, v := range []struct{ input, show, raw string }{{"1", "1ms", "1"}, {"1000ms", "1s", "1000"}, {"1.5s", "1500ms", "1500"}, {"1min", "1min", "60000"}, {"0x3e8", "1s", "1000"}, {"1500us", "2ms", "2"}} {
					parsed, err := d.Parse(v.input)
					if err != nil || d.Format(parsed) != v.show || d.Raw(parsed) != v.raw { t.Fatal(v, parsed, err) }
				}
				check("broken", `invalid value for parameter "deadlock_timeout": "broken"`, "")
				check("1MS", `invalid value for parameter "deadlock_timeout": "1MS"`, `Valid units for this parameter are "us", "ms", "s", "min", "h", and "d".`)
				check("500us", `0 ms is outside the valid range for parameter "deadlock_timeout" (1 ms .. 2147483647 ms)`, "")
			}
		})
	}
}
