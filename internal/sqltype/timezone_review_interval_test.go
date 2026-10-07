package sqltype

import (
	"errors"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

// REL_18_0 datetime.c DecodeInterval requires a nonzero decoded field mask,
// not a nonzero value: noise/sign markers alone are not an interval.
func TestTimezoneReviewIntervalDecodedField(t *testing.T) {
	for _, text := range []string{"@", "@ ", "@ ago", "@  ago", "ago"} {
		t.Run(text, func(t *testing.T) {
			_, err := DecodeZoneInterval(text, "", -1)
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22007", Message: "invalid input syntax for type interval: \"" + text + "\""}
			var got *sqlstate.Error
			if !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
				t.Fatalf("diagnostic = %#v (%v), want %#v", got, err, want)
			}
		})
	}
	for _, text := range []string{"0 seconds", "@ 0 seconds", "@ 0 seconds ago", "00:00", "@ 00:00 ago"} {
		value, err := DecodeZoneInterval(text, "", -1)
		if err != nil || value != (ZoneInterval{}) {
			t.Fatalf("zero interval %q = %#v, %v", text, value, err)
		}
	}
}

func TestTimezoneReviewIntervalSettingDecoderErrors(t *testing.T) {
	for _, tc := range []struct{ text, code, message string }{
		{"nonsense", "22007", `invalid input syntax for type interval: "nonsense"`},
		{"@", "22007", `invalid input syntax for type interval: "@"`},
		{"9223372036854775808 microseconds", "22015", `interval field value out of range: "9223372036854775808 microseconds"`},
	} {
		zone, detail, err := ParseSessionZone("INTERVAL '" + tc.text + "'")
		want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		var got *sqlstate.Error
		if zone != nil || detail != "" || !errors.As(err, &got) || !reflect.DeepEqual(got, want) {
			t.Fatalf("setting %q = %v, %q, %#v (%v), want %#v", tc.text, zone, detail, got, err, want)
		}
	}
}
