package settings

import (
	"reflect"
	"strings"
	"testing"

	"stuntgres/internal/sqlstate"
)

func TestClientEncodingStartupName(t *testing.T) {
	for _, tc := range []struct {
		name, raw, clipped, want string
		notice                   bool
	}{
		{"raw16", strings.Repeat("\xe9", 16), "", strings.Repeat(`\xe9`, 16), false},
		{"raw63", strings.Repeat("\xe9", 63), "", strings.Repeat(`\xe9`, 63), false},
		{"raw64", strings.Repeat("\xe9", 64), strings.Repeat("\xe9", 63), strings.Repeat(`\xe9`, 63), true},
		{"raw_boundary", strings.Repeat("a", 62) + "\xe9tail", strings.Repeat("a", 62), strings.Repeat("a", 62), true},
		{"utf8_boundary", strings.Repeat("a", 62) + "é_tail", strings.Repeat("a", 62), strings.Repeat("a", 62), true},
		{"utf8_retained", strings.Repeat("a", 61) + "é_tail", strings.Repeat("a", 61) + "é", strings.Repeat("a", 61) + `\xc3\xa9`, true},
		{"invalid_lead", strings.Repeat("a", 62) + "\xfftail", strings.Repeat("a", 62) + "\xff", strings.Repeat("a", 62) + `\xff`, true},
		{"controls", "a\t\x1fb", "", `a\x09\x1fb`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, notice, err := StartupApplicationName(tc.raw)
			if err != nil || value.Text != tc.want || (notice != nil) != tc.notice {
				t.Fatalf("original %x: value=%x notice=%+v error=%v", tc.raw, value.Text, notice, err)
			}
			if notice != nil {
				want := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + tc.raw + `" will be truncated to "` + tc.clipped + `"`}
				if !reflect.DeepEqual(*notice, want) {
					t.Fatalf("original NAME notice %+v want %+v", *notice, want)
				}
			}
		})
	}
	r := NewRegistry(nil, false)
	d, _ := r.Lookup("application_name")
	for _, raw := range []string{"\xe9", "a\x00b"} {
		if _, err := d.Parse(raw); err == nil {
			t.Fatal("ordinary Unicode admission accepted raw invalid input", raw)
		}
	}
	if _, _, err := StartupApplicationName("a\x00b"); err == nil {
		t.Fatal("raw startup admission accepted NUL")
	}
	value, err := d.Parse(strings.Repeat("é", 32))
	if err != nil || value.Text != strings.Repeat(`\xc3\xa9`, 31) {
		t.Fatal("ordinary Unicode clipping/sanitization order", value, err)
	}
}
